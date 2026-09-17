package adapters

import (
	"encoding/json"
	"fmt"

	"github.com/lastpersonlabs/agentcmd/model"
)

// OpenAI decodes Responses API items and streaming events:
//
//   - output items {"type":"function_call","call_id","name","arguments"} and
//     {"type":"custom_tool_call","call_id","name","input"};
//   - results {"type":"function_call_output","call_id","output"} and
//     {"type":"custom_tool_call_output","call_id","output"};
//   - streaming events response.output_item.added/done (carrying an item),
//     response.function_call_arguments.delta/done and
//     response.custom_tool_call_input.delta/done keyed by item_id.
//
// A session is the optional top-level "session" or "response_id" field.
type OpenAI struct{}

func (OpenAI) Name() string    { return "openai.responses" }
func (OpenAI) Version() string { return "responses-2025-03" }

func (a OpenAI) Decode(line []byte) ([]Record, error) {
	var m map[string]json.RawMessage
	if err := json.Unmarshal(line, &m); err != nil {
		return nil, fmt.Errorf("openai: %w", err)
	}
	session := firstNonEmpty(str(m, "session"), str(m, "response_id"))
	typ := str(m, "type")
	switch typ {
	case "function_call", "custom_tool_call":
		call, err := a.item(m, session)
		if err != nil {
			return nil, err
		}
		return []Record{{Call: call}}, nil
	case "function_call_output", "custom_tool_call_output":
		return []Record{{Result: &Result{Session: session, CallID: str(m, "call_id"), Lifecycle: model.PhaseCompleted,
			Observation: model.Observation{Kind: model.ObsToolResult, Basis: model.BasisToolReport, Relation: model.RelationInforms, Collector: a.Name(), Detail: textOf(m["output"])}}}}, nil
	case "response.output_item.added":
		item := obj(m, "item")
		if t := str(item, "type"); t != "function_call" && t != "custom_tool_call" {
			return nil, nil
		}
		// The item announces the call; arguments follow as deltas. Use the
		// item id as the streaming key and record the call_id mapping.
		d := &Delta{Session: session, CallID: firstNonEmpty(str(item, "id"), str(item, "call_id")), Tool: model.ToolIdentity{Namespace: "openai", Name: str(item, "name")}, Index: -1, Custom: str(item, "type") == "custom_tool_call"}
		if cid := str(item, "call_id"); cid != "" && cid != d.CallID {
			d.Aliases = append(d.Aliases, cid)
		}
		return []Record{{Delta: d}}, nil
	case "response.output_item.done":
		item := obj(m, "item")
		if t := str(item, "type"); t != "function_call" && t != "custom_tool_call" {
			return nil, nil
		}
		call, err := a.item(item, session)
		if err != nil {
			return nil, err
		}
		if id := str(item, "id"); id != "" && id != call.CallID {
			if call.CallID == "" {
				call.CallID = id
			} else {
				call.Aliases = append(call.Aliases, id)
			}
		}
		return []Record{{Call: call}}, nil
	case "response.function_call_arguments.delta", "response.custom_tool_call_input.delta":
		return []Record{{Delta: &Delta{Session: session, CallID: str(m, "item_id"), Index: intOf(m["output_index"]), Text: str(m, "delta"), Custom: typ == "response.custom_tool_call_input.delta"}}}, nil
	case "response.function_call_arguments.done", "response.custom_tool_call_input.done":
		text := str(m, "arguments")
		if typ == "response.custom_tool_call_input.done" {
			text = str(m, "input")
		}
		return []Record{{Delta: &Delta{Session: session, CallID: str(m, "item_id"), Index: intOf(m["output_index"]), Text: text, Done: true, Custom: typ == "response.custom_tool_call_input.done"}}}, nil
	}
	if m["output"] != nil && m["object"] != nil {
		// A whole response object: decode its output items.
		var items []json.RawMessage
		if err := json.Unmarshal(m["output"], &items); err != nil {
			return nil, fmt.Errorf("openai: output: %w", err)
		}
		var out []Record
		for _, it := range items {
			var im map[string]json.RawMessage
			if json.Unmarshal(it, &im) != nil {
				continue
			}
			if im["session"] == nil && session != "" {
				im["session"], _ = json.Marshal(session)
			}
			b, _ := json.Marshal(im)
			recs, err := a.Decode(b)
			if err != nil && err != ErrUnrecognized {
				return nil, err
			}
			out = append(out, recs...)
		}
		return out, nil
	}
	return nil, ErrUnrecognized
}

func (a OpenAI) item(m map[string]json.RawMessage, session string) (*Call, error) {
	call := &Call{Session: session, CallID: str(m, "call_id"), Provider: "openai", Tool: model.ToolIdentity{Namespace: "openai", Name: str(m, "name")}, Snapshot: true}
	if call.Tool.Name == "" {
		return nil, fmt.Errorf("openai: %s item without name", str(m, "type"))
	}
	if str(m, "type") == "custom_tool_call" {
		call.Input = model.Input{Kind: model.InputText, Text: str(m, "input")}
		return call, nil
	}
	args := str(m, "arguments")
	if args == "" {
		args = "{}"
	}
	if !json.Valid([]byte(args)) {
		// Arguments the model emitted are not valid JSON; keep them as text so
		// nothing disappears, and let the binding refuse them.
		call.Input = model.Input{Kind: model.InputText, Text: args}
		return call, nil
	}
	call.Input = model.Input{Kind: model.InputJSON, JSON: json.RawMessage(args)}
	return call, nil
}

func intOf(raw json.RawMessage) int {
	var n int
	_ = json.Unmarshal(raw, &n)
	return n
}
