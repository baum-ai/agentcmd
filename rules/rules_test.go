package rules

import (
	"testing"

	"github.com/baum-ai/agentcmd/model"
)

func vals(s ...string) []model.Value {
	out := make([]model.Value, len(s))
	for i, x := range s {
		out[i] = model.Literal(x)
	}
	return out
}

func TestParseOptions(t *testing.T) {
	spec := OptionSpec{ShortArgs: "n", ShortFlags: "vr", LongArgs: []string{"lines"}, LongFlags: []string{"verbose"}}
	o := ParseOptions(vals("-n", "40", "-rv", "--lines=3", "--verbose", "-n5", "--", "-rf", "file"), spec)
	if len(o.Operands) != 2 || o.Operands[0].Text != "-rf" || o.Operands[1].Text != "file" {
		t.Fatalf("operands %+v", o.Operands)
	}
	if v, _ := o.Value("n"); v.Text != "5" || len(o.Values["n"]) != 2 || o.Values["lines"][0].Text != "3" {
		t.Fatalf("values %+v", o.Values)
	}
	if !o.Has("r") || !o.Has("v") || !o.Has("verbose") || len(o.Unknown) != 0 {
		t.Fatalf("flags %+v unknown %v", o.Flags, o.Unknown)
	}

	o = ParseOptions(vals("-x", "file", "--weird", "b"), spec)
	if len(o.Unknown) != 2 || len(o.Operands) != 2 || len(o.Assumptions) != 2 {
		t.Fatalf("unknown handling: %+v", o)
	}

	o = ParseOptions([]model.Value{model.Symbolic("$FLAGS"), model.Literal("f")}, spec)
	if len(o.Operands) != 2 || len(o.Assumptions) != 1 {
		t.Fatalf("symbolic: %+v", o)
	}

	o = ParseOptions(vals("-i", "FOO=1", "go", "test", "-v"), OptionSpec{ShortFlags: "i", OperandsStopOptions: true})
	if len(o.Operands) != 1 || o.Operands[0].Text != "FOO=1" || len(o.Rest) != 3 {
		t.Fatalf("stop at operand: %+v", o)
	}

	o = ParseOptions(vals("-name", "*.go", "-delete"), OptionSpec{NoCluster: true, LongArgs: []string{"name"}, LongFlags: []string{"delete"}})
	if v, _ := o.Value("name"); v.Text != "*.go" || !o.Has("delete") {
		t.Fatalf("no cluster: %+v", o)
	}
}

func TestRegistryAndResources(t *testing.T) {
	r1 := Rule{ID: "a", Version: "1", Executables: []string{"cat"}, Apply: func(Command) Result { return Result{} }}
	r2 := Rule{ID: "b", Version: "1", Executables: []string{"cat"}, Apply: r1.Apply}
	if _, err := NewRegistry(r1, r2); err == nil {
		t.Fatal("collision must be rejected")
	}
	reg, err := NewRegistry(r1)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := reg.Lookup("/bin/cat"); !ok {
		t.Fatal("lookup by path")
	}
	if reg.Fingerprint() == (*Registry)(nil).Fingerprint() {
		t.Fatal("fingerprint")
	}

	cmd := Command{Cwd: model.Literal("/w"), Namespace: model.Namespace{Kind: model.NamespaceHost}}
	if r := cmd.Resource(model.Literal("a/../b"), "target"); r.Resolved != "/w/b" || r.Knowledge != model.KnowledgeLiteral {
		t.Fatalf("%+v", r)
	}
	if r := cmd.Resource(model.Value{Knowledge: model.KnowledgeGlob, Text: "*.go"}, "scope"); r.Knowledge != model.KnowledgeGlob || r.Resolved != "/w/*.go" {
		t.Fatalf("%+v", r)
	}
	if r := cmd.Resource(model.Literal("*.go"), "scope"); r.Knowledge != model.KnowledgeLiteral {
		t.Fatal("without a shell, metacharacters are literal")
	}
	alt := Command{Cwd: model.Value{Knowledge: model.KnowledgeDerived, Text: "{/a|/b}", Alternatives: []string{"/a", "/b"}}}
	if r := alt.Resource(model.Literal("x"), "target"); r.Resolved != "" || len(r.Alternatives) != 2 || r.Alternatives[1] != "/b/x" || r.Knowledge != model.KnowledgeDerived {
		t.Fatalf("%+v", r)
	}
	if r := cmd.Resource(model.Symbolic("$HOME/x"), "target"); r.Resolved != "" || r.Expression != "$HOME/x" {
		t.Fatalf("%+v", r)
	}
	if r := cmd.Resource(model.Unknown(), "target"); r.Expression != "" || r.Knowledge != model.KnowledgeUnknown {
		t.Fatalf("%+v", r)
	}
	unknownCwd := Command{Cwd: model.Unknown()}
	if r := unknownCwd.Resource(model.Literal("rel"), "target"); r.Resolved != "" {
		t.Fatal("relative path with unknown cwd must not resolve")
	}
	if r := unknownCwd.Resource(model.Literal("/abs/./x"), "target"); r.Resolved != "/abs/x" {
		t.Fatal("absolute path resolves lexically")
	}
}
