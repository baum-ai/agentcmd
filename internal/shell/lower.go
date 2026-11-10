package shell

import (
	"fmt"
	"strings"

	"github.com/baum-ai/agentcmd/internal/analysis"
	"github.com/baum-ai/agentcmd/model"
	"github.com/baum-ai/agentcmd/rules"
	"mvdan.cc/sh/v3/syntax"
)

// lowerer walks the mvdan AST and appends owned nodes to the builder.
type lowerer struct {
	b       *analysis.Builder
	src     string // source ID being lowered
	text0   []byte
	dialect Dialect
	ns      model.Namespace
	env     map[string]model.Value
	steps   int
	stopped bool
	// calling tracks functions being expanded to bound recursion.
	calling map[string]int
}

// ctxState is the control-flow context for one statement.
type ctxState struct {
	guard      *model.Guard
	reach      model.Reachability
	env        map[string]model.Value
	positional []model.Value
	namespace  model.Namespace
}

// plain drops the guard for nested lists: inside `X && (a; b)`, b is not
// guarded on X directly but inherits the conditional reachability.
func (c ctxState) plain() ctxState {
	c.guard = nil
	if c.reach == model.ReachAlways {
		return c
	}
	return c
}

func (c ctxState) with(guard *model.Guard, reach model.Reachability) ctxState {
	c.guard = guard
	c.reach = combineReach(c.reach, reach)
	return c
}

// combineReach keeps the weaker of two reachabilities.
func combineReach(outer, inner model.Reachability) model.Reachability {
	rank := map[model.Reachability]int{model.ReachAlways: 0, model.ReachConditional: 1, model.ReachRepeated: 2, model.ReachLatent: 3, model.ReachUnreachable: 4, model.ReachUnknown: 5}
	if rank[inner] > rank[outer] {
		return inner
	}
	return outer
}

type function struct {
	decl *syntax.FuncDecl
	node string
}

func nodeContext(st *state, ctx ctxState) model.NodeContext {
	return model.NodeContext{Cwd: st.cwd, Namespace: ctx.namespace}
}

func spanPtr(s model.SourceSpan) *model.SourceSpan { return &s }

func (l *lowerer) span(n syntax.Node) model.SourceSpan {
	return model.SourceSpan{Source: l.src, Start: int(n.Pos().Offset()), End: int(n.End().Offset())}
}

// text returns the original source text of a node.
func (l *lowerer) text(n syntax.Node) string {
	sp := l.span(n)
	if sp.Start < 0 || sp.End > len(l.text0) || sp.Start > sp.End {
		return ""
	}
	return string(l.text0[sp.Start:sp.End])
}

// step accounts one syntax node against the budget.
func (l *lowerer) step(parent string, sp model.SourceSpan, st *state, ctx ctxState) bool {
	if l.stopped {
		return false
	}
	l.steps++
	if l.b.Limits.SyntaxNodes > 0 && l.steps > l.b.Limits.SyntaxNodes {
		l.stopped = true
		l.b.LimitReached(parent, fmt.Sprintf("more than %d shell syntax nodes", l.b.Limits.SyntaxNodes))
		l.b.AddNode(model.Node{Parent: parent, Kind: model.NodeUnknown, Span: &sp, Guard: ctx.guard, Reach: ctx.reach, Syntax: model.SyntaxLimit, Detail: "analysis budget exhausted", Context: nodeContext(st, ctx)})
		return false
	}
	if l.b.Ctx != nil && l.b.Ctx.Err() != nil {
		l.stopped = true
		return false
	}
	return true
}

// stmts lowers a statement list in order. After an unconditional exit or
// return the remaining statements are unreachable.
func (l *lowerer) stmts(parent string, stmts []*syntax.Stmt, st *state, ctx ctxState) {
	for _, s := range stmts {
		_, exits := l.stmt(parent, s, st, ctx)
		if exits && ctx.reach != model.ReachUnreachable {
			ctx.reach = model.ReachUnreachable
		}
	}
}

// stmt lowers one statement and returns its node ID and whether it
// unconditionally leaves the enclosing shell (exit/return/exec).
func (l *lowerer) stmt(parent string, s *syntax.Stmt, st *state, ctx ctxState) (id string, exits bool) {
	sp := l.span(s)
	if !l.step(parent, sp, st, ctx) {
		return "", false
	}
	if s.Background {
		id = l.b.AddNode(model.Node{Parent: parent, Kind: model.NodeBackground, Span: &sp, Guard: ctx.guard, Reach: ctx.reach, Syntax: model.SyntaxParsed, Context: nodeContext(st, ctx)})
		inner := *s
		inner.Background = false
		// A background job runs in a subshell; its state does not escape.
		l.stmt(id, &inner, st.clone(), ctx.plain())
		return id, false
	}
	if s.Coprocess {
		return l.b.Unknown(parent, &sp, l.actx(st, ctx), model.DiagUnsupported, "coprocess"), false
	}
	switch c := s.Cmd.(type) {
	case nil:
		// Redirection-only statement: `> file` truncates without a command.
		id = l.b.AddNode(model.Node{Parent: parent, Kind: model.NodeCommand, Span: &sp, Guard: ctx.guard, Reach: ctx.reach, Syntax: model.SyntaxParsed, Detail: "redirection only",
			Command: &model.Command{Executable: model.Value{Knowledge: model.KnowledgeUnset}}, Context: nodeContext(st, ctx)})
		l.redirects(id, s.Redirs, st, ctx)
		return id, false
	case *syntax.CallExpr:
		return l.call(parent, s, c, st, ctx)
	case *syntax.BinaryCmd:
		return l.binary(parent, s, c, st, ctx)
	case *syntax.Subshell:
		id = l.compound(parent, s, model.NodeSubshell, "", st, ctx)
		l.stmts(id, c.Stmts, st.clone(), ctx.plain())
		return id, false
	case *syntax.Block:
		id = l.compound(parent, s, model.NodeGroup, "", st, ctx)
		l.stmts(id, c.Stmts, st, ctx.plain())
		return id, false
	case *syntax.IfClause:
		return l.ifClause(parent, s, c, st, ctx), false
	case *syntax.CaseClause:
		return l.caseClause(parent, s, c, st, ctx), false
	case *syntax.WhileClause:
		id = l.compound(parent, s, model.NodeLoop, map[bool]string{false: "while", true: "until"}[c.Until], st, ctx)
		body := st.clone()
		l.stmts(id, c.Cond, body, ctx.plain().with(nil, model.ReachRepeated))
		l.stmts(id, c.Do, body, ctx.plain().with(nil, model.ReachRepeated))
		*st = *join(st, body, l.b.Limits.JoinAlternatives)
		return id, false
	case *syntax.ForClause:
		return l.forClause(parent, s, c, st, ctx), false
	case *syntax.FuncDecl:
		if c.Name == nil || c.Body == nil {
			// Anonymous (zsh-style) function: its body runs immediately in
			// some dialects; report it rather than guess.
			return l.b.Unknown(parent, &sp, l.actx(st, ctx), model.DiagUnsupported, "anonymous function"), false
		}
		id = l.b.AddNode(model.Node{Parent: parent, Kind: model.NodeFunction, Span: &sp, Guard: ctx.guard, Reach: model.ReachLatent, Syntax: model.SyntaxParsed, Detail: c.Name.Value, Context: nodeContext(st, ctx)})
		st.funcs[c.Name.Value] = &function{decl: c, node: id}
		return id, false
	case *syntax.DeclClause:
		return l.decl(parent, s, c, st, ctx), false
	case *syntax.ArithmCmd, *syntax.TestClause, *syntax.LetClause:
		id = l.b.AddNode(model.Node{Parent: parent, Kind: model.NodeCommand, Span: &sp, Guard: ctx.guard, Reach: ctx.reach, Syntax: model.SyntaxParsed, Detail: "evaluation without external effects",
			Command: &model.Command{Executable: model.Literal(strings.TrimSpace(strings.SplitN(l.text(s), " ", 2)[0])), Rule: "builtin"}, Context: nodeContext(st, ctx)})
		l.redirects(id, s.Redirs, st, ctx)
		return id, false
	case *syntax.TimeClause:
		if c.Stmt == nil {
			return l.b.Unknown(parent, &sp, l.actx(st, ctx), model.DiagUnsupported, "empty time clause"), false
		}
		return l.stmt(parent, c.Stmt, st, ctx)
	}
	return l.b.Unknown(parent, &sp, l.actx(st, ctx), model.DiagUnsupported, fmt.Sprintf("shell construct %T", s.Cmd)), false
}

func (l *lowerer) actx(st *state, ctx ctxState) analysis.Context {
	return analysis.Context{Cwd: st.cwd, Namespace: ctx.namespace, Env: ctx.env, Positional: ctx.positional, Dialect: string(l.dialect), Guard: ctx.guard, Reach: ctx.reach}
}

// compound adds a container node and its redirections.
func (l *lowerer) compound(parent string, s *syntax.Stmt, kind model.NodeKind, detail string, st *state, ctx ctxState) string {
	sp := l.span(s)
	id := l.b.AddNode(model.Node{Parent: parent, Kind: kind, Span: &sp, Guard: ctx.guard, Reach: ctx.reach, Syntax: model.SyntaxParsed, Detail: detail, Context: nodeContext(st, ctx)})
	l.redirects(id, s.Redirs, st, ctx)
	return id
}

// binary lowers && || | |&.
func (l *lowerer) binary(parent string, s *syntax.Stmt, c *syntax.BinaryCmd, st *state, ctx ctxState) (string, bool) {
	sp := l.span(s)
	switch c.Op {
	case syntax.Pipe, syntax.PipeAll:
		id := l.compound(parent, s, model.NodePipeline, c.Op.String(), st, ctx)
		// Each element runs in its own subshell (Bash default without
		// lastpipe): state changes do not escape.
		for _, part := range flattenPipe(c) {
			l.stmt(id, part, st.clone(), ctx.plain())
		}
		return id, false
	case syntax.AndStmt, syntax.OrStmt:
		kind, on := model.NodeAnd, model.GuardSuccess
		if c.Op == syntax.OrStmt {
			kind, on = model.NodeOr, model.GuardFailure
		}
		id := l.compound(parent, s, kind, c.Op.String(), st, ctx)
		left, leftExits := l.stmt(id, c.X, st, ctx.plain())
		if c.X.Negated {
			on = map[model.GuardCondition]model.GuardCondition{model.GuardSuccess: model.GuardFailure, model.GuardFailure: model.GuardSuccess}[on]
		}
		var guard *model.Guard
		if left != "" {
			guard = &model.Guard{Node: left, On: on}
		}
		// Optimistic state: assume the left side succeeded (recorded on the
		// values it changed), so for `a || b` the right side did not run.
		right := st.clone()
		_, rightExits := l.stmt(id, c.Y, right, ctx.plain().with(guard, model.ReachConditional))
		if c.Op == syntax.AndStmt {
			*st = *right
		}
		return id, leftExits && rightExits
	}
	return l.b.Unknown(parent, &sp, l.actx(st, ctx), model.DiagUnsupported, "binary operator "+c.Op.String()), false
}

// flattenPipe turns the (left-associative) binary pipe tree into a list.
func flattenPipe(c *syntax.BinaryCmd) []*syntax.Stmt {
	var out []*syntax.Stmt
	for _, side := range []*syntax.Stmt{c.X, c.Y} {
		if inner, ok := side.Cmd.(*syntax.BinaryCmd); ok && (inner.Op == syntax.Pipe || inner.Op == syntax.PipeAll) && len(side.Redirs) == 0 && !side.Negated && !side.Background {
			out = append(out, flattenPipe(inner)...)
			continue
		}
		out = append(out, side)
	}
	return out
}

func (l *lowerer) ifClause(parent string, s *syntax.Stmt, c *syntax.IfClause, st *state, ctx ctxState) string {
	id := l.compound(parent, s, model.NodeIf, "if", st, ctx)
	l.ifChain(id, c, st, ctx.plain())
	return id
}

// ifChain lowers if/elif/else, joining branch states.
func (l *lowerer) ifChain(parent string, c *syntax.IfClause, st *state, ctx ctxState) {
	if len(c.Cond) == 0 { // else branch
		l.stmts(parent, c.Then, st, ctx.with(nil, model.ReachConditional))
		return
	}
	var condID string
	for _, cs := range c.Cond {
		condID, _ = l.stmt(parent, cs, st, ctx)
	}
	var okGuard, failGuard *model.Guard
	if condID != "" {
		okGuard, failGuard = &model.Guard{Node: condID, On: model.GuardSuccess}, &model.Guard{Node: condID, On: model.GuardFailure}
	}
	thenState := st.clone()
	l.stmts(parent, c.Then, thenState, ctx.with(okGuard, model.ReachConditional))
	elseState := st.clone()
	if c.Else != nil {
		sub := l.b.AddNode(model.Node{Parent: parent, Kind: model.NodeIf, Span: spanPtr(l.span(c.Else)), Guard: failGuard, Reach: combineReach(ctx.reach, model.ReachConditional), Syntax: model.SyntaxParsed, Detail: "else", Context: nodeContext(st, ctx)})
		l.ifChain(sub, c.Else, elseState, ctx.plain())
	}
	*st = *join(thenState, elseState, l.b.Limits.JoinAlternatives)
}

func (l *lowerer) caseClause(parent string, s *syntax.Stmt, c *syntax.CaseClause, st *state, ctx ctxState) string {
	id := l.compound(parent, s, model.NodeIf, "case", st, ctx)
	l.word(id, c.Word, st, ctx.plain())
	joined := st.clone() // no pattern matched
	for _, item := range c.Items {
		branch := st.clone()
		l.stmts(id, item.Stmts, branch, ctx.plain().with(nil, model.ReachConditional))
		joined = join(joined, branch, l.b.Limits.JoinAlternatives)
	}
	*st = *joined
	return id
}

func (l *lowerer) forClause(parent string, s *syntax.Stmt, c *syntax.ForClause, st *state, ctx ctxState) string {
	id := l.compound(parent, s, model.NodeLoop, "for", st, ctx)
	body := st.clone()
	switch loop := c.Loop.(type) {
	case *syntax.WordIter:
		var items []model.Value
		for _, w := range loop.Items {
			items = append(items, l.word(id, w, st, ctx.plain()))
		}
		if loop.Items == nil || len(loop.InPos.String()) == 0 && len(items) == 0 {
			items = ctx.positional
		}
		body.vars[loop.Name.Value] = loopVar(loop.Name.Value, items, l.b.Limits.JoinAlternatives)
	case *syntax.CStyleLoop:
		body.vars = st.clone().vars
		body.invalidatedVars(l.text(loop))
	}
	l.stmts(id, c.Do, body, ctx.plain().with(nil, model.ReachRepeated))
	*st = *join(st, body, l.b.Limits.JoinAlternatives)
	return id
}

// invalidatedVars is a hook for C-style loops whose variables are unknown.
func (s *state) invalidatedVars(expr string) {
	// Assignments inside arithmetic are not modeled; nothing to record
	// beyond leaving the variables symbolic on lookup.
	_ = expr
}

// loopVar is the abstract value of a for-loop variable over items.
func loopVar(name string, items []model.Value, limit int) model.Value {
	if items == nil {
		return model.Value{Knowledge: model.KnowledgeSymbolic, Text: "$" + name, Assumptions: []string{"iterates over unknown positional parameters"}}
	}
	var alts []string
	for _, it := range items {
		switch {
		case it.Concrete():
			alts = append(alts, it.Text)
		case it.Knowledge == model.KnowledgeGlob:
			return model.Value{Knowledge: model.KnowledgeGlob, Text: it.Text, Assumptions: []string{"one match of the glob " + it.Text}}
		default:
			return model.Value{Knowledge: model.KnowledgeSymbolic, Text: "$" + name, Assumptions: []string{"iterates over " + it.Text}}
		}
	}
	if len(alts) == 1 {
		return model.Value{Knowledge: model.KnowledgeDerived, Text: alts[0], Assumptions: []string{"single loop item"}}
	}
	if len(alts) > limit {
		return model.Value{Knowledge: model.KnowledgeSymbolic, Text: "$" + name, Assumptions: []string{fmt.Sprintf("iterates over %d literal items", len(alts))}}
	}
	return model.Value{Knowledge: model.KnowledgeDerived, Text: "{" + strings.Join(alts, "|") + "}", Alternatives: alts, Assumptions: []string{"one of the loop items"}}
}

// decl lowers export/declare/local/readonly/typeset.
func (l *lowerer) decl(parent string, s *syntax.Stmt, c *syntax.DeclClause, st *state, ctx ctxState) string {
	sp := l.span(s)
	id := l.b.AddNode(model.Node{Parent: parent, Kind: model.NodeAssignment, Span: &sp, Guard: ctx.guard, Reach: ctx.reach, Syntax: model.SyntaxParsed, Detail: c.Variant.Value, Context: nodeContext(st, ctx)})
	for _, a := range c.Args {
		l.assign(id, a, st, ctx)
	}
	l.redirects(id, s.Redirs, st, ctx)
	return id
}

// assign records one assignment in the state.
func (l *lowerer) assign(parent string, a *syntax.Assign, st *state, ctx ctxState) {
	if a.Name == nil {
		return
	}
	name := a.Name.Value
	switch {
	case a.Array != nil || a.Index != nil:
		st.vars[name] = model.Value{Knowledge: model.KnowledgeSymbolic, Text: "$" + name, Assumptions: []string{"arrays are not modeled"}}
	case a.Value == nil:
		if !a.Naked {
			st.vars[name] = model.Literal("")
		}
	case a.Append:
		prev := st.lookup(name, ctx.env)
		v := l.word(parent, a.Value, st, ctx)
		if prev.Concrete() && v.Concrete() {
			st.vars[name] = model.Value{Knowledge: model.KnowledgeDerived, Text: prev.Text + v.Text}
		} else {
			st.vars[name] = model.Value{Knowledge: model.KnowledgeSymbolic, Text: "$" + name}
		}
	default:
		v := l.word(parent, a.Value, st, ctx)
		v.Span = nil
		if v.Knowledge == model.KnowledgeGlob {
			v.Knowledge = model.KnowledgeLiteral // no globbing in assignments
		}
		st.vars[name] = v
	}
	delete(st.unset, name)
}

// call lowers a simple command: assignments, builtins, functions, aliases,
// external commands and their redirections.
func (l *lowerer) call(parent string, s *syntax.Stmt, c *syntax.CallExpr, st *state, ctx ctxState) (string, bool) {
	sp := l.span(s)
	if len(c.Args) == 0 {
		id := l.b.AddNode(model.Node{Parent: parent, Kind: model.NodeAssignment, Span: &sp, Guard: ctx.guard, Reach: ctx.reach, Syntax: model.SyntaxParsed, Context: nodeContext(st, ctx)})
		for _, a := range c.Assigns {
			l.assign(id, a, st, ctx)
		}
		l.redirects(id, s.Redirs, st, ctx)
		return id, false
	}
	// Redirections are opened before the command runs and before word
	// expansion of the command itself completes; they get their own nodes
	// under the command node, in order.
	words := make([]model.Value, 0, len(c.Args))
	for _, w := range c.Args {
		if !l.step(parent, sp, st, ctx) {
			return "", false
		}
		words = append(words, l.word(parent, w, st, ctx))
	}
	env := map[string]model.Value{}
	for _, a := range c.Assigns {
		if a.Name != nil && a.Value != nil {
			env[a.Name.Value] = l.word(parent, a.Value, st, ctx)
		}
	}
	cmd := rules.Command{Executable: words[0], Args: words[1:], Span: sp, Env: env}
	if h := l.heredoc(parent, s.Redirs, st, ctx); h != nil {
		cmd.Stdin = h
	}
	actx := l.actx(st, ctx)
	// Strip shell-level prefixes that do not change the command's meaning.
	var wrappers []string
	for cmd.Executable.Concrete() {
		name := cmd.Executable.Text
		if (name == "command" || name == "builtin" || name == "exec" || name == "nohup") && len(cmd.Args) > 0 && cmd.Args[0].Concrete() && !strings.HasPrefix(cmd.Args[0].Text, "-") {
			wrappers = append(wrappers, name)
			cmd.Executable, cmd.Args = cmd.Args[0], cmd.Args[1:]
			continue
		}
		break
	}
	cmd.Wrappers = wrappers
	var id string
	exits := false
	switch {
	case !cmd.Executable.Concrete():
		id = l.b.RunCommand(parent, cmd, actx)
	case st.funcs[cmd.Executable.Text] != nil && len(wrappers) == 0:
		id = l.functionCall(parent, cmd, st, ctx, st.funcs[cmd.Executable.Text])
	case st.aliases[cmd.Executable.Text] && len(wrappers) == 0:
		cmd.Executable = model.Value{Knowledge: model.KnowledgeSymbolic, Text: cmd.Executable.Text, Assumptions: []string{"name is an alias defined in this program"}, Span: cmd.Executable.Span}
		id = l.b.RunCommand(parent, cmd, actx)
	default:
		var handled bool
		id, exits, handled = l.builtin(parent, cmd, st, ctx)
		if !handled {
			id = l.b.RunCommand(parent, cmd, actx)
		}
	}
	if id != "" {
		l.redirects(id, s.Redirs, st, ctx)
	}
	return id, exits
}

// functionCall expands a function defined earlier in this program at its
// call site, once per nesting level, with the call's positional arguments.
func (l *lowerer) functionCall(parent string, cmd rules.Command, st *state, ctx ctxState, fn *function) string {
	id := l.b.AddNode(model.Node{Parent: parent, Kind: model.NodeCommand, Span: &cmd.Span, Guard: ctx.guard, Reach: ctx.reach, Syntax: model.SyntaxParsed, Detail: "function call",
		Command: &model.Command{Executable: cmd.Executable, Args: cmd.Args, Rule: "function", Assumptions: []string{"invokes the function defined at " + fn.node}}, Context: nodeContext(st, ctx)})
	name := cmd.Executable.Text
	if l.calling[name] >= 1 {
		l.b.Diag(model.Diagnostic{Code: model.DiagLimitReached, Severity: model.SeverityInfo, Node: id, Message: "recursive function call not expanded"})
		return id
	}
	l.calling[name]++
	defer func() { l.calling[name]-- }()
	inner := ctx.plain()
	inner.positional = cmd.Args
	l.stmt(id, fn.decl.Body, st, inner)
	return id
}
