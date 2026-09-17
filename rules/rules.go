// Package rules defines the registration contract for command rules: how a
// recognized executable's argv is turned into actions, wrapped commands or
// embedded programs. Rules are plain values registered explicitly; there is
// no global registry.
package rules

import (
	"fmt"
	"path"
	"sort"

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
	// Namespace switches the program's resource namespace (ssh).
	Namespace *model.Namespace
}

// Script asks the engine to resolve a project script from a package
// manifest (package.json "scripts") when a snapshot resolver is available.
type Script struct {
	Manager  string
	Name     string
	Manifest string // manifest file name relative to the working directory
}

// Result is what a rule produces for one command.
type Result struct {
	Actions []model.Action
	// Program requests analysis of nested source.
	Program *Program
	// Script requests resolution of a project script; without a resolver
	// the engine records an unresolved execution.
	Script *Script
	// Wrapped asks the engine to analyze the inner command instead; the
	// wrapper is recorded in the command's ancestry.
	Wrapped *Command
	// Namespace, when set with Wrapped, switches the inner command's
	// resource namespace (docker exec, kubectl exec).
	Namespace *model.Namespace
	// Cwd, when set with Wrapped, is the inner command's working directory
	// (env -C, sudo -D, docker exec -w).
	Cwd         *model.Value
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
// command's working directory. Symbolic or unknown values are never
// resolved; bounded alternatives of the operand or the directory are listed.
func (c Command) Resource(v model.Value, role string) model.Resource {
	res := model.Resource{Role: role, Expression: v.Text, Knowledge: v.Knowledge, Namespace: c.Namespace, Span: v.Span}
	switch v.Knowledge {
	case model.KnowledgeUnknown, model.KnowledgeUnset:
		res.Expression = ""
		return res
	case model.KnowledgeSymbolic, model.KnowledgeRedacted:
		return res
	}
	// literal, derived or glob: resolve each alternative of operand × cwd.
	ops := v.Alternatives
	if len(ops) == 0 {
		ops = []string{v.Text}
	}
	dirs := c.Cwd.Alternatives
	if len(dirs) == 0 {
		dirs = []string{c.Cwd.Text}
	}
	var out []string
	for _, op := range ops {
		for _, dir := range dirs {
			r := resolve(c.Cwd.Knowledge, dir, op)
			if r != "" && !contains(out, r) {
				out = append(out, r)
			}
		}
	}
	switch {
	case len(out) == 1 && len(ops) == 1:
		res.Resolved = out[0]
	case len(out) > 1:
		res.Alternatives = out
		if res.Knowledge == model.KnowledgeLiteral {
			res.Knowledge = model.KnowledgeDerived
		}
	}
	return res
}

func contains(xs []string, x string) bool {
	for _, y := range xs {
		if y == x {
			return true
		}
	}
	return false
}

// Resolve joins a path with an abstract working directory lexically. It
// returns "" when the directory is not concrete and the path is relative.
func Resolve(cwd model.Value, p string) string {
	if !cwd.Concrete() {
		return resolve(model.KnowledgeUnknown, "", p)
	}
	return resolve(cwd.Knowledge, cwd.Text, p)
}

func resolve(cwdKnowledge model.Knowledge, cwd, p string) string {
	if p == "" {
		return ""
	}
	if path.IsAbs(p) {
		return path.Clean(p)
	}
	switch cwdKnowledge {
	case model.KnowledgeLiteral, model.KnowledgeDerived:
	default:
		return ""
	}
	if cwd == "" {
		return ""
	}
	return path.Join(cwd, p)
}

// NewAction builds an action with static evidence; the engine fills ID, node
// and rule identity.
func NewAction(kind model.ActionKind, basis model.Basis, resources ...model.Resource) model.Action {
	return model.Action{Kind: kind, Resources: resources, Evidence: model.Evidence{Basis: basis}}
}
