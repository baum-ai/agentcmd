package adapters

import (
	"encoding/json"
	"fmt"

	"github.com/lastpersonlabs/toolparser/model"
)

// Anthropic decodes Messages API content and streaming events:
//
//   - a message {"role","content":[{"type":"tool_use","id","name","input"}]}
//     or a bare content block;
//   - {"type":"tool_result","tool_use_id","content","is_error"} blocks;
//   - streaming content_block_start (tool_use), content_block_delta with
//     input_json_delta.partial_json, and content_block_stop, keyed by the
//     block index within the message stream ("session" scopes the stream).
type Anthropic struct{}

func (Anthropic) Name() string    { return "anthropic.messages" }
func (Anthropic) Version() string { return "messages-2023-06-01" }

func (a Anthropic) Decode(line []byte) ([]Record, error) {
	var m map[string]json.RawMessage
	if err := json.Unmarshal(line, &m); err != nil {
		return nil, fmt.Errorf("anthropic: %w", err)
	}
	session := firstNonEmpty(str(m, "session"), str(m, "message_id"))
	switch str(m, "type") {
	case "tool_use":
		return []Record{{Call: a.toolUse(m, session)}}, nil
	case "tool_result":
		obs := model.Observation{Kind: model.ObsToolResult, Basis: model.BasisToolReport, Relation: model.RelationInforms, Collector: a.Name(), Detail: textOf(m["content"])}
		if m["is_error"] != nil {
			var isErr bool
			_ = json.Unmarshal(m["is_error"], &isErr)
			ok := !isErr
			obs.Success = &ok
		}
		return []Record{{Result: &Result{Session: session, CallID: str(m, "tool_use_id"), Lifecycle: model.PhaseCompleted, Observation: obs}}}, nil
	case "content_block_start":
		block := obj(m, "content_block")
		if str(block, "type") != "tool_use" {
			return nil, nil
		}
		return []Record{{Delta: &Delta{Session: session, CallID: streamKey(m), Tool: model.ToolIdentity{Namespace: "anthropic", Name: str(block, "name")}, Index: -1, Text: ""}},
			{Lifecycle: &Lifecycle{Session: session, CallID: streamKey(m), Event: model.LifecycleEvent{Phase: model.PhaseRequested, Source: "provider", Detail: "tool_use id " + str(block, "id")}}}}, nil
	case "content_block_delta":
		delta := obj(m, "delta")
		if str(delta, "type") != "input_json_delta" {
			return nil, nil
		}
		return []Record{{Delta: &Delta{Session: session, CallID: streamKey(m), Index: intOf(m["index"]), Text: str(delta, "partial_json")}}}, nil
	case "content_block_stop":
		return []Record{{Delta: &Delta{Session: session, CallID: streamKey(m), Index: intOf(m["index"]), Done: true}}}, nil
	case "message", "":
		if m["content"] == nil {
			return nil, ErrUnrecognized
		}
		var blocks []map[string]json.RawMessage
		if err := json.Unmarshal(m["content"], &blocks); err != nil {
			return nil, ErrUnrecognized
		}
		var out []Record
		for _, b := range blocks {
			switch str(b, "type") {
			case "tool_use":
				out = append(out, Record{Call: a.toolUse(b, session)})
			case "tool_result":
				if b["session"] == nil && session != "" {
					b["session"], _ = json.Marshal(session)
				}
				raw, _ := json.Marshal(b)
				recs, err := a.Decode(raw)
				if err != nil {
					return nil, err
				}
				out = append(out, recs...)
			}
		}
		return out, nil
	}
	return nil, ErrUnrecognized
}

// streamKey identifies a streamed block: "block:<index>" within the session.
func streamKey(m map[string]json.RawMessage) string {
	return fmt.Sprintf("block:%d", intOf(m["index"]))
}

func (a Anthropic) toolUse(m map[string]json.RawMessage, session string) *Call {
	input := m["input"]
	if input == nil {
		input = json.RawMessage("{}")
	}
	return &Call{Session: session, CallID: str(m, "id"), Provider: "anthropic", Tool: model.ToolIdentity{Namespace: "anthropic", Name: str(m, "name")},
		Input: model.Input{Kind: model.InputJSON, JSON: input}, Snapshot: true}
}
