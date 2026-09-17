package shell

import (
	"github.com/lastpersonlabs/toolparser/internal/analysis"
	"github.com/lastpersonlabs/toolparser/model"
	"github.com/lastpersonlabs/toolparser/rules"
	"mvdan.cc/sh/v3/syntax"
)

// Analyze parses src as shell and lowers it under parent. The dialect comes
// from ctx.Dialect; an empty dialect is Bash with a stated assumption.
func Analyze(b *analysis.Builder, parent string, src []byte, sourceID string, ctx analysis.Context) string {
	d := Dialect(ctx.Dialect)
	span := &model.SourceSpan{Source: sourceID, Start: 0, End: len(src)}
	node := model.Node{Parent: parent, Kind: model.NodeShell, Span: span, Guard: ctx.Guard, Reach: ctx.Reach, Detail: string(d), Context: ctx.NodeContext()}
	if d == "" {
		node.Detail = string(DialectBash)
	}
	id := b.AddNode(node)
	if !b.CheckSourceSize(id, len(src)) {
		b.Node(id).Syntax = model.SyntaxLimit
		return id
	}
	f, err := parse(src, d)
	if err != nil {
		se, _ := err.(*SyntaxError)
		status, code := model.SyntaxInvalid, model.DiagSyntaxError
		if se != nil {
			switch se.Status {
			case StatusPartial:
				status, code = model.SyntaxPartial, model.DiagTruncated
			case StatusUnsupported:
				status, code = model.SyntaxUnsupported, model.DiagUnsupported
			}
			span = &model.SourceSpan{Source: sourceID, Start: se.Offset, End: len(src)}
		}
		b.Node(id).Syntax = status
		b.Diag(model.Diagnostic{Code: code, Severity: model.SeverityError, Message: err.Error(), Node: id, Span: span})
		b.MarkPartial()
		return id
	}
	b.Node(id).Syntax = model.SyntaxParsed
	if !d.Certified() {
		b.Diag(model.Diagnostic{Code: model.DiagUnsupported, Node: id, Message: "semantic analysis is not certified for dialect " + string(d)})
	}
	l := &lowerer{b: b, src: sourceID, ctx: ctx}
	l.stmts(id, f.Stmts, ctx)
	return id
}

// lowerer walks the mvdan AST. M1 handles literal simple commands only;
// everything else is an explicit unknown region.
type lowerer struct {
	b   *analysis.Builder
	src string
	ctx analysis.Context
}

func (l *lowerer) span(n syntax.Node) model.SourceSpan {
	return model.SourceSpan{Source: l.src, Start: int(n.Pos().Offset()), End: int(n.End().Offset())}
}

func (l *lowerer) stmts(parent string, stmts []*syntax.Stmt, ctx analysis.Context) {
	for _, st := range stmts {
		l.stmt(parent, st, ctx)
	}
}

func (l *lowerer) stmt(parent string, st *syntax.Stmt, ctx analysis.Context) {
	sp := l.span(st)
	call, ok := st.Cmd.(*syntax.CallExpr)
	if !ok || st.Background || st.Negated || len(st.Redirs) > 0 || len(call.Assigns) > 0 || len(call.Args) == 0 {
		l.b.Unknown(parent, &sp, ctx, model.DiagUnsupported, "shell construct not lowered")
		return
	}
	cmd := rules.Command{Span: sp}
	words := make([]model.Value, 0, len(call.Args))
	for _, w := range call.Args {
		lit := w.Lit()
		wsp := l.span(w)
		if lit == "" {
			words = append(words, model.Value{Knowledge: model.KnowledgeSymbolic, Text: string(l.b.Store.Text(l.src)[wsp.Start:wsp.End]), Span: &wsp})
			continue
		}
		words = append(words, model.Value{Knowledge: model.KnowledgeLiteral, Text: lit, Span: &wsp})
	}
	cmd.Executable, cmd.Args = words[0], words[1:]
	l.b.RunCommand(parent, cmd, ctx)
}
