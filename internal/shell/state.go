package shell

import (
	"fmt"
	"sort"
	"strings"

	"github.com/baum-ai/agentcmd/model"
)

// state is the abstract shell state at one program point. Subshells and
// pipeline elements operate on clones so their changes do not escape.
type state struct {
	cwd   model.Value
	vars  map[string]model.Value // assigned in this program; absent means unknown or env
	unset map[string]bool
	funcs map[string]*function
	// aliases defined in this program; a name in here is not the standard
	// executable of that name.
	aliases map[string]bool
	// invalidated is set once unknown code (source, eval) may have changed
	// anything, after which unknown variables are symbolic and cwd unknown.
	invalidated bool
}

func newState(cwd model.Value) *state {
	return &state{cwd: cwd, vars: map[string]model.Value{}, unset: map[string]bool{}, funcs: map[string]*function{}, aliases: map[string]bool{}}
}

func (s *state) clone() *state {
	c := &state{cwd: s.cwd, vars: make(map[string]model.Value, len(s.vars)), unset: make(map[string]bool, len(s.unset)), funcs: s.funcs, aliases: s.aliases, invalidated: s.invalidated}
	for k, v := range s.vars {
		c.vars[k] = v
	}
	for k := range s.unset {
		c.unset[k] = true
	}
	return c
}

// invalidate forgets everything unknown code could have changed.
func (s *state) invalidate(reason string) {
	s.cwd = model.Value{Knowledge: model.KnowledgeUnknown, Assumptions: []string{reason}}
	s.vars = map[string]model.Value{}
	s.unset = map[string]bool{}
	s.invalidated = true
}

// lookup returns a variable's value: assigned, unset, from the supplied
// environment, or symbolic.
func (s *state) lookup(name string, env map[string]model.Value) model.Value {
	if v, ok := s.vars[name]; ok {
		return v
	}
	if s.unset[name] {
		return model.Value{Knowledge: model.KnowledgeUnset}
	}
	if v, ok := env[name]; ok && !s.invalidated {
		return v
	}
	return model.Symbolic("$" + name)
}

// join merges the state after two alternative paths, keeping bounded
// alternatives and widening to symbolic beyond limit. See ADR 0007.
func join(a, b *state, limit int) *state {
	out := a.clone()
	out.cwd = joinValue(a.cwd, b.cwd, "<cwd>", limit)
	out.invalidated = a.invalidated || b.invalidated
	names := map[string]bool{}
	for k := range a.vars {
		names[k] = true
	}
	for k := range b.vars {
		names[k] = true
	}
	for k := range a.unset {
		names[k] = true
	}
	for k := range b.unset {
		names[k] = true
	}
	for name := range names {
		va, vb := a.lookup(name, nil), b.lookup(name, nil)
		delete(out.unset, name)
		out.vars[name] = joinValue(va, vb, "$"+name, limit)
	}
	return out
}

// joinValue merges two abstract values.
func joinValue(a, b model.Value, symbol string, limit int) model.Value {
	if valueEqual(a, b) {
		return a
	}
	alts := map[string]bool{}
	add := func(v model.Value) bool {
		switch {
		case len(v.Alternatives) > 0:
			for _, x := range v.Alternatives {
				alts[x] = true
			}
		case v.Concrete():
			alts[v.Text] = true
		case v.Knowledge == model.KnowledgeUnset:
			alts[""] = true
		default:
			return false
		}
		return true
	}
	if !add(a) || !add(b) || len(alts) > limit {
		return model.Value{Knowledge: model.KnowledgeSymbolic, Text: symbol, Assumptions: []string{"value differs between branches"}}
	}
	list := make([]string, 0, len(alts))
	for x := range alts {
		list = append(list, x)
	}
	sort.Strings(list)
	return model.Value{Knowledge: model.KnowledgeDerived, Text: "{" + strings.Join(list, "|") + "}", Alternatives: list, Assumptions: []string{"one of the listed branch values"}}
}

func valueEqual(a, b model.Value) bool {
	if a.Knowledge != b.Knowledge || a.Text != b.Text || len(a.Alternatives) != len(b.Alternatives) {
		return false
	}
	for i := range a.Alternatives {
		if a.Alternatives[i] != b.Alternatives[i] {
			return false
		}
	}
	return true
}

// changeDir applies cd/pushd to the working directory under the recorded
// assumption that the change succeeded.
func (s *state) changeDir(target model.Value) {
	assume := fmt.Sprintf("directory change to %s succeeded", describe(target))
	switch {
	case target.Concrete() && strings.HasPrefix(target.Text, "/"):
		s.cwd = model.Value{Knowledge: model.KnowledgeDerived, Text: cleanPath(target.Text), Assumptions: []string{assume}}
	case target.Concrete() && s.cwd.Concrete():
		s.cwd = model.Value{Knowledge: model.KnowledgeDerived, Text: cleanPath(s.cwd.Text + "/" + target.Text), Assumptions: []string{assume}}
	case target.Concrete() && len(s.cwd.Alternatives) > 0:
		alts := make([]string, len(s.cwd.Alternatives))
		for i, d := range s.cwd.Alternatives {
			alts[i] = cleanPath(d + "/" + target.Text)
		}
		s.cwd = model.Value{Knowledge: model.KnowledgeDerived, Text: "{" + strings.Join(alts, "|") + "}", Alternatives: alts, Assumptions: []string{assume}}
	case target.Knowledge == model.KnowledgeSymbolic:
		text := target.Text
		if !strings.HasPrefix(text, "/") && s.cwd.Concrete() {
			text = s.cwd.Text + "/" + text
		}
		s.cwd = model.Value{Knowledge: model.KnowledgeSymbolic, Text: text, Assumptions: []string{assume}}
	default:
		s.cwd = model.Value{Knowledge: model.KnowledgeUnknown, Assumptions: []string{assume}}
	}
}

func describe(v model.Value) string {
	if v.Text == "" {
		return string(v.Knowledge)
	}
	return v.Text
}
