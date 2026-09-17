// Package languages holds the embedded-language frontends (Python and
// JavaScript). They implement a documented semantic subset over tree-sitter
// syntax: lexical bindings, literal and bounded values, known
// filesystem/path/subprocess calls, and control-flow qualification. Function
// bodies are latent until called; everything else is an explicit unknown.
package languages

import (
	"fmt"
	"path"
	"strings"
	"time"

	ts "github.com/odvcencio/gotreesitter"

	"github.com/lastpersonlabs/toolparser/internal/analysis"
	"github.com/lastpersonlabs/toolparser/internal/languages/treesitter"
	"github.com/lastpersonlabs/toolparser/model"
	"github.com/lastpersonlabs/toolparser/rules"
)

// Enabled selects the languages that receive semantic analysis.
type Enabled struct {
	Python     bool
	JavaScript bool
}

// Analyzer returns the program hook for the engine. It reports false for
// languages that are disabled or unknown so the engine records them as
// unsupported programs.
func Analyzer(en Enabled) analysis.ProgramFunc {
	return func(b *analysis.Builder, parent string, p rules.Program, ctx analysis.Context) bool {
		switch strings.ToLower(p.Language) {
		case "python":
			if !en.Python {
				return false
			}
			return run(b, parent, p, ctx, treesitter.Python, (*pyFrontend).module)
		case "javascript", "js", "node", "typescript":
			if !en.JavaScript {
				return false
			}
			return run(b, parent, p, ctx, treesitter.JavaScript, (*jsFrontend).program)
		}
		return false
	}
}

// bindingKind says how a name became bound.
type bindingKind int

const (
	bindImport bindingKind = iota // qualified names a library object
	bindValue                     // holds an abstract value
	bindFunc                      // user-defined function in this program
	bindOpaque                    // assigned something we cannot model
)

type binding struct {
	kind      bindingKind
	qualified string      // for bindImport: "os.path", "pathlib.Path", "fs"
	value     model.Value // for bindValue
	fn        *ts.Node    // for bindFunc
	isPath    bool        // value is a pathlib.Path-like object
	shadowed  bool        // rebinding of a name that also has a library meaning
}

type scope struct {
	parent *scope
	names  map[string]*binding
}

func newScope(parent *scope) *scope { return &scope{parent: parent, names: map[string]*binding{}} }

func (s *scope) lookup(name string) *binding {
	for sc := s; sc != nil; sc = sc.parent {
		if b, ok := sc.names[name]; ok {
			return b
		}
	}
	return nil
}

func (s *scope) set(name string, b *binding) { s.names[name] = b }

// flow is the control-flow context of a statement inside a program.
type flow struct {
	guard *model.Guard
	reach model.Reachability
}

func (f flow) with(reach model.Reachability) flow {
	rank := map[model.Reachability]int{model.ReachAlways: 0, model.ReachConditional: 1, model.ReachRepeated: 2, model.ReachLatent: 3}
	if rank[reach] > rank[f.reach] {
		f.reach = reach
	}
	f.guard = nil
	return f
}

// frontend is the state shared by both language analyzers.
type frontend struct {
	b        *analysis.Builder
	lang     *ts.Language
	src      []byte
	sourceID string
	ctx      analysis.Context
	cwd      model.Value
	scope    *scope
	calling  map[string]int
	steps    int
	stopped  bool
	langName string
}

type moduleFunc[F any] func(F, string, *ts.Node, flow)

// run parses the program and hands the root to the language module walker.
func run[F any](b *analysis.Builder, parent string, p rules.Program, ctx analysis.Context, lang treesitter.Language, walk moduleFunc[F]) bool {
	src := b.Store.Text(p.SourceID)
	node := b.Node(parent)
	timeout := 2 * time.Second
	if dl, ok := b.Ctx.Deadline(); ok {
		if rem := time.Until(dl); rem < timeout {
			timeout = rem
		}
	}
	tree, err := treesitter.Parse(lang, src, timeout, b.Limits.SyntaxNodes)
	if err != nil {
		node.Syntax = model.SyntaxInvalid
		b.Diag(model.Diagnostic{Code: model.DiagSyntaxError, Severity: model.SeverityError, Node: parent, Message: err.Error()})
		b.MarkPartial()
		return true
	}
	defer tree.Release()
	switch tree.Status {
	case treesitter.StatusLimit:
		node.Syntax = model.SyntaxLimit
		b.LimitReached(parent, fmt.Sprintf("%s parse stopped: %s", lang, tree.Reason))
		return true
	case treesitter.StatusPartial:
		node.Syntax = model.SyntaxPartial
		b.Diag(model.Diagnostic{Code: model.DiagSyntaxError, Severity: model.SeverityWarning, Node: parent, Message: string(lang) + " source has syntax errors; only well-formed regions are analyzed"})
		b.MarkPartial()
	default:
		node.Syntax = model.SyntaxParsed
	}
	fe := &frontend{b: b, lang: tree.Lang, src: src, sourceID: p.SourceID, ctx: ctx, cwd: ctx.Cwd, scope: newScope(nil), calling: map[string]int{}, langName: string(lang)}
	var f F
	switch any(f).(type) {
	case *pyFrontend:
		f = any(&pyFrontend{frontend: fe}).(F)
	case *jsFrontend:
		f = any(&jsFrontend{frontend: fe}).(F)
	}
	reach := ctx.Reach
	if reach == "" {
		reach = model.ReachAlways
	}
	walk(f, parent, tree.Root, flow{reach: reach})
	return true
}

func (fe *frontend) span(n *ts.Node) model.SourceSpan {
	return model.SourceSpan{Source: fe.sourceID, Start: int(n.StartByte()), End: int(n.EndByte())}
}

func (fe *frontend) text(n *ts.Node) string { return n.Text(fe.src) }

func (fe *frontend) typ(n *ts.Node) string { return n.Type(fe.lang) }

func (fe *frontend) field(n *ts.Node, name string) *ts.Node { return n.ChildByFieldName(name, fe.lang) }

// named returns the named children of n.
func (fe *frontend) named(n *ts.Node) []*ts.Node {
	var out []*ts.Node
	for i := 0; i < n.NamedChildCount(); i++ {
		out = append(out, n.NamedChild(i))
	}
	return out
}

func (fe *frontend) nodeContext() model.NodeContext {
	return model.NodeContext{Cwd: fe.cwd, Namespace: fe.ctx.Namespace}
}

func (fe *frontend) actx(fl flow) analysis.Context {
	c := fe.ctx
	c.Cwd, c.Guard, c.Reach = fe.cwd, fl.guard, fl.reach
	return c
}

// step accounts one syntax node against the budget.
func (fe *frontend) step(parent string, n *ts.Node, fl flow) bool {
	if fe.stopped {
		return false
	}
	fe.steps++
	if fe.b.Limits.SyntaxNodes > 0 && fe.steps > fe.b.Limits.SyntaxNodes {
		fe.stopped = true
		fe.b.LimitReached(parent, fmt.Sprintf("more than %d %s syntax nodes", fe.b.Limits.SyntaxNodes, fe.langName))
		return false
	}
	if fe.b.Ctx != nil && fe.b.Ctx.Err() != nil {
		fe.stopped = true
		return false
	}
	return true
}

// unknown records an unmodelled region.
func (fe *frontend) unknown(parent string, n *ts.Node, fl flow, reason string) {
	sp := fe.span(n)
	fe.b.Unknown(parent, &sp, fe.actx(fl), model.DiagUnsupported, reason)
}

// container adds a structural node (if, loop, group, function).
func (fe *frontend) container(parent string, n *ts.Node, kind model.NodeKind, detail string, fl flow) string {
	sp := fe.span(n)
	return fe.b.AddNode(model.Node{Parent: parent, Kind: kind, Span: &sp, Guard: fl.guard, Reach: fl.reach, Syntax: model.SyntaxParsed, Detail: detail, Context: fe.nodeContext()})
}

// call adds a command node for a known library call and attaches actions.
// Every action states the binding assumption and the execution assumption.
func (fe *frontend) call(parent string, n *ts.Node, qualified string, args []model.Value, fl flow, bindingAssumption string, actions ...model.Action) string {
	sp := fe.span(n)
	id := fe.b.AddNode(model.Node{Parent: parent, Kind: model.NodeCommand, Span: &sp, Guard: fl.guard, Reach: fl.reach, Syntax: model.SyntaxParsed,
		Command: &model.Command{Executable: model.Value{Knowledge: model.KnowledgeDerived, Text: qualified}, Args: args, Rule: fe.langName + "/" + qualified,
			Assumptions: []string{bindingAssumption, "preceding statements executed without raising"}},
		Context: fe.nodeContext()})
	for _, a := range actions {
		a.Evidence.Basis = model.BasisBoundedValue
		a.Evidence.Rule, a.Evidence.RuleVersion = fe.langName+"/"+qualified, "1"
		a.Evidence.Spans = []model.SourceSpan{sp}
		a.Evidence.Assumptions = append([]string{bindingAssumption, "preceding statements executed without raising"}, a.Evidence.Assumptions...)
		for i := range a.Resources {
			if a.Resources[i].Namespace.Kind == "" {
				a.Resources[i].Namespace = fe.ctx.Namespace
			}
		}
		if !fe.b.AddAction(id, a) {
			break
		}
	}
	return id
}

// resource builds a resource for a value against the program's cwd.
func (fe *frontend) resource(v model.Value, role string) model.Resource {
	cmd := rules.Command{Cwd: fe.cwd, Namespace: fe.ctx.Namespace}
	r := cmd.Resource(v, role)
	if r.Knowledge == model.KnowledgeLiteral && !fe.cwd.Concrete() && !strings.HasPrefix(v.Text, "/") {
		// The interpreter's working directory is not known.
		r.Resolved = ""
	}
	return r
}

// joinPath concatenates two path values the way os.path.join / path.join /
// pathlib's / operator do.
func joinPath(a, b model.Value) model.Value {
	out := model.Value{Knowledge: model.KnowledgeDerived}
	switch {
	case !a.Concrete() || !b.Concrete():
		out.Knowledge = model.KnowledgeSymbolic
		out.Text = a.Text + "/" + b.Text
		out.Assumptions = append(append([]string(nil), a.Assumptions...), b.Assumptions...)
	case strings.HasPrefix(b.Text, "/"):
		out.Text = path.Clean(b.Text)
	default:
		out.Text = path.Join(a.Text, b.Text)
	}
	return out
}

func concat(a, b model.Value) model.Value {
	if a.Concrete() && b.Concrete() {
		return model.Value{Knowledge: model.KnowledgeDerived, Text: a.Text + b.Text}
	}
	return model.Value{Knowledge: model.KnowledgeSymbolic, Text: a.Text + b.Text, Assumptions: append(append([]string(nil), a.Assumptions...), b.Assumptions...)}
}

func symbolic(text, why string) model.Value {
	return model.Value{Knowledge: model.KnowledgeSymbolic, Text: text, Assumptions: []string{why}}
}

// openMode maps an open() mode / fs flag string to actions.
func openMode(mode string) (read, write bool, wm model.WriteMode) {
	switch {
	case strings.ContainsAny(mode, "w"):
		return strings.Contains(mode, "+"), true, model.WriteTruncate
	case strings.ContainsAny(mode, "a"):
		return strings.Contains(mode, "+"), true, model.WriteAppend
	case strings.ContainsAny(mode, "x"):
		return strings.Contains(mode, "+"), true, model.WriteCreateExclusive
	default:
		return true, strings.Contains(mode, "+"), model.WriteInPlace
	}
}

// fileActions builds read/write actions for a path and mode value.
func (fe *frontend) fileActions(p model.Value, mode model.Value, defaultMode string) []model.Action {
	var out []model.Action
	m := defaultMode
	var assumptions []string
	switch {
	case mode.Knowledge == "":
	case mode.Concrete():
		m = mode.Text
	default:
		out = append(out, model.Action{Kind: model.FSRead, Resources: []model.Resource{fe.resource(p, "target")}, Evidence: model.Evidence{Assumptions: []string{"open mode is not literal; read and write are both possible"}}})
		out = append(out, model.Action{Kind: model.FSWrite, Write: &model.WriteDetail{Mode: model.WriteUnknown}, Resources: []model.Resource{fe.resource(p, "target")}, Evidence: model.Evidence{Assumptions: []string{"open mode is not literal"}}})
		return out
	}
	read, write, wm := openMode(m)
	if read {
		out = append(out, model.Action{Kind: model.FSRead, Resources: []model.Resource{fe.resource(p, "target")}, Evidence: model.Evidence{Assumptions: assumptions}})
	}
	if write {
		out = append(out, model.Action{Kind: model.FSWrite, Write: &model.WriteDetail{Mode: wm}, Resources: []model.Resource{fe.resource(p, "target")}, Evidence: model.Evidence{Assumptions: assumptions}})
	}
	return out
}

// spawn hands a child process to the engine: a shell string becomes nested
// shell analysis, an argv becomes a command, anything else is unresolved.
func (fe *frontend) spawn(parent string, n *ts.Node, fl flow, qualified string, program model.Value, argv []model.Value, shell bool, cwd *model.Value) {
	actx := fe.actx(fl)
	if cwd != nil {
		switch {
		case cwd.Concrete() && strings.HasPrefix(cwd.Text, "/"):
			actx.Cwd = model.Value{Knowledge: model.KnowledgeDerived, Text: path.Clean(cwd.Text)}
		case cwd.Concrete() && fe.cwd.Concrete():
			actx.Cwd = model.Value{Knowledge: model.KnowledgeDerived, Text: path.Join(fe.cwd.Text, cwd.Text)}
		default:
			actx.Cwd = model.Value{Knowledge: model.KnowledgeSymbolic, Text: cwd.Text}
		}
	}
	sp := fe.span(n)
	launcher := rules.Command{Executable: model.Value{Knowledge: model.KnowledgeDerived, Text: qualified}, Args: append([]model.Value{program}, argv...), Span: sp}
	if shell {
		src := program
		if len(argv) > 0 && program.Concrete() {
			// Python: with shell=True only the first list element is the
			// command string; Node: spawn(cmd, args, {shell:true}) joins.
			joined := program
			for _, a := range argv {
				joined = concat(concat(joined, model.Literal(" ")), a)
			}
			src = joined
		}
		fe.b.RunProgram(parent, rules.Program{Language: "shell", Interpreter: qualified, Mode: "inline", Dialect: "sh", Source: &src}, launcher, actx)
		return
	}
	if !program.Concrete() {
		id := fe.call(parent, n, qualified, launcher.Args, fl, qualified+" denotes the standard library API")
		fe.b.AddAction(id, model.Action{Kind: model.ProcessUnresolved, Evidence: model.Evidence{Basis: model.BasisBoundedValue, Spans: []model.SourceSpan{sp}}, Process: &model.ProcessDetail{Executable: program, Args: argv, Reason: "child program is not literal"}})
		return
	}
	fe.b.RunCommand(parent, rules.Command{Executable: program, Args: argv, Span: sp, Wrappers: []string{qualified}}, actx)
}
