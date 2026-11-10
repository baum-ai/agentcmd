package standard

import (
	"encoding/json"
	"fmt"

	"github.com/baum-ai/agentcmd/model"
	"github.com/baum-ai/agentcmd/native"
)

// bindExecCommand binds Codex's `exec_command` tool: {cmd, workdir?, ...}.
// A non-empty string workdir is the directory the command runs in; any other
// present value makes the directory unknown.
func bindExecCommand(inv *model.Invocation) (native.Result, error) {
	var in map[string]json.RawMessage
	if err := args(inv, &in); err != nil {
		return native.Result{}, err
	}
	var cmd string
	if err := json.Unmarshal(in["cmd"], &cmd); err != nil {
		return native.Result{}, fmt.Errorf("field cmd: %w", err)
	}
	src := native.ShellSource{Text: cmd, Field: "/cmd"}
	if raw, ok := in["workdir"]; ok && string(raw) != "null" {
		var dir string
		if err := json.Unmarshal(raw, &dir); err != nil || dir == "" {
			// Not a string, or explicitly empty: not a directory we know.
			src.WorkdirUnknown = true
		} else {
			src.Workdir = dir
		}
	}
	return native.Result{Detail: "shell", Shell: &src}, nil
}

// bindCodexExec binds Codex's code-mode tool `exec`, whose input is a
// JavaScript program (free text, a JSON string, or {"input": "<js>"}; a
// derived "command" field beside "input" is ignored). The program is only
// scanned, never run: each `tools.exec_command({cmd, workdir})` call whose
// argument is an object literal becomes one shell analysis with its own
// working directory. A call whose command is not a static string becomes an
// unresolved process action; a present but dynamic workdir makes that call's
// directory unknown. See docs/ingestion.md.
func bindCodexExec(inv *model.Invocation) (native.Result, error) {
	program, err := codexExecProgram(inv)
	if err != nil {
		return native.Result{}, err
	}
	scan := scanCodexExec(program)
	whole := model.SourceSpan{Source: "s0", End: len(program)}
	if inv.Input.Kind == model.InputJSON {
		whole.End = len(inv.Input.JSON)
	}
	// Offsets into the program are byte-exact only for text input.
	span := func(start, end int) *model.SourceSpan {
		if inv.Input.Kind == model.InputText {
			return &model.SourceSpan{Source: "s0", Start: start, End: end}
		}
		w := whole
		return &w
	}
	res := native.Result{Detail: "exec"}
	flow := false
	for _, c := range scan.calls {
		sp := span(c.Start, c.End)
		flow = flow || c.Reach != model.ReachAlways
		if !c.CommandStatic {
			res.Parts = append(res.Parts, native.Part{Reach: c.Reach, Span: sp, Unresolved: c.Reason,
				Assumptions: []string{"the program is not executed; its values and control flow are not modeled"}})
			continue
		}
		csp := span(c.CmdStart, c.CmdEnd)
		src := native.ShellSource{Text: c.Command, Field: "/input", Span: csp, Reach: c.Reach}
		switch {
		case c.HasWorkdir && !c.WorkdirStatic:
			src.WorkdirUnknown = true
		case c.HasWorkdir:
			src.Workdir = c.Workdir
		}
		res.Shells = append(res.Shells, src)
	}
	for _, r := range scan.refs {
		flow = flow || r.Reach != model.ReachAlways
		res.Parts = append(res.Parts, native.Part{Reach: r.Reach, Span: span(r.Start, r.End),
			Unresolved:  "the program refers to " + r.Name + " without calling it directly, so its calls are not followed",
			Assumptions: []string{"the program is not executed; its values and control flow are not modeled"}})
	}
	for _, p := range scan.patches {
		sp := span(p.Start, p.End)
		flow = flow || p.Reach != model.ReachAlways
		if !p.Static {
			res.Parts = append(res.Parts, native.Part{Reach: p.Reach, Span: sp, Unresolved: "the patch text of the code-mode apply_patch call is not a static string",
				Actions: []model.Action{{Kind: model.NativePatch, Evidence: model.Evidence{Basis: model.BasisToolContract, Assumptions: []string{"patch text is computed; files unknown"}},
					Native: &model.NativeDetail{Operation: "apply", Format: "unknown"}}}})
			continue
		}
		pr, err := native.PatchResult(inv, p.Patch)
		if err != nil {
			return native.Result{}, err
		}
		res.Parts = append(res.Parts, native.Part{Reach: p.Reach, Span: sp, Actions: pr.Actions, Assumptions: pr.Assumptions})
		res.Diagnostics = append(res.Diagnostics, pr.Diagnostics...)
	}
	if flow {
		res.Diagnostics = append(res.Diagnostics, model.Diagnostic{Code: model.DiagUnsupported, Severity: model.SeverityInfo,
			Message: "JavaScript control flow is not modeled: calls inside blocks, after &&, || or ?, in loops or in function bodies are marked conditional, repeated or latent, not known to run"})
	}
	if scan.unbalanced {
		res.Diagnostics = append(res.Diagnostics, model.Diagnostic{Code: model.DiagSyntaxError, Severity: model.SeverityWarning,
			Message: "code-mode program does not parse as JavaScript (unbalanced brackets); only the calls found by the lexical scan are analyzed"})
	}
	if scan.unterminated {
		res.Diagnostics = append(res.Diagnostics, model.Diagnostic{Code: model.DiagSyntaxError, Severity: model.SeverityWarning,
			Message: "code-mode program has an unterminated string, template literal or comment; calls inside it are not known to run"})
	}
	if scan.exhausted {
		res.Diagnostics = append(res.Diagnostics, model.Diagnostic{Code: model.DiagLimitReached, Severity: model.SeverityWarning,
			Message: "code-mode program scan budget exhausted; calls after that point are not analyzed"})
	}
	if scan.tooDeep {
		res.Diagnostics = append(res.Diagnostics, model.Diagnostic{Code: model.DiagLimitReached, Severity: model.SeverityWarning,
			Message: fmt.Sprintf("template literals nested deeper than %d; the rest of the program is not analyzed", maxTemplateDepth)})
	}
	if scan.truncated {
		res.Diagnostics = append(res.Diagnostics, model.Diagnostic{Code: model.DiagLimitReached, Severity: model.SeverityWarning,
			Message: fmt.Sprintf("code-mode program has more than %d calls; the rest are not analyzed", maxCodexExecCalls)})
	}
	if len(scan.calls) == 0 && len(scan.patches) == 0 && len(scan.refs) == 0 {
		res.Diagnostics = append(res.Diagnostics, model.Diagnostic{Code: model.DiagUnsupported, Severity: model.SeverityInfo,
			Message: "code-mode program calls neither exec_command, shell nor apply_patch; nothing to analyze"})
	}
	return res, nil
}

// codexExecProgram extracts the JavaScript source of an `exec` input.
func codexExecProgram(inv *model.Invocation) (string, error) {
	switch inv.Input.Kind {
	case model.InputText:
		return inv.Input.Text, nil
	case model.InputJSON:
		var s string
		if json.Unmarshal(inv.Input.JSON, &s) == nil {
			return s, nil
		}
		var in struct {
			Input *string `json:"input"`
		}
		if err := json.Unmarshal(inv.Input.JSON, &in); err != nil {
			return "", fmt.Errorf("exec input is neither program text nor {input}: %w", err)
		}
		if in.Input == nil {
			return "", fmt.Errorf("exec input has no input field")
		}
		return *in.Input, nil
	}
	return "", fmt.Errorf("tool %s expects text or JSON input, got %s", inv.Tool.Name, inv.Input.Kind)
}
