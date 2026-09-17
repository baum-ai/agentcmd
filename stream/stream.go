// Package stream assembles transport records into invocations, analyzes
// them and associates results, within fixed memory bounds. Processing is
// sequential and deterministic: events carry the input sequence number.
package stream

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/lastpersonlabs/agentcmd"
	"github.com/lastpersonlabs/agentcmd/adapters"
	"github.com/lastpersonlabs/agentcmd/model"
)

// Options configure a Processor. Zero values take the documented defaults.
type Options struct {
	Adapter adapters.Adapter
	// FrameBytes bounds one input record; longer records are skipped.
	FrameBytes int
	// BufferBytes bounds argument fragments buffered across active calls.
	BufferBytes int
	// ActiveCalls bounds concurrently incomplete calls.
	ActiveCalls int
	// Window is how many completed calls remain associable with late results.
	Window int
	// Harness labels invocations whose adapter does not name one.
	Harness string
	// ToolNamespace, when set, replaces the adapter's provider namespace on
	// every call so that harness bindings apply. It is an explicit statement
	// by the caller about which harness produced the records; nothing is
	// autodetected.
	ToolNamespace string
	// Context is the execution context for calls that carry none.
	Context model.ExecutionContext
}

// Processor turns a record stream into events.
type Processor struct {
	engine *agentcmd.Engine
	opts   Options
}

// New validates options.
func New(engine *agentcmd.Engine, opts Options) (*Processor, error) {
	if engine == nil {
		return nil, errors.New("stream: engine is required")
	}
	if opts.Adapter == nil {
		opts.Adapter = adapters.Canonical{}
	}
	d := agentcmd.DefaultLimits()
	if opts.FrameBytes <= 0 {
		opts.FrameBytes = d.InputFrameBytes
	}
	if opts.BufferBytes <= 0 {
		opts.BufferBytes = d.StreamBufferBytes
	}
	if opts.ActiveCalls <= 0 {
		opts.ActiveCalls = d.StreamActiveCalls
	}
	if opts.Window <= 0 {
		opts.Window = 4096
	}
	return &Processor{engine: engine, opts: opts}, nil
}

type key struct{ session, call string }

// pending is an incomplete call whose arguments are still streaming.
type pending struct {
	key      key
	seq      int
	tool     model.ToolIdentity
	buf      strings.Builder
	custom   bool
	aliases  []string
	results  []adapters.Result
	lifecyle []model.LifecycleEvent
}

// completed remembers an analyzed call for result association and for
// recognizing a repeated completion snapshot of the same arguments.
type completed struct {
	id string
	fp string
}

type state struct {
	p        *Processor
	emit     func(model.Event) error
	active   map[key]*pending
	order    []key // active calls in arrival order, for eviction
	buffered int
	known    map[key]completed // completed call → invocation
	knownSeq []key             // completion order, for window eviction
	counts   map[string]int    // positional keys for id-less calls (Gemini)
	rcounts  map[string]int
	seq      int
}

// Process reads records until EOF. emit is called synchronously in input
// order; its error aborts processing (backpressure). Incomplete calls are
// flushed as diagnostics at EOF.
func (p *Processor) Process(ctx context.Context, input io.Reader, emit func(model.Event) error) error {
	st := &state{p: p, emit: emit, active: map[key]*pending{}, known: map[key]completed{}, counts: map[string]int{}, rcounts: map[string]int{}}
	r := bufio.NewReaderSize(input, 64<<10)
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		line, tooLong, err := ReadLine(r, p.opts.FrameBytes)
		if err != nil && err != io.EOF {
			return err
		}
		if err == io.EOF && len(line) == 0 && !tooLong {
			break
		}
		st.seq++
		if tooLong {
			if e := st.diag(model.DiagLimitReached, model.SeverityError, fmt.Sprintf("record exceeds %d bytes and was skipped", p.opts.FrameBytes)); e != nil {
				return e
			}
			continue
		}
		if len(strings.TrimSpace(string(line))) == 0 {
			continue
		}
		if e := st.record(ctx, line); e != nil {
			return e
		}
		if err == io.EOF {
			break
		}
	}
	return st.flush()
}

func (st *state) diag(code string, sev model.Severity, msg string) error {
	return st.emit(model.Event{Kind: model.EventDiagnostic, Sequence: st.seq, Diagnostic: &model.Diagnostic{Code: code, Severity: sev, Message: msg}})
}

func (st *state) record(ctx context.Context, line []byte) error {
	recs, err := st.p.opts.Adapter.Decode(line)
	switch {
	case errors.Is(err, adapters.ErrUnrecognized):
		return st.diag(model.DiagUnsupported, model.SeverityInfo, "record is outside the "+st.p.opts.Adapter.Name()+" schema and was not interpreted")
	case err != nil:
		return st.diag("invalid_record", model.SeverityError, err.Error())
	}
	for _, rec := range recs {
		var e error
		switch {
		case rec.Call != nil:
			e = st.snapshot(ctx, *rec.Call)
		case rec.Delta != nil:
			e = st.delta(ctx, *rec.Delta)
		case rec.Result != nil:
			e = st.result(*rec.Result)
		case rec.Lifecycle != nil:
			e = st.lifecycle(*rec.Lifecycle)
		}
		if e != nil {
			return e
		}
	}
	return nil
}

// callKey resolves the association key; id-less calls get a positional key.
func (st *state) callKey(session, callID, name string, counts map[string]int) (key, bool) {
	if callID != "" {
		return key{session, callID}, false
	}
	n := counts[session+"\x00"+name]
	counts[session+"\x00"+name] = n + 1
	return key{session, fmt.Sprintf("%s#%d", name, n)}, true
}

// snapshot completes a call from a complete argument set, superseding any
// fragments buffered for it.
func (st *state) snapshot(ctx context.Context, c adapters.Call) error {
	k, positional := st.callKey(c.Session, c.CallID, c.Tool.Name, st.counts)
	var carried *pending
	for _, kk := range st.keys(k, c.Aliases) {
		pnd, ok := st.active[kk]
		if !ok {
			continue
		}
		carried = pnd
		if pnd.buf.Len() > 0 {
			if err := st.diag(model.DiagTruncated, model.SeverityInfo, fmt.Sprintf("snapshot for call %s superseded %d buffered fragment bytes", k.call, pnd.buf.Len())); err != nil {
				return err
			}
		}
		st.remove(kk)
		break
	}
	return st.complete(ctx, c, k, positional, carried)
}

// keys lists the primary key and alias keys of a call.
func (st *state) keys(k key, aliases []string) []key {
	out := []key{k}
	for _, a := range aliases {
		if a != "" && a != k.call {
			out = append(out, key{k.session, a})
		}
	}
	return out
}

// complete builds the invocation, analyzes it and emits the analysis, then
// releases any results that arrived before completion.
func (st *state) complete(ctx context.Context, c adapters.Call, k key, positional bool, carried *pending) error {
	fp := model.Fingerprint([]any{c.Tool, c.Input})
	if carried != nil {
		c.Aliases = append(c.Aliases, carried.aliases...)
	}
	keys := st.keys(k, c.Aliases)
	var prev completed
	dup := false
	for _, kk := range keys {
		if p, ok := st.known[kk]; ok {
			prev, dup = p, true
			break
		}
	}
	if dup {
		if prev.fp == fp && carried == nil {
			// The same completion delivered twice (OpenAI emits both
			// arguments.done and output_item.done): register aliases only.
			for _, kk := range keys {
				st.remember(kk, prev)
			}
			return nil
		}
		msg := fmt.Sprintf("call id %s reused in session %q; later results attach to the latest occurrence", k.call, k.session)
		if prev.fp != fp && c.Snapshot {
			msg = fmt.Sprintf("completion snapshot for call %s conflicts with the earlier completion; analyzed as a new occurrence", k.call)
		}
		if err := st.diag("duplicate_call_id", model.SeverityWarning, msg); err != nil {
			return err
		}
	}
	if st.p.opts.ToolNamespace != "" {
		c.Tool.Namespace = st.p.opts.ToolNamespace
	}
	inv := model.Invocation{
		ID: fmt.Sprintf("%s/%s#%d", c.Session, k.call, st.seq), Session: c.Session, CallID: c.CallID,
		Provider: c.Provider, Harness: firstNonEmpty(c.Harness, st.p.opts.Harness), Tool: c.Tool, Input: c.Input, Context: c.Context,
		Source:    &model.SourceRef{Kind: st.p.opts.Adapter.Name(), Offset: st.seq},
		Lifecycle: []model.LifecycleEvent{{Phase: model.PhaseRequested, Source: "provider", Time: c.Time}},
	}
	if positional {
		inv.Lifecycle[0].Detail = "call has no identifier; associated positionally by name and order"
	}
	if !inv.Context.WorkingDir.Known && !inv.Context.Shell.Known && inv.Context.Namespace.Kind == "" {
		inv.Context = st.p.opts.Context
	}
	if inv.Context.Namespace.Kind == "" {
		inv.Context.Namespace.Kind = model.NamespaceUnknown
	}
	if carried != nil {
		inv.Lifecycle = append(inv.Lifecycle, carried.lifecyle...)
	}
	a, err := st.p.engine.Analyze(ctx, inv)
	if err != nil && !errors.Is(err, agentcmd.ErrPartial) {
		return st.emit(model.Event{Kind: model.EventDiagnostic, Sequence: st.seq, Invocation: inv.ID, Diagnostic: &model.Diagnostic{Code: "invalid_record", Severity: model.SeverityError, Message: err.Error()}})
	}
	if err := st.emit(model.Event{Kind: model.EventAnalysis, Sequence: st.seq, Source: inv.Source, Invocation: inv.ID, Analysis: &a}); err != nil {
		return err
	}
	done := completed{id: inv.ID, fp: fp}
	for _, kk := range keys {
		st.remember(kk, done)
	}
	if carried != nil {
		for _, r := range carried.results {
			if err := st.result(r); err != nil {
				return err
			}
		}
	}
	return nil
}

func (st *state) remember(k key, c completed) {
	if _, ok := st.known[k]; !ok {
		st.knownSeq = append(st.knownSeq, k)
	}
	st.known[k] = c
	for len(st.knownSeq) > st.p.opts.Window {
		delete(st.known, st.knownSeq[0])
		st.knownSeq = st.knownSeq[1:]
	}
}

func (st *state) remove(k key) {
	if pnd, ok := st.active[k]; ok {
		st.buffered -= pnd.buf.Len()
		delete(st.active, k)
		for i, o := range st.order {
			if o == k {
				st.order = append(st.order[:i], st.order[i+1:]...)
				break
			}
		}
	}
}

func (st *state) delta(ctx context.Context, d adapters.Delta) error {
	k := key{d.Session, d.CallID}
	pnd, ok := st.active[k]
	if !ok {
		pnd = &pending{key: k, seq: st.seq, tool: d.Tool, custom: d.Custom}
		st.active[k] = pnd
		st.order = append(st.order, k)
		for len(st.order) > st.p.opts.ActiveCalls {
			if err := st.evict(st.order[0], "too many concurrently incomplete calls"); err != nil {
				return err
			}
		}
	}
	if d.Tool.Name != "" {
		pnd.tool = d.Tool
	}
	pnd.aliases = append(pnd.aliases, d.Aliases...)
	if d.Done && d.Text != "" {
		// A done event carrying text is the complete argument set, not
		// another fragment.
		st.buffered -= pnd.buf.Len()
		pnd.buf.Reset()
	}
	pnd.buf.WriteString(d.Text)
	st.buffered += len(d.Text)
	for st.buffered > st.p.opts.BufferBytes && len(st.order) > 0 {
		victim := st.order[0]
		if victim == k && len(st.order) == 1 {
			// The single active call alone exceeds the buffer.
			if err := st.evict(k, "buffered fragments exceed the stream buffer limit"); err != nil {
				return err
			}
			return nil
		}
		if err := st.evict(victim, "buffered fragments exceed the stream buffer limit"); err != nil {
			return err
		}
	}
	if !d.Done {
		return nil
	}
	if _, still := st.active[k]; !still {
		return nil
	}
	text := pnd.buf.String()
	st.remove(k)
	call := adapters.Call{Session: d.Session, CallID: d.CallID, Tool: pnd.tool, Snapshot: false}
	if call.Tool.Name == "" {
		return st.diag(model.DiagTruncated, model.SeverityError, fmt.Sprintf("fragments for call %s completed without a tool name; call dropped", d.CallID))
	}
	switch {
	case pnd.custom:
		call.Input = model.Input{Kind: model.InputText, Text: text}
	case text == "":
		call.Input = model.Input{Kind: model.InputJSON, JSON: []byte("{}")}
	case isJSON(text):
		call.Input = model.Input{Kind: model.InputJSON, JSON: []byte(text)}
	default:
		call.Input = model.Input{Kind: model.InputText, Text: text}
		if err := st.diag(model.DiagTruncated, model.SeverityWarning, fmt.Sprintf("assembled arguments for call %s are not valid JSON; kept as text", d.CallID)); err != nil {
			return err
		}
	}
	return st.complete(ctx, call, k, false, pnd)
}

func isJSON(s string) bool {
	var v any
	return jsonUnmarshal(s, &v) == nil
}

// evict drops an incomplete call with a diagnostic; its buffered results
// become orphans.
func (st *state) evict(k key, why string) error {
	pnd := st.active[k]
	st.remove(k)
	if err := st.diag(model.DiagBudgetExhausted, model.SeverityError, fmt.Sprintf("incomplete call %s in session %q dropped: %s", k.call, k.session, why)); err != nil {
		return err
	}
	for _, r := range pnd.results {
		if err := st.orphan(r); err != nil {
			return err
		}
	}
	return nil
}

func (st *state) result(r adapters.Result) error {
	k, _ := st.callKey(r.Session, r.CallID, r.Name, st.rcounts)
	if pnd, ok := st.active[k]; ok {
		pnd.results = append(pnd.results, r)
		return nil
	}
	done, ok := st.known[k]
	if !ok {
		return st.orphan(r)
	}
	id := done.id
	obs := r.Observation
	obs.Subject = id
	if r.CallID == "" {
		obs.Detail = strings.TrimSpace("associated positionally by name and order. " + obs.Detail)
	}
	ev := model.Event{Kind: model.EventObservation, Sequence: st.seq, Invocation: id, Observation: &obs}
	if r.Lifecycle != "" {
		ev.Lifecycle = []model.LifecycleEvent{{Phase: r.Lifecycle, Source: "provider"}}
	}
	return st.emit(ev)
}

func (st *state) orphan(r adapters.Result) error {
	obs := r.Observation
	return st.emit(model.Event{Kind: model.EventOrphan, Sequence: st.seq, Observation: &obs,
		Diagnostic: &model.Diagnostic{Code: "orphan_result", Severity: model.SeverityWarning, Message: fmt.Sprintf("result for call %q in session %q has no known call", r.CallID, r.Session)}})
}

func (st *state) lifecycle(l adapters.Lifecycle) error {
	k := key{l.Session, l.CallID}
	if pnd, ok := st.active[k]; ok {
		pnd.lifecyle = append(pnd.lifecyle, l.Event)
		return nil
	}
	if done, ok := st.known[k]; ok {
		return st.emit(model.Event{Kind: model.EventObservation, Sequence: st.seq, Invocation: done.id, Lifecycle: []model.LifecycleEvent{l.Event}})
	}
	return st.emit(model.Event{Kind: model.EventDiagnostic, Sequence: st.seq, Lifecycle: []model.LifecycleEvent{l.Event},
		Diagnostic: &model.Diagnostic{Code: "orphan_lifecycle", Severity: model.SeverityInfo, Message: fmt.Sprintf("lifecycle event for unknown call %q", l.CallID)}})
}

// flush reports calls still incomplete at end of stream.
func (st *state) flush() error {
	for _, k := range append([]key(nil), st.order...) {
		pnd := st.active[k]
		st.remove(k)
		if err := st.diag(model.DiagTruncated, model.SeverityError, fmt.Sprintf("call %s in session %q incomplete at end of stream (%d buffered bytes); not analyzed", k.call, k.session, pnd.buf.Len())); err != nil {
			return err
		}
		for _, r := range pnd.results {
			if err := st.orphan(r); err != nil {
				return err
			}
		}
	}
	return nil
}

// ReadLine reads one line, discarding the remainder of lines longer than
// max without buffering them in full.
func ReadLine(r *bufio.Reader, max int) ([]byte, bool, error) {
	var line []byte
	tooLong := false
	for {
		chunk, isPrefix, err := r.ReadLine()
		if err != nil {
			return line, tooLong, err
		}
		if !tooLong {
			if len(line)+len(chunk) > max {
				tooLong = true
				line = nil
			} else {
				line = append(line, chunk...)
			}
		}
		if !isPrefix {
			return line, tooLong, nil
		}
	}
}

func firstNonEmpty(xs ...string) string {
	for _, x := range xs {
		if x != "" {
			return x
		}
	}
	return ""
}
