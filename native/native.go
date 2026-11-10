// Package native defines tool-contract bindings for structured (non-shell)
// tool invocations. A binding turns a tool's declared input into actions
// without re-encoding it as a shell command.
package native

import (
	"fmt"
	"sort"

	"github.com/baum-ai/agentcmd/model"
)

// ShellSource asks the engine to analyze a shell command string carried in a
// native tool's input.
type ShellSource struct {
	Text string
	// Field is the JSON pointer of the input field, for provenance.
	Field   string
	Dialect string
	// Workdir, when non-empty, is the working directory the text runs in. A
	// relative value resolves against the invocation's working directory and
	// is unknown when that is not known.
	Workdir string
	// WorkdirUnknown says the tool call names a working directory that is
	// not statically known; the text is then analyzed with an unknown one.
	// It takes precedence over Workdir.
	WorkdirUnknown bool
	// Span, when set, is the range of the root source (s0) the text was
	// taken from, for providers that carry shell text inside a larger
	// program. Without it the text maps onto the whole input.
	Span *model.SourceSpan
	// Reach qualifies how surely the text runs when the invocation does;
	// empty means always. A program that runs the text conditionally, in a
	// loop or inside an uncalled function says so here.
	Reach model.Reachability
}

// Part is a group of actions a binding attributes to one region of the
// input, with its own reachability. The engine gives it a node of its own.
type Part struct {
	// Reach qualifies the part; empty means always.
	Reach model.Reachability
	// Span is the region of the root source (s0) the part comes from.
	Span *model.SourceSpan
	// Actions of the part. With Unresolved set and no actions the engine
	// adds a process.unresolved action.
	Actions []model.Action
	// Unresolved, when set, says why the region could not be analyzed. The
	// part becomes an unknown node, with a diagnostic, and the analysis is
	// partial.
	Unresolved string
	// Assumptions are added to every action of the part.
	Assumptions []string
}

// Exec asks the engine to analyze a literal argv without shell parsing.
type Exec struct {
	Executable string
	Args       []string
	// Workdir overrides the invocation's working directory when set.
	Workdir string
}

// Result is what a binding produces.
type Result struct {
	Actions []model.Action
	Shell   *ShellSource
	// Shells are further shell texts, each analyzed independently with its
	// own working directory and shell state (a call that runs several
	// commands, such as a code-mode program). They follow Shell.
	Shells []ShellSource
	// Parts are further regions with their own reachability: nested actions
	// and unresolved calls of a program.
	Parts []Part
	Exec  *Exec
	// Detail labels the native node (e.g. "read", "edit", "patch").
	Detail      string
	Assumptions []string
	Diagnostics []model.Diagnostic
	// Hint marks classifications derived from declared annotations rather
	// than a verified contract.
	Hint bool
}

// Binding is a versioned tool contract.
type Binding struct {
	ID      string
	Version string
	// Namespace and Tool identify the tool. Namespace "" matches only tools
	// without a namespace.
	Namespace string
	Tool      string
	// SchemaFingerprint, when set, must match the invocation's.
	SchemaFingerprint string
	// Harness names the profile the binding was captured from.
	Harness string
	Doc     string
	// Bind interprets the invocation's input. It must not execute anything.
	Bind func(inv *model.Invocation) (Result, error)
}

type key struct{ ns, tool string }

// Registry indexes bindings by namespace and tool.
type Registry struct {
	m   map[key][]Binding
	ids []string
}

// NewRegistry validates bindings. Two bindings for the same tool must differ
// in schema fingerprint.
func NewRegistry(bindings ...Binding) (*Registry, error) {
	r := &Registry{m: map[key][]Binding{}}
	for _, b := range bindings {
		if b.ID == "" || b.Version == "" || b.Tool == "" || b.Bind == nil {
			return nil, fmt.Errorf("binding %q: id, version, tool and bind are required", b.ID)
		}
		k := key{b.Namespace, b.Tool}
		for _, prev := range r.m[k] {
			if prev.SchemaFingerprint == b.SchemaFingerprint {
				return nil, fmt.Errorf("tool %s/%s claimed by both %s and %s", b.Namespace, b.Tool, prev.ID, b.ID)
			}
		}
		r.m[k] = append(r.m[k], b)
		r.ids = append(r.ids, b.ID+"@"+b.Version)
	}
	sort.Strings(r.ids)
	return r, nil
}

// Lookup finds the binding for a tool identity. A binding with a schema
// fingerprint only matches an identical fingerprint; a binding without one
// matches any.
func (r *Registry) Lookup(t model.ToolIdentity) (Binding, bool) {
	if r == nil {
		return Binding{}, false
	}
	var fallback *Binding
	for i, b := range r.m[key{t.Namespace, t.Name}] {
		if b.SchemaFingerprint == "" {
			fallback = &r.m[key{t.Namespace, t.Name}][i]
			continue
		}
		if b.SchemaFingerprint == t.SchemaFingerprint {
			return b, true
		}
	}
	if fallback != nil {
		return *fallback, true
	}
	return Binding{}, false
}

// Fingerprint identifies the registered bindings and versions.
func (r *Registry) Fingerprint() string {
	if r == nil {
		return "none"
	}
	return model.Fingerprint(r.ids)
}

// Resource builds a resource for a native path argument resolved against the
// invocation's working directory when it is known.
func Resource(inv *model.Invocation, p, role string) model.Resource {
	res := model.Resource{Role: role, Expression: p, Knowledge: model.KnowledgeLiteral, Namespace: inv.Context.Namespace, Snapshot: inv.Context.Snapshot}
	if res.Namespace.Kind == "" {
		res.Namespace.Kind = model.NamespaceUnknown
	}
	switch {
	case p == "":
		res.Knowledge = model.KnowledgeUnknown
	case len(p) > 0 && p[0] == '/':
		res.Resolved = cleanPath(p)
	case inv.Context.WorkingDir.Known:
		res.Resolved = cleanPath(inv.Context.WorkingDir.Value + "/" + p)
	}
	return res
}

// Annotations are MCP ToolAnnotations. The protocol states they are hints
// that need not describe behaviour faithfully; a binding built from them is
// marked Hint and produces no actions.
type Annotations struct {
	Title           string `json:"title,omitempty"`
	ReadOnlyHint    *bool  `json:"readOnlyHint,omitempty"`
	DestructiveHint *bool  `json:"destructiveHint,omitempty"`
	IdempotentHint  *bool  `json:"idempotentHint,omitempty"`
	OpenWorldHint   *bool  `json:"openWorldHint,omitempty"`
}

// FromAnnotations builds a hint-only binding for an MCP tool.
func FromAnnotations(namespace, tool string, ann Annotations) Binding {
	detail := "declared:"
	flag := func(name string, v *bool) {
		if v != nil {
			detail += fmt.Sprintf(" %s=%t", name, *v)
		}
	}
	flag("read-only", ann.ReadOnlyHint)
	flag("destructive", ann.DestructiveHint)
	flag("idempotent", ann.IdempotentHint)
	flag("open-world", ann.OpenWorldHint)
	return Binding{ID: "mcp/annotations/" + namespace + "/" + tool, Version: "2025-11-25", Namespace: namespace, Tool: tool, Doc: "MCP ToolAnnotations",
		Bind: func(*model.Invocation) (Result, error) {
			return Result{Detail: detail, Hint: true, Diagnostics: []model.Diagnostic{{Code: model.DiagUnknownTool, Severity: model.SeverityInfo, Message: "tool effects known only from declared annotations, which are hints"}}}, nil
		}}
}
