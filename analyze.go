// Package agentcmd analyzes coding-agent tool invocations without executing
// them. It produces a deterministic, evidence-backed model.Analysis.
//
// The root package depends only on the model, the rule/native contracts and
// the shell frontend. Built-in rules (rules/standard), bindings
// (native/standard), adapters, streaming, rendering and the Python and
// JavaScript frontends (languages, which links tree-sitter) are opt-in
// packages.
package agentcmd

import (
	"context"
	"errors"
	"fmt"
	"path"
	"strings"

	"github.com/baum-ai/agentcmd/internal/analysis"
	"github.com/baum-ai/agentcmd/internal/shell"
	"github.com/baum-ai/agentcmd/internal/source"
	"github.com/baum-ai/agentcmd/model"
	"github.com/baum-ai/agentcmd/native"
	"github.com/baum-ai/agentcmd/rules"
	"github.com/baum-ai/agentcmd/snapshot"
)

// Version of the analyzer, recorded in every result.
const Version = "1.0.0"

// Config is frozen by New. The zero value analyzes structure only: no
// command rules, no native bindings, no embedded languages.
type Config struct {
	Limits Limits
	// Rules are command rules, selected explicitly (rules/standard.Rules()).
	Rules []rules.Rule
	// Native are tool-contract bindings (native/standard.Bindings()).
	Native []native.Binding
	// Languages enables embedded-language semantics for the languages the
	// Frontend supports.
	Languages Languages
	// Frontend supplies the embedded-language analyzers
	// (languages.Frontend()). It is optional: the root package does not link
	// the tree-sitter runtime, so without a Frontend Python and JavaScript
	// programs are reported as unsupported whatever Languages says.
	Frontend Frontend
	// RetainSourceBytes is the largest source text kept in results.
	RetainSourceBytes int
	// Resolver optionally supplies script files and package manifests at the
	// invocation's snapshot. Without it nothing outside the input is read.
	Resolver snapshot.Resolver
}

// Frontend is an optional embedded-language analyzer; see package languages.
type Frontend = analysis.Frontend

// Engine analyzes invocations. It is immutable and safe for concurrent use.
type Engine struct {
	cfg     Config
	rules   *rules.Registry
	native  *native.Registry
	options string
}

// ErrPartial is joined onto errors returned alongside a usable partial
// analysis, so callers can tell them from configuration and I/O failures.
var ErrPartial = errors.New("partial analysis")

// New validates and freezes a configuration.
func New(cfg Config) (*Engine, error) {
	cfg.Limits = fillLimits(cfg.Limits)
	if cfg.RetainSourceBytes == 0 {
		cfg.RetainSourceBytes = 4096
	}
	rr, err := rules.NewRegistry(cfg.Rules...)
	if err != nil {
		return nil, fmt.Errorf("agentcmd: %w", err)
	}
	nr, err := native.NewRegistry(cfg.Native...)
	if err != nil {
		return nil, fmt.Errorf("agentcmd: %w", err)
	}
	e := &Engine{cfg: cfg, rules: rr, native: nr}
	e.options = model.Fingerprint([]any{cfg.Limits, cfg.Languages, cfg.Frontend.Runtime, cfg.RetainSourceBytes, rr.Fingerprint(), nr.Fingerprint(), cfg.Resolver != nil})
	return e, nil
}

func (e *Engine) versions() model.Versions {
	v := model.Versions{Analyzer: Version, Ruleset: e.rules.Fingerprint() + "/" + e.native.Fingerprint(), ShellParser: shell.ParserVersion}
	if e.cfg.Frontend.Program != nil && (e.cfg.Languages.Python || e.cfg.Languages.JavaScript) {
		v.LanguageRuntime = e.cfg.Frontend.Runtime
	}
	return v
}

// Analyze analyzes one invocation. It never executes the invocation or reads
// the host environment. Expected incompleteness is reported through
// Analysis.Partial and diagnostics; errors are reserved for invalid input,
// cancellation and configuration problems.
func (e *Engine) Analyze(ctx context.Context, inv model.Invocation) (model.Analysis, error) {
	if err := inv.Validate(); err != nil {
		return model.Analysis{}, err
	}
	if n := inputSize(inv.Input); n > e.cfg.Limits.InputFrameBytes {
		return model.Analysis{}, fmt.Errorf("agentcmd: input of %d bytes exceeds frame limit %d", n, e.cfg.Limits.InputFrameBytes)
	}
	store := source.New(rootKind(inv.Input), rootText(inv.Input))
	b := e.builder(ctx, store)
	b.Snapshot = inv.Context.Snapshot
	actx := contextFrom(inv.Context)
	root := b.AddNode(model.Node{Kind: model.NodeInvocation, Detail: inv.Tool.Name, Context: actx.NodeContext()})

	switch inv.Input.Kind {
	case model.InputJSON, model.InputText:
		e.analyzeNative(b, root, &inv, actx)
	case model.InputArgv:
		exe, argv := argvValues(inv.Input.Executable, inv.Input.Argv)
		b.RunCommand(root, rules.Command{Executable: exe, Args: argv, Span: model.SourceSpan{Source: "s0", End: len(store.Text("s0"))}}, actx)
	case model.InputSource:
		span := model.SourceSpan{Source: "s0", End: len(inv.Input.Source)}
		p := rules.Program{Language: inv.Input.Language, Mode: "inline", SourceID: "s0", Source: &model.Value{Knowledge: model.KnowledgeLiteral, Text: inv.Input.Source, Span: &span}}
		b.RunProgram(root, p, rules.Command{Executable: model.Literal(inv.Input.Language), Span: span}, actx)
	case model.InputPatch:
		e.analyzeNative(b, root, &inv, actx)
	case model.InputBlob:
		b.Unknown(root, nil, actx, model.DiagMissingSnapshot, "blob content is caller-owned and was not supplied")
	}
	return e.finish(ctx, b, &inv)
}

// AnalyzeShell analyzes shell source with an explicit execution context.
func (e *Engine) AnalyzeShell(ctx context.Context, src []byte, execution model.ExecutionContext) (model.Analysis, error) {
	inv := model.Invocation{ID: "shell", Tool: model.ToolIdentity{Name: "shell"}, Input: model.Input{Kind: model.InputText, Text: string(src)}, Context: execution}
	if len(src) > e.cfg.Limits.InputFrameBytes {
		return model.Analysis{}, fmt.Errorf("agentcmd: input of %d bytes exceeds frame limit %d", len(src), e.cfg.Limits.InputFrameBytes)
	}
	store := source.New("shell", src)
	b := e.builder(ctx, store)
	b.Snapshot = execution.Snapshot
	actx := contextFrom(execution)
	root := b.AddNode(model.Node{Kind: model.NodeInvocation, Detail: "shell", Context: actx.NodeContext()})
	shell.Analyze(b, root, src, "s0", actx)
	return e.finish(ctx, b, &inv)
}

func (e *Engine) builder(ctx context.Context, store *source.Store) *analysis.Builder {
	b := analysis.New(ctx, store, e.cfg.Limits)
	b.Rules, b.Native = e.rules, e.native
	b.Shell = shell.Analyze
	if fe := e.cfg.Frontend.Program; fe != nil {
		b.Program = func(b *analysis.Builder, parent string, p rules.Program, ctx analysis.Context) bool {
			switch analysis.LanguageKey(p.Language) {
			case "python":
				if !e.cfg.Languages.Python {
					return false
				}
			case "javascript":
				if !e.cfg.Languages.JavaScript {
					return false
				}
			}
			return fe(b, parent, p, ctx)
		}
	}
	b.Resolver = e.cfg.Resolver
	return b
}

func (e *Engine) finish(ctx context.Context, b *analysis.Builder, inv *model.Invocation) (model.Analysis, error) {
	a := b.Finish(e.cfg.RetainSourceBytes)
	a.ID = model.AnalysisID(inv, e.versions(), e.options)
	a.Invocation = inv.ID
	a.Versions = e.versions()
	if err := a.Validate(); err != nil {
		return model.Analysis{}, fmt.Errorf("agentcmd: internal error: produced invalid analysis: %w", err)
	}
	if err := ctx.Err(); err != nil {
		a.Partial = true
		return a, fmt.Errorf("%w: %w", ErrPartial, err)
	}
	return a, nil
}

func (e *Engine) analyzeNative(b *analysis.Builder, root string, inv *model.Invocation, actx analysis.Context) {
	node := model.Node{Parent: root, Kind: model.NodeNative, Span: &model.SourceSpan{Source: "s0", End: len(b.Store.Text("s0"))}, Context: actx.NodeContext(),
		Native: &model.NativeCall{Namespace: inv.Tool.Namespace, Tool: inv.Tool.Name}}
	binding, ok := e.native.Lookup(inv.Tool)
	if !ok && inv.Input.Kind == model.InputPatch {
		// Patch input needs no tool binding: the format is self-describing.
		binding, ok = native.Binding{ID: "native/patch", Version: "1", Tool: inv.Tool.Name, Bind: func(inv *model.Invocation) (native.Result, error) {
			return native.PatchResult(inv, inv.Input.Patch)
		}}, true
	}
	if !ok {
		id := b.AddNode(node)
		b.Diag(model.Diagnostic{Code: model.DiagUnknownTool, Severity: model.SeverityInfo, Node: id, Message: fmt.Sprintf("no binding for tool %s/%s; input kept opaque", inv.Tool.Namespace, inv.Tool.Name)})
		return
	}
	node.Native.Binding, node.Native.BindingVersion = binding.ID, binding.Version
	id := b.AddNode(node)
	res, err := binding.Bind(inv)
	if err != nil {
		b.Diag(model.Diagnostic{Code: model.DiagUnsupported, Severity: model.SeverityError, Node: id, Message: "binding " + binding.ID + ": " + err.Error()})
		b.MarkPartial()
		return
	}
	n := b.Node(id)
	n.Detail = res.Detail
	n.Syntax = model.SyntaxParsed
	for _, d := range res.Diagnostics {
		d.Node = id
		b.Diag(d)
	}
	for _, a := range res.Actions {
		a.Evidence.Rule, a.Evidence.RuleVersion = binding.ID, binding.Version
		a.Evidence.Assumptions = append(a.Evidence.Assumptions, res.Assumptions...)
		if res.Hint {
			a.Evidence.Basis = model.BasisHeuristic
			a.Evidence.Assumptions = append(a.Evidence.Assumptions, "classification derived from declared tool annotations, not a verified contract")
		}
		b.AddAction(id, a)
	}
	if res.Exec != nil {
		execCtx := actx
		if res.Exec.Workdir != "" {
			execCtx.Cwd = model.Value{Knowledge: model.KnowledgeLiteral, Text: res.Exec.Workdir}
			if !strings.HasPrefix(res.Exec.Workdir, "/") && actx.Cwd.Concrete() {
				execCtx.Cwd.Text = path.Join(actx.Cwd.Text, res.Exec.Workdir)
			}
		}
		exe, argv := argvValues(res.Exec.Executable, res.Exec.Args)
		b.RunCommand(id, rules.Command{Executable: exe, Args: argv, Span: model.SourceSpan{Source: "s0", End: len(b.Store.Text("s0"))}}, execCtx)
	}
	shells := res.Shells
	if res.Shell != nil {
		shells = append([]native.ShellSource{*res.Shell}, shells...)
	}
	for _, sh := range shells {
		sid := "s0"
		switch {
		case sh.Span != nil && inv.Input.Kind != model.InputJSON && sh.Span.Start >= 0 && sh.Span.Start <= sh.Span.End && sh.Span.End <= len(b.Store.Text("s0")):
			sid = b.Store.DeriveWhole("shell", []byte(sh.Text), "s0", sh.Span.Start, sh.Span.End)
		case sh.Span != nil:
			// The program text is decoded from the input, so the nested
			// text maps approximately onto the whole input.
			sid = b.Store.DeriveWhole("shell", []byte(sh.Text), "s0", 0, len(b.Store.Text("s0")))
		case inv.Input.Kind == model.InputJSON:
			// The command string is a JSON field; unescaping makes the map
			// approximate, so the whole input is the parent range.
			sid = b.Store.DeriveWhole("shell", []byte(sh.Text), "s0", 0, len(b.Store.Text("s0")))
		}
		shellCtx := actx
		if sh.Dialect != "" {
			shellCtx.Dialect = sh.Dialect
		}
		switch {
		case sh.WorkdirUnknown:
			shellCtx.Cwd = model.Value{Knowledge: model.KnowledgeUnknown, Assumptions: []string{"the tool call's working directory is not statically known"}}
		case sh.Workdir != "":
			switch {
			case strings.HasPrefix(sh.Workdir, "/"):
				shellCtx.Cwd = model.Value{Knowledge: model.KnowledgeLiteral, Text: path.Clean(sh.Workdir)}
			case actx.Cwd.Concrete():
				shellCtx.Cwd = model.Value{Knowledge: model.KnowledgeLiteral, Text: path.Join(actx.Cwd.Text, sh.Workdir)}
			default:
				shellCtx.Cwd = model.Value{Knowledge: model.KnowledgeUnknown, Assumptions: []string{"relative working directory of the tool call, and the invocation's working directory is not known"}}
			}
		}
		shell.Analyze(b, id, []byte(sh.Text), sid, shellCtx)
	}
}

func contextFrom(ec model.ExecutionContext) analysis.Context {
	ctx := analysis.Context{Cwd: model.Unknown(), Namespace: ec.Namespace, Reach: model.ReachAlways, Env: map[string]model.Value{}}
	if ec.WorkingDir.Known {
		ctx.Cwd = model.Value{Knowledge: model.KnowledgeLiteral, Text: ec.WorkingDir.Value}
	}
	if ec.Shell.Known {
		ctx.Dialect = ec.Shell.Value
	}
	if ctx.Namespace.Kind == "" {
		ctx.Namespace.Kind = model.NamespaceUnknown
	}
	for k, f := range ec.Env {
		if f.Known {
			ctx.Env[k] = model.Literal(f.Value)
		}
	}
	return ctx
}

func rootKind(in model.Input) string {
	switch in.Kind {
	case model.InputJSON:
		return "json"
	case model.InputText:
		return "text"
	case model.InputArgv:
		return "argv"
	case model.InputSource:
		return in.Language
	case model.InputPatch:
		return "patch"
	}
	return "blob"
}

func rootText(in model.Input) []byte {
	switch in.Kind {
	case model.InputJSON:
		return []byte(in.JSON)
	case model.InputText:
		return []byte(in.Text)
	case model.InputArgv:
		var b []byte
		b = append(b, in.Executable...)
		for _, a := range in.Argv {
			b = append(b, 0)
			b = append(b, a...)
		}
		return b
	case model.InputSource:
		return []byte(in.Source)
	case model.InputPatch:
		return []byte(in.Patch)
	}
	return nil
}

func inputSize(in model.Input) int { return len(rootText(in)) }

// argvValues builds literal values for an argv. When the root source is the
// NUL-joined argv (argv input) each value gets its byte span; otherwise the
// values carry no span and nested sources map approximately onto the input.
func argvValues(executable string, args []string) (model.Value, []model.Value) {
	exe := model.Literal(executable)
	argv := make([]model.Value, len(args))
	for i, a := range args {
		argv[i] = model.Literal(a)
	}
	return exe, argv
}
