package shell

import (
	"github.com/lastpersonlabs/agentcmd/internal/analysis"
	"github.com/lastpersonlabs/agentcmd/model"
)

// Analyze parses src as shell and lowers it under parent. The dialect comes
// from ctx.Dialect; an empty dialect is Bash.
func Analyze(b *analysis.Builder, parent string, src []byte, sourceID string, ctx analysis.Context) string {
	d := Dialect(ctx.Dialect)
	if d == "" {
		d = DialectBash
	}
	span := &model.SourceSpan{Source: sourceID, Start: 0, End: len(src)}
	id := b.AddNode(model.Node{Parent: parent, Kind: model.NodeShell, Span: span, Guard: ctx.Guard, Reach: ctx.Reach, Detail: string(d), Context: ctx.NodeContext()})
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
		b.MarkPartial()
	}
	l := &lowerer{b: b, src: sourceID, text0: src, dialect: d, ns: ctx.Namespace, env: ctx.Env, calling: map[string]int{}}
	st := newState(ctx.Cwd)
	reach := ctx.Reach
	if reach == "" {
		reach = model.ReachAlways
	}
	l.stmts(id, f.Stmts, st, ctxState{reach: reach, env: ctx.Env, positional: ctx.Positional, namespace: ctx.Namespace})
	return id
}
