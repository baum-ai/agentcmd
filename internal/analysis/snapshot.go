package analysis

import (
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"strings"

	"github.com/lastpersonlabs/agentcmd/model"
	"github.com/lastpersonlabs/agentcmd/rules"
	"github.com/lastpersonlabs/agentcmd/snapshot"
)

// trackModified records resources an action may change, so later script
// resolution does not use the invocation's starting snapshot for them.
func (b *Builder) trackModified(a model.Action) {
	switch a.Kind {
	case model.FSWrite, model.FSDelete, model.FSMove, model.FSCopy, model.NativePatch:
	default:
		return
	}
	for _, r := range a.Resources {
		if r.Role == "source" {
			continue
		}
		if r.Resolved != "" {
			b.modified[modKey(r.Namespace, r.Resolved)] = true
		}
		for _, alt := range r.Alternatives {
			b.modified[modKey(r.Namespace, alt)] = true
		}
	}
}

func modKey(ns model.Namespace, p string) string { return string(ns.Kind) + ":" + ns.ID + ":" + p }

// resolve fetches file content through the configured resolver and registers
// it as an external source. It returns "" when nothing usable was obtained;
// the reason is recorded as a diagnostic on node.
func (b *Builder) resolve(node string, file model.Resource, kind string) (string, *model.ContentOrigin) {
	if b.Resolver == nil {
		b.Diag(model.Diagnostic{Code: model.DiagMissingSnapshot, Severity: model.SeverityInfo, Node: node, Message: kind + " contents are not part of the invocation and no snapshot resolver is configured"})
		return "", nil
	}
	if file.Resolved == "" || (file.Knowledge != model.KnowledgeLiteral && file.Knowledge != model.KnowledgeDerived) {
		b.Diag(model.Diagnostic{Code: model.DiagMissingSnapshot, Severity: model.SeverityInfo, Node: node, Message: kind + " path is not resolvable literally; contents not fetched"})
		return "", nil
	}
	if b.modified[modKey(file.Namespace, file.Resolved)] {
		b.Diag(model.Diagnostic{Code: model.DiagStaleContent, Severity: model.SeverityWarning, Node: node, Message: fmt.Sprintf("%s %s may have been modified earlier in this invocation; snapshot contents not used", kind, file.Resolved)})
		b.partial = true
		return "", nil
	}
	if b.Limits.DerivedBytes > 0 && b.Store.DerivedBytes() > b.Limits.DerivedBytes {
		b.LimitReached(node, fmt.Sprintf("derived source exceeds %d bytes", b.Limits.DerivedBytes))
		return "", nil
	}
	req := snapshot.Request{Path: file.Resolved, Namespace: file.Namespace, Snapshot: b.Snapshot, MaxBytes: b.Limits.SourceBytes}
	content, err := b.Resolver.Resolve(b.Ctx, req)
	switch {
	case errors.Is(err, snapshot.ErrNotFound):
		b.Diag(model.Diagnostic{Code: model.DiagMissingSnapshot, Severity: model.SeverityInfo, Node: node, Message: fmt.Sprintf("%s %s not present in the snapshot", kind, file.Resolved)})
		return "", nil
	case errors.Is(err, snapshot.ErrTooLarge), errors.Is(err, snapshot.ErrBudget):
		b.LimitReached(node, fmt.Sprintf("%s %s: %v", kind, file.Resolved, err))
		return "", nil
	case err != nil:
		b.Diag(model.Diagnostic{Code: model.DiagMissingSnapshot, Severity: model.SeverityWarning, Node: node, Message: fmt.Sprintf("%s %s: %v", kind, file.Resolved, err)})
		return "", nil
	}
	origin := model.ContentOrigin{Path: content.Path, Namespace: file.Namespace, Snapshot: content.Snapshot, Digest: content.Digest, Approximate: content.Approximate}
	id := b.Store.External(kind, content.Data, origin)
	if content.Approximate {
		b.Diag(model.Diagnostic{Code: model.DiagStaleContent, Severity: model.SeverityInfo, Node: node, Message: fmt.Sprintf("%s %s resolved from snapshot %q which is not known to match the invocation; inferences from it are approximate", kind, file.Resolved, content.Snapshot)})
	}
	return id, &origin
}

// programLanguage maps a launcher/program language to the frontend language.
func programLanguage(p rules.Program) string {
	switch strings.ToLower(p.Language) {
	case "shell", "sh", "bash":
		return "shell"
	default:
		return strings.ToLower(p.Language)
	}
}

// resolveScriptFile analyzes a script file's content under the program node.
func (b *Builder) resolveScriptFile(id string, p rules.Program, ctx Context) {
	lang := programLanguage(p)
	sid, origin := b.resolve(id, *p.File, lang)
	if sid == "" {
		return
	}
	text := b.Store.Text(sid)
	node := b.Node(id)
	node.Program.Source = sid
	if origin.Approximate {
		node.Command = nil
		node.Detail = "contents approximate: snapshot not known to match"
	}
	if !b.Enter(id, sid, ctx) {
		node.Syntax = model.SyntaxLimit
		return
	}
	defer b.Leave()
	inner := ctx
	inner.Guard = nil
	inner.Dialect, inner.Positional = p.Dialect, p.Args
	if lang == "shell" && b.Shell != nil {
		b.Shell(b, id, text, sid, inner)
		b.markApproximate(id, origin)
		return
	}
	p.SourceID = sid
	if b.Program != nil && b.Program(b, id, p, inner) {
		b.markApproximate(id, origin)
		return
	}
	node.Syntax = model.SyntaxUnsupported
	b.Diag(model.Diagnostic{Code: model.DiagUnsupported, Node: id, Message: lang + " analysis is not enabled"})
	b.partial = true
}

// markApproximate adds the approximation assumption to every action derived
// from content that is not known to be contemporaneous.
func (b *Builder) markApproximate(programNode string, origin *model.ContentOrigin) {
	if origin == nil || !origin.Approximate {
		return
	}
	under := map[string]bool{programNode: true}
	for _, n := range b.nodes {
		if under[n.Parent] {
			under[n.ID] = true
		}
	}
	for i := range b.actions {
		if under[b.actions[i].Node] && b.actions[i].Node != programNode {
			b.actions[i].Evidence.Assumptions = append(b.actions[i].Evidence.Assumptions, "derived from approximate snapshot content; not a claim about what ran historically")
		}
	}
}

// resolveScript resolves a package.json script and analyzes its command
// lines, including npm's pre/post lifecycle scripts.
func (b *Builder) resolveScript(id string, s rules.Script, cmd rules.Command, ctx Context) {
	unresolved := func(reason string) {
		b.AddAction(id, model.Action{Kind: model.ProcessUnresolved, Evidence: model.Evidence{Basis: model.BasisSyntax, Spans: []model.SourceSpan{cmd.Span}},
			Process: &model.ProcessDetail{Executable: cmd.Executable, Args: cmd.Args, Shell: true, Reason: reason}})
	}
	if s.Name == "" {
		unresolved("project script name is not literal")
		return
	}
	if b.Resolver == nil || !ctx.Cwd.Concrete() {
		unresolved("project script " + s.Name + " is not resolved")
		if b.Resolver != nil {
			b.Diag(model.Diagnostic{Code: model.DiagMissingSnapshot, Severity: model.SeverityInfo, Node: id, Message: "package manifest location unknown: working directory is not concrete"})
		}
		return
	}
	manifest := model.Resource{Role: "manifest", Expression: s.Manifest, Resolved: path.Join(ctx.Cwd.Text, s.Manifest), Knowledge: model.KnowledgeDerived, Namespace: ctx.Namespace}
	sid, origin := b.resolve(id, manifest, "manifest")
	if sid == "" {
		unresolved("project script " + s.Name + " is not resolved")
		return
	}
	b.AddAction(id, model.Action{Kind: model.FSRead, Resources: []model.Resource{manifest}, Evidence: model.Evidence{Basis: model.BasisToolContract, Rule: "snapshot/package.json", RuleVersion: "1", Spans: []model.SourceSpan{cmd.Span}, Assumptions: []string{s.Manager + " reads the package manifest"}}})
	var pkg struct {
		Scripts map[string]string `json:"scripts"`
	}
	if err := json.Unmarshal(b.Store.Text(sid), &pkg); err != nil {
		b.Diag(model.Diagnostic{Code: model.DiagUnsupported, Severity: model.SeverityWarning, Node: id, Message: "package.json: " + err.Error()})
		unresolved("package.json could not be parsed")
		return
	}
	body, ok := pkg.Scripts[s.Name]
	if !ok {
		b.Diag(model.Diagnostic{Code: model.DiagMissingSnapshot, Severity: model.SeverityInfo, Node: id, Message: fmt.Sprintf("script %q is not declared in package.json", s.Name)})
		unresolved("project script " + s.Name + " is not declared in the manifest")
		return
	}
	names := []string{"pre" + s.Name, s.Name, "post" + s.Name}
	for _, name := range names {
		script, declared := pkg.Scripts[name]
		if !declared {
			continue
		}
		if name != s.Name && s.Manager != "npm" && s.Manager != "yarn" {
			// pnpm and bun do not run lifecycle scripts by default.
			continue
		}
		_ = body
		start := strings.Index(string(b.Store.Text(sid)), script)
		src := model.Value{Knowledge: model.KnowledgeDerived, Text: script, Assumptions: []string{"script body taken from package.json; node_modules/.bin assumed on PATH"}}
		if start >= 0 {
			src.Span = &model.SourceSpan{Source: sid, Start: start, End: start + len(script)}
		} else {
			src.Span = &model.SourceSpan{Source: sid, Start: 0, End: len(b.Store.Text(sid))}
		}
		p := rules.Program{Language: "shell", Interpreter: s.Manager + " run " + name, Mode: "inline", Dialect: "sh", Source: &src}
		progID := b.RunProgram(id, p, cmd, ctx)
		if n := b.Node(progID); n != nil && name != s.Name {
			n.Detail = "lifecycle script " + name
		}
		b.markApproximate(progID, origin)
	}
}
