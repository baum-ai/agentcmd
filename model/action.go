package model

import (
	"encoding/json"
	"fmt"
	"strings"
)

// ActionKind is "family.operation".
type ActionKind string

// Known action kinds. Unknown kinds decode as opaque payloads.
const (
	FSRead     ActionKind = "filesystem.read"
	FSWrite    ActionKind = "filesystem.write"
	FSDelete   ActionKind = "filesystem.delete"
	FSCopy     ActionKind = "filesystem.copy"
	FSMove     ActionKind = "filesystem.move"
	FSList     ActionKind = "filesystem.list"
	FSMetadata ActionKind = "filesystem.metadata"

	SearchContent ActionKind = "search.content"
	SearchPath    ActionKind = "search.path"

	ProcessExecute     ActionKind = "process.execute"
	ProcessShell       ActionKind = "process.shell"
	ProcessInterpreter ActionKind = "process.interpreter"
	ProcessUnresolved  ActionKind = "process.unresolved"

	GitInspect ActionKind = "git.inspect"
	GitStage   ActionKind = "git.stage"
	GitCommit  ActionKind = "git.commit"
	GitSwitch  ActionKind = "git.switch"
	GitMerge   ActionKind = "git.merge"
	GitReset   ActionKind = "git.reset"
	GitNetwork ActionKind = "git.network"

	DevTest   ActionKind = "development.test"
	DevBuild  ActionKind = "development.build"
	DevLint   ActionKind = "development.lint"
	DevFormat ActionKind = "development.format"

	PkgInstall   ActionKind = "packages.install"
	PkgRemove    ActionKind = "packages.remove"
	PkgRunScript ActionKind = "packages.run_script"

	NetRequest    ActionKind = "network.request"
	NetUpload     ActionKind = "network.upload"
	NetDownload   ActionKind = "network.download"
	NetUnresolved ActionKind = "network.unresolved"

	NativePatch    ActionKind = "native.patch"
	NativeFileOp   ActionKind = "native.file_operation"
	NativeDelegate ActionKind = "native.delegate"
)

var knownKinds = map[ActionKind]bool{
	FSRead: true, FSWrite: true, FSDelete: true, FSCopy: true, FSMove: true, FSList: true, FSMetadata: true,
	SearchContent: true, SearchPath: true,
	ProcessExecute: true, ProcessShell: true, ProcessInterpreter: true, ProcessUnresolved: true,
	GitInspect: true, GitStage: true, GitCommit: true, GitSwitch: true, GitMerge: true, GitReset: true, GitNetwork: true,
	DevTest: true, DevBuild: true, DevLint: true, DevFormat: true,
	PkgInstall: true, PkgRemove: true, PkgRunScript: true,
	NetRequest: true, NetUpload: true, NetDownload: true, NetUnresolved: true,
	NativePatch: true, NativeFileOp: true, NativeDelegate: true,
}

// Known reports whether this reader understands the kind.
func (k ActionKind) Known() bool { return knownKinds[k] }

// Family returns the part before the dot.
func (k ActionKind) Family() string {
	fam, _, _ := strings.Cut(string(k), ".")
	return fam
}

// WriteMode is how a write affects existing content.
type WriteMode string

const (
	WriteAppend          WriteMode = "append"
	WriteTruncate        WriteMode = "truncate"
	WriteCreateExclusive WriteMode = "create_exclusive"
	WriteInPlace         WriteMode = "in_place"
	WriteUnknown         WriteMode = "unknown"
)

// WriteDetail qualifies filesystem.write.
type WriteDetail struct {
	Mode WriteMode `json:"mode"`
	// Directory is set for mkdir-like operations.
	Directory bool `json:"directory,omitempty"`
}

// DeleteDetail qualifies filesystem.delete.
type DeleteDetail struct {
	Recursive bool `json:"recursive,omitempty"`
	Force     bool `json:"force,omitempty"`
}

// ProcessDetail qualifies process.* actions.
type ProcessDetail struct {
	Executable Value   `json:"executable"`
	Args       []Value `json:"args,omitempty"`
	// Shell is set when a shell interprets a command string.
	Shell bool `json:"shell,omitempty"`
	// Reason explains an unresolved execution.
	Reason string `json:"reason,omitempty"`
}

// GitDetail qualifies git.* actions.
type GitDetail struct {
	Subcommand string `json:"subcommand"`
	Remote     string `json:"remote,omitempty"`
	Ref        string `json:"ref,omitempty"`
}

// IntentDetail qualifies development.* actions. Verified is true only when a
// known tool was invoked directly, not for an arbitrary target named "test".
type IntentDetail struct {
	Tool     string `json:"tool"`
	Target   string `json:"target,omitempty"`
	Verified bool   `json:"verified"`
}

// PackageDetail qualifies packages.* actions.
type PackageDetail struct {
	Manager  string   `json:"manager"`
	Packages []string `json:"packages,omitempty"`
	Script   string   `json:"script,omitempty"`
}

// NetworkDetail qualifies network.* actions.
type NetworkDetail struct {
	URL    Value  `json:"url"`
	Method string `json:"method,omitempty"`
}

// SearchDetail qualifies search.* actions.
type SearchDetail struct {
	Pattern Value `json:"pattern"`
}

// PatchFile is one file touched by a patch, as declared by the patch text.
type PatchFile struct {
	Path      string `json:"path"`
	Operation string `json:"operation"` // add, update, delete, move
	MoveTo    string `json:"move_to,omitempty"`
	Hunks     int    `json:"hunks,omitempty"`
}

// NativeDetail qualifies native.* actions.
type NativeDetail struct {
	Operation string      `json:"operation,omitempty"`
	Format    string      `json:"format,omitempty"`
	Files     []PatchFile `json:"files,omitempty"`
	Agent     string      `json:"agent,omitempty"`
}

// Action is an inferred or requested operation attached to a node.
type Action struct {
	ID        string     `json:"id"`
	Node      string     `json:"node"`
	Kind      ActionKind `json:"kind"`
	Resources []Resource `json:"resources,omitempty"`
	Evidence  Evidence   `json:"evidence"`

	Write   *WriteDetail   `json:"write,omitempty"`
	Delete  *DeleteDetail  `json:"delete,omitempty"`
	Process *ProcessDetail `json:"process,omitempty"`
	Git     *GitDetail     `json:"git,omitempty"`
	Intent  *IntentDetail  `json:"intent,omitempty"`
	Package *PackageDetail `json:"package,omitempty"`
	Network *NetworkDetail `json:"network,omitempty"`
	Search  *SearchDetail  `json:"search,omitempty"`
	Native  *NativeDetail  `json:"native,omitempty"`

	// opaque holds the original JSON for unknown kinds so they round-trip.
	opaque json.RawMessage
}

// Opaque reports whether the action was decoded from an unknown kind.
func (a *Action) Opaque() bool { return a.opaque != nil }

type actionJSON Action

// UnmarshalJSON keeps unknown kinds as opaque payloads.
func (a *Action) UnmarshalJSON(data []byte) error {
	var plain actionJSON
	if err := json.Unmarshal(data, &plain); err != nil {
		return err
	}
	*a = Action(plain)
	if !a.Kind.Known() {
		a.opaque = append(json.RawMessage(nil), data...)
	}
	return nil
}

// MarshalJSON emits opaque actions verbatim.
func (a Action) MarshalJSON() ([]byte, error) {
	if a.opaque != nil {
		return a.opaque, nil
	}
	return json.Marshal(actionJSON(a))
}

// Validate rejects contradictory known payloads. Opaque actions are accepted
// as long as they have an ID and node.
func (a *Action) Validate() error {
	if a.ID == "" || a.Node == "" {
		return fmt.Errorf("action %q: id and node are required", a.ID)
	}
	if !a.Evidence.Basis.Static() {
		return fmt.Errorf("action %s: basis %q is not a static inference", a.ID, a.Evidence.Basis)
	}
	if !a.Kind.Known() {
		return nil
	}
	payloads := map[string]bool{
		"write": a.Write != nil, "delete": a.Delete != nil, "process": a.Process != nil,
		"git": a.Git != nil, "intent": a.Intent != nil, "package": a.Package != nil,
		"network": a.Network != nil, "search": a.Search != nil, "native": a.Native != nil,
	}
	allowed := map[string]string{
		"filesystem": "", "search": "search", "process": "process", "git": "git",
		"development": "intent", "packages": "package", "network": "network", "native": "native",
	}
	want := allowed[a.Kind.Family()]
	if a.Kind == FSWrite {
		want = "write"
	}
	if a.Kind == FSDelete {
		want = "delete"
	}
	for name, set := range payloads {
		if set && name != want {
			return fmt.Errorf("action %s: kind %s must not carry %s payload", a.ID, a.Kind, name)
		}
	}
	if a.Kind == FSWrite && a.Write == nil {
		return fmt.Errorf("action %s: filesystem.write requires write.mode", a.ID)
	}
	return nil
}
