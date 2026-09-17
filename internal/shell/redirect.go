package shell

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/lastpersonlabs/toolparser/model"
	"github.com/lastpersonlabs/toolparser/rules"
	"mvdan.cc/sh/v3/syntax"
)

// redirects adds one redirect node per redirection under owner, in source
// order, each carrying the file action it implies. Descriptor duplication
// yields no file action.
func (l *lowerer) redirects(owner string, redirs []*syntax.Redirect, st *state, ctx ctxState) {
	for _, r := range redirs {
		sp := l.span(r)
		fd := "1"
		if r.N != nil {
			fd = r.N.Value
		}
		if r.Op == syntax.RdrIn || r.Op == syntax.Hdoc || r.Op == syntax.DashHdoc || r.Op == syntax.WordHdoc || r.Op == syntax.DplIn {
			if r.N == nil {
				fd = "0"
			}
		}
		if r.N != nil && strings.HasPrefix(r.N.Value, "{") {
			fd = r.N.Value
		}
		id := l.b.AddNode(model.Node{Parent: owner, Kind: model.NodeRedirect, Span: &sp, Guard: ctx.guard, Reach: ctx.reach, Syntax: model.SyntaxParsed, Detail: fmt.Sprintf("fd %s %s", fd, r.Op.String()), Context: nodeContext(st, ctx)})
		cmd := rules.Command{Cwd: st.cwd, Namespace: ctx.namespace}
		ev := func() model.Evidence {
			return model.Evidence{Basis: model.BasisSyntax, Rule: "shell/redirect", RuleVersion: "1", Spans: []model.SourceSpan{sp}}
		}
		switch r.Op {
		case syntax.Hdoc, syntax.DashHdoc, syntax.WordHdoc:
			l.b.Node(id).Detail = "stdin from here-document"
			continue
		case syntax.DplIn, syntax.DplOut:
			target := l.word(owner, r.Word, st, ctx)
			if _, err := strconv.Atoi(strings.TrimSuffix(target.Text, "-")); err == nil || target.Text == "-" {
				l.b.Node(id).Detail = fmt.Sprintf("fd %s duplicates fd %s", fd, target.Text)
				continue
			}
			// >&word with a non-numeric word is a file redirection in Bash.
			if r.Op == syntax.DplOut {
				l.fileAction(id, model.FSWrite, &model.WriteDetail{Mode: model.WriteTruncate}, cmd.Resource(target, "output"), ev())
			} else {
				l.fileAction(id, model.FSRead, nil, cmd.Resource(target, "input"), ev())
			}
			continue
		}
		target := l.word(owner, r.Word, st, ctx)
		switch r.Op {
		case syntax.RdrOut, syntax.ClbOut, syntax.RdrAll, syntax.RdrAllClob:
			l.fileAction(id, model.FSWrite, &model.WriteDetail{Mode: model.WriteTruncate}, cmd.Resource(target, "output"), ev())
		case syntax.AppOut, syntax.AppAll, syntax.AppAllClob:
			l.fileAction(id, model.FSWrite, &model.WriteDetail{Mode: model.WriteAppend}, cmd.Resource(target, "output"), ev())
		case syntax.RdrIn:
			l.fileAction(id, model.FSRead, nil, cmd.Resource(target, "input"), ev())
		case syntax.RdrInOut:
			l.fileAction(id, model.FSRead, nil, cmd.Resource(target, "input"), ev())
			l.fileAction(id, model.FSWrite, &model.WriteDetail{Mode: model.WriteUnknown}, cmd.Resource(target, "output"), ev())
		default:
			l.b.Diag(model.Diagnostic{Code: model.DiagUnsupported, Node: id, Message: "redirection operator " + r.Op.String()})
		}
	}
}

func (l *lowerer) fileAction(node string, kind model.ActionKind, w *model.WriteDetail, res model.Resource, ev model.Evidence) {
	if res.Knowledge == model.KnowledgeUnknown {
		ev.Assumptions = append(ev.Assumptions, "redirection target is unknown")
	}
	l.b.AddAction(node, model.Action{Kind: kind, Write: w, Resources: []model.Resource{res}, Evidence: ev})
}

// heredoc extracts a here-document or here-string feeding stdin. A quoted
// delimiter suppresses expansion, so the body is literal and gets its own
// derived source; an unquoted body with expansions stays symbolic.
func (l *lowerer) heredoc(parent string, redirs []*syntax.Redirect, st *state, ctx ctxState) *rules.Stdin {
	var out *rules.Stdin
	for _, r := range redirs {
		switch r.Op {
		case syntax.WordHdoc:
			v := l.word(parent, r.Word, st, ctx)
			v.Text += "\n"
			out = &rules.Stdin{Value: v}
		case syntax.Hdoc, syntax.DashHdoc:
			if r.Hdoc == nil {
				continue
			}
			quoted := false
			for _, p := range r.Word.Parts {
				switch p.(type) {
				case *syntax.SglQuoted, *syntax.DblQuoted:
					quoted = true
				case *syntax.Lit:
					if strings.Contains(p.(*syntax.Lit).Value, `\`) {
						quoted = true
					}
				}
			}
			body := l.heredocBody(parent, r, quoted, st, ctx)
			out = &rules.Stdin{Value: body}
			if body.Concrete() {
				sp := *body.Span
				out.Source = l.b.Store.DeriveWhole("heredoc", []byte(body.Text), l.src, sp.Start, sp.End)
				body.Span = &model.SourceSpan{Source: out.Source, Start: 0, End: len(body.Text)}
				out.Value = body
			}
		}
	}
	return out
}

// heredocBody evaluates the body. The upstream word's end position covers
// the closing delimiter, so the body span is computed from the parts.
func (l *lowerer) heredocBody(parent string, r *syntax.Redirect, quoted bool, st *state, ctx ctxState) model.Value {
	var v model.Value
	rawLen := 0
	for _, p := range r.Hdoc.Parts {
		if lit, ok := p.(*syntax.Lit); ok {
			rawLen += len(lit.Value)
		} else {
			rawLen += int(p.End().Offset() - p.Pos().Offset())
		}
	}
	start := int(r.Hdoc.Pos().Offset())
	if quoted {
		var raw strings.Builder
		for _, p := range r.Hdoc.Parts {
			if lit, ok := p.(*syntax.Lit); ok {
				raw.WriteString(lit.Value)
			} else {
				raw.WriteString(l.text(p))
			}
		}
		v = model.Literal(raw.String())
		v.Assumptions = []string{"quoted here-document delimiter: body taken literally"}
	} else {
		v = l.parts(parent, r.Hdoc.Parts, st, ctx, true)
		v.Assumptions = append(v.Assumptions, "unquoted here-document delimiter: body subject to expansion")
	}
	if r.Op == syntax.DashHdoc {
		var b strings.Builder
		for _, line := range strings.SplitAfter(v.Text, "\n") {
			b.WriteString(strings.TrimLeft(line, "\t"))
		}
		v.Text = b.String()
		v.Assumptions = append(v.Assumptions, "<<- strips leading tabs")
	}
	if v.Knowledge == model.KnowledgeGlob {
		v.Knowledge = model.KnowledgeLiteral
	}
	v.Span = &model.SourceSpan{Source: l.src, Start: start, End: start + rawLen}
	return v
}
