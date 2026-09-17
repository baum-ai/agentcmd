package adapters

import (
	"encoding/json"
	"fmt"
)

// Codex decodes Codex CLI's own on-disk session log
// (~/.codex/sessions/<year>/<month>/<day>/rollout-*.jsonl): one JSON object
// per line, each carrying a top-level "type" and a "payload". Only a
// "response_item" line can carry a tool call; every other line kind
// (session_meta, event_msg, token_usage_record, turn_context, world_state,
// compacted, …) is session bookkeeping with no call information and is
// skipped. Within a response_item, "message" and "reasoning" payloads are
// plain conversation turns and are also skipped. A payload of
// "function_call", "custom_tool_call", "function_call_output" or
// "custom_tool_call_output" uses the same shape as the Responses API, so
// decoding is delegated to OpenAI; any other payload type is reported as
// unsupported rather than dropped, since it may carry a call kind (such as
// a built-in shell or web tool) this adapter does not yet decode.
type Codex struct{}

func (Codex) Name() string    { return "codex.session" }
func (Codex) Version() string { return "cli-session-log" }

func (c Codex) Decode(line []byte) ([]Record, error) {
	var m map[string]json.RawMessage
	if err := json.Unmarshal(line, &m); err != nil {
		return nil, fmt.Errorf("codex: %w", err)
	}
	if str(m, "type") != "response_item" {
		return nil, nil
	}
	payload := m["payload"]
	if payload == nil {
		return nil, ErrUnrecognized
	}
	var p map[string]json.RawMessage
	if err := json.Unmarshal(payload, &p); err != nil {
		return nil, ErrUnrecognized
	}
	switch str(p, "type") {
	case "message", "reasoning":
		return nil, nil
	case "function_call", "custom_tool_call", "function_call_output", "custom_tool_call_output":
		return (OpenAI{}).Decode(payload)
	}
	return nil, ErrUnrecognized
}
