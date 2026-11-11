package agentcmd

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/baum-ai/agentcmd/model"
	"github.com/baum-ai/agentcmd/native"
	nstd "github.com/baum-ai/agentcmd/native/standard"
	rstd "github.com/baum-ai/agentcmd/rules/standard"
)

func nativeInv(id, tool, args string) model.Invocation {
	return model.Invocation{ID: id, Tool: model.ToolIdentity{Namespace: "harness", Name: tool}, Input: model.Input{Kind: model.InputJSON, JSON: json.RawMessage(args)}, Context: ctxWith("/w")}
}

func TestNativeBindings(t *testing.T) {
	e := engine(t)
	run := func(inv model.Invocation) model.Analysis {
		a, err := e.Analyze(context.Background(), inv)
		if err != nil {
			t.Fatal(err)
		}
		return a
	}
	a := run(nativeInv("g", "Glob", `{"pattern":"**/*.go"}`))
	if len(a.Actions) != 1 || a.Actions[0].Kind != model.SearchPath || a.Actions[0].Resources[0].Resolved != "/w" || a.Actions[0].Resources[1].Knowledge != model.KnowledgeGlob {
		t.Fatalf("%s", describeActions(a))
	}
	a = run(nativeInv("gr", "Grep", `{"pattern":"TODO","path":"/w/internal","glob":"*.go"}`))
	if a.Actions[0].Kind != model.SearchContent || a.Actions[0].Search.Pattern.Text != "TODO" || a.Actions[0].Evidence.Basis != model.BasisToolContract {
		t.Fatalf("%s", describeActions(a))
	}
	a = run(nativeInv("t", "Task", `{"prompt":"x","subagent_type":"Explore"}`))
	if a.Actions[0].Kind != model.NativeDelegate || a.Actions[0].Native.Agent != "Explore" {
		t.Fatalf("%s", describeActions(a))
	}
	a = run(nativeInv("w", "WebFetch", `{"url":"https://example.com"}`))
	if a.Actions[0].Kind != model.NetRequest {
		t.Fatalf("%s", describeActions(a))
	}
	a = run(nativeInv("me", "MultiEdit", `{"file_path":"a.go","edits":[{"old_string":"a","new_string":"b"}]}`))
	if a.Actions[0].Kind != model.FSWrite || a.Actions[0].Write.Mode != model.WriteInPlace {
		t.Fatalf("%s", describeActions(a))
	}
}

func TestPatchIsDataNotCode(t *testing.T) {
	e := engine(t)
	patch := "*** Begin Patch\n*** Update File: main.go\n@@\n-import os\n+import sys\n*** Add File: run.py\n+import subprocess\n+subprocess.run('rm -rf /', shell=True)\n*** Delete File: old.txt\n*** End Patch\n"
	inv := model.Invocation{ID: "p1", Tool: model.ToolIdentity{Namespace: "codex", Name: "apply_patch"}, Input: model.Input{Kind: model.InputJSON, JSON: mustJSON(map[string]string{"input": patch})}, Context: ctxWith("/w")}
	a, err := e.Analyze(context.Background(), inv)
	if err != nil {
		t.Fatal(err)
	}
	kinds := kinds(a)
	wantKinds := []model.ActionKind{model.NativePatch, model.FSWrite, model.FSWrite, model.FSDelete}
	if len(kinds) != len(wantKinds) {
		t.Fatalf("%s", describeActions(a))
	}
	for i := range kinds {
		if kinds[i] != wantKinds[i] {
			t.Fatalf("%s", describeActions(a))
		}
	}
	for _, act := range a.Actions {
		if act.Kind.Family() == "process" {
			t.Fatal("inserted source must not become an execution")
		}
	}
	if a.Actions[1].Write.Mode != model.WriteInPlace || a.Actions[2].Write.Mode != model.WriteCreateExclusive || a.Actions[2].Resources[0].Resolved != "/w/run.py" {
		t.Fatalf("%s", describeActions(a))
	}
	if len(a.Actions[0].Native.Files) != 3 || a.Actions[0].Native.Format != "codex-apply-patch" {
		t.Fatalf("%+v", a.Actions[0].Native)
	}

	// Patch input kind needs no binding.
	inv = model.Invocation{ID: "p2", Tool: model.ToolIdentity{Name: "whatever"}, Input: model.Input{Kind: model.InputPatch, Patch: "--- a/x.txt\n+++ b/x.txt\n@@ -1 +1 @@\n-a\n+b\n"}, Context: ctxWith("/w")}
	a, err = e.Analyze(context.Background(), inv)
	if err != nil || len(a.Actions) != 2 || a.Actions[1].Resources[0].Resolved != "/w/x.txt" {
		t.Fatalf("%v %s", err, describeActions(a))
	}
	// Malformed patch: explicit diagnostic, no invented files.
	inv.ID, inv.Input.Patch = "p3", "not a patch"
	a, _ = e.Analyze(context.Background(), inv)
	if len(a.Actions) != 1 || len(a.Diagnostics) == 0 || a.Diagnostics[0].Code != model.DiagUnsupported {
		t.Fatalf("%s %+v", describeActions(a), a.Diagnostics)
	}
}

func TestCodexArgvShell(t *testing.T) {
	e := engine(t)
	inv := model.Invocation{ID: "s1", Tool: model.ToolIdentity{Namespace: "codex", Name: "shell"}, Input: model.Input{Kind: model.InputJSON, JSON: json.RawMessage(`{"command":["cat","a && rm -rf /"],"workdir":"sub"}`)}, Context: ctxWith("/w")}
	a, err := e.Analyze(context.Background(), inv)
	if err != nil {
		t.Fatal(err)
	}
	if len(a.Actions) != 1 || a.Actions[0].Kind != model.FSRead || a.Actions[0].Resources[0].Expression != "a && rm -rf /" || a.Actions[0].Resources[0].Resolved != "/w/sub/a && rm -rf " {
		t.Fatalf("%s", describeActions(a))
	}
	inv.Input.JSON = json.RawMessage(`{"command":["bash","-lc","cat a && rm -rf build"]}`)
	a, _ = e.Analyze(context.Background(), inv)
	if len(actionsOf(a, model.FSDelete)) != 1 || len(actionsOf(a, model.FSRead)) != 1 {
		t.Fatalf("bash -lc argv must be analyzed as nested shell: %s", describeActions(a))
	}
}

func TestMCPAnnotationsAreHints(t *testing.T) {
	ro := true
	e, err := New(Config{Rules: rstd.Rules(), Native: append(nstd.Bindings(), native.FromAnnotations("mcp:fs", "read_file", native.Annotations{ReadOnlyHint: &ro}))})
	if err != nil {
		t.Fatal(err)
	}
	inv := model.Invocation{ID: "m1", Tool: model.ToolIdentity{Namespace: "mcp:fs", Name: "read_file"}, Input: model.Input{Kind: model.InputJSON, JSON: json.RawMessage(`{"path":"/etc/passwd"}`)}}
	a, err := e.Analyze(context.Background(), inv)
	if err != nil {
		t.Fatal(err)
	}
	if len(a.Actions) != 0 || a.Nodes[1].Detail != "declared: read-only=true" || a.Diagnostics[0].Code != model.DiagUnknownTool {
		t.Fatalf("%+v %+v", a.Nodes[1], a.Diagnostics)
	}
}

func mustJSON(v any) json.RawMessage {
	b, _ := json.Marshal(v)
	return b
}
