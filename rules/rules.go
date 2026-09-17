// Package rules defines the registration contract for command rules: how a
// recognized executable's argv is turned into actions, wrapped commands or
// embedded programs. Rules are plain values registered explicitly; there is
// no global registry.
package rules

import (
	"fmt"
	"path"
	"sort"
	"strings"

	"github.com/lastpersonlabs/toolparser/model"
)

// Command is a simple command as seen by a rule.
type Command struct {
	Executable model.Value
	Args       []model.Value
	Cwd        model.Value
	Namespace  model.Namespace
	Span       model.SourceSpan
	// Wrappers already stripped to reach this command, outermost first.
	Wrappers []string
	// Env holds environment assignments that apply to this command only.
	Env map[string]model.Value
	// Stdin is set when the command's standard input is a known here-document
	// or here-string.
	Stdin *Stdin
}

// Stdin describes a known standard input.
type Stdin struct {
	Value model.Value
	// Source is the derived source ID holding the body when it is literal.
	Source string
}

// Program is an embedded program a launcher rule extracted.
type Program struct {
	// Language is shell, python, javascript, or any other name; unsupported
	// languages are preserved as unresolved executions.
	Language    string
	Interpreter string
	// Mode is inline (-c/-e), stdin, file, or module.
	Mode string
	// Source is the inline or stdin program text when known. Its Span points
	// at the argument or here-document in the parent source.
	Source *model.Value
	// SourceID is the derived source holding the text, when the caller
	// already registered one (here-documents).
	SourceID string
	// File is the script resource for mode file.
	File *model.Resource
	// Args are the positional arguments handed to the program.
	Args []model.Value
	// Dialect is the shell dialect for Language shell.
	Dialect string
}

// Result is what a rule produces for one command.
type Result struct {
	Actions []model.Action
	// Program requests analysis of nested source.
	Program *Program
	// Wrapped asks the engine to analyze the inner command instead; the
	// wrapper is recorded in the command's ancestry.
	Wrapped *Command
	// Namespace, when set with Wrapped, switches the inner command's
	// resource namespace (ssh, docker exec).
	Namespace   *model.Namespace
	Assumptions []string
	Diagnostics []model.Diagnostic
}

// Rule recognizes one or more executables.
type Rule struct {
	// ID is stable across versions, e.g. "std/cat".
	ID      string
	Version string
	// Executables are basenames the rule handles.
	Executables []string
	// Platform is "posix", "windows" or "" for any.
	Platform string
	// Doc points at the option grammar documentation used.
	Doc string
	// Apply interprets the command. It must not execute anything.
	Apply func(Command) Result
}

// Registry maps executables to rules. Collisions are rejected.
type Registry struct {
	byExe map[string]Rule
	ids   []string
}

// NewRegistry validates and indexes rules.
func NewRegistry(rules ...Rule) (*Registry, error) {
	r := &Registry{byExe: map[string]Rule{}}
	seen := map[string]bool{}
	for _, rule := range rules {
		if rule.ID == "" || rule.Version == "" || rule.Apply == nil || len(rule.Executables) == 0 {
			return nil, fmt.Errorf("rule %q: id, version, executables and apply are required", rule.ID)
		}
		if seen[rule.ID] {
			return nil, fmt.Errorf("rule %q registered twice", rule.ID)
		}
		seen[rule.ID] = true
		for _, exe := range rule.Executables {
			if prev, dup := r.byExe[exe]; dup {
				return nil, fmt.Errorf("executable %q claimed by both %s and %s", exe, prev.ID, rule.ID)
			}
			r.byExe[exe] = rule
		}
		r.ids = append(r.ids, rule.ID+"@"+rule.Version)
	}
	sort.Strings(r.ids)
	return r, nil
}

// Lookup finds the rule for an executable name or path.
func (r *Registry) Lookup(executable string) (Rule, bool) {
	if r == nil {
		return Rule{}, false
	}
	rule, ok := r.byExe[path.Base(executable)]
	return rule, ok
}

// Fingerprint identifies the registered rule set and versions.
func (r *Registry) Fingerprint() string {
	if r == nil {
		return "none"
	}
	return model.Fingerprint(r.ids)
}

// Resource builds a resource for an operand, lexically resolved against the
// command's working directory. Glob metacharacters downgrade knowledge to
// glob; symbolic or unknown values are never resolved.
func (c Command) Resource(v model.Value, role string) model.Resource {
	res := model.Resource{Role: role, Expression: v.Text, Knowledge: v.Knowledge, Namespace: c.Namespace, Span: v.Span}
	if v.Knowledge == model.KnowledgeUnknown || v.Knowledge == model.KnowledgeUnset {
		res.Expression = ""
		return res
	}
	if v.Concrete() && strings.ContainsAny(v.Text, "*?[") {
		res.Knowledge = model.KnowledgeGlob
	}
	if v.Concrete() || res.Knowledge == model.KnowledgeGlob {
		res.Resolved = Resolve(c.Cwd, v.Text)
	}
	return res
}

// Resolve joins a path with an abstract working directory lexically. It
// returns "" when the directory is not concrete and the path is relative.
func Resolve(cwd model.Value, p string) string {
	if p == "" {
		return ""
	}
	if path.IsAbs(p) {
		return path.Clean(p)
	}
	if !cwd.Concrete() || cwd.Text == "" {
		return ""
	}
	return path.Join(cwd.Text, p)
}

// NewAction builds an action with static evidence; the engine fills ID, node
// and rule identity.
func NewAction(kind model.ActionKind, basis model.Basis, resources ...model.Resource) model.Action {
	return model.Action{Kind: kind, Resources: resources, Evidence: model.Evidence{Basis: basis}}
}
