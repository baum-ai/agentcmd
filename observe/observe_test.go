package observe

import (
	"context"
	"testing"

	"github.com/lastpersonlabs/agentcmd"
	"github.com/lastpersonlabs/agentcmd/model"
	rstd "github.com/lastpersonlabs/agentcmd/rules/standard"
)

func analyze(t *testing.T, src string) model.Analysis {
	t.Helper()
	e, err := agentcmd.New(agentcmd.Config{Rules: rstd.Rules()})
	if err != nil {
		t.Fatal(err)
	}
	a, err := e.AnalyzeShell(context.Background(), []byte(src), model.ExecutionContext{WorkingDir: model.KnownFact("/w", "reported"), Namespace: model.Namespace{Kind: model.NamespaceHost}})
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func TestExitStatusStaysOnInvocation(t *testing.T) {
	a := analyze(t, "cat a && echo x > b")
	if err := Attach(&a, ExitStatus(0, "harness")); err != nil {
		t.Fatal(err)
	}
	o := a.Observations[0]
	if o.Subject != a.Invocation || o.ID != "o1" || o.Basis != model.BasisToolReport {
		t.Fatalf("%+v", o)
	}
	for _, act := range a.Actions {
		if !act.Evidence.Basis.Static() {
			t.Fatal("observation must not upgrade action bases")
		}
	}
}

func TestFileChangeConfirmsMatchingAction(t *testing.T) {
	a := analyze(t, "cat a && echo x > b")
	host := model.Namespace{Kind: model.NamespaceHost}
	if err := Attach(&a, FileChange("/w/b", host, "modified", "fswatch")); err != nil {
		t.Fatal(err)
	}
	o := a.Observations[0]
	if o.Relation != model.RelationConfirms || o.Subject != "a2" {
		t.Fatalf("%+v", o)
	}
	if err := Attach(&a, FileChange("/w/other", host, "modified", "fswatch")); err != nil {
		t.Fatal(err)
	}
	if o := a.Observations[1]; o.Subject != a.Invocation || o.Relation != model.RelationInforms {
		t.Fatalf("%+v", o)
	}
	// Same path in another namespace does not match.
	if err := Attach(&a, FileChange("/w/b", model.Namespace{Kind: model.NamespaceContainer, ID: "c"}, "modified", "fswatch")); err != nil {
		t.Fatal(err)
	}
	if o := a.Observations[2]; o.Subject != a.Invocation {
		t.Fatalf("%+v", o)
	}
}

func TestSubjectsAndValidation(t *testing.T) {
	a := analyze(t, "go test ./...")
	if err := Attach(&a, model.Observation{Kind: model.ObsTestResult, Basis: model.BasisToolReport, Detail: "3 passed"}); err != nil {
		t.Fatal(err)
	}
	if a.Observations[0].Subject != "a1" {
		t.Fatalf("%+v", a.Observations[0])
	}
	if err := Attach(&a, model.Observation{Kind: model.ObsExitStatus, Basis: model.BasisSyntax}); err == nil {
		t.Fatal("static basis must be rejected")
	}
	if err := Attach(&a, model.Observation{Kind: model.ObsExitStatus, Basis: model.BasisToolReport, Subject: "n99"}); err == nil {
		t.Fatal("unknown subject must be rejected by validation")
	}
	// Identical commands in different calls stay distinct.
	b := analyze(t, "go test ./...")
	if a.ID != b.ID {
		t.Fatal("same input and context yield the same analysis ID for the same invocation identity")
	}
	if len(b.Observations) != 0 {
		t.Fatal("observations must not leak between analyses")
	}
}
