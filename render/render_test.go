package render

import (
	"bytes"
	"strings"
	"testing"

	"github.com/baum-ai/agentcmd/model"
)

func TestTextEscapesUntrustedAndKeepsVerbs(t *testing.T) {
	exit := 1
	a := model.Analysis{SchemaVersion: model.SchemaVersion, ID: "an_1", Invocation: "inv\x1b[31m", Versions: model.Versions{Analyzer: "t"},
		Sources: []model.Source{{ID: "s0", Kind: "shell", Length: 3, Text: "cat"}},
		Nodes:   []model.Node{{ID: "n1", Kind: model.NodeShell}, {ID: "n2", Parent: "n1", Kind: model.NodeCommand, Reach: model.ReachConditional, Guard: &model.Guard{Node: "n1", On: model.GuardFailure}, Command: &model.Command{Executable: model.Literal("cat"), Wrappers: []string{"sudo"}}}},
		Actions: []model.Action{
			{ID: "a1", Node: "n2", Kind: model.FSRead, Resources: []model.Resource{{Expression: "evil\nname", Resolved: "/w/evil\nname", Knowledge: model.KnowledgeLiteral, Namespace: model.Namespace{Kind: model.NamespaceContainer, ID: "app"}}}, Evidence: model.Evidence{Basis: model.BasisSyntax, Assumptions: []string{"x"}}},
			{ID: "a2", Node: "n2", Kind: model.FSWrite, Write: &model.WriteDetail{Mode: model.WriteAppend}, Evidence: model.Evidence{Basis: model.BasisToolContract}},
			{ID: "a3", Node: "n2", Kind: model.DevTest, Intent: &model.IntentDetail{Tool: "make", Target: "test"}, Evidence: model.Evidence{Basis: model.BasisHeuristic}},
		},
		Observations: []model.Observation{{ID: "o1", Subject: "n1", Kind: model.ObsExitStatus, Basis: model.BasisToolReport, ExitCode: &exit, Relation: model.RelationInforms}},
		Partial:      true,
	}
	var buf bytes.Buffer
	if err := Text(&buf, &a, TextOptions{Structure: true, ShowSource: true}); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	for _, want := range []string{"PARTIAL", "inferred  filesystem.read", "requested filesystem.write", "intent    development.test", "unverified", "reported  exit_status", "exit=1", `evil\nname`, "container:app", "if n1 failure", "via sudo", "conditional", "structure:", "| cat", `inv\u001b`} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	if strings.Contains(out, "\x1b") || strings.Contains(out, "evil\nname") {
		t.Fatal("control characters must be escaped")
	}
	var buf2 bytes.Buffer
	Text(&buf2, &a, TextOptions{})
	if strings.Contains(buf2.String(), "| cat") {
		t.Fatal("source hidden by default")
	}
	var j bytes.Buffer
	if err := JSON(&j, &a, false); err != nil || !strings.HasPrefix(j.String(), `{"schema_version"`) {
		t.Fatal(err)
	}
}

func TestTruncation(t *testing.T) {
	s := sanitizer{max: 5}
	if got := s.clean("abcdefgh"); got != "abcde…" {
		t.Fatal(got)
	}
}
