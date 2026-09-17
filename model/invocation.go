// Package model is the public, versioned representation of invocations,
// analyses, actions, evidence and observations. It has no dependencies on the
// analyzer and is safe to embed in other systems.
package model

import (
	"encoding/json"
	"errors"
	"fmt"
)

// SchemaVersion is the version of the JSON contract produced and accepted by
// this package. Readers accept the same major version.
const SchemaVersion = "0.1.0"

// Invocation is one tool call as requested by an agent, with whatever
// context and lifecycle evidence the source supplied.
type Invocation struct {
	// ID is the caller-assigned local occurrence identity. Required.
	ID string `json:"id"`
	// Source points at the record this invocation was decoded from.
	Source *SourceRef `json:"source,omitempty"`

	Provider       string `json:"provider,omitempty"`
	Harness        string `json:"harness,omitempty"`
	HarnessVersion string `json:"harness_version,omitempty"`
	Session        string `json:"session,omitempty"`
	Run            string `json:"run,omitempty"`

	Tool ToolIdentity `json:"tool"`
	// CallID is the provider's call identifier, scoped by Session.
	CallID string `json:"call_id,omitempty"`

	Input     Input            `json:"input"`
	Context   ExecutionContext `json:"context"`
	Lifecycle []LifecycleEvent `json:"lifecycle,omitempty"`
}

// SourceRef identifies where a record came from.
type SourceRef struct {
	Kind    string `json:"kind"`              // e.g. "openai.responses", "anthropic.messages", "file"
	Locator string `json:"locator,omitempty"` // path, URL or stream name
	Offset  int    `json:"offset,omitempty"`  // record index or byte offset
	Field   string `json:"field,omitempty"`   // JSON pointer to the field carrying the input
}

// ToolIdentity names a tool within a namespace.
type ToolIdentity struct {
	Namespace         string `json:"namespace,omitempty"` // e.g. "harness", "mcp:github"
	Name              string `json:"name"`
	SchemaFingerprint string `json:"schema_fingerprint,omitempty"`
}

// InputKind discriminates Input.
type InputKind string

const (
	InputJSON   InputKind = "json"   // structured arguments
	InputText   InputKind = "text"   // free-form text (custom tools, shell strings)
	InputArgv   InputKind = "argv"   // executable plus literal arguments
	InputSource InputKind = "source" // inline program source in a named language
	InputPatch  InputKind = "patch"  // patch text
	InputBlob   InputKind = "blob"   // caller-owned reference
)

// Input is a tagged union. Exactly the fields for Kind may be set.
type Input struct {
	Kind InputKind `json:"kind"`

	JSON json.RawMessage `json:"json,omitempty"`
	Text string          `json:"text,omitempty"`

	Executable string   `json:"executable,omitempty"`
	Argv       []string `json:"argv,omitempty"`

	Language string `json:"language,omitempty"`
	Source   string `json:"source,omitempty"`

	Patch string   `json:"patch,omitempty"`
	Blob  *BlobRef `json:"blob,omitempty"`
}

// BlobRef refers to content held by the caller.
type BlobRef struct {
	ID     string `json:"id"`
	Length int    `json:"length,omitempty"`
	Digest string `json:"digest,omitempty"`
}

// Fact is a context value with an origin. Known=false means unknown, which
// is different from an empty value.
type Fact struct {
	Known  bool   `json:"known"`
	Value  string `json:"value,omitempty"`
	Origin string `json:"origin,omitempty"` // reported, configured, inferred, assumed
}

// KnownFact builds a known fact.
func KnownFact(value, origin string) Fact { return Fact{Known: true, Value: value, Origin: origin} }

// PathFlavor is the path syntax of the execution environment.
type PathFlavor string

const (
	PathPOSIX   PathFlavor = "posix"
	PathWindows PathFlavor = "windows"
)

// NamespaceKind classifies where resources live.
type NamespaceKind string

const (
	NamespaceHost      NamespaceKind = "host"
	NamespaceContainer NamespaceKind = "container"
	NamespaceRemote    NamespaceKind = "remote"
	NamespaceUnknown   NamespaceKind = "unknown"
)

// Namespace is an execution/resource namespace. A relative path in a
// container is not a host path.
type Namespace struct {
	Kind NamespaceKind `json:"kind"`
	ID   string        `json:"id,omitempty"`
}

// ExecutionContext holds what is known about where the tool ran.
type ExecutionContext struct {
	WorkingDir   Fact            `json:"working_dir"`
	PathFlavor   PathFlavor      `json:"path_flavor,omitempty"`
	Shell        Fact            `json:"shell"`
	Env          map[string]Fact `json:"env,omitempty"`
	Namespace    Namespace       `json:"namespace"`
	Snapshot     string          `json:"snapshot,omitempty"`
	ShellSession string          `json:"shell_session,omitempty"`
}

// LifecyclePhase is one step in a call's life.
type LifecyclePhase string

const (
	PhaseRequested LifecyclePhase = "requested"
	PhaseApproved  LifecyclePhase = "approved"
	PhaseDenied    LifecyclePhase = "denied"
	PhaseStarted   LifecyclePhase = "started"
	PhaseCompleted LifecyclePhase = "completed" // reported completed by the tool/harness
	PhaseCancelled LifecyclePhase = "cancelled"
	PhaseTimedOut  LifecyclePhase = "timed_out"
	PhaseUnknown   LifecyclePhase = "unknown"
)

// LifecycleEvent is append-only evidence about a call's progress.
type LifecycleEvent struct {
	Phase  LifecyclePhase `json:"phase"`
	Time   string         `json:"time,omitempty"`   // RFC 3339 when known
	Source string         `json:"source,omitempty"` // provider, harness, collector
	Detail string         `json:"detail,omitempty"`
}

// Validate checks structural validity, including the input union.
func (inv *Invocation) Validate() error {
	if inv.ID == "" {
		return errors.New("invocation: id is required")
	}
	if inv.Tool.Name == "" {
		return errors.New("invocation: tool.name is required")
	}
	if err := inv.Input.Validate(); err != nil {
		return fmt.Errorf("invocation %s: %w", inv.ID, err)
	}
	for i, ev := range inv.Lifecycle {
		if ev.Phase == "" {
			return fmt.Errorf("invocation %s: lifecycle[%d]: phase is required", inv.ID, i)
		}
	}
	return nil
}

// Validate rejects unions with missing or contradictory fields.
func (in *Input) Validate() error {
	has := map[string]bool{
		"json":       in.JSON != nil,
		"text":       in.Text != "",
		"executable": in.Executable != "" || in.Argv != nil,
		"source":     in.Language != "" || in.Source != "",
		"patch":      in.Patch != "",
		"blob":       in.Blob != nil,
	}
	want := map[InputKind]string{
		InputJSON: "json", InputText: "text", InputArgv: "executable",
		InputSource: "source", InputPatch: "patch", InputBlob: "blob",
	}
	field, ok := want[in.Kind]
	if !ok {
		return fmt.Errorf("input: unknown kind %q", in.Kind)
	}
	for name, set := range has {
		if set && name != field {
			return fmt.Errorf("input: kind %q must not set %s", in.Kind, name)
		}
	}
	switch in.Kind {
	case InputJSON:
		if !json.Valid(in.JSON) {
			return errors.New("input: json is not valid JSON")
		}
	case InputArgv:
		if in.Executable == "" {
			return errors.New("input: argv requires executable")
		}
	case InputSource:
		if in.Language == "" {
			return errors.New("input: source requires language")
		}
	case InputBlob:
		if in.Blob.ID == "" {
			return errors.New("input: blob requires id")
		}
	case InputText:
		// An empty text input is allowed only through Kind; nothing to check.
	}
	return nil
}
