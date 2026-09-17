// Package toolparser analyzes coding-agent tool invocations without executing
// them. It produces a deterministic, evidence-backed model.Analysis.
//
// The root package depends only on the model, the rule/native contracts and
// the internal frontends. Built-in rules (rules/standard), bindings
// (native/standard), adapters, streaming and rendering are opt-in packages.
package toolparser

import (
	"context"
	"errors"
	"fmt"
	"path"
	"strings"

	"github.com/lastpersonlabs/toolparser/internal/analysis"
	"github.com/lastpersonlabs/toolparser/internal/languages"
	"github.com/lastpersonlabs/toolparser/internal/languages/treesitter"
	"github.com/lastpersonlabs/toolparser/internal/shell"
	"github.com/lastpersonlabs/toolparser/internal/source"
	"github.com/lastpersonlabs/toolparser/model"
	"github.com/lastpersonlabs/toolparser/native"
	"github.com/lastpersonlabs/toolparser/rules"
	"github.com/lastpersonlabs/toolparser/snapshot"
)

// Version of the analyzer, recorded in every result.
const Version = "0.1.0"

// Config is frozen by New. The zero value analyzes structure only: no
// command rules, no native bindings, no embedded languages.
type Config struct {
	Limits Limits
	// Rules are command rules, selected explicitly (rules/standard.Rules()).
	Rules []rules.Rule
	// Native are tool-contract bindings (native/standard.Bindings()).
	Native []native.Binding
	// Languages enables embedded-language semantics.
	Languages Languages
	// RetainSourceBytes is the largest source text kept in results.
	RetainSourceBytes int
	// Resolver optionally supplies script files and package manifests at the
	// invocation's snapshot. Without it nothing outside the input is read.
	Resolver snapshot.Resolver
}

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
		return nil, fmt.Errorf("toolparser: %w", err)
	}
	nr, err := native.NewRegistry(cfg.Native...)
	if err != nil {
		return nil, fmt.Errorf("toolparser: %w", err)
	}
	e := &Engine{cfg: cfg, rules: rr, native: nr}
	e.options = model.Fingerprint([]any{cfg.Limits, cfg.Languages, cfg.RetainSourceBytes, rr.Fingerprint(), nr.Fingerprint(), cfg.Resolver != nil})
	return e, nil
}

func (e *Engine) versions() model.Versions {
	v := model.Versions{Analyzer: Version, Ruleset: e.rules.Fingerprint() + "/" + e.native.Fingerprint(), ShellParser: shell.ParserVersion}
	if e.cfg.Languages.Python || e.cfg.Languages.JavaScript {
		v.LanguageRuntime = treesitter.Version
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
		return model.Analysis{}, fmt.Errorf("toolparser: input of %d bytes exceeds frame limit %d", n, e.cfg.Limits.InputFrameBytes)
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
		return model.Analysis{}, fmt.Errorf("toolparser: input of %d bytes exceeds frame limit %d", len(src), e.cfg.Limits.InputFrameBytes)
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
	b.Program = languages.Analyzer(languages.Enabled{Python: e.cfg.Languages.Python, JavaScript: e.cfg.Languages.JavaScript})
	b.Resolver = e.cfg.Resolver
	return b
}

func (e *Engine) finish(ctx context.Context, b *analysis.Builder, inv *model.Invocation) (model.Analysis, error) {
	a := b.Finish(e.cfg.RetainSourceBytes)
	a.ID = model.AnalysisID(inv, e.versions(), e.options)
	a.Invocation = inv.ID
	a.Versions = e.versions()
	if err := a.Validate(); err != nil {
		return model.Analysis{}, fmt.Errorf("toolparser: internal error: produced invalid analysis: %w", err)
	}
	if err := ctx.Err(); err != nil {
		a.Partial = true
		return a, fmt.Errorf("%w: %w", ErrPartial, err)
	}
	return a, nil
}

func (e *Engine) analyzeNative(b *analysis.Builder, root string, inv *model.Invocation, actx analysis.Context) {
	node := model.Node{Parent: root, Kind: model.NodeNative, Span: &model.SourceSpan{Source: "s0", End: b.Store.Len()}, Context: actx.NodeContext(),
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
	if res.Shell != nil {
		sid := "s0"
		if inv.Input.Kind == model.InputJSON {
			// The command string is a JSON field; unescaping makes the map
			// approximate, so the whole input is the parent range.
			sid = b.Store.DeriveWhole("shell", []byte(res.Shell.Text), "s0", 0, b.Store.Len())
		}
		shellCtx := actx
		if res.Shell.Dialect != "" {
			shellCtx.Dialect = res.Shell.Dialect
		}
		shell.Analyze(b, id, []byte(res.Shell.Text), sid, shellCtx)
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
