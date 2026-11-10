package adapters

import (
	"encoding/json"
	"fmt"

	"github.com/baum-ai/agentcmd/model"
)

// Canonical decodes this project's own record shapes: a model.Invocation
// object, or an envelope {"observation": {...}, "session": ..., "call_id":
// ...} / {"lifecycle": {...}, ...}.
type Canonical struct{}

func (Canonical) Name() string    { return "canonical" }
func (Canonical) Version() string { return model.SchemaVersion }

func (Canonical) Decode(line []byte) ([]Record, error) {
	var probe map[string]json.RawMessage
	if err := json.Unmarshal(line, &probe); err != nil {
		return nil, fmt.Errorf("canonical: %w", err)
	}
	switch {
	case probe["observation"] != nil:
		var o model.Observation
		if err := json.Unmarshal(probe["observation"], &o); err != nil {
			return nil, fmt.Errorf("canonical: observation: %w", err)
		}
		if o.Basis == "" {
			o.Basis = model.BasisToolReport
		}
		return []Record{{Result: &Result{Session: str(probe, "session"), CallID: str(probe, "call_id"), Observation: o}}}, nil
	case probe["lifecycle"] != nil:
		var ev model.LifecycleEvent
		if err := json.Unmarshal(probe["lifecycle"], &ev); err != nil {
			return nil, fmt.Errorf("canonical: lifecycle: %w", err)
		}
		return []Record{{Lifecycle: &Lifecycle{Session: str(probe, "session"), CallID: str(probe, "call_id"), Event: ev}}}, nil
	case probe["tool"] != nil && probe["input"] != nil:
		var inv model.Invocation
		if err := json.Unmarshal(line, &inv); err != nil {
			return nil, fmt.Errorf("canonical: invocation: %w", err)
		}
		if err := inv.Validate(); err != nil {
			return nil, fmt.Errorf("canonical: %w", err)
		}
		return []Record{{Call: &Call{Session: inv.Session, CallID: firstNonEmpty(inv.CallID, inv.ID), Provider: inv.Provider, Harness: inv.Harness, Tool: inv.Tool, Input: inv.Input, Context: inv.Context, Snapshot: true}}}, nil
	}
	return nil, ErrUnrecognized
}

func firstNonEmpty(xs ...string) string {
	for _, x := range xs {
		if x != "" {
			return x
		}
	}
	return ""
}
