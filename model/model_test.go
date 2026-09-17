package model

import (
	"encoding/json"
	"strings"
	"testing"
)

func sample() Analysis {
	return Analysis{
		SchemaVersion: SchemaVersion, ID: "an_x", Invocation: "c001",
		Versions: Versions{Analyzer: "t", Ruleset: "r"},
		Sources: []Source{
			{ID: "s0", Kind: "shell", Length: 20, Text: "cat a && echo b > c\n"},
			{ID: "s1", Kind: "heredoc", Length: 5, Derived: &Derivation{From: "s0", Exact: true, Segments: []Segment{{Start: 0, End: 5, FromStart: 10, FromEnd: 15}}}},
		},
		Nodes: []Node{
			{ID: "n1", Kind: NodeShell, Span: &SourceSpan{Source: "s0", Start: 0, End: 20}, Context: NodeContext{Cwd: Literal("/w"), Namespace: Namespace{Kind: NamespaceHost}}},
			{ID: "n2", Parent: "n1", Kind: NodeCommand, Reach: ReachAlways, Command: &Command{Executable: Literal("cat"), Args: []Value{Literal("a")}}},
			{ID: "n3", Parent: "n1", Kind: NodeCommand, Guard: &Guard{Node: "n2", On: GuardSuccess}, Reach: ReachConditional},
		},
		Actions: []Action{
			{ID: "a1", Node: "n2", Kind: FSRead, Resources: []Resource{{Role: "target", Expression: "a", Resolved: "/w/a", Knowledge: KnowledgeLiteral, Namespace: Namespace{Kind: NamespaceHost}}}, Evidence: Evidence{Basis: BasisSyntax, Rule: "cat", RuleVersion: "1"}},
			{ID: "a2", Node: "n3", Kind: FSWrite, Write: &WriteDetail{Mode: WriteTruncate}, Evidence: Evidence{Basis: BasisSyntax}},
		},
		Observations: []Observation{{ID: "o1", Subject: "n1", Kind: ObsExitStatus, Basis: BasisToolReport, ExitCode: new(int)}},
	}
}

func TestRoundTrip(t *testing.T) {
	a := sample()
	if err := a.Validate(); err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(a)
	if err != nil {
		t.Fatal(err)
	}
	var back Analysis
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatal(err)
	}
	if err := back.Validate(); err != nil {
		t.Fatal(err)
	}
	b2, _ := json.Marshal(back)
	if string(b) != string(b2) {
		t.Fatalf("round trip changed JSON:\n%s\n%s", b, b2)
	}
}

func TestOpaqueActionRoundTrips(t *testing.T) {
	raw := `{"id":"a9","node":"n1","kind":"future.thing","evidence":{"basis":"syntax"},"thing":{"x":1}}`
	var act Action
	if err := json.Unmarshal([]byte(raw), &act); err != nil {
		t.Fatal(err)
	}
	if !act.Opaque() || act.Kind != "future.thing" {
		t.Fatalf("not opaque: %+v", act)
	}
	if err := act.Validate(); err != nil {
		t.Fatal(err)
	}
	out, _ := json.Marshal(act)
	if string(out) != raw {
		t.Fatalf("opaque payload lost: %s", out)
	}
}

func TestMalformedUnionsFail(t *testing.T) {
	cases := map[string]func(*Analysis){
		"write without mode":        func(a *Analysis) { a.Actions[1].Write = nil },
		"read with git payload":     func(a *Analysis) { a.Actions[0].Git = &GitDetail{Subcommand: "x"} },
		"action with dynamic basis": func(a *Analysis) { a.Actions[0].Evidence.Basis = BasisToolReport },
		"observation static basis":  func(a *Analysis) { a.Observations[0].Basis = BasisSyntax },
		"unknown parent":            func(a *Analysis) { a.Nodes[1].Parent = "zz" },
		"unknown guard":             func(a *Analysis) { a.Nodes[2].Guard.Node = "zz" },
		"span outside source":       func(a *Analysis) { a.Nodes[0].Span.End = 99 },
		"derived from unknown":      func(a *Analysis) { a.Sources[1].Derived.From = "s9" },
		"observation subject":       func(a *Analysis) { a.Observations[0].Subject = "nope" },
		"schema major":              func(a *Analysis) { a.SchemaVersion = "9.0.0" },
	}
	for name, mutate := range cases {
		a := sample()
		mutate(&a)
		if err := a.Validate(); err == nil {
			t.Errorf("%s: expected validation error", name)
		}
	}
}

func TestInputUnion(t *testing.T) {
	good := []Input{
		{Kind: InputText, Text: "ls"},
		{Kind: InputJSON, JSON: json.RawMessage(`{"a":1}`)},
		{Kind: InputArgv, Executable: "ls", Argv: []string{"-l"}},
		{Kind: InputSource, Language: "python", Source: "print(1)"},
		{Kind: InputPatch, Patch: "*** Begin Patch"},
		{Kind: InputBlob, Blob: &BlobRef{ID: "b1"}},
	}
	for _, in := range good {
		if err := in.Validate(); err != nil {
			t.Errorf("%s: %v", in.Kind, err)
		}
	}
	bad := []Input{
		{Kind: InputText, Text: "ls", Patch: "x"},
		{Kind: InputJSON, JSON: json.RawMessage(`{`)},
		{Kind: InputArgv, Argv: []string{"-l"}},
		{Kind: InputSource, Source: "print(1)"},
		{Kind: "weird"},
		{Kind: InputBlob, Blob: &BlobRef{}},
	}
	for _, in := range bad {
		if err := in.Validate(); err == nil {
			t.Errorf("%s: expected error", in.Kind)
		}
	}
	var inv Invocation
	if err := json.Unmarshal([]byte(`{"id":"x","tool":{"name":"bash"},"input":{"kind":"text","text":"ls"},"context":{"working_dir":{"known":false},"shell":{"known":false},"namespace":{"kind":"host"}}}`), &inv); err != nil {
		t.Fatal(err)
	}
	if err := inv.Validate(); err != nil {
		t.Fatal(err)
	}
	if inv.Context.WorkingDir.Known {
		t.Fatal("unknown cwd decoded as known")
	}
}

func TestAnalysisIDIsDeterministicAndScoped(t *testing.T) {
	inv := Invocation{ID: "c1", Session: "s", Tool: ToolIdentity{Name: "bash"}, Input: Input{Kind: InputText, Text: "ls"}}
	v := Versions{Analyzer: "1", Ruleset: "1"}
	a := AnalysisID(&inv, v, "opts")
	if a != AnalysisID(&inv, v, "opts") || !strings.HasPrefix(a, "an_") {
		t.Fatal("not deterministic")
	}
	other := inv
	other.ID = "c2"
	if AnalysisID(&other, v, "opts") == a {
		t.Fatal("distinct invocations with identical content must not share an analysis ID")
	}
	if AnalysisID(&inv, Versions{Analyzer: "2", Ruleset: "1"}, "opts") == a {
		t.Fatal("version change must change the ID")
	}
	inv.Context.WorkingDir = KnownFact("/w", "reported")
	if AnalysisID(&inv, v, "opts") == a {
		t.Fatal("context change must change the ID")
	}
}
