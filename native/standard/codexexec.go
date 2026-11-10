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
		if err := json.Unmarshal(raw, &dir); err != nil {
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
	calls, patches, truncated, unbalanced := scanCodexExec(program)
	whole := model.SourceSpan{Source: "s0", End: len(program)}
	if inv.Input.Kind == model.InputJSON {
		whole.End = len(inv.Input.JSON)
	}
	// Offsets into the program are byte-exact only for text input.
	span := func(start, end int) model.SourceSpan {
		if inv.Input.Kind == model.InputText {
			return model.SourceSpan{Source: "s0", Start: start, End: end}
		}
		return whole
	}
	res := native.Result{Detail: "exec"}
	for _, c := range calls {
		sp := span(c.Start, c.End)
		if !c.CommandStatic {
			res.Actions = append(res.Actions, model.Action{Kind: model.ProcessUnresolved,
				Evidence: model.Evidence{Basis: model.BasisSyntax, Spans: []model.SourceSpan{sp},
					Assumptions: []string{"the command of a code-mode " + c.Callee + " call is not a static string; the program is not executed"}},
				Process: &model.ProcessDetail{Executable: model.Unknown(), Shell: true, Reason: "code-mode " + c.Callee + " command is not a static string"}})
			continue
		}
		csp := span(c.CmdStart, c.CmdEnd)
		src := native.ShellSource{Text: c.Command, Field: "/input", Span: &csp}
		switch {
		case c.HasWorkdir && !c.WorkdirStatic:
			src.WorkdirUnknown = true
		case c.HasWorkdir:
			src.Workdir = c.Workdir
		}
		res.Shells = append(res.Shells, src)
	}
	for _, p := range patches {
		pr, err := native.PatchResult(inv, p.Patch)
		if err != nil {
			return native.Result{}, err
		}
		res.Actions = append(res.Actions, pr.Actions...)
		res.Diagnostics = append(res.Diagnostics, pr.Diagnostics...)
	}
	if unbalanced {
		res.Diagnostics = append(res.Diagnostics, model.Diagnostic{Code: model.DiagSyntaxError, Severity: model.SeverityWarning,
			Message: "code-mode program does not parse as JavaScript (unbalanced brackets); only the calls found by the lexical scan are analyzed"})
	}
	if len(calls) == 0 && len(patches) == 0 {
		res.Diagnostics = append(res.Diagnostics, model.Diagnostic{Code: model.DiagUnsupported, Severity: model.SeverityInfo,
			Message: "code-mode program has no exec_command call with an object literal argument; nothing to analyze"})
	}
	if truncated {
		res.Diagnostics = append(res.Diagnostics, model.Diagnostic{Code: model.DiagLimitReached, Severity: model.SeverityWarning,
			Message: fmt.Sprintf("code-mode program has more than %d commands; the rest are not analyzed", maxCodexExecCalls)})
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
