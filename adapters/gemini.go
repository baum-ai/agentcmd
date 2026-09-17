package adapters

import (
	"encoding/json"
	"fmt"

	"github.com/lastpersonlabs/agentcmd/model"
)

// Gemini decodes generateContent parts:
//
//   - {"functionCall":{"id"?,"name","args"}} and
//   - {"functionResponse":{"id"?,"name","response"}},
//
// either as bare parts or inside {"content":{"parts":[...]}} /
// {"candidates":[{"content":{"parts":[...]}}]}. Function calls without an
// id get a positional key "<name>#<n>" within the session; the matching
// response uses the same rule, and the observation records that the
// association is positional rather than by identifier.
type Gemini struct{}

func (Gemini) Name() string    { return "gemini.generate" }
func (Gemini) Version() string { return "v1beta-2025" }

func (g Gemini) Decode(line []byte) ([]Record, error) {
	var m map[string]json.RawMessage
	if err := json.Unmarshal(line, &m); err != nil {
		return nil, fmt.Errorf("gemini: %w", err)
	}
	session := str(m, "session")
	switch {
	case m["functionCall"] != nil || m["functionResponse"] != nil:
		return g.part(m, session)
	case m["content"] != nil:
		return g.content(obj(m, "content"), session)
	case m["candidates"] != nil:
		var cands []map[string]json.RawMessage
		if err := json.Unmarshal(m["candidates"], &cands); err != nil {
			return nil, fmt.Errorf("gemini: candidates: %w", err)
		}
		var out []Record
		for _, c := range cands {
			recs, err := g.content(obj(c, "content"), session)
			if err != nil {
				return nil, err
			}
			out = append(out, recs...)
		}
		return out, nil
	}
	return nil, ErrUnrecognized
}

func (g Gemini) content(content map[string]json.RawMessage, session string) ([]Record, error) {
	var parts []map[string]json.RawMessage
	if err := json.Unmarshal(content["parts"], &parts); err != nil {
		return nil, ErrUnrecognized
	}
	var out []Record
	for _, p := range parts {
		recs, err := g.part(p, session)
		if err != nil && err != ErrUnrecognized {
			return nil, err
		}
		out = append(out, recs...)
	}
	return out, nil
}

func (g Gemini) part(p map[string]json.RawMessage, session string) ([]Record, error) {
	if fc := obj(p, "functionCall"); fc != nil {
		name := str(fc, "name")
		if name == "" {
			return nil, fmt.Errorf("gemini: functionCall without name")
		}
		args := fc["args"]
		if args == nil {
			args = json.RawMessage("{}")
		}
		return []Record{{Call: &Call{Session: session, CallID: str(fc, "id"), Provider: "gemini", Tool: model.ToolIdentity{Namespace: "gemini", Name: name},
			Input: model.Input{Kind: model.InputJSON, JSON: args}, Snapshot: true}}}, nil
	}
	if fr := obj(p, "functionResponse"); fr != nil {
		obs := model.Observation{Kind: model.ObsToolResult, Basis: model.BasisToolReport, Relation: model.RelationInforms, Collector: g.Name(), Detail: textOf(fr["response"])}
		return []Record{{Result: &Result{Session: session, CallID: str(fr, "id"), Name: str(fr, "name"), Lifecycle: model.PhaseCompleted, Observation: obs}}}, nil
	}
	return nil, ErrUnrecognized
}
