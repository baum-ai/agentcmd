package adapters

import (
	"errors"
	"testing"

	"github.com/lastpersonlabs/agentcmd/model"
)

func decode(t *testing.T, a Adapter, line string) []Record {
	t.Helper()
	recs, err := a.Decode([]byte(line))
	if err != nil {
		t.Fatalf("%s: %v", line, err)
	}
	return recs
}

func TestOpenAI(t *testing.T) {
	a := OpenAI{}
	recs := decode(t, a, `{"type":"function_call","call_id":"call_1","name":"shell","arguments":"{\"command\":[\"ls\"]}","session":"r1"}`)
	if len(recs) != 1 || recs[0].Call == nil || recs[0].Call.Tool.Name != "shell" || recs[0].Call.Input.Kind != model.InputJSON || recs[0].Call.Session != "r1" {
		t.Fatalf("%+v", recs)
	}
	recs = decode(t, a, `{"type":"custom_tool_call","call_id":"c2","name":"apply_patch","input":"*** Begin Patch\n*** End Patch"}`)
	if recs[0].Call.Input.Kind != model.InputText {
		t.Fatalf("%+v", recs[0].Call.Input)
	}
	recs = decode(t, a, `{"type":"function_call_output","call_id":"call_1","output":"ok"}`)
	if recs[0].Result == nil || recs[0].Result.CallID != "call_1" || recs[0].Result.Observation.Basis != model.BasisToolReport || recs[0].Result.Observation.Detail != "ok" {
		t.Fatalf("%+v", recs[0].Result)
	}
	recs = decode(t, a, `{"type":"response.output_item.added","item":{"type":"function_call","id":"fc_9","call_id":"call_9","name":"shell"}}`)
	if recs[0].Delta == nil || recs[0].Delta.CallID != "fc_9" || recs[0].Delta.Tool.Name != "shell" {
		t.Fatalf("%+v", recs[0].Delta)
	}
	recs = decode(t, a, `{"type":"response.function_call_arguments.delta","item_id":"fc_9","output_index":0,"delta":"{\"comm"}`)
	if recs[0].Delta.Text != `{"comm` || recs[0].Delta.Done {
		t.Fatalf("%+v", recs[0].Delta)
	}
	recs = decode(t, a, `{"type":"response.function_call_arguments.done","item_id":"fc_9","arguments":"{\"command\":[\"ls\"]}"}`)
	if !recs[0].Delta.Done {
		t.Fatalf("%+v", recs[0].Delta)
	}
	// Invalid JSON arguments are kept as text, never dropped.
	recs = decode(t, a, `{"type":"function_call","call_id":"c3","name":"shell","arguments":"{oops"}`)
	if recs[0].Call.Input.Kind != model.InputText || recs[0].Call.Input.Text != "{oops" {
		t.Fatalf("%+v", recs[0].Call.Input)
	}
	// Whole response object.
	recs = decode(t, a, `{"object":"response","id":"resp_1","output":[{"type":"message","content":[]},{"type":"function_call","call_id":"c4","name":"Read","arguments":"{}"}]}`)
	if len(recs) != 1 || recs[0].Call.CallID != "c4" {
		t.Fatalf("%+v", recs)
	}
	if _, err := a.Decode([]byte(`{"type":"weird"}`)); !errors.Is(err, ErrUnrecognized) {
		t.Fatal(err)
	}
	if _, err := a.Decode([]byte(`not json`)); err == nil || errors.Is(err, ErrUnrecognized) {
		t.Fatal("malformed input must be a hard error")
	}
	if _, err := a.Decode([]byte(`{"type":"function_call","call_id":"x"}`)); err == nil {
		t.Fatal("call without name must fail explicitly")
	}
}

func TestAnthropic(t *testing.T) {
	a := Anthropic{}
	recs := decode(t, a, `{"role":"assistant","content":[{"type":"text","text":"hi"},{"type":"tool_use","id":"toolu_1","name":"Bash","input":{"command":"ls"}}],"session":"s1"}`)
	if len(recs) != 1 || recs[0].Call.CallID != "toolu_1" || recs[0].Call.Tool.Namespace != "anthropic" || string(recs[0].Call.Input.JSON) != `{"command":"ls"}` {
		t.Fatalf("%+v", recs)
	}
	recs = decode(t, a, `{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_1","content":"file list","is_error":false}]}`)
	if recs[0].Result == nil || recs[0].Result.CallID != "toolu_1" || recs[0].Result.Observation.Success == nil || !*recs[0].Result.Observation.Success {
		t.Fatalf("%+v", recs[0].Result)
	}
	recs = decode(t, a, `{"type":"content_block_start","index":1,"content_block":{"type":"tool_use","id":"toolu_2","name":"Read","input":{}}}`)
	if len(recs) != 2 || recs[0].Delta.CallID != "block:1" || recs[0].Delta.Tool.Name != "Read" || recs[1].Lifecycle == nil {
		t.Fatalf("%+v", recs)
	}
	recs = decode(t, a, `{"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"{\"file_path\":"}}`)
	if recs[0].Delta.Text != `{"file_path":` {
		t.Fatalf("%+v", recs[0].Delta)
	}
	recs = decode(t, a, `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"hi"}}`)
	if len(recs) != 0 {
		t.Fatal("text deltas are not tool records")
	}
	recs = decode(t, a, `{"type":"content_block_stop","index":1}`)
	if !recs[0].Delta.Done {
		t.Fatalf("%+v", recs[0].Delta)
	}
	if _, err := a.Decode([]byte(`{"type":"message_start"}`)); !errors.Is(err, ErrUnrecognized) {
		t.Fatal(err)
	}
}

func TestGemini(t *testing.T) {
	g := Gemini{}
	recs := decode(t, g, `{"candidates":[{"content":{"parts":[{"text":"x"},{"functionCall":{"name":"run_shell","args":{"command":"ls"}}}]}}],"session":"g1"}`)
	if len(recs) != 1 || recs[0].Call.CallID != "" || recs[0].Call.Tool.Name != "run_shell" {
		t.Fatalf("%+v", recs)
	}
	recs = decode(t, g, `{"functionResponse":{"name":"run_shell","response":{"output":"ok"}}}`)
	if recs[0].Result == nil || recs[0].Result.Name != "run_shell" || recs[0].Result.CallID != "" {
		t.Fatalf("%+v", recs[0].Result)
	}
	recs = decode(t, g, `{"functionCall":{"id":"fc-1","name":"read","args":{"path":"x"}}}`)
	if recs[0].Call.CallID != "fc-1" {
		t.Fatalf("%+v", recs[0].Call)
	}
	if _, err := g.Decode([]byte(`{"text":"hi"}`)); !errors.Is(err, ErrUnrecognized) {
		t.Fatal(err)
	}
}

func TestCanonical(t *testing.T) {
	c := Canonical{}
	recs := decode(t, c, `{"id":"c1","session":"s","call_id":"k1","tool":{"namespace":"harness","name":"bash"},"input":{"kind":"text","text":"ls"},"context":{"working_dir":{"known":true,"value":"/w"},"shell":{"known":false},"namespace":{"kind":"host"}}}`)
	if recs[0].Call == nil || recs[0].Call.CallID != "k1" || recs[0].Call.Context.WorkingDir.Value != "/w" {
		t.Fatalf("%+v", recs)
	}
	recs = decode(t, c, `{"session":"s","call_id":"k1","observation":{"kind":"exit_status","exit_code":0,"collector":"harness"}}`)
	if recs[0].Result == nil || recs[0].Result.Observation.Basis != model.BasisToolReport || *recs[0].Result.Observation.ExitCode != 0 {
		t.Fatalf("%+v", recs[0].Result)
	}
	recs = decode(t, c, `{"session":"s","call_id":"k1","lifecycle":{"phase":"denied","source":"harness"}}`)
	if recs[0].Lifecycle == nil || recs[0].Lifecycle.Event.Phase != model.PhaseDenied {
		t.Fatalf("%+v", recs)
	}
	if _, err := c.Decode([]byte(`{"id":"x","tool":{"name":"t"},"input":{"kind":"nope"}}`)); err == nil {
		t.Fatal("invalid invocation must fail")
	}
	if _, err := ByName("bogus"); err == nil {
		t.Fatal("unknown adapter must fail")
	}
}
