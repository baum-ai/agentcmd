// Package standard holds built-in native tool bindings captured from common
// harness profiles. Callers select it explicitly:
// toolparser.Config{Native: standard.Bindings()}.
package standard

import (
	"encoding/json"
	"fmt"

	"github.com/lastpersonlabs/toolparser/model"
	"github.com/lastpersonlabs/toolparser/native"
)

// Version of the standard binding set.
const Version = "0.1.0"

// Bindings returns every built-in binding.
func Bindings() []native.Binding {
	var out []native.Binding
	for _, ns := range []string{"harness", ""} {
		out = append(out,
			native.Binding{ID: "std/bash-text", Version: "1", Namespace: ns, Tool: "bash", Harness: "generic", Doc: "free-form shell command as text input", Bind: bindShellText},
			native.Binding{ID: "std/claude-code/Bash", Version: "1", Namespace: ns, Tool: "Bash", Harness: "claude-code", Doc: "{command, description?, timeout?}", Bind: bindShellField("command")},
			native.Binding{ID: "std/claude-code/Read", Version: "1", Namespace: ns, Tool: "Read", Harness: "claude-code", Doc: "{file_path, offset?, limit?}", Bind: bindRead},
			native.Binding{ID: "std/claude-code/Write", Version: "1", Namespace: ns, Tool: "Write", Harness: "claude-code", Doc: "{file_path, content}", Bind: bindWrite},
			native.Binding{ID: "std/claude-code/Edit", Version: "1", Namespace: ns, Tool: "Edit", Harness: "claude-code", Doc: "{file_path, old_string, new_string, replace_all?}", Bind: bindEdit},
		)
	}
	return out
}

func args(inv *model.Invocation, into any) error {
	if inv.Input.Kind != model.InputJSON {
		return fmt.Errorf("tool %s expects JSON input, got %s", inv.Tool.Name, inv.Input.Kind)
	}
	return json.Unmarshal(inv.Input.JSON, into)
}

func bindShellText(inv *model.Invocation) (native.Result, error) {
	if inv.Input.Kind != model.InputText {
		return native.Result{}, fmt.Errorf("tool %s expects text input, got %s", inv.Tool.Name, inv.Input.Kind)
	}
	return native.Result{Detail: "shell", Shell: &native.ShellSource{Text: inv.Input.Text, Field: "/text"}}, nil
}

func bindShellField(field string) func(*model.Invocation) (native.Result, error) {
	return func(inv *model.Invocation) (native.Result, error) {
		var in map[string]json.RawMessage
		if err := args(inv, &in); err != nil {
			return native.Result{}, err
		}
		var cmd string
		if err := json.Unmarshal(in[field], &cmd); err != nil {
			return native.Result{}, fmt.Errorf("field %s: %w", field, err)
		}
		return native.Result{Detail: "shell", Shell: &native.ShellSource{Text: cmd, Field: "/" + field}}, nil
	}
}

func bindRead(inv *model.Invocation) (native.Result, error) {
	var in struct {
		FilePath string `json:"file_path"`
	}
	if err := args(inv, &in); err != nil {
		return native.Result{}, err
	}
	a := model.Action{Kind: model.FSRead, Evidence: model.Evidence{Basis: model.BasisToolContract}, Resources: []model.Resource{native.Resource(inv, in.FilePath, "target")}}
	return native.Result{Detail: "read", Actions: []model.Action{a}}, nil
}

func bindWrite(inv *model.Invocation) (native.Result, error) {
	var in struct {
		FilePath string `json:"file_path"`
	}
	if err := args(inv, &in); err != nil {
		return native.Result{}, err
	}
	a := model.Action{Kind: model.FSWrite, Write: &model.WriteDetail{Mode: model.WriteTruncate}, Evidence: model.Evidence{Basis: model.BasisToolContract},
		Resources: []model.Resource{native.Resource(inv, in.FilePath, "target")}}
	return native.Result{Detail: "write", Actions: []model.Action{a}}, nil
}

func bindEdit(inv *model.Invocation) (native.Result, error) {
	var in struct {
		FilePath string `json:"file_path"`
	}
	if err := args(inv, &in); err != nil {
		return native.Result{}, err
	}
	a := model.Action{Kind: model.FSWrite, Write: &model.WriteDetail{Mode: model.WriteInPlace}, Evidence: model.Evidence{Basis: model.BasisToolContract},
		Resources: []model.Resource{native.Resource(inv, in.FilePath, "target")}}
	return native.Result{Detail: "edit", Actions: []model.Action{a}}, nil
}
