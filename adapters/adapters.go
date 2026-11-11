// Package adapters normalizes provider and harness records into calls,
// argument fragments, results and lifecycle updates. Adapters decode
// transport shape only; they never interpret what a tool does.
//
// Every adapter pins the schema it was written against in its Version and
// fails explicitly on records it does not recognize.
package adapters

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/baum-ai/agentcmd/model"
)

// ErrUnrecognized is returned for records outside the adapter's schema.
var ErrUnrecognized = errors.New("adapters: record not recognized")

// Call is a complete tool call or a complete argument snapshot.
type Call struct {
	Session  string
	CallID   string
	Provider string
	Harness  string
	Tool     model.ToolIdentity
	Input    model.Input
	Context  model.ExecutionContext
	// Snapshot marks a complete argument set that supersedes any fragments
	// buffered for the same call.
	Snapshot bool
	// Aliases are other identifiers results may use for this call (an
	// OpenAI item id alongside its call_id).
	Aliases []string
	Time    string
}

// Delta is one streamed argument fragment.
type Delta struct {
	Session string
	CallID  string
	Tool    model.ToolIdentity
	Index   int
	Text    string
	// Done marks the final fragment; the call is complete after it.
	Done bool
	// Custom marks free-form (non-JSON) tool input.
	Custom bool
	// Aliases are other identifiers for the same call (an OpenAI call_id
	// announced alongside the streaming item id).
	Aliases []string
}

// Result is a tool result for a call.
type Result struct {
	Session string
	CallID  string
	// Name is the tool name when the transport carries one (Gemini);
	// it is used only to detect mismatches, never to associate.
	Name        string
	Observation model.Observation
	Lifecycle   model.LifecyclePhase
}

// Lifecycle is a harness-level state change for a call.
type Lifecycle struct {
	Session string
	CallID  string
	Event   model.LifecycleEvent
}

// Record is one decoded unit. Exactly one field is set.
type Record struct {
	Call      *Call
	Delta     *Delta
	Result    *Result
	Lifecycle *Lifecycle
}

// Adapter decodes one transport record (usually one JSON line).
type Adapter interface {
	// Name identifies the adapter ("openai.responses").
	Name() string
	// Version is the pinned upstream schema version.
	Version() string
	// Decode returns zero or more records. ErrUnrecognized means the line is
	// valid JSON outside the schema; other errors mean malformed input.
	Decode(line []byte) ([]Record, error)
}

// ByName returns a built-in adapter.
func ByName(name string) (Adapter, error) {
	switch name {
	case "canonical", "":
		return Canonical{}, nil
	case "openai", "openai.responses":
		return OpenAI{}, nil
	case "anthropic", "anthropic.messages":
		return Anthropic{}, nil
	case "gemini", "gemini.generate":
		return Gemini{}, nil
	case "claude-code", "claude-code.transcript":
		return ClaudeCode{}, nil
	case "codex", "codex.session":
		return Codex{}, nil
	}
	return nil, fmt.Errorf("adapters: unknown adapter %q", name)
}

// Names lists the built-in adapters.
func Names() []string {
	return []string{"canonical", "openai", "anthropic", "gemini", "claude-code", "codex"}
}

func str(m map[string]json.RawMessage, key string) string {
	var s string
	_ = json.Unmarshal(m[key], &s)
	return s
}

func obj(m map[string]json.RawMessage, key string) map[string]json.RawMessage {
	var o map[string]json.RawMessage
	_ = json.Unmarshal(m[key], &o)
	return o
}

// textOf renders a result payload as a short detail string without
// retaining large content.
func textOf(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return truncate(s)
	}
	return truncate(string(raw))
}

func truncate(s string) string {
	if len(s) > 200 {
		return s[:200] + "…"
	}
	return s
}
