// Package render produces JSON and deterministic human-readable summaries of
// an analysis. Text output leads with operations, qualifies each by its
// evidence basis, and lists unknowns. It never prints static order as
// timestamps and never upgrades an inference to an observation.
package render

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/baum-ai/agentcmd/model"
)

// JSON writes the analysis as JSON, optionally indented.
func JSON(w io.Writer, a *model.Analysis, indent bool) error {
	enc := json.NewEncoder(w)
	if indent {
		enc.SetIndent("", "  ")
	}
	return enc.Encode(a)
}

// TextOptions control the human summary.
type TextOptions struct {
	// ShowSource includes retained source text. Off by default: sources may
	// contain secrets.
	ShowSource bool
	// Structure prints the execution tree.
	Structure bool
	// MaxField truncates untrusted strings; 0 means 160 bytes.
	MaxField int
}

// verb maps an evidence basis to the qualifier a reader sees.
func verb(b model.Basis) string {
	switch b {
	case model.BasisToolContract:
		return "requested"
	case model.BasisSyntax, model.BasisBoundedValue:
		return "inferred"
	case model.BasisHeuristic:
		return "intent"
	case model.BasisToolReport:
		return "reported"
	case model.BasisRuntime:
		return "observed"
	}
	return string(b)
}

// Text writes the deterministic summary.
func Text(w io.Writer, a *model.Analysis, opts TextOptions) error {
	if opts.MaxField <= 0 {
		opts.MaxField = 160
	}
	s := sanitizer{max: opts.MaxField}
	nodes := map[string]model.Node{}
	for _, n := range a.Nodes {
		nodes[n.ID] = n
	}
	var b strings.Builder
	status := "complete"
	if a.Partial {
		status = "PARTIAL"
	}
	fmt.Fprintf(&b, "analysis %s of invocation %s (%s; analyzer %s)\n", a.ID, s.clean(a.Invocation), status, a.Versions.Analyzer)

	if len(a.Actions) == 0 {
		b.WriteString("\noperations: none inferred or requested (this is not a safety claim)\n")
	} else {
		b.WriteString("\noperations:\n")
		for _, act := range a.Actions {
			fmt.Fprintf(&b, "  %-4s %-9s %-22s", act.ID, verb(act.Evidence.Basis), act.Kind)
			for _, r := range act.Resources {
				b.WriteString(" " + s.resource(r))
			}
			b.WriteString(detail(act, s))
			if q := qualifiers(act, nodes); q != "" {
				b.WriteString("  [" + q + "]")
			}
			b.WriteString("\n")
			if len(act.Evidence.Assumptions) > 0 {
				fmt.Fprintf(&b, "       assuming: %s\n", s.clean(strings.Join(act.Evidence.Assumptions, "; ")))
			}
		}
	}

	var unknowns []model.Node
	for _, n := range a.Nodes {
		if n.Kind == model.NodeUnknown || n.Syntax == model.SyntaxUnsupported || n.Syntax == model.SyntaxInvalid || n.Syntax == model.SyntaxPartial || n.Syntax == model.SyntaxLimit {
			unknowns = append(unknowns, n)
		}
	}
	if len(unknowns) > 0 {
		b.WriteString("\nunknown regions:\n")
		for _, n := range unknowns {
			fmt.Fprintf(&b, "  %-4s %-8s %s %s\n", n.ID, n.Kind, n.Syntax, s.clean(n.Detail))
		}
	}
	if len(a.Diagnostics) > 0 {
		b.WriteString("\ndiagnostics:\n")
		for _, d := range a.Diagnostics {
			loc := ""
			if d.Node != "" {
				loc = " (" + d.Node + ")"
			}
			fmt.Fprintf(&b, "  %-7s %-20s %s%s\n", d.Severity, d.Code, s.clean(d.Message), loc)
		}
	}
	if len(a.Observations) > 0 {
		b.WriteString("\nobservations:\n")
		for _, o := range a.Observations {
			extra := ""
			if o.ExitCode != nil {
				extra = fmt.Sprintf(" exit=%d", *o.ExitCode)
			}
			if o.Success != nil {
				extra += fmt.Sprintf(" success=%t", *o.Success)
			}
			fmt.Fprintf(&b, "  %-4s %-9s %-13s on %s%s %s %s\n", o.ID, verb(o.Basis), o.Kind, o.Subject, extra, o.Relation, s.clean(o.Detail))
		}
	}
	if opts.Structure {
		b.WriteString("\nstructure:\n")
		writeTree(&b, a.Nodes, "", 0, s)
	}
	if opts.ShowSource {
		b.WriteString("\nsources:\n")
		for _, src := range a.Sources {
			from := ""
			if src.Derived != nil {
				from = fmt.Sprintf(" derived from %s exact=%t", src.Derived.From, src.Derived.Exact)
			}
			fmt.Fprintf(&b, "  %s %s %d bytes%s\n", src.ID, src.Kind, src.Length, from)
			if src.Text != "" {
				for _, line := range strings.Split(strings.TrimRight(src.Text, "\n"), "\n") {
					fmt.Fprintf(&b, "    | %s\n", s.clean(line))
				}
			}
		}
	}
	_, err := io.WriteString(w, b.String())
	return err
}

func detail(act model.Action, s sanitizer) string {
	switch {
	case act.Write != nil:
		out := " mode=" + string(act.Write.Mode)
		if act.Write.Directory {
			out += " directory"
		}
		return out
	case act.Delete != nil:
		var f []string
		if act.Delete.Recursive {
			f = append(f, "recursive")
		}
		if act.Delete.Force {
			f = append(f, "force")
		}
		if len(f) > 0 {
			return " " + strings.Join(f, ",")
		}
	case act.Process != nil:
		out := " exec=" + s.value(act.Process.Executable)
		if act.Process.Shell {
			out += " shell"
		}
		if act.Process.Reason != "" {
			out += " reason=" + s.clean(act.Process.Reason)
		}
		return out
	case act.Git != nil:
		out := " " + act.Git.Subcommand
		if act.Git.Remote != "" {
			out += " remote=" + s.clean(act.Git.Remote)
		}
		if act.Git.Ref != "" {
			out += " ref=" + s.clean(act.Git.Ref)
		}
		return out
	case act.Intent != nil:
		out := " tool=" + s.clean(act.Intent.Tool)
		if act.Intent.Target != "" {
			out += " target=" + s.clean(act.Intent.Target)
		}
		if act.Intent.Verified {
			return out + " verified"
		}
		return out + " unverified"
	case act.Package != nil:
		out := " " + act.Package.Manager
		if act.Package.Script != "" {
			out += " script=" + s.clean(act.Package.Script)
		}
		if len(act.Package.Packages) > 0 {
			out += " " + s.clean(strings.Join(act.Package.Packages, ","))
		}
		return out
	case act.Network != nil:
		out := " url=" + s.value(act.Network.URL)
		if act.Network.Method != "" {
			out += " method=" + s.clean(act.Network.Method)
		}
		return out
	case act.Search != nil:
		return " pattern=" + s.value(act.Search.Pattern)
	case act.Native != nil:
		out := ""
		if act.Native.Format != "" {
			out += " format=" + act.Native.Format
		}
		if act.Native.Agent != "" {
			out += " agent=" + s.clean(act.Native.Agent)
		}
		for _, f := range act.Native.Files {
			out += " " + f.Operation + ":" + s.clean(f.Path)
		}
		return out
	}
	return ""
}

// qualifiers summarizes guards, reachability, wrappers and namespace.
func qualifiers(act model.Action, nodes map[string]model.Node) string {
	var q []string
	seen := map[string]bool{}
	add := func(s string) {
		if !seen[s] {
			seen[s] = true
			q = append(q, s)
		}
	}
	n, ok := nodes[act.Node]
	for ok {
		if n.Guard != nil {
			add(fmt.Sprintf("if %s %s", n.Guard.Node, n.Guard.On))
		}
		if n.Kind == model.NodeCommand && n.Command != nil && len(n.Command.Wrappers) > 0 {
			add("via " + strings.Join(n.Command.Wrappers, ","))
		}
		if n.Kind == model.NodeLoop {
			add("repeated")
		}
		if n.Kind == model.NodeBackground {
			add("background")
		}
		if n.Kind == model.NodeProgram && n.Program != nil {
			add("in " + n.Program.Language)
		}
		n, ok = nodes[n.Parent]
	}
	if n, ok := nodes[act.Node]; ok && n.Reach != model.ReachAlways && n.Reach != "" {
		q = append([]string{string(n.Reach)}, q...)
	}
	sort.Strings(q)
	return strings.Join(q, ", ")
}

func writeTree(b *strings.Builder, nodes []model.Node, parent string, depth int, s sanitizer) {
	for _, n := range nodes {
		if n.Parent != parent {
			continue
		}
		label := string(n.Kind)
		if n.Command != nil && n.Command.Executable.Text != "" {
			label += " " + s.value(n.Command.Executable)
		}
		if n.Detail != "" {
			label += " (" + s.clean(n.Detail) + ")"
		}
		var q []string
		if n.Guard != nil {
			q = append(q, fmt.Sprintf("if %s %s", n.Guard.Node, n.Guard.On))
		}
		if n.Reach != model.ReachAlways && n.Reach != "" {
			q = append(q, string(n.Reach))
		}
		if n.Syntax != "" && n.Syntax != model.SyntaxParsed {
			q = append(q, string(n.Syntax))
		}
		if n.Context.Namespace.Kind != "" && n.Context.Namespace.Kind != model.NamespaceHost && n.Context.Namespace.Kind != model.NamespaceUnknown {
			q = append(q, string(n.Context.Namespace.Kind)+":"+s.clean(n.Context.Namespace.ID))
		}
		fmt.Fprintf(b, "  %s%-4s %s", strings.Repeat("  ", depth), n.ID, label)
		if len(q) > 0 {
			fmt.Fprintf(b, "  [%s]", strings.Join(q, ", "))
		}
		b.WriteString("\n")
		writeTree(b, nodes, n.ID, depth+1, s)
	}
}

// sanitizer bounds and escapes untrusted text.
type sanitizer struct{ max int }

func (s sanitizer) clean(text string) string {
	var b strings.Builder
	n := 0
	for _, r := range text {
		if n >= s.max {
			b.WriteString("…")
			break
		}
		switch {
		case r == utf8.RuneError:
			b.WriteString(`\x?`)
		case r == '\n':
			b.WriteString(`\n`)
		case r == '\t':
			b.WriteString(`\t`)
		case unicode.IsControl(r) || !unicode.IsPrint(r) && r != ' ':
			fmt.Fprintf(&b, `\u%04x`, r)
		default:
			b.WriteRune(r)
		}
		n++
	}
	return b.String()
}

func (s sanitizer) value(v model.Value) string {
	switch v.Knowledge {
	case model.KnowledgeUnknown:
		return "<unknown>"
	case model.KnowledgeUnset:
		return "<unset>"
	case model.KnowledgeLiteral, model.KnowledgeDerived:
		return s.clean(v.Text)
	}
	return s.clean(v.Text) + "?"
}

func (s sanitizer) resource(r model.Resource) string {
	text := s.clean(r.Expression)
	if r.Expression == "" && len(r.ExpressionBytes) > 0 {
		text = fmt.Sprintf("<%d non-UTF-8 bytes>", len(r.ExpressionBytes))
	}
	switch {
	case len(r.Alternatives) > 0:
		text += " → {" + s.clean(strings.Join(r.Alternatives, "|")) + "}"
	case r.Resolved != "" && r.Resolved != r.Expression:
		text += " → " + s.clean(r.Resolved)
	}
	tag := string(r.Knowledge)
	if r.Namespace.Kind != model.NamespaceHost && r.Namespace.Kind != "" {
		tag += "," + string(r.Namespace.Kind)
		if r.Namespace.ID != "" {
			tag += ":" + s.clean(r.Namespace.ID)
		}
	}
	if r.Role != "" && r.Role != "target" {
		tag = r.Role + "," + tag
	}
	return text + " [" + tag + "]"
}
