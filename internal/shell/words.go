package shell

import (
	"strconv"
	"strings"

	"github.com/baum-ai/agentcmd/model"
	"mvdan.cc/sh/v3/syntax"
)

// word evaluates a word into an abstract value. Command substitutions inside
// the word are lowered as substitution nodes under parent before the value is
// returned, because they execute before the command that uses them.
func (l *lowerer) word(parent string, w *syntax.Word, st *state, ctx ctxState) model.Value {
	sp := l.span(w)
	v := l.parts(parent, w.Parts, st, ctx, false)
	// A single quoted part maps to the text between the quotes, which keeps
	// nested program sources byte-exact.
	if len(w.Parts) == 1 {
		switch q := w.Parts[0].(type) {
		case *syntax.SglQuoted:
			if !q.Dollar {
				sp = model.SourceSpan{Source: l.src, Start: sp.Start + 1, End: sp.End - 1}
			}
		case *syntax.DblQuoted:
			sp = model.SourceSpan{Source: l.src, Start: sp.Start + 1, End: sp.End - 1}
		}
	}
	v.Span = &sp
	return v
}

// parts concatenates word parts. quoted is true inside double quotes.
// Bounded alternatives of one part are carried through the concatenation.
func (l *lowerer) parts(parent string, parts []syntax.WordPart, st *state, ctx ctxState, quoted bool) model.Value {
	var text strings.Builder
	knowledge := model.KnowledgeLiteral
	var assumptions []string
	alts := []string{""}
	limit := l.b.Limits.JoinAlternatives
	for _, p := range parts {
		v := l.part(parent, p, st, ctx, quoted)
		text.WriteString(v.Text)
		assumptions = append(assumptions, v.Assumptions...)
		knowledge = weaker(knowledge, v.Knowledge)
		switch {
		case len(v.Alternatives) > 0:
			var next []string
			for _, prefix := range alts {
				for _, a := range v.Alternatives {
					next = append(next, prefix+a)
				}
			}
			if limit > 0 && len(next) > limit {
				knowledge = model.KnowledgeSymbolic
				assumptions = append(assumptions, "too many alternatives; widened")
				alts = nil
			} else {
				alts = next
			}
		case v.Concrete() || v.Knowledge == model.KnowledgeGlob || v.Knowledge == model.KnowledgeUnset:
			for i := range alts {
				alts[i] += v.Text
			}
		default:
			alts = nil
		}
	}
	out := model.Value{Knowledge: knowledge, Text: text.String(), Assumptions: dedupe(assumptions)}
	if len(alts) > 1 && (knowledge == model.KnowledgeLiteral || knowledge == model.KnowledgeDerived) {
		out.Knowledge = model.KnowledgeDerived
		out.Alternatives = alts
		out.Text = "{" + strings.Join(alts, "|") + "}"
	}
	return out
}

// weaker picks the less precise of two knowledge states for concatenation.
func weaker(a, b model.Knowledge) model.Knowledge {
	rank := map[model.Knowledge]int{model.KnowledgeLiteral: 0, model.KnowledgeDerived: 1, model.KnowledgeGlob: 2, model.KnowledgeSymbolic: 3, model.KnowledgeUnknown: 4}
	if b == model.KnowledgeUnset {
		return a // unset expands to nothing
	}
	if rank[b] > rank[a] {
		return b
	}
	return a
}

func (l *lowerer) part(parent string, p syntax.WordPart, st *state, ctx ctxState, quoted bool) model.Value {
	switch x := p.(type) {
	case *syntax.Lit:
		if quoted {
			return model.Literal(unescapeDouble(x.Value))
		}
		return unquotedLit(x.Value)
	case *syntax.SglQuoted:
		if x.Dollar {
			return model.Literal(unescapeANSI(x.Value))
		}
		return model.Literal(x.Value)
	case *syntax.DblQuoted:
		// Literal metacharacters inside quotes are literal; an expansion
		// whose value is already a glob match keeps that knowledge.
		return l.parts(parent, x.Parts, st, ctx, true)
	case *syntax.ParamExp:
		return l.param(x, st, ctx, quoted)
	case *syntax.CmdSubst:
		id := l.b.AddNode(model.Node{Parent: parent, Kind: model.NodeSubstitution, Span: spanPtr(l.span(x)), Guard: ctx.guard, Reach: ctx.reach, Detail: "$(...)", Syntax: model.SyntaxParsed, Context: nodeContext(st, ctx)})
		l.stmts(id, x.Stmts, st.clone(), ctx.plain())
		return model.Value{Knowledge: model.KnowledgeSymbolic, Text: l.text(x), Assumptions: []string{"command substitution output is not evaluated"}}
	case *syntax.ProcSubst:
		id := l.b.AddNode(model.Node{Parent: parent, Kind: model.NodeSubstitution, Span: spanPtr(l.span(x)), Guard: ctx.guard, Reach: ctx.reach, Detail: x.Op.String() + "...)", Syntax: model.SyntaxParsed, Context: nodeContext(st, ctx)})
		l.stmts(id, x.Stmts, st.clone(), ctx.plain())
		return model.Value{Knowledge: model.KnowledgeSymbolic, Text: l.text(x), Assumptions: []string{"process substitution yields a pipe path"}}
	case *syntax.ArithmExp:
		return model.Value{Knowledge: model.KnowledgeSymbolic, Text: l.text(x), Assumptions: []string{"arithmetic is not evaluated"}}
	case *syntax.ExtGlob:
		return model.Value{Knowledge: model.KnowledgeGlob, Text: l.text(x)}
	case *syntax.BraceExp:
		return model.Value{Knowledge: model.KnowledgeSymbolic, Text: l.text(x), Assumptions: []string{"brace expansion is not modeled"}}
	}
	return model.Value{Knowledge: model.KnowledgeSymbolic, Text: l.text(p), Assumptions: []string{"unsupported word part"}}
}

// param resolves a parameter expansion.
func (l *lowerer) param(x *syntax.ParamExp, st *state, ctx ctxState, quoted bool) model.Value {
	name := x.Param.Value
	symbolic := func(why string) model.Value {
		v := model.Value{Knowledge: model.KnowledgeSymbolic, Text: l.text(x)}
		if why != "" {
			v.Assumptions = []string{why}
		}
		return v
	}
	if x.Exp != nil || x.Slice != nil || x.Repl != nil || x.Length || x.Width || x.Index != nil || x.Excl || x.Names != 0 {
		return symbolic("parameter operators are not evaluated")
	}
	switch name {
	case "@", "*":
		if ctx.positional == nil {
			return symbolic("positional parameters are unknown")
		}
		var texts []string
		k := model.KnowledgeLiteral
		for _, a := range ctx.positional {
			texts = append(texts, a.Text)
			k = weaker(k, a.Knowledge)
		}
		v := model.Value{Knowledge: k, Text: strings.Join(texts, " ")}
		if !quoted {
			v.Assumptions = []string{"unquoted expansion may split into several words"}
		}
		return v
	case "#":
		if ctx.positional == nil {
			return symbolic("positional parameters are unknown")
		}
		return model.Literal(strconv.Itoa(len(ctx.positional)))
	case "?", "$", "!", "-", "0":
		return symbolic("special parameter is runtime state")
	}
	if n, err := strconv.Atoi(name); err == nil {
		if ctx.positional == nil {
			return symbolic("positional parameters are unknown")
		}
		if n >= 1 && n <= len(ctx.positional) {
			v := ctx.positional[n-1]
			if v.Knowledge == model.KnowledgeLiteral {
				v.Knowledge = model.KnowledgeDerived
			}
			return v
		}
		return model.Value{Knowledge: model.KnowledgeUnset}
	}
	v := st.lookup(name, ctx.env)
	switch v.Knowledge {
	case model.KnowledgeLiteral:
		v.Knowledge = model.KnowledgeDerived
		v.Assumptions = append(v.Assumptions, "$"+name+" holds its assigned value")
	case model.KnowledgeUnset:
		return model.Value{Knowledge: model.KnowledgeUnset}
	case model.KnowledgeSymbolic:
		if len(v.Assumptions) == 0 {
			v.Assumptions = []string{"$" + name + " is not assigned in this program"}
		}
	}
	if !quoted && !v.Concrete() {
		v.Assumptions = append(v.Assumptions, "unquoted expansion may split into several words")
	}
	if !quoted && v.Concrete() && strings.ContainsAny(v.Text, "*?[") {
		v.Knowledge = model.KnowledgeGlob
		v.Assumptions = append(v.Assumptions, "unquoted expansion is subject to pathname expansion")
	}
	v.Span = nil
	return v
}

// unquotedLit interprets an unquoted literal: backslash escapes, tilde and
// glob metacharacters.
func unquotedLit(raw string) model.Value {
	var b strings.Builder
	glob := false
	for i := 0; i < len(raw); i++ {
		c := raw[i]
		switch {
		case c == '\\' && i+1 < len(raw):
			i++
			if raw[i] != '\n' {
				b.WriteByte(raw[i])
			}
		case c == '*' || c == '?' || (c == '[' && strings.IndexByte(raw[i+1:], ']') >= 0):
			glob = true
			b.WriteByte(c)
		default:
			b.WriteByte(c)
		}
	}
	v := model.Literal(b.String())
	if strings.HasPrefix(v.Text, "~") {
		return model.Value{Knowledge: model.KnowledgeSymbolic, Text: v.Text, Assumptions: []string{"tilde expands to a home directory"}}
	}
	if glob {
		v.Knowledge = model.KnowledgeGlob
	}
	return v
}

// unescapeDouble removes the escapes that are special inside double quotes.
func unescapeDouble(raw string) string {
	var b strings.Builder
	for i := 0; i < len(raw); i++ {
		c := raw[i]
		if c == '\\' && i+1 < len(raw) {
			switch raw[i+1] {
			case '$', '`', '"', '\\':
				i++
				b.WriteByte(raw[i])
				continue
			case '\n':
				i++
				continue
			}
		}
		b.WriteByte(c)
	}
	return b.String()
}

// unescapeANSI handles the common $'...' escapes.
func unescapeANSI(raw string) string {
	r := strings.NewReplacer(`\n`, "\n", `\t`, "\t", `\\`, `\`, `\'`, "'", `\"`, `"`, `\r`, "\r")
	return r.Replace(raw)
}

func dedupe(xs []string) []string {
	if len(xs) < 2 {
		return xs
	}
	seen := map[string]bool{}
	out := xs[:0]
	for _, x := range xs {
		if !seen[x] {
			seen[x] = true
			out = append(out, x)
		}
	}
	return out
}
