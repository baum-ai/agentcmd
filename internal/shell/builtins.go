package shell

import (
	"strings"

	"github.com/lastpersonlabs/toolparser/model"
	"github.com/lastpersonlabs/toolparser/rules"
)

// noEffect are builtins whose effects stay inside the shell process.
var noEffect = map[string]bool{
	"true": true, "false": true, ":": true, "echo": true, "printf": true, "test": true, "[": true, "read": true,
	"shift": true, "trap": true, "wait": true, "type": true, "hash": true, "umask": true, "ulimit": true, "times": true,
	"jobs": true, "fg": true, "bg": true, "kill": true, "getopts": true, "let": true, "local": true, "readonly": true,
	"set": true, "shopt": true, "break": true, "continue": true, "pwd": true, "dirs": true, "disown": true, "suspend": true,
	"unalias": true, "declare": true, "typeset": true, "mapfile": true, "readarray": true, "compgen": true, "complete": true,
}

// builtin handles shell builtins that change shell state or leave the shell.
// It returns handled=false for external commands.
func (l *lowerer) builtin(parent string, cmd rules.Command, st *state, ctx ctxState) (id string, exits, handled bool) {
	name := cmd.Executable.Text
	node := func(detail string) string {
		return l.b.AddNode(model.Node{Parent: parent, Kind: model.NodeCommand, Span: &cmd.Span, Guard: ctx.guard, Reach: ctx.reach, Syntax: model.SyntaxParsed, Detail: detail,
			Command: &model.Command{Executable: cmd.Executable, Args: cmd.Args, Wrappers: cmd.Wrappers, Rule: "builtin"}, Context: nodeContext(st, ctx)})
	}
	switch name {
	case "cd", "pushd":
		id = node(name)
		target := model.Value{Knowledge: model.KnowledgeSymbolic, Text: "$HOME", Assumptions: []string{"cd without operand goes to $HOME"}}
		o := rules.ParseOptions(cmd.Args, rules.OptionSpec{ShortFlags: "LPe@"})
		if len(o.Operands) > 0 {
			target = o.Operands[len(o.Operands)-1]
			if target.Concrete() && target.Text == "-" {
				target = model.Value{Knowledge: model.KnowledgeSymbolic, Text: "$OLDPWD"}
			}
		}
		st.changeDir(target)
		l.b.Node(id).Context = nodeContext(st, ctx)
		return id, false, true
	case "popd":
		id = node(name)
		st.cwd = model.Value{Knowledge: model.KnowledgeUnknown, Assumptions: []string{"directory stack is not modeled"}}
		return id, false, true
	case "export":
		id = node(name)
		for _, a := range cmd.Args {
			if k, v, ok := strings.Cut(a.Text, "="); ok && a.Concrete() {
				st.vars[k] = model.Literal(v)
				delete(st.unset, k)
			}
		}
		return id, false, true
	case "unset":
		id = node(name)
		for _, a := range cmd.Args {
			if a.Concrete() && !strings.HasPrefix(a.Text, "-") {
				delete(st.vars, a.Text)
				st.unset[a.Text] = true
			}
		}
		return id, false, true
	case "alias":
		id = node(name)
		for _, a := range cmd.Args {
			if k, _, ok := strings.Cut(a.Text, "="); ok && a.Concrete() {
				st.aliases[k] = true
			}
		}
		return id, false, true
	case "exit", "return", "logout":
		return node(name), ctx.guard == nil, true
	case "source", ".":
		id = node(name)
		if len(cmd.Args) > 0 {
			res := cmd.Resource(cmd.Args[0], "target")
			res.Namespace = ctx.namespace
			l.b.AddAction(id, model.Action{Kind: model.FSRead, Resources: []model.Resource{res}, Evidence: model.Evidence{Basis: model.BasisSyntax, Rule: "builtin/source", RuleVersion: "1", Spans: []model.SourceSpan{cmd.Span}}})
			l.b.AddAction(id, model.Action{Kind: model.ProcessUnresolved, Resources: []model.Resource{res}, Evidence: model.Evidence{Basis: model.BasisSyntax, Rule: "builtin/source", RuleVersion: "1", Spans: []model.SourceSpan{cmd.Span}},
				Process: &model.ProcessDetail{Executable: cmd.Executable, Args: cmd.Args, Shell: true, Reason: "sourced file contents are not part of the invocation"}})
		}
		st.invalidate("sourced code may have changed shell state")
		l.b.Diag(model.Diagnostic{Code: model.DiagMissingSnapshot, Severity: model.SeverityInfo, Node: id, Message: "sourced file contents unknown; working directory and variables are unknown afterwards"})
		l.b.MarkPartial()
		return id, false, true
	case "eval":
		id = node(name)
		var parts []string
		literal := true
		for _, a := range cmd.Args {
			literal = literal && a.Concrete()
			parts = append(parts, a.Text)
		}
		src := strings.Join(parts, " ")
		if literal && src != "" {
			// eval of a literal string is nested shell in the same process.
			p := rules.Program{Language: "shell", Interpreter: "eval", Mode: "inline", Dialect: string(l.dialect), Source: &model.Value{Knowledge: model.KnowledgeDerived, Text: src, Span: &cmd.Span}}
			l.b.RunProgram(id, p, cmd, l.actx(st, ctx))
			st.invalidate("eval may have changed shell state")
			return id, false, true
		}
		l.b.AddAction(id, model.Action{Kind: model.ProcessUnresolved, Evidence: model.Evidence{Basis: model.BasisSyntax, Rule: "builtin/eval", RuleVersion: "1", Spans: []model.SourceSpan{cmd.Span}},
			Process: &model.ProcessDetail{Executable: cmd.Executable, Args: cmd.Args, Shell: true, Reason: "eval argument is not literal"}})
		st.invalidate("eval of unknown code may have changed shell state")
		l.b.MarkPartial()
		return id, false, true
	}
	if noEffect[name] {
		return node("builtin without external effects"), false, true
	}
	return "", false, false
}
