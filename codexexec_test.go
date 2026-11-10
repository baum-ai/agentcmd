package agentcmd

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/baum-ai/agentcmd/model"
)

// execInv builds a Codex code-mode `exec` invocation. A text input carries the
// program as is; a JSON input carries {"input": program, "command": derived}.
func execInv(program string, kind model.InputKind, cwd string) model.Invocation {
	inv := model.Invocation{ID: "x", Tool: model.ToolIdentity{Namespace: "harness", Name: "exec"}, Context: ctxWith(cwd)}
	if cwd == "" {
		inv.Context.WorkingDir = model.Fact{}
	}
	switch kind {
	case model.InputText:
		inv.Input = model.Input{Kind: model.InputText, Text: program}
	default:
		inv.Input = model.Input{Kind: model.InputJSON, JSON: mustJSON(map[string]string{"input": program, "command": "ignored: rm -rf /"})}
	}
	return inv
}

func runExec(t *testing.T, program string, kind model.InputKind, cwd string) model.Analysis {
	t.Helper()
	a, err := engine(t).Analyze(context.Background(), execInv(program, kind, cwd))
	if err != nil {
		t.Fatal(err)
	}
	return a
}

// gitDirs lists "kind@cwd" for every git action: the working directory the
// action's repository resource resolved against.
func gitDirs(a model.Analysis) []string {
	var out []string
	for _, act := range a.Actions {
		if !strings.HasPrefix(string(act.Kind), "git.") {
			continue
		}
		dir := "?"
		for _, r := range act.Resources {
			if r.Role == "repository" {
				dir = r.Resolved
			}
		}
		out = append(out, string(act.Kind)+"@"+dir)
	}
	return out
}

func eq(t *testing.T, got []string, want ...string) {
	t.Helper()
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("got %v want %v", got, want)
	}
}

const realProgram = `text(await tools.exec_command({cmd:"git add a b\n git diff --cached --check\n git commit -m 'Subject' -m 'Body text.'","workdir":"/abs/wt","sandbox_permissions":"require_escalated","justification":"May I commit?","max_output_tokens":2500,"yield_time_ms":1000}));
text(await tools.mcp__codex_apps__github_get_user_login({}));
`

func TestCodexExecCommandWorkdir(t *testing.T) {
	e := engine(t)
	a, err := e.Analyze(context.Background(), nativeInv("c", "exec_command", `{"cmd":"git add a","workdir":"/abs/wt","yield_time_ms":10}`))
	if err != nil {
		t.Fatal(err)
	}
	eq(t, gitDirs(a), "git.stage@/abs/wt")
	// Relative workdir resolves against the invocation's directory.
	a, _ = e.Analyze(context.Background(), nativeInv("c", "exec_command", `{"cmd":"git add a","workdir":"sub"}`))
	eq(t, gitDirs(a), "git.stage@/w/sub")
	// Without a workdir the invocation's directory applies.
	a, _ = e.Analyze(context.Background(), nativeInv("c", "exec_command", `{"cmd":"git add a"}`))
	eq(t, gitDirs(a), "git.stage@/w")
	// A non-string workdir is not a directory we know.
	a, _ = e.Analyze(context.Background(), nativeInv("c", "exec_command", `{"cmd":"git add a","workdir":7}`))
	eq(t, gitDirs(a), "git.stage@")
	// A relative workdir with no known invocation directory is unknown.
	inv := nativeInv("c", "exec_command", `{"cmd":"git add a","workdir":"sub"}`)
	inv.Context.WorkingDir = model.Fact{}
	a, _ = e.Analyze(context.Background(), inv)
	eq(t, gitDirs(a), "git.stage@")
}

func TestCodexExecRealProgram(t *testing.T) {
	for _, kind := range []model.InputKind{model.InputText, model.InputJSON} {
		a := runExec(t, realProgram, kind, "/w")
		eq(t, gitDirs(a), "git.stage@/abs/wt", "git.inspect@/abs/wt", "git.commit@/abs/wt")
		if len(actionsOf(a, model.ProcessUnresolved)) != 0 {
			t.Fatalf("the MCP call must be ignored: %s", describeActions(a))
		}
	}
}

func TestCodexExecDirectoriesAreIndependent(t *testing.T) {
	prog := `await Promise.allSettled([
  tools.exec_command({cmd: "cd sub && git add x", workdir: "/one"}),
  tools.exec_command({cmd: "git add y", workdir: "/two"}),
  tools.exec_command({cmd: "git add z"}),
]);`
	a := runExec(t, prog, model.InputText, "/w")
	eq(t, gitDirs(a), "git.stage@/one/sub", "git.stage@/two", "git.stage@/w")
}

func TestCodexExecKeysAndEscapes(t *testing.T) {
	prog := "tools.exec_command({\"cmd\": \"git add \\\"a b\\\"\\n\\tgit add \\u0063\", 'workdir': '/q'});\n" +
		"exec_command({cmd: `git add t`, workdir: `/tpl`});\n" +
		"tools.exec_command({cmd: `git add ${name}`, workdir: '/sub'});\n"
	a := runExec(t, prog, model.InputText, "/w")
	eq(t, gitDirs(a), "git.stage@/q", "git.stage@/q", "git.stage@/tpl")
	got := actionsOf(a, model.GitStage)
	if len(got) != 3 {
		t.Fatalf("%s", describeActions(a))
	}
	u := actionsOf(a, model.ProcessUnresolved)
	if len(u) != 1 || !strings.Contains(u[0].Process.Reason, "not a static string") {
		t.Fatalf("template with substitution must be unresolved, not guessed: %s", describeActions(a))
	}
	// The decoded text is what was analyzed: the quoted operand and the
	// \u0063 escape (c).
	var exprs []string
	for _, act := range got {
		for _, r := range act.Resources {
			exprs = append(exprs, r.Expression)
		}
	}
	if !strings.Contains(strings.Join(exprs, ","), "a b") || !strings.Contains(strings.Join(exprs, ","), "c") {
		t.Fatalf("escapes were not decoded: %v", exprs)
	}
}

func TestCodexExecDynamicInputs(t *testing.T) {
	cases := map[string]struct {
		prog       string
		unresolved int
		dirs       []string
	}{
		"dynamic cmd":        {`tools.exec_command({cmd: c, workdir: "/w1"})`, 1, nil},
		"shorthand cmd":      {`tools.exec_command({cmd, workdir: "/w1"})`, 1, nil},
		"concatenation":      {`tools.exec_command({cmd: "git add " + f, workdir: "/w1"})`, 1, nil},
		"spread":             {`tools.exec_command({...opts})`, 1, nil},
		"computed key":       {`tools.exec_command({["c"+"md"]: "git add a", cmd: "git add b"})`, 1, nil},
		"shorthand workdir":  {`tools.exec_command({cmd: "git add a", workdir})`, 0, []string{"git.stage@"}},
		"identifier workdir": {`tools.exec_command({cmd: "git add a", workdir: dir})`, 0, []string{"git.stage@"}},
		"template workdir":   {"tools.exec_command({cmd: \"git add a\", workdir: `/x/${n}`})", 0, []string{"git.stage@"}},
		"concat workdir":     {`tools.exec_command({cmd: "git add a", workdir: base + "/x"})`, 0, []string{"git.stage@"}},
		"spread after cmd":   {`tools.exec_command({cmd: "git add a", ...opts})`, 1, nil},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			a := runExec(t, c.prog, model.InputText, "/w")
			if n := len(actionsOf(a, model.ProcessUnresolved)); n != c.unresolved {
				t.Fatalf("unresolved = %d: %s", n, describeActions(a))
			}
			eq(t, gitDirs(a), c.dirs...)
			if !a.Partial && c.unresolved > 0 {
				t.Log("note: unresolved call without partial flag")
			}
		})
	}
}

func TestCodexExecIgnoresOtherObjects(t *testing.T) {
	prog := `const steps = [{cmd: "git commit -m x", workdir: "/a"}];
const nested = foo({opts: {cmd: "git add y"}});
for (const s of steps) { await tools.exec_command(s); }
const re = /cmd: "git add z"/;
// tools.exec_command({cmd: "git add commented"})
const str = 'tools.exec_command({cmd: "git add quoted"})';
await tools.write_stdin({session_id: 5, chars: "", yield_time_ms: 1000});
await tools.wait({session_id: 5});
text(await tools.mcp__codex_apps__github_get_user_login({}));
`
	a := runExec(t, prog, model.InputText, "/w")
	if len(a.Actions) != 0 {
		t.Fatalf("no call is statically known, got %s", describeActions(a))
	}
}

func TestCodexExecApplyPatchAndShell(t *testing.T) {
	prog := "await tools.apply_patch(\"*** Begin Patch\\n*** Add File: n.txt\\n+hi\\n*** End Patch\\n\");\n" +
		"await tools.shell({command: [\"git\", \"add\", \"it's\"], workdir: \"/s\"});\n" +
		"await tools.shell({command: [\"bash\", \"-lc\", \"git add q\"], workdir: \"/s2\"});\n" +
		"await apply_patch(patchText);\n"
	a := runExec(t, prog, model.InputText, "/w")
	if len(actionsOf(a, model.NativePatch)) != 1 || len(actionsOf(a, model.FSWrite)) != 1 {
		t.Fatalf("%s", describeActions(a))
	}
	eq(t, gitDirs(a), "git.stage@/s", "git.stage@/s2")
}

func TestCodexExecMalformed(t *testing.T) {
	for name, prog := range map[string]string{
		"unbalanced":       `tools.exec_command({cmd: "git add a", workdir: "/x"}`,
		"unterminated str": `tools.exec_command({cmd: "git add a`,
		"unterminated cmt": `/* tools.exec_command({cmd: "git add a"})`,
		"empty":            ``,
		"garbage":          "}}}{{{ ((( ]]] \x00\xff `${",
	} {
		t.Run(name, func(t *testing.T) {
			a := runExec(t, prog, model.InputText, "/w")
			if err := a.Validate(); err != nil {
				t.Fatal(err)
			}
			if len(a.Diagnostics) == 0 {
				t.Fatal("expected a diagnostic")
			}
		})
	}
	a := runExec(t, `tools.exec_command({cmd: "git add a", workdir: "/x"}`, model.InputText, "/w")
	if !hasDiag(a, model.DiagSyntaxError, "unbalanced") {
		t.Fatalf("%v", a.Diagnostics)
	}
}

func TestCodexExecJSONInputShapes(t *testing.T) {
	e := engine(t)
	prog := `tools.exec_command({cmd: "git add a", workdir: "/j"})`
	for name, in := range map[string]model.Input{
		"string":  {Kind: model.InputJSON, JSON: mustJSON(prog)},
		"object":  {Kind: model.InputJSON, JSON: mustJSON(map[string]string{"input": prog})},
		"derived": {Kind: model.InputJSON, JSON: mustJSON(map[string]string{"input": prog, "command": "git add other"})},
	} {
		a, err := e.Analyze(context.Background(), model.Invocation{ID: "x", Tool: model.ToolIdentity{Namespace: "harness", Name: "exec"}, Input: in, Context: ctxWith("/w")})
		if err != nil {
			t.Fatal(name, err)
		}
		t.Run(name, func(t *testing.T) { eq(t, gitDirs(a), "git.stage@/j") })
	}
	a, _ := e.Analyze(context.Background(), model.Invocation{ID: "x", Tool: model.ToolIdentity{Namespace: "harness", Name: "exec"}, Input: model.Input{Kind: model.InputJSON, JSON: json.RawMessage(`{"nope":1}`)}, Context: ctxWith("/w")})
	if !a.Partial {
		t.Fatal("an input without a program must be reported")
	}
}

func TestCodexExecBounds(t *testing.T) {
	// Many calls are capped with a diagnostic; adversarial nesting terminates.
	var b strings.Builder
	for i := 0; i < 300; i++ {
		b.WriteString(`tools.exec_command({cmd: "git add a", workdir: "/x"});` + "\n")
	}
	a := runExec(t, b.String(), model.InputText, "/w")
	if !hasDiag(a, model.DiagLimitReached, "more than") {
		t.Fatalf("expected a cap diagnostic: %v", a.Diagnostics)
	}
	deep := "tools.exec_command(" + strings.Repeat("{a:", 20000) + strings.Repeat("}", 20000) + ")"
	runExec(t, deep, model.InputText, "/w")
	runExec(t, strings.Repeat("exec_command({", 20000), model.InputText, "/w")
}

func TestCodexExecGolden(t *testing.T) {
	cases := map[string]string{
		"codex_exec_commit":    realProgram,
		"codex_exec_two_dirs":  "await Promise.all([tools.exec_command({cmd: \"cd sub && git add x\", workdir: \"/one\"}), tools.exec_command({cmd: \"git add y\", workdir: \"/two\"})]);",
		"codex_exec_dynamic":   "tools.exec_command({cmd: \"git add a\", workdir});\ntools.exec_command({cmd: run});\n",
		"codex_exec_malformed": "tools.exec_command({cmd: \"git add a\"",
	}
	e := engine(t)
	for name, prog := range cases {
		t.Run(name, func(t *testing.T) {
			a, err := e.Analyze(context.Background(), execInv(prog, model.InputText, "/w"))
			if err != nil {
				t.Fatal(err)
			}
			got, _ := json.MarshalIndent(a, "", "  ")
			path := filepath.Join("testdata", "golden", name+".json")
			if *update {
				if err := os.WriteFile(path, got, 0o644); err != nil {
					t.Fatal(err)
				}
			}
			want, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("%v (run with -update)", err)
			}
			if string(want) != string(got) {
				t.Errorf("golden mismatch for %s; run go test -update", name)
			}
		})
	}
}
