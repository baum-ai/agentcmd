package stream

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/lastpersonlabs/agentcmd"
	"github.com/lastpersonlabs/agentcmd/adapters"
	"github.com/lastpersonlabs/agentcmd/model"
	nstd "github.com/lastpersonlabs/agentcmd/native/standard"
	rstd "github.com/lastpersonlabs/agentcmd/rules/standard"
)

func engine(t *testing.T) *agentcmd.Engine {
	t.Helper()
	e, err := agentcmd.New(agentcmd.Config{Rules: rstd.Rules(), Native: nstd.Bindings()})
	if err != nil {
		t.Fatal(err)
	}
	return e
}

func run(t *testing.T, opts Options, lines ...string) []model.Event {
	t.Helper()
	p, err := New(engine(t), opts)
	if err != nil {
		t.Fatal(err)
	}
	var out []model.Event
	if err := p.Process(context.Background(), strings.NewReader(strings.Join(lines, "\n")+"\n"), func(ev model.Event) error {
		out = append(out, ev)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return out
}

func kinds(evs []model.Event) string {
	var ks []string
	for _, e := range evs {
		ks = append(ks, string(e.Kind))
	}
	return strings.Join(ks, ",")
}

var ctx = model.ExecutionContext{WorkingDir: model.KnownFact("/w", "configured"), Shell: model.KnownFact("bash", "configured"), Namespace: model.Namespace{Kind: model.NamespaceHost}}

// OpenAI bindings: the standard set registers namespace "" and "harness";
// tests use the canonical adapter with harness tools, and OpenAI events
// with a custom binding namespace mapping handled by tool name only.
func TestInterleavedDeltasAndSnapshot(t *testing.T) {
	evs := run(t, Options{Adapter: adapters.OpenAI{}, Context: ctx, ToolNamespace: "harness"},
		`{"type":"response.output_item.added","item":{"type":"function_call","id":"fc_a","name":"Bash"}}`,
		`{"type":"response.output_item.added","item":{"type":"function_call","id":"fc_b","name":"Read"}}`,
		`{"type":"response.function_call_arguments.delta","item_id":"fc_a","delta":"{\"command\":"}`,
		`{"type":"response.function_call_arguments.delta","item_id":"fc_b","delta":"{\"file_path\":\"/w/a.go\"}"}`,
		`{"type":"response.function_call_arguments.delta","item_id":"fc_a","delta":"\"ls src\"}"}`,
		`{"type":"response.function_call_arguments.done","item_id":"fc_b","arguments":"{\"file_path\":\"/w/a.go\"}"}`,
		`{"type":"response.output_item.done","item":{"type":"function_call","id":"fc_a","call_id":"call_a","name":"Bash","arguments":"{\"command\":\"ls src\"}"}}`,
		`{"type":"function_call_output","call_id":"call_a","output":"src listing"}`,
	)
	// fc_b completes first (done), then fc_a's snapshot supersedes its
	// fragments (with an info diagnostic), then the result associates.
	if kinds(evs) != "analysis,diagnostic,analysis,observation" {
		t.Fatalf("%s", kinds(evs))
	}
	if evs[0].Analysis.Actions[0].Kind != model.FSRead || evs[2].Analysis.Actions[0].Kind != model.FSList {
		t.Fatalf("%+v %+v", evs[0].Analysis.Actions, evs[2].Analysis.Actions)
	}
	if evs[3].Invocation != evs[2].Invocation || evs[3].Observation.Subject != evs[2].Invocation || evs[3].Lifecycle[0].Phase != model.PhaseCompleted {
		t.Fatalf("%+v", evs[3])
	}
}

func TestOpenAIDoubleCompletionIsOneInvocation(t *testing.T) {
	evs := run(t, Options{Adapter: adapters.OpenAI{}, Context: ctx, ToolNamespace: "harness"},
		`{"type":"response.output_item.added","item":{"type":"function_call","id":"fc_1","call_id":"call_1","name":"Read"}}`,
		`{"type":"response.function_call_arguments.delta","item_id":"fc_1","delta":"{\"file_path\":\"/w/a\"}"}`,
		`{"type":"response.function_call_arguments.done","item_id":"fc_1","arguments":"{\"file_path\":\"/w/a\"}"}`,
		`{"type":"response.output_item.done","item":{"type":"function_call","id":"fc_1","call_id":"call_1","name":"Read","arguments":"{\"file_path\":\"/w/a\"}"}}`,
		`{"type":"function_call_output","call_id":"call_1","output":"contents"}`,
		`{"type":"response.output_item.done","item":{"type":"function_call","id":"fc_1","call_id":"call_1","name":"Read","arguments":"{\"file_path\":\"/w/CHANGED\"}"}}`,
	)
	if kinds(evs) != "analysis,observation,diagnostic,analysis" {
		t.Fatalf("%s", kinds(evs))
	}
	if evs[1].Invocation != evs[0].Invocation || !strings.Contains(evs[2].Diagnostic.Message, "conflicts") {
		t.Fatalf("%+v %+v", evs[1], evs[2])
	}
}

func TestOpenAINamespaceNeedsBinding(t *testing.T) {
	// The OpenAI adapter puts tools in namespace "openai"; without a binding
	// for it the call stays opaque rather than guessing.
	evs := run(t, Options{Adapter: adapters.OpenAI{}, Context: ctx}, `{"type":"function_call","call_id":"c","name":"Bash","arguments":"{\"command\":\"ls\"}"}`)
	if len(evs) != 1 || len(evs[0].Analysis.Actions) != 0 || evs[0].Analysis.Diagnostics[0].Code != model.DiagUnknownTool {
		t.Fatalf("%+v", evs)
	}
}

func TestOrphansDuplicatesAndOrder(t *testing.T) {
	inv := func(id, session, cmd string) string {
		return `{"id":"` + id + `","session":"` + session + `","call_id":"k","tool":{"namespace":"harness","name":"bash"},"input":{"kind":"text","text":"` + cmd + `"},"context":{"working_dir":{"known":true,"value":"/w"},"shell":{"known":true,"value":"bash"},"namespace":{"kind":"host"}}}`
	}
	evs := run(t, Options{Adapter: adapters.Canonical{}},
		`{"session":"s1","call_id":"zzz","observation":{"kind":"exit_status","exit_code":1}}`,
		inv("a", "s1", "cat x"),
		inv("b", "s2", "cat x"),
		`{"session":"s2","call_id":"k","observation":{"kind":"exit_status","exit_code":0}}`,
		inv("c", "s1", "cat y"),
		`{"session":"s1","call_id":"k","observation":{"kind":"exit_status","exit_code":2}}`,
	)
	if kinds(evs) != "orphan_result,analysis,analysis,observation,diagnostic,analysis,observation" {
		t.Fatalf("%s", kinds(evs))
	}
	if evs[1].Analysis.ID == evs[2].Analysis.ID || evs[1].Invocation == evs[2].Invocation {
		t.Fatal("identical commands in different sessions must be distinct invocations")
	}
	if evs[3].Invocation != evs[2].Invocation {
		t.Fatal("result must attach by session-scoped call id")
	}
	if evs[4].Diagnostic.Code != "duplicate_call_id" || evs[6].Invocation != evs[5].Invocation || *evs[6].Observation.ExitCode != 2 {
		t.Fatalf("%+v %+v", evs[4], evs[6])
	}
	// Sequence numbers restore input order.
	for i := 1; i < len(evs); i++ {
		if evs[i].Sequence < evs[i-1].Sequence {
			t.Fatal("events out of input order")
		}
	}
}

func TestResultBeforeCompletionAndEOFFlush(t *testing.T) {
	evs := run(t, Options{Adapter: adapters.OpenAI{}, Context: ctx},
		`{"type":"response.output_item.added","item":{"type":"function_call","id":"fc_1","name":"Read"}}`,
		`{"type":"function_call_output","call_id":"fc_1","output":"early"}`,
		`{"type":"response.function_call_arguments.done","item_id":"fc_1","arguments":"{\"file_path\":\"/w/a\"}"}`,
		`{"type":"response.output_item.added","item":{"type":"function_call","id":"fc_2","name":"Read"}}`,
		`{"type":"response.function_call_arguments.delta","item_id":"fc_2","delta":"{\"file_pa"}`,
	)
	if kinds(evs) != "analysis,observation,diagnostic" {
		t.Fatalf("%s", kinds(evs))
	}
	if evs[1].Observation.Detail != "early" || evs[2].Diagnostic.Code != model.DiagTruncated || !strings.Contains(evs[2].Diagnostic.Message, "incomplete at end of stream") {
		t.Fatalf("%+v %+v", evs[1], evs[2])
	}
}

func TestLimitsAndBadRecords(t *testing.T) {
	evs := run(t, Options{Adapter: adapters.OpenAI{}, FrameBytes: 200, BufferBytes: 30, ActiveCalls: 1, Context: ctx},
		`not json`,
		`{"type":"weird"}`,
		`{"type":"response.output_item.added","item":{"type":"function_call","id":"fc_1","name":"Read"}}`,
		`{"type":"response.output_item.added","item":{"type":"function_call","id":"fc_2","name":"Read"}}`,
		`{"type":"response.function_call_arguments.delta","item_id":"fc_2","delta":"`+strings.Repeat("x", 40)+`"}`,
		`{"type":"function_call","call_id":"big","name":"Read","arguments":"`+strings.Repeat("y", 300)+`"}`,
	)
	want := []string{"invalid_record", "unsupported", "budget_exhausted", "budget_exhausted", "limit_reached"}
	if len(evs) != len(want) {
		t.Fatalf("%s", kinds(evs))
	}
	for i, w := range want {
		if evs[i].Kind != model.EventDiagnostic || evs[i].Diagnostic.Code != w {
			t.Fatalf("event %d: %+v", i, evs[i])
		}
	}
}

func TestGeminiPositionalAssociation(t *testing.T) {
	evs := run(t, Options{Adapter: adapters.Gemini{}, Context: ctx},
		`{"session":"g","functionCall":{"name":"Read","args":{"file_path":"/w/a"}}}`,
		`{"session":"g","functionCall":{"name":"Read","args":{"file_path":"/w/b"}}}`,
		`{"session":"g","functionResponse":{"name":"Read","response":{"r":"first"}}}`,
		`{"session":"g","functionResponse":{"name":"Read","response":{"r":"second"}}}`,
	)
	if kinds(evs) != "analysis,analysis,observation,observation" {
		t.Fatalf("%s", kinds(evs))
	}
	if evs[2].Invocation != evs[0].Invocation || evs[3].Invocation != evs[1].Invocation || !strings.Contains(evs[2].Observation.Detail, "positionally") {
		t.Fatalf("%+v %+v", evs[2], evs[3])
	}
	if evs[0].Analysis.Actions != nil && len(evs[0].Analysis.Actions) != 0 {
		t.Fatal("gemini namespace has no binding in the standard set; must stay opaque")
	}
}

func TestBackpressureAndCancellation(t *testing.T) {
	p, _ := New(engine(t), Options{Adapter: adapters.Canonical{}})
	stop := errors.New("consumer full")
	err := p.Process(context.Background(), strings.NewReader(`{"id":"a","tool":{"namespace":"harness","name":"bash"},"input":{"kind":"text","text":"ls"},"context":{"working_dir":{"known":false},"shell":{"known":false},"namespace":{"kind":"host"}}}`+"\n"), func(model.Event) error { return stop })
	if !errors.Is(err, stop) {
		t.Fatal(err)
	}
	c, cancel := context.WithCancel(context.Background())
	cancel()
	if err := p.Process(c, strings.NewReader("x\n"), func(model.Event) error { return nil }); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err := New(nil, Options{}); err == nil {
		t.Fatal("engine required")
	}
}
