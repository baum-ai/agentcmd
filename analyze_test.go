package toolparser

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"github.com/lastpersonlabs/toolparser/internal/corpus"
	"github.com/lastpersonlabs/toolparser/model"
	nstd "github.com/lastpersonlabs/toolparser/native/standard"
	"github.com/lastpersonlabs/toolparser/rules"
	rstd "github.com/lastpersonlabs/toolparser/rules/standard"
)

func engine(t *testing.T) *Engine {
	t.Helper()
	e, err := New(Config{Rules: rstd.Rules(), Native: nstd.Bindings()})
	if err != nil {
		t.Fatal(err)
	}
	return e
}

func ctxWith(cwd string) model.ExecutionContext {
	return model.ExecutionContext{WorkingDir: model.KnownFact(cwd, "reported"), Shell: model.KnownFact("bash", "reported"), Namespace: model.Namespace{Kind: model.NamespaceHost}}
}

func kinds(a model.Analysis) []model.ActionKind {
	var out []model.ActionKind
	for _, act := range a.Actions {
		out = append(out, act.Kind)
	}
	return out
}

func TestAnalyzeShellCanonical(t *testing.T) {
	e := engine(t)
	a, err := e.AnalyzeShell(context.Background(), []byte("cat README.md"), ctxWith("/w"))
	if err != nil {
		t.Fatal(err)
	}
	if a.Partial || len(a.Actions) != 1 || a.Actions[0].Kind != model.FSRead {
		t.Fatalf("%+v", a)
	}
	res := a.Actions[0].Resources[0]
	if res.Resolved != "/w/README.md" || res.Knowledge != model.KnowledgeLiteral || res.Namespace.Kind != model.NamespaceHost {
		t.Fatalf("%+v", res)
	}
	if a.Actions[0].Evidence.Basis != model.BasisSyntax || a.Actions[0].Evidence.Rule != "std/cat" {
		t.Fatalf("%+v", a.Actions[0].Evidence)
	}
	if a.Sources[0].Text != "cat README.md" || a.Versions.ShellParser == "" {
		t.Fatalf("%+v", a)
	}
}

func TestUnknownCwdDoesNotResolve(t *testing.T) {
	e := engine(t)
	a, _ := e.AnalyzeShell(context.Background(), []byte("cat a.txt"), model.ExecutionContext{})
	if a.Actions[0].Resources[0].Resolved != "" || a.Actions[0].Resources[0].Expression != "a.txt" {
		t.Fatalf("%+v", a.Actions[0].Resources[0])
	}
	if a.Nodes[0].Context.Cwd.Knowledge != model.KnowledgeUnknown {
		t.Fatalf("cwd %+v", a.Nodes[0].Context.Cwd)
	}
}

func TestSyntaxErrorIsPartialNotError(t *testing.T) {
	e := engine(t)
	a, err := e.AnalyzeShell(context.Background(), []byte("echo )"), ctxWith("/w"))
	if err != nil || !a.Partial {
		t.Fatalf("err=%v partial=%v", err, a.Partial)
	}
	if a.Diagnostics[0].Code != model.DiagSyntaxError || a.Diagnostics[0].Span.Start != 5 {
		t.Fatalf("%+v", a.Diagnostics)
	}
	if a.Nodes[1].Syntax != model.SyntaxInvalid {
		t.Fatalf("%+v", a.Nodes[1])
	}
}

func TestNativeInputs(t *testing.T) {
	e := engine(t)
	inv := model.Invocation{ID: "n1", Tool: model.ToolIdentity{Namespace: "harness", Name: "Write"}, Input: model.Input{Kind: model.InputJSON, JSON: json.RawMessage(`{"file_path":"new.go","content":"x"}`)}, Context: ctxWith("/w")}
	a, err := e.Analyze(context.Background(), inv)
	if err != nil {
		t.Fatal(err)
	}
	if len(a.Actions) != 1 || a.Actions[0].Kind != model.FSWrite || a.Actions[0].Write.Mode != model.WriteTruncate || a.Actions[0].Evidence.Basis != model.BasisToolContract {
		t.Fatalf("%+v", a.Actions)
	}
	if a.Actions[0].Resources[0].Resolved != "/w/new.go" {
		t.Fatalf("%+v", a.Actions[0].Resources[0])
	}

	inv = model.Invocation{ID: "n2", Tool: model.ToolIdentity{Namespace: "harness", Name: "Bash"}, Input: model.Input{Kind: model.InputJSON, JSON: json.RawMessage(`{"command":"ls src"}`)}, Context: ctxWith("/w")}
	a, err = e.Analyze(context.Background(), inv)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(kinds(a), []model.ActionKind{model.FSList}) {
		t.Fatalf("%+v", a.Actions)
	}
	if len(a.Sources) != 2 || a.Sources[1].Derived == nil || a.Sources[1].Derived.Exact {
		t.Fatalf("shell source must be derived approximately from the JSON field: %+v", a.Sources)
	}

	inv = model.Invocation{ID: "n3", Tool: model.ToolIdentity{Namespace: "mcp:x", Name: "mystery"}, Input: model.Input{Kind: model.InputJSON, JSON: json.RawMessage(`{"x":1}`)}}
	a, err = e.Analyze(context.Background(), inv)
	if err != nil {
		t.Fatal(err)
	}
	if len(a.Actions) != 0 || a.Diagnostics[0].Code != model.DiagUnknownTool || a.Nodes[1].Kind != model.NodeNative || a.Partial {
		t.Fatalf("unknown tool must stay opaque without being partial: %+v", a)
	}

	inv = model.Invocation{ID: "n4", Tool: model.ToolIdentity{Name: "exec"}, Input: model.Input{Kind: model.InputArgv, Executable: "/bin/cat", Argv: []string{"-n", "x"}}, Context: ctxWith("/w")}
	a, err = e.Analyze(context.Background(), inv)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(kinds(a), []model.ActionKind{model.FSRead}) || a.Actions[0].Resources[0].Resolved != "/w/x" {
		t.Fatalf("%+v", a.Actions)
	}

	inv = model.Invocation{ID: "n5", Tool: model.ToolIdentity{Name: "blob"}, Input: model.Input{Kind: model.InputBlob, Blob: &model.BlobRef{ID: "b"}}}
	a, err = e.Analyze(context.Background(), inv)
	if err != nil || !a.Partial || a.Diagnostics[0].Code != model.DiagMissingSnapshot {
		t.Fatalf("%v %+v", err, a.Diagnostics)
	}
}

func TestArgvIsNotReparsedAsShell(t *testing.T) {
	e := engine(t)
	inv := model.Invocation{ID: "n6", Tool: model.ToolIdentity{Name: "exec"}, Input: model.Input{Kind: model.InputArgv, Executable: "cat", Argv: []string{"a && rm -rf /"}}, Context: ctxWith("/w")}
	a, err := e.Analyze(context.Background(), inv)
	if err != nil {
		t.Fatal(err)
	}
	if len(a.Actions) != 1 || a.Actions[0].Resources[0].Expression != "a && rm -rf /" {
		t.Fatalf("%+v", a.Actions)
	}
}

func TestConfigErrors(t *testing.T) {
	dup := rules.Rule{ID: "x", Version: "1", Executables: []string{"cat"}, Apply: func(rules.Command) rules.Result { return rules.Result{} }}
	if _, err := New(Config{Rules: append(rstd.Rules(), dup)}); err == nil {
		t.Fatal("collision must be rejected")
	}
	e := engine(t)
	if _, err := e.Analyze(context.Background(), model.Invocation{}); err == nil {
		t.Fatal("invalid invocation must error")
	}
	small, _ := New(Config{Limits: Limits{InputFrameBytes: 4}})
	if _, err := small.AnalyzeShell(context.Background(), []byte("echo hello"), ctxWith("/")); err == nil {
		t.Fatal("frame limit")
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := e.AnalyzeShell(cancelled, []byte("ls"), ctxWith("/")); !errors.Is(err, ErrPartial) || !errors.Is(err, context.Canceled) {
		t.Fatalf("%v", err)
	}
}

func TestDeterministicIDs(t *testing.T) {
	e := engine(t)
	inv := model.Invocation{ID: "d1", Session: "s", Tool: model.ToolIdentity{Namespace: "harness", Name: "bash"}, Input: model.Input{Kind: model.InputText, Text: "cat a"}, Context: ctxWith("/w")}
	a1, _ := e.Analyze(context.Background(), inv)
	a2, _ := e.Analyze(context.Background(), inv)
	if !reflect.DeepEqual(a1, a2) {
		t.Fatal("analysis is not deterministic")
	}
	inv.ID = "d2"
	a3, _ := e.Analyze(context.Background(), inv)
	if a3.ID == a1.ID {
		t.Fatal("distinct invocations share an ID")
	}
}

func TestCorpusInvocationsAnalyze(t *testing.T) {
	e := engine(t)
	for _, split := range []string{"commands", "heldout"} {
		recs, err := corpus.Load(split)
		if err != nil {
			t.Fatal(err)
		}
		for _, r := range recs {
			var inv model.Invocation
			if err := json.Unmarshal(r.Invocation, &inv); err != nil {
				t.Fatalf("%s: %v", r.ID, err)
			}
			a, err := e.Analyze(context.Background(), inv)
			if err != nil {
				t.Fatalf("%s: %v", r.ID, err)
			}
			if a.Invocation != r.ID || len(a.Nodes) == 0 {
				t.Fatalf("%s: %+v", r.ID, a)
			}
		}
	}
}
