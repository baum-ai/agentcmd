package standard

import (
	"encoding/json"
	"fmt"

	"github.com/lastpersonlabs/agentcmd/model"
	"github.com/lastpersonlabs/agentcmd/native"
)

func bindGlob(inv *model.Invocation) (native.Result, error) {
	var in struct {
		Pattern string `json:"pattern"`
		Path    string `json:"path"`
	}
	if err := args(inv, &in); err != nil {
		return native.Result{}, err
	}
	scope := native.Resource(inv, orDot(in.Path), "scope")
	pat := native.Resource(inv, in.Pattern, "filter")
	pat.Knowledge, pat.Resolved = model.KnowledgeGlob, ""
	a := model.Action{Kind: model.SearchPath, Evidence: model.Evidence{Basis: model.BasisToolContract, Assumptions: []string{"matches are not enumerated"}},
		Resources: []model.Resource{scope, pat}, Search: &model.SearchDetail{Pattern: model.Literal(in.Pattern)}}
	return native.Result{Detail: "glob", Actions: []model.Action{a}}, nil
}

func bindGrep(inv *model.Invocation) (native.Result, error) {
	var in struct {
		Pattern string `json:"pattern"`
		Path    string `json:"path"`
		Glob    string `json:"glob"`
		Type    string `json:"type"`
	}
	if err := args(inv, &in); err != nil {
		return native.Result{}, err
	}
	a := model.Action{Kind: model.SearchContent, Evidence: model.Evidence{Basis: model.BasisToolContract, Assumptions: []string{"scope is searched; which files were read is not established"}},
		Resources: []model.Resource{native.Resource(inv, orDot(in.Path), "scope")}, Search: &model.SearchDetail{Pattern: model.Literal(in.Pattern)}}
	if in.Glob != "" {
		g := native.Resource(inv, in.Glob, "filter")
		g.Knowledge, g.Resolved = model.KnowledgeGlob, ""
		a.Resources = append(a.Resources, g)
	}
	return native.Result{Detail: "grep", Actions: []model.Action{a}}, nil
}

func bindLS(inv *model.Invocation) (native.Result, error) {
	var in struct {
		Path string `json:"path"`
	}
	if err := args(inv, &in); err != nil {
		return native.Result{}, err
	}
	a := model.Action{Kind: model.FSList, Evidence: model.Evidence{Basis: model.BasisToolContract}, Resources: []model.Resource{native.Resource(inv, orDot(in.Path), "scope")}}
	return native.Result{Detail: "list", Actions: []model.Action{a}}, nil
}

func bindMultiEdit(inv *model.Invocation) (native.Result, error) {
	var in struct {
		FilePath string            `json:"file_path"`
		Edits    []json.RawMessage `json:"edits"`
	}
	if err := args(inv, &in); err != nil {
		return native.Result{}, err
	}
	a := model.Action{Kind: model.FSWrite, Write: &model.WriteDetail{Mode: model.WriteInPlace}, Evidence: model.Evidence{Basis: model.BasisToolContract},
		Resources: []model.Resource{native.Resource(inv, in.FilePath, "target")}}
	return native.Result{Detail: fmt.Sprintf("edit (%d edits)", len(in.Edits)), Actions: []model.Action{a}}, nil
}

func bindNotebookEdit(inv *model.Invocation) (native.Result, error) {
	var in struct {
		NotebookPath string `json:"notebook_path"`
	}
	if err := args(inv, &in); err != nil {
		return native.Result{}, err
	}
	a := model.Action{Kind: model.FSWrite, Write: &model.WriteDetail{Mode: model.WriteInPlace}, Evidence: model.Evidence{Basis: model.BasisToolContract},
		Resources: []model.Resource{native.Resource(inv, in.NotebookPath, "target")}}
	return native.Result{Detail: "notebook edit", Actions: []model.Action{a}}, nil
}

func bindTask(inv *model.Invocation) (native.Result, error) {
	var in struct {
		SubagentType string `json:"subagent_type"`
		Description  string `json:"description"`
	}
	if err := args(inv, &in); err != nil {
		return native.Result{}, err
	}
	a := model.Action{Kind: model.NativeDelegate, Evidence: model.Evidence{Basis: model.BasisToolContract, Assumptions: []string{"the subagent's own tool calls are separate invocations"}},
		Native: &model.NativeDetail{Operation: "delegate", Agent: in.SubagentType}}
	return native.Result{Detail: "delegate", Actions: []model.Action{a}}, nil
}

func bindWebFetch(inv *model.Invocation) (native.Result, error) {
	var in struct {
		URL   string `json:"url"`
		Query string `json:"query"`
	}
	if err := args(inv, &in); err != nil {
		return native.Result{}, err
	}
	a := model.Action{Kind: model.NetRequest, Evidence: model.Evidence{Basis: model.BasisToolContract}, Network: &model.NetworkDetail{URL: model.Literal(in.URL), Method: "GET"}}
	if in.URL == "" {
		a.Kind, a.Network.URL = model.NetUnresolved, model.Symbolic("<search>")
		if in.Query != "" {
			a.Network.URL = model.Symbolic("<search: " + in.Query + ">")
		}
	}
	return native.Result{Detail: "fetch", Actions: []model.Action{a}}, nil
}

// bindPatchField binds Codex-style apply_patch tools whose input carries the
// patch text in a field. The patch is parsed as data and never applied.
func bindPatchField(field string) func(*model.Invocation) (native.Result, error) {
	return func(inv *model.Invocation) (native.Result, error) {
		var in map[string]json.RawMessage
		if err := args(inv, &in); err != nil {
			return native.Result{}, err
		}
		var text string
		if err := json.Unmarshal(in[field], &text); err != nil {
			return native.Result{}, fmt.Errorf("field %s: %w", field, err)
		}
		return native.PatchResult(inv, text)
	}
}

// bindArgvShell binds tools whose input is an argv array plus an optional
// working directory (the Codex `shell` tool). The argv is executed directly;
// it is never re-parsed as a shell program.
func bindArgvShell(inv *model.Invocation) (native.Result, error) {
	var in struct {
		Command []string `json:"command"`
		Workdir string   `json:"workdir"`
	}
	if err := args(inv, &in); err != nil {
		return native.Result{}, err
	}
	if len(in.Command) == 0 {
		return native.Result{}, fmt.Errorf("command array is empty")
	}
	return native.Result{Detail: "exec", Exec: &native.Exec{Executable: in.Command[0], Args: in.Command[1:], Workdir: in.Workdir}}, nil
}

func orDot(p string) string {
	if p == "" {
		return "."
	}
	return p
}
