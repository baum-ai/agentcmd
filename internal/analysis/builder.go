// Package analysis owns the result under construction: nodes, actions,
// diagnostics, deterministic IDs and budget accounting. Frontends (shell,
// languages, native) append to a Builder; the engine finalizes it.
package analysis

import (
	"context"
	"fmt"
	"path"
	"strings"

	"github.com/lastpersonlabs/toolparser/internal/source"
	"github.com/lastpersonlabs/toolparser/model"
	"github.com/lastpersonlabs/toolparser/native"
	"github.com/lastpersonlabs/toolparser/rules"
)

// Limits bound the work of one analysis. Zero values mean unlimited only in
// tests; the engine always fills defaults.
type Limits struct {
	InputFrameBytes   int `json:"input_frame_bytes"`
	SourceBytes       int `json:"source_bytes"`
	DerivedBytes      int `json:"derived_bytes"`
	NestingDepth      int `json:"nesting_depth"`
	SyntaxNodes       int `json:"syntax_nodes"`
	Actions           int `json:"actions"`
	JoinAlternatives  int `json:"join_alternatives"`
	StreamBufferBytes int `json:"stream_buffer_bytes"`
	StreamActiveCalls int `json:"stream_active_calls"`
}

// Context is the abstract execution context flowing through a frontend.
type Context struct {
	Cwd       model.Value
	Namespace model.Namespace
	Env       map[string]model.Value
	// Positional holds $1.. when the caller supplied them.
	Positional []model.Value
	Dialect    string
	Guard      *model.Guard
	Reach      model.Reachability
}

// NodeContext converts to the public form.
func (c Context) NodeContext() model.NodeContext {
	return model.NodeContext{Cwd: c.Cwd, Namespace: c.Namespace}
}

// ProgramFunc analyzes an embedded program under parent. It returns false
// when the language is not supported by the engine configuration.
type ProgramFunc func(b *Builder, parent string, p rules.Program, ctx Context) bool

// ShellFunc analyzes shell source under parent.
type ShellFunc func(b *Builder, parent string, src []byte, sourceID string, ctx Context) string

// Builder accumulates one analysis.
type Builder struct {
	Ctx    context.Context
	Store  *source.Store
	Rules  *rules.Registry
	Native *native.Registry
	Limits Limits

	// Program and Shell are set by the engine so frontends can recurse
	// without importing each other.
	Program ProgramFunc
	Shell   ShellFunc

	nodes   []model.Node
	actions []model.Action
	diags   []model.Diagnostic
	partial bool
	depth   int
	seen    map[string]bool
}

// New creates a builder over a store.
func New(ctx context.Context, store *source.Store, limits Limits) *Builder {
	return &Builder{Ctx: ctx, Store: store, Limits: limits, seen: map[string]bool{}}
}

// AddNode appends a node, assigning its ID. Parent must already exist.
func (b *Builder) AddNode(n model.Node) string {
	n.ID = fmt.Sprintf("n%d", len(b.nodes)+1)
	if n.Reach == "" {
		n.Reach = model.ReachAlways
	}
	if n.Context.Cwd.Knowledge == "" {
		n.Context.Cwd = model.Unknown()
	}
	if n.Context.Namespace.Kind == "" {
		n.Context.Namespace.Kind = model.NamespaceUnknown
	}
	b.nodes = append(b.nodes, n)
	return n.ID
}

// Node returns a pointer to a node for in-place amendment.
func (b *Builder) Node(id string) *model.Node {
	for i := range b.nodes {
		if b.nodes[i].ID == id {
			return &b.nodes[i]
		}
	}
	return nil
}

// AddAction attaches an action to a node, assigning its ID. It returns false
// once the action budget is exhausted, which is diagnosed once.
func (b *Builder) AddAction(node string, a model.Action) bool {
	if b.Limits.Actions > 0 && len(b.actions) >= b.Limits.Actions {
		b.LimitReached(node, fmt.Sprintf("more than %d actions", b.Limits.Actions))
		return false
	}
	a.ID = fmt.Sprintf("a%d", len(b.actions)+1)
	a.Node = node
	for i := range a.Resources {
		if a.Resources[i].Namespace.Kind == "" {
			a.Resources[i].Namespace.Kind = model.NamespaceUnknown
		}
		if a.Resources[i].Knowledge == "" {
			a.Resources[i].Knowledge = model.KnowledgeUnknown
		}
	}
	b.actions = append(b.actions, a)
	return true
}

// Diag records a diagnostic.
func (b *Builder) Diag(d model.Diagnostic) {
	if d.Severity == "" {
		d.Severity = model.SeverityWarning
	}
	b.diags = append(b.diags, d)
}

// MarkPartial flags the analysis as incomplete.
func (b *Builder) MarkPartial() { b.partial = true }

// LimitReached records a budget diagnostic once per message and marks the
// analysis partial.
func (b *Builder) LimitReached(node, msg string) {
	for _, d := range b.diags {
		if d.Code == model.DiagLimitReached && d.Message == msg {
			return
		}
	}
	b.partial = true
	b.Diag(model.Diagnostic{Code: model.DiagLimitReached, Severity: model.SeverityError, Message: msg, Node: node})
}

// Unknown adds an unknown-region node with a diagnostic.
func (b *Builder) Unknown(parent string, span *model.SourceSpan, ctx Context, code, reason string) string {
	id := b.AddNode(model.Node{Parent: parent, Kind: model.NodeUnknown, Span: span, Guard: ctx.Guard, Reach: ctx.Reach, Detail: reason, Context: ctx.NodeContext()})
	b.Diag(model.Diagnostic{Code: code, Message: reason, Node: id, Span: span})
	if code != model.DiagUnknownTool {
		b.partial = true
	}
	return id
}

// Enter records descent into nested source. It returns false when the depth
// or derived-byte budget is exhausted or the same source was already visited
// in this context (a cycle).
func (b *Builder) Enter(node string, sourceID string, ctx Context) bool {
	if b.Ctx != nil && b.Ctx.Err() != nil {
		return false
	}
	if b.Limits.NestingDepth > 0 && b.depth >= b.Limits.NestingDepth {
		b.LimitReached(node, fmt.Sprintf("nesting deeper than %d", b.Limits.NestingDepth))
		return false
	}
	if b.Limits.DerivedBytes > 0 && b.Store.DerivedBytes() > b.Limits.DerivedBytes {
		b.LimitReached(node, fmt.Sprintf("derived source exceeds %d bytes", b.Limits.DerivedBytes))
		return false
	}
	key := model.Fingerprint([]any{string(b.Store.Text(sourceID)), ctx.Cwd, ctx.Namespace, ctx.Dialect})
	if b.seen[key] {
		b.LimitReached(node, "cyclic nested source")
		return false
	}
	b.seen[key] = true
	b.depth++
	return true
}

// Leave undoes Enter.
func (b *Builder) Leave() { b.depth-- }

// Depth is the current nesting depth.
func (b *Builder) Depth() int { return b.depth }

// CheckSourceSize enforces the per-source limit before parsing.
func (b *Builder) CheckSourceSize(node string, n int) bool {
	if b.Limits.SourceBytes > 0 && n > b.Limits.SourceBytes {
		b.LimitReached(node, fmt.Sprintf("source of %d bytes exceeds %d", n, b.Limits.SourceBytes))
		return false
	}
	return true
}

// Finish assembles the analysis. IDs, versions and validation are the
// engine's responsibility.
func (b *Builder) Finish(retain int) model.Analysis {
	a := model.Analysis{
		SchemaVersion: model.SchemaVersion,
		Partial:       b.partial,
		Nodes:         b.nodes,
		Actions:       b.actions,
		Sources:       b.Store.Export(retain),
		Diagnostics:   b.diags,
	}
	if a.Nodes == nil {
		a.Nodes = []model.Node{}
	}
	if a.Actions == nil {
		a.Actions = []model.Action{}
	}
	return a
}

// maxWrappers bounds wrapper unwrapping (env sudo timeout ...).
const maxWrappers = 8

// RunCommand creates a command node under parent and dispatches it through
// the rule registry, unwrapping wrappers and recursing into programs.
func (b *Builder) RunCommand(parent string, cmd rules.Command, ctx Context) string {
	id := b.AddNode(model.Node{
		Parent: parent, Kind: model.NodeCommand, Span: &cmd.Span, Guard: ctx.Guard, Reach: ctx.Reach,
		Syntax:  model.SyntaxParsed,
		Command: &model.Command{Executable: cmd.Executable, Args: cmd.Args},
		Context: ctx.NodeContext(),
	})
	span := cmd.Span
	cmd.Cwd, cmd.Namespace = ctx.Cwd, ctx.Namespace
	for hops := 0; ; hops++ {
		node := b.Node(id)
		node.Command.Executable, node.Command.Args, node.Command.Wrappers = cmd.Executable, cmd.Args, cmd.Wrappers
		if !cmd.Executable.Concrete() {
			b.AddAction(id, model.Action{Kind: model.ProcessUnresolved, Evidence: model.Evidence{Basis: model.BasisSyntax, Spans: []model.SourceSpan{span}},
				Process: &model.ProcessDetail{Executable: cmd.Executable, Args: cmd.Args, Reason: "executable is not literal"}})
			return id
		}
		rule, ok := b.Rules.Lookup(cmd.Executable.Text)
		if !ok {
			b.AddAction(id, model.Action{Kind: model.ProcessExecute, Evidence: model.Evidence{Basis: model.BasisSyntax, Spans: []model.SourceSpan{span},
				Assumptions: []string{fmt.Sprintf("no rule for %q; effects unknown", path.Base(cmd.Executable.Text))}},
				Process: &model.ProcessDetail{Executable: cmd.Executable, Args: cmd.Args}})
			return id
		}
		node.Command.Rule = rule.ID + "@" + rule.Version
		node.Command.Assumptions = append(node.Command.Assumptions, fmt.Sprintf("%q denotes the standard executable", path.Base(cmd.Executable.Text)))
		res := rule.Apply(cmd)
		node.Command.Assumptions = append(node.Command.Assumptions, res.Assumptions...)
		for _, d := range res.Diagnostics {
			d.Node = id
			if d.Span == nil {
				d.Span = &span
			}
			b.Diag(d)
		}
		for _, a := range res.Actions {
			if a.Evidence.Rule == "" {
				a.Evidence.Rule, a.Evidence.RuleVersion = rule.ID, rule.Version
			}
			if len(a.Evidence.Spans) == 0 {
				a.Evidence.Spans = []model.SourceSpan{span}
			}
			if !b.AddAction(id, a) {
				return id
			}
		}
		if res.Program != nil {
			b.RunProgram(id, *res.Program, cmd, ctx)
		}
		if res.Wrapped == nil {
			return id
		}
		if hops >= maxWrappers {
			b.LimitReached(id, fmt.Sprintf("more than %d wrapper layers", maxWrappers))
			return id
		}
		inner := *res.Wrapped
		inner.Wrappers = append(append([]string(nil), cmd.Wrappers...), path.Base(cmd.Executable.Text))
		inner.Span, inner.Cwd = span, cmd.Cwd
		inner.Namespace = cmd.Namespace
		if res.Namespace != nil {
			inner.Namespace = *res.Namespace
			ctx.Namespace = *res.Namespace
			// Inside a remote or container namespace the working directory
			// of the host shell is meaningless.
			ctx.Cwd, inner.Cwd = model.Unknown(), model.Unknown()
			node.Context = ctx.NodeContext()
		}
		if inner.Stdin == nil {
			inner.Stdin = cmd.Stdin
		}
		cmd = inner
	}
}

// RunProgram adds a program node and hands it to the language frontend, or
// records why it could not be analyzed. cmd is the launcher command.
func (b *Builder) RunProgram(parent string, p rules.Program, cmd rules.Command, ctx Context) string {
	node := model.Node{Parent: parent, Kind: model.NodeProgram, Guard: ctx.Guard, Reach: ctx.Reach, Context: ctx.NodeContext(),
		Program: &model.Program{Language: p.Language, Interpreter: p.Interpreter, Mode: p.Mode, File: p.File}}
	if p.Source != nil {
		node.Span = p.Source.Span
	}
	id := b.AddNode(node)
	prog := b.Node(id).Program
	action := model.Action{Kind: model.ProcessInterpreter, Evidence: model.Evidence{Basis: model.BasisSyntax, Spans: []model.SourceSpan{cmd.Span}},
		Process: &model.ProcessDetail{Executable: cmd.Executable, Args: cmd.Args, Shell: p.Language == "shell"}}
	if p.Language == "shell" {
		action.Kind = model.ProcessShell
	}
	if p.File != nil {
		action.Resources = []model.Resource{*p.File}
	}
	switch {
	case p.Mode == "file", p.Mode == "module":
		// Script contents are not part of the invocation; M6 enrichment may
		// supply them from a snapshot.
		b.AddAction(id, action)
		b.Node(id).Syntax = model.SyntaxUnsupported
		b.Diag(model.Diagnostic{Code: model.DiagMissingSnapshot, Severity: model.SeverityInfo, Node: id,
			Message: fmt.Sprintf("%s program contents are not part of the invocation", p.Language)})
	case p.Source == nil || !p.Source.Concrete():
		action.Kind = model.ProcessUnresolved
		action.Process.Reason = "program source is not available literally"
		b.AddAction(id, action)
		b.Node(id).Syntax = model.SyntaxUnsupported
		b.Diag(model.Diagnostic{Code: model.DiagUnresolvedBinding, Node: id, Message: "program source is not available literally (unresolved expansion or unknown stdin)"})
	default:
		b.AddAction(id, action)
		if p.SourceID == "" && p.Source.Span != nil {
			p.SourceID = b.Store.DeriveWhole(p.Language, []byte(p.Source.Text), p.Source.Span.Source, p.Source.Span.Start, p.Source.Span.End)
		}
		prog.Source = p.SourceID
		if !b.CheckSourceSize(id, len(p.Source.Text)) {
			b.Node(id).Syntax = model.SyntaxLimit
			return id
		}
		if !b.Enter(id, p.SourceID, ctx) {
			b.Node(id).Syntax = model.SyntaxLimit
			return id
		}
		defer b.Leave()
		inner := ctx
		inner.Guard, inner.Reach = nil, model.ReachAlways
		inner.Dialect, inner.Positional = p.Dialect, p.Args
		inner.Env = mergeEnv(ctx.Env, cmd.Env)
		if p.Language == "shell" && b.Shell != nil {
			b.Shell(b, id, []byte(p.Source.Text), p.SourceID, inner)
			return id
		}
		if b.Program != nil && b.Program(b, id, p, inner) {
			return id
		}
		b.Node(id).Syntax = model.SyntaxUnsupported
		b.Diag(model.Diagnostic{Code: model.DiagUnsupported, Node: id, Message: fmt.Sprintf("%s analysis is not enabled", strings.ToLower(p.Language))})
		b.partial = true
	}
	return id
}

func mergeEnv(base, extra map[string]model.Value) map[string]model.Value {
	if len(extra) == 0 {
		return base
	}
	out := make(map[string]model.Value, len(base)+len(extra))
	for k, v := range base {
		out[k] = v
	}
	for k, v := range extra {
		out[k] = v
	}
	return out
}
