package otel

import (
	"context"
	"testing"

	"github.com/baum-ai/agentcmd"
	"github.com/baum-ai/agentcmd/model"
	"github.com/baum-ai/agentcmd/observe"
	rstd "github.com/baum-ai/agentcmd/rules/standard"
)

func TestProjectUsesEventsNotChildSpans(t *testing.T) {
	e, _ := agentcmd.New(agentcmd.Config{Rules: rstd.Rules()})
	inv := model.Invocation{ID: "i", Session: "s", CallID: "c", Provider: "anthropic", Harness: "claude-code", Tool: model.ToolIdentity{Namespace: "harness", Name: "bash"},
		Input: model.Input{Kind: model.InputText, Text: "cat a"}, Context: model.ExecutionContext{WorkingDir: model.KnownFact("/w", "reported"), Namespace: model.Namespace{Kind: model.NamespaceHost}}}
	// Text input without a binding for "bash" in this engine stays opaque;
	// use AnalyzeShell for actions.
	a, _ := e.AnalyzeShell(context.Background(), []byte("cat a"), inv.Context)
	if err := observe.Attach(&a, observe.ExitStatus(3, "harness")); err != nil {
		t.Fatal(err)
	}
	span := Project(&inv, &a)
	if span.Name != "execute_tool bash" || span.Attributes["gen_ai.tool.call.id"] != "c" || span.Attributes["gen_ai.conversation.id"] != "s" || span.Attributes["agentcmd.semconv.version"] != SemconvVersion {
		t.Fatalf("%+v", span.Attributes)
	}
	if len(span.Events) != 2 || span.Events[0].Name != "agentcmd.action" || span.Events[0].Attributes["agentcmd.action.kind"] != "filesystem.read" || span.Events[1].Name != "agentcmd.observation" {
		t.Fatalf("%+v", span.Events)
	}
	if span.Attributes["agentcmd.observed.exit_code"] != 3 {
		t.Fatalf("%+v", span.Attributes)
	}
	res := span.Events[0].Attributes["agentcmd.action.resources"].([]string)
	if res[0] != "host /w/a [literal]" {
		t.Fatalf("%v", res)
	}
}
