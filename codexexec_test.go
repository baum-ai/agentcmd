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
	// The computed patch is reported as an unknown patch, not dropped.
	if len(actionsOf(a, model.NativePatch)) != 2 || len(actionsOf(a, model.FSWrite)) != 1 || !a.Partial {
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
		"codex_exec_commit":     realProgram,
		"codex_exec_two_dirs":   "await Promise.all([tools.exec_command({cmd: \"cd sub && git add x\", workdir: \"/one\"}), tools.exec_command({cmd: \"git add y\", workdir: \"/two\"})]);",
		"codex_exec_dynamic":    "tools.exec_command({cmd: \"git add a\", workdir});\ntools.exec_command({cmd: run});\n",
		"codex_exec_reach":      "if (changed) { await tools.exec_command({cmd: \"git commit -am x\", workdir: \"/wt\"}) }\nasync function cleanup() { await tools.exec_command({cmd: \"git reset --hard\"}) }\nawait tools.exec_command({cmd: \"git status\", workdir: \"/wt\"});\n",
		"codex_exec_unresolved": "for (const s of steps) await tools.exec_command(s);\ntools.exec_command({cmd: \"git status\", cmd: \"git commit -m x\"});\ntools.apply_patch(`${body}`);\n",
		"codex_exec_refs":       "function shell(o) { return o }\nconst run = tools.exec_command\nawait run({cmd: \"git push\"})\nconsole.log(shell({command: [\"git\", \"push\"]}))\nif (a) { b() } else if (c) { d() }\nawait tools?.exec_command({cmd: \"git status\", workdir: \"/wt\"})\n",
		"codex_exec_malformed":  "tools.exec_command({cmd: \"git add a\"",
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

func reachOfGit(a model.Analysis, kind model.ActionKind) []model.Reachability {
	var out []model.Reachability
	for _, act := range actionsOf(a, kind) {
		for _, n := range a.Nodes {
			if n.ID == act.Node {
				out = append(out, n.Reach)
			}
		}
	}
	return out
}

func TestCodexExecReachability(t *testing.T) {
	cases := []struct {
		name, prog string
		kind       model.ActionKind
		want       model.Reachability
	}{
		{"top level", `tools.exec_command({cmd: "git commit -m x"});`, model.GitCommit, model.ReachAlways},
		{"promise.all", `await Promise.allSettled([tools.exec_command({cmd: "git commit -m x"}), tools.exec_command({cmd: "git add y"})]);`, model.GitCommit, model.ReachAlways},
		{"text wrapper", `text(await tools.exec_command({cmd: "git commit -m x"}));`, model.GitCommit, model.ReachAlways},
		{"if block", `if (r.output.trim()) { await tools.exec_command({cmd: "git commit -am x"}) }`, model.GitCommit, model.ReachConditional},
		{"if without braces", `if (ok) await tools.exec_command({cmd: "git commit -am x"});`, model.GitCommit, model.ReachConditional},
		{"else block", `if (ok) { a() } else { await tools.exec_command({cmd: "git commit -am x"}) }`, model.GitCommit, model.ReachConditional},
		{"and", `ok && await tools.exec_command({cmd: "git commit -am x"});`, model.GitCommit, model.ReachConditional},
		{"or", `ok || await tools.exec_command({cmd: "git commit -am x"});`, model.GitCommit, model.ReachConditional},
		{"nullish", `ok ?? await tools.exec_command({cmd: "git commit -am x"});`, model.GitCommit, model.ReachConditional},
		{"ternary", `ok ? await tools.exec_command({cmd: "git commit -am x"}) : 0;`, model.GitCommit, model.ReachConditional},
		{"call before && is always", `await tools.exec_command({cmd: "git commit -m x"}) && ok;`, model.GitCommit, model.ReachAlways},
		{"after the statement", "ok && f();\nawait tools.exec_command({cmd: \"git commit -m x\"});", model.GitCommit, model.ReachAlways},
		{"for loop", `for (const f of files) { await tools.exec_command({cmd: "git commit -am x"}) }`, model.GitCommit, model.ReachRepeated},
		{"while loop", `while (more) { await tools.exec_command({cmd: "git commit -am x"}) }`, model.GitCommit, model.ReachRepeated},
		{"function declaration", `async function cleanup() { await tools.exec_command({cmd: "git reset --hard"}) }`, model.GitReset, model.ReachLatent},
		{"arrow block", `const f = async () => { await tools.exec_command({cmd: "git reset --hard"}) };`, model.GitReset, model.ReachLatent},
		{"arrow expression", `const f = () => tools.exec_command({cmd: "git reset --hard"});`, model.GitReset, model.ReachLatent},
		{"map callback", `await Promise.all(xs.map(x => tools.exec_command({cmd: "git reset --hard"})));`, model.GitReset, model.ReachLatent},
		{"try block", `try { await tools.exec_command({cmd: "git reset --hard"}) } catch (e) {}`, model.GitReset, model.ReachConditional},
		{"object literal arg is transparent", `await run({a: await tools.exec_command({cmd: "git reset --hard"})});`, model.GitReset, model.ReachAlways},
		{"template substitution", "text(`${await tools.exec_command({cmd: \"git reset --hard\"})}`);", model.GitReset, model.ReachAlways},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			a := runExec(t, c.prog, model.InputText, "/w")
			got := reachOfGit(a, c.kind)
			if len(got) == 0 || got[0] != c.want {
				t.Fatalf("reach = %v, want %s\n%s", got, c.want, describeActions(a))
			}
			if c.want != model.ReachAlways && !hasDiag(a, model.DiagUnsupported, "control flow is not modeled") {
				t.Fatal("missing control-flow assumption")
			}
		})
	}
	// An unresolved call is attributed the same way.
	a := runExec(t, `if (ok) { await tools.exec_command({cmd}) }`, model.InputText, "/w")
	n := 0
	for _, node := range a.Nodes {
		if node.Kind == model.NodeUnknown && node.Reach == model.ReachConditional {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("unresolved call node reach: %+v", a.Nodes)
	}
}

func TestCodexExecRegexAndDivision(t *testing.T) {
	// A string after a regular expression that follows a condition must not
	// swallow the real call, and a quoted call in a string must not count.
	cases := map[string]struct {
		prog string
		want []string
	}{
		"regex after if":                 {"if (ok) /\"/.test(s);\nconst msg = \"say tools.exec_command({cmd: 'git push --force', workdir: '/prod'}) to run\";\n", nil},
		"regex after if keeps real call": {"if (ok) /\"/.test(s);\nawait tools.exec_command({cmd: 'git add a', workdir: '/real'});\n", []string{"git.stage@/real"}},
		"regex after while":              {"while (x) /'/.test(s);\nawait tools.exec_command({cmd: 'git add a', workdir: '/real'});\n", []string{"git.stage@/real"}},
		"regex after for":                {"for (;;) /\"/.test(s);\nawait tools.exec_command({cmd: 'git add a', workdir: '/real'});\n", []string{"git.stage@/real"}},
		"postfix then division":          {"let r = i++ / 2 + \"/\"; const m = \"tools.exec_command({cmd:'git push',workdir:'/prod'})\";\n", nil},
		"postfix decrement":              {"let r = i-- / 2 + \"/\"; const m = \"tools.exec_command({cmd:'git push',workdir:'/prod'})\";\n", nil},
		"paren division":                 {"const h = (a + b) / 2; const m = \"/\"; await tools.exec_command({cmd: 'git add a', workdir: '/real'});\n", []string{"git.stage@/real"}},
		"regex statement start":          {"/'/.test(s);\nawait tools.exec_command({cmd: 'git add a', workdir: '/real'});\n", []string{"git.stage@/real"}},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			a := runExec(t, c.prog, model.InputText, "/w")
			eq(t, gitDirs(a), c.want...)
		})
	}
}

func TestCodexExecDuplicateKeys(t *testing.T) {
	for name, prog := range map[string]string{
		"cmd":     `tools.exec_command({cmd: "git status", cmd: "git commit -m x", workdir: "/a"})`,
		"workdir": `tools.exec_command({cmd: "git add a", workdir: "/a", workdir: "/b"})`,
		"quoted":  `tools.exec_command({cmd: "git status", "cmd": "git commit -m x"})`,
	} {
		t.Run(name, func(t *testing.T) {
			a := runExec(t, prog, model.InputText, "/w")
			u := actionsOf(a, model.ProcessUnresolved)
			if len(u) != 1 || !strings.Contains(u[0].Process.Reason, "repeats a property") || len(gitDirs(a)) != 0 {
				t.Fatalf("%s", describeActions(a))
			}
		})
	}
}

func TestCodexExecPartialOnLimits(t *testing.T) {
	var b strings.Builder
	for i := 0; i < 300; i++ {
		b.WriteString("tools.exec_command({cmd: \"git add a\", workdir: \"/x\"});\n")
	}
	a := runExec(t, b.String(), model.InputText, "/w")
	if !a.Partial || !hasDiag(a, model.DiagLimitReached, "more than") {
		t.Fatalf("partial=%v %v", a.Partial, a.Diagnostics)
	}
	a = runExec(t, `tools.exec_command({cmd: "git add a", workdir: "/x"}`, model.InputText, "/w")
	if !a.Partial || !hasDiag(a, model.DiagSyntaxError, "unbalanced") {
		t.Fatalf("partial=%v %v", a.Partial, a.Diagnostics)
	}
	for _, n := range a.Nodes {
		if n.Kind == model.NodeNative && n.Syntax == model.SyntaxParsed {
			t.Fatal("native node must not claim a clean parse")
		}
	}
	// Exhausting the scan budget is reported, and nothing is called "not a
	// static string" on the way.
	a = runExec(t, strings.Repeat("exec_command({", 20000), model.InputText, "/w")
	if !a.Partial {
		t.Fatal("budget exhaustion must be partial")
	}
	// Template literals nested too deep.
	a = runExec(t, strings.Repeat("`${", 100)+"tools.exec_command({cmd: 'git add a'})", model.InputText, "/w")
	if !a.Partial || !hasDiag(a, model.DiagLimitReached, "nested deeper") {
		t.Fatalf("%v", a.Diagnostics)
	}
}

func TestCodexExecNonLiteralArguments(t *testing.T) {
	cases := map[string]string{
		"loop variable":     "for (const s of steps) await tools.exec_command(s);",
		"ternary arg":       `tools.exec_command(c ? {cmd: "git add a"} : {cmd: "git add b"})`,
		"spread arg":        `tools.exec_command(...args)`,
		"object expression": `tools.exec_command({cmd: "git add a"} || other)`,
		"bare call":         `exec_command(opts)`,
		"shell variable":    `tools.shell(spec)`,
	}
	for name, prog := range cases {
		t.Run(name, func(t *testing.T) {
			a := runExec(t, prog, model.InputText, "/w")
			if len(actionsOf(a, model.ProcessUnresolved)) != 1 || !a.Partial || len(gitDirs(a)) != 0 {
				t.Fatalf("partial=%v %s", a.Partial, describeActions(a))
			}
		})
	}
	for name, prog := range map[string]string{
		"template patch": "tools.apply_patch(`*** Begin Patch\n${body}\n*** End Patch`)",
		"concatenation":  `tools.apply_patch("*** Begin Patch\n" + body)`,
		"variable":       `apply_patch(text)`,
	} {
		t.Run("patch "+name, func(t *testing.T) {
			a := runExec(t, prog, model.InputText, "/w")
			p := actionsOf(a, model.NativePatch)
			if len(p) != 1 || p[0].Native.Format != "unknown" || !a.Partial {
				t.Fatalf("partial=%v %s", a.Partial, describeActions(a))
			}
		})
	}
}

func TestCodexExecDefinitionsAreNotCalls(t *testing.T) {
	for name, prog := range map[string]string{
		"function":         `async function exec_command({cmd, workdir}) { return run(cmd); }`,
		"anonymous":        `const f = function exec_command(opts) { return 1; };`,
		"method":           `const o = { exec_command({cmd}) { return cmd; }, async apply_patch(p) {} };`,
		"class method":     `class T { async shell(opts) { return 1; } }`,
		"other object":     `await sandbox.exec_command({cmd: "git add a"}); await t.apply_patch("x"); await api.shell({command: ["git"]});`,
		"no argument":      `await tools.exec_command();`,
		"generator":        `function* exec_command(x) {}`,
		"only a reference": `const g = exec_command;`,
	} {
		t.Run(name, func(t *testing.T) {
			a := runExec(t, prog, model.InputText, "/w")
			if len(a.Actions) != 0 || a.Partial && !hasDiag(a, model.DiagUnsupported, "nothing to analyze") {
				t.Fatalf("partial=%v %s %v", a.Partial, describeActions(a), a.Diagnostics)
			}
			if len(actionsOf(a, model.ProcessUnresolved)) != 0 {
				t.Fatalf("phantom unresolved action: %s", describeActions(a))
			}
		})
	}
}

func TestCodexExecEscapedKeysAndTemplates(t *testing.T) {
	// An identifier written with a unicode escape is the same key.
	a := runExec(t, `tools.exec_command({\u0063md: "git add a", workdir: "/k"})`, model.InputText, "/w")
	eq(t, gitDirs(a), "git.stage@/k")
	// Calls inside template substitutions are seen, even after a comment
	// holding a brace.
	a = runExec(t, "text(`out: ${/* } */ await tools.exec_command({cmd: 'git add a', workdir: '/t'})}`);\ntools.exec_command({cmd: 'git add b', workdir: '/after'});", model.InputText, "/w")
	eq(t, gitDirs(a), "git.stage@/t", "git.stage@/after")
}

func TestCodexExecEmptyWorkdirIsUnknown(t *testing.T) {
	a := runExec(t, `tools.exec_command({cmd: "git add a", workdir: ""})`, model.InputText, "/w")
	eq(t, gitDirs(a), "git.stage@")
	a, err := engine(t).Analyze(context.Background(), nativeInv("c", "exec_command", `{"cmd":"git add a","workdir":""}`))
	if err != nil {
		t.Fatal(err)
	}
	eq(t, gitDirs(a), "git.stage@")
}

func TestCodexShellExecWorkdir(t *testing.T) {
	e := engine(t)
	run := func(args string, cwd string) model.Analysis {
		inv := nativeInv("s", "shell", args)
		if cwd == "" {
			inv.Context.WorkingDir = model.Fact{}
		}
		a, err := e.Analyze(context.Background(), inv)
		if err != nil {
			t.Fatal(err)
		}
		return a
	}
	// Relative workdir with an unknown base is not resolved.
	a := run(`{"command":["git","add","a"],"workdir":"sub"}`, "")
	if got := actionsOf(a, model.GitStage); len(got) != 1 || got[0].Resources[0].Resolved != "" || got[0].Resources[1].Resolved != "" {
		t.Fatalf("%s", describeActions(a))
	}
	// Absolute workdir is cleaned.
	a = run(`{"command":["git","add","a"],"workdir":"/abs/./wt/"}`, "")
	eq(t, gitDirs(a), "git.stage@/abs/wt")
	// Relative workdir with a known base resolves against it.
	a = run(`{"command":["git","add","a"],"workdir":"sub"}`, "/w")
	eq(t, gitDirs(a), "git.stage@/w/sub")
	// The command's span covers the whole input.
	for _, n := range a.Nodes {
		if n.Kind == model.NodeCommand && n.Span.End != len(`{"command":["git","add","a"],"workdir":"sub"}`) {
			t.Fatalf("exec command span = %+v", n.Span)
		}
	}
}

func TestNativeSpans(t *testing.T) {
	e := engine(t)
	raw := `{"cmd":"git add a","workdir":"/abs/wt"}`
	a, err := e.Analyze(context.Background(), nativeInv("c", "exec_command", raw))
	if err != nil {
		t.Fatal(err)
	}
	var native, shell *model.Node
	for i := range a.Nodes {
		switch a.Nodes[i].Kind {
		case model.NodeNative:
			native = &a.Nodes[i]
		case model.NodeShell:
			shell = &a.Nodes[i]
		}
	}
	if native == nil || native.Span.End != len(raw) {
		t.Fatalf("native node span = %+v, want end %d", native, len(raw))
	}
	if shell == nil || shell.Span == nil {
		t.Fatal("no shell node")
	}
	// The shell text is carried inside a JSON string, so it maps onto the
	// whole input.
	var src *model.Source
	for i := range a.Sources {
		if a.Sources[i].ID == shell.Span.Source {
			src = &a.Sources[i]
		}
	}
	if src == nil || src.Derived == nil || src.Derived.From != "s0" || len(src.Derived.Segments) != 1 || src.Derived.Segments[0].FromEnd != len(raw) {
		t.Fatalf("shell source derivation = %+v", src)
	}
	// Empty text input to a text-based native tool is valid.
	if _, err := e.Analyze(context.Background(), model.Invocation{ID: "e", Tool: model.ToolIdentity{Namespace: "harness", Name: "bash"}, Input: model.Input{Kind: model.InputText}, Context: ctxWith("/w")}); err != nil {
		t.Fatal(err)
	}
}

func TestGitDashC(t *testing.T) {
	e := engine(t)
	run := func(cmd string) model.Analysis {
		a, err := e.AnalyzeShell(context.Background(), []byte(cmd), ctxWith("/a/b"))
		if err != nil {
			t.Fatal(err)
		}
		return a
	}
	a := run("git -C ../other add a")
	st := actionsOf(a, model.GitStage)
	if len(st) != 1 || st[0].Resources[0].Resolved != "/a/other" || st[0].Resources[0].Expression != "../other" || st[0].Resources[1].Resolved != "/a/other/a" {
		t.Fatalf("%s", describeActions(a))
	}
	// Several -C compose in order, each relative to the previous one.
	a = run("git -C x -C ../y add a")
	st = actionsOf(a, model.GitStage)
	if len(st) != 1 || st[0].Resources[0].Resolved != "/a/b/y" || st[0].Resources[1].Resolved != "/a/b/y/a" {
		t.Fatalf("%s", describeActions(a))
	}
	// An absolute -C resets the base.
	a = run("git -C x -C /abs add a")
	if st = actionsOf(a, model.GitStage); len(st) != 1 || st[0].Resources[1].Resolved != "/abs/a" {
		t.Fatalf("%s", describeActions(a))
	}
	// A non-literal -C makes the directory unknown.
	a = run(`git -C "$D" add a`)
	if st = actionsOf(a, model.GitStage); len(st) != 1 || st[0].Resources[1].Resolved != "" {
		t.Fatalf("%s", describeActions(a))
	}
}

func reachEq(t *testing.T, prog string, kind model.ActionKind, want ...model.Reachability) {
	t.Helper()
	a := runExec(t, prog, model.InputText, "/w")
	got := reachOfGit(a, kind)
	if len(got) != len(want) {
		t.Fatalf("%q: reach = %v want %v\n%s", prog, got, want, describeActions(a))
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("%q: reach = %v want %v", prog, got, want)
		}
	}
}

func TestCodexExecStatementEnds(t *testing.T) {
	const always, cond, rep, latent = model.ReachAlways, model.ReachConditional, model.ReachRepeated, model.ReachLatent
	push := `await tools.exec_command({cmd: 'git push'});`
	// S1: a comma does not end an unbraced body.
	reachEq(t, `if (x) tools.exec_command({cmd:'git add a'}), tools.exec_command({cmd:'git push'});`, model.GitNetwork, cond)
	reachEq(t, `for (const f of fs) a(), tools.exec_command({cmd:'git push'});`, model.GitNetwork, rep)
	reachEq(t, `while (x) a(), tools.exec_command({cmd:'git push'});`, model.GitNetwork, rep)
	// ... but it does end a conditional or arrow tail.
	reachEq(t, `ok && f(), tools.exec_command({cmd:'git push'});`, model.GitNetwork, always)
	// S2: a block ends the unbraced body it was.
	reachEq(t, "if (a) { x(); } else if (b) { y(); }\n"+push, model.GitNetwork, always)
	reachEq(t, "if (a) for (const f of fs) { x(); }\n"+push, model.GitNetwork, always)
	reachEq(t, "for (const f of fs) if (c) { x(); }\n"+push, model.GitNetwork, always)
	reachEq(t, "if (a) { x(); } else { y(); } "+push, model.GitNetwork, always)
	reachEq(t, "if (a) { x(); } else if (b) { "+push+" }", model.GitNetwork, cond)
	reachEq(t, "if (a) x(); else if (b) y();\n"+push, model.GitNetwork, always)
	// S3: a line break ends a statement when the next line cannot continue it.
	reachEq(t, "const f = x => x + 1\n"+push, model.GitNetwork, always)
	reachEq(t, "const ok = a && b\n"+push, model.GitNetwork, always)
	reachEq(t, "if (a) x = 1\n"+push, model.GitNetwork, always)
	reachEq(t, "for (const f of fs) g(f)\n"+push, model.GitNetwork, always)
	reachEq(t, "const o = {a: 1}\n"+push, model.GitNetwork, always)
	reachEq(t, "let s = \"x\"\n"+push, model.GitNetwork, always)
	// ... but not when it could continue: an operator or call follows, or the
	// head of a statement is incomplete.
	reachEq(t, "ok &&\n"+push, model.GitNetwork, cond)
	reachEq(t, "ok\n  && "+push, model.GitNetwork, cond)
	reachEq(t, "if (a)\n  "+push, model.GitNetwork, cond)
	reachEq(t, "if (a) x = y\n  .z(); "+push, model.GitNetwork, always)
	reachEq(t, "const f = x =>\n  tools.exec_command({cmd:'git push'});", model.GitNetwork, latent)
	reachEq(t, "else_ = 1\nif (a) b()\nelse\n  "+push, model.GitNetwork, cond)
	// Do-while.
	reachEq(t, `do tools.exec_command({cmd:'git push'}); while (x);`, model.GitNetwork, rep)
	reachEq(t, `do { await tools.exec_command({cmd:'git push'}) } while (x);`, model.GitNetwork, rep)
	// Object and class method bodies are latent.
	reachEq(t, `const o = { run() { return tools.exec_command({cmd:'git push'}); } };`, model.GitNetwork, latent)
	reachEq(t, `const o = { async run(a) { return tools.exec_command({cmd:'git push'}); } };`, model.GitNetwork, latent)
	reachEq(t, `class T { static go() { return tools.exec_command({cmd:'git push'}); } }`, model.GitNetwork, latent)
	reachEq(t, `class T { async *go() { return tools.exec_command({cmd:'git push'}); } }`, model.GitNetwork, latent)
	// A catch block is not a method.
	reachEq(t, `try { a() } catch (e) { await tools.exec_command({cmd:'git push'}) }`, model.GitNetwork, cond)
}

func TestCodexExecUnterminatedInput(t *testing.T) {
	for name, prog := range map[string]string{
		"template":      "text(`out ${await tools.exec_command({cmd: 'git push'})",
		"template text": "text(`out: tools.exec_command({cmd: 'git push'})",
		"string":        `const s = "tools.exec_command({cmd: 'git push'}) `,
		"comment":       `/* tools.exec_command({cmd: 'git push'})`,
	} {
		t.Run(name, func(t *testing.T) {
			a := runExec(t, prog, model.InputText, "/w")
			if !a.Partial || !hasDiag(a, model.DiagSyntaxError, "unterminated") {
				t.Fatalf("partial=%v %v", a.Partial, a.Diagnostics)
			}
			for _, r := range reachOfGit(a, model.GitNetwork) {
				if r == model.ReachAlways {
					t.Fatalf("a call inside an unterminated literal is reported as always: %s", describeActions(a))
				}
			}
		})
	}
	// A terminated template does not weaken later calls.
	reachEq(t, "text(`ok ${1}`);\nawait tools.exec_command({cmd: 'git push'});", model.GitNetwork, model.ReachAlways)
}

func TestCodexExecBareNamesAndReferences(t *testing.T) {
	// A bare name the program declares itself is not the tool.
	for name, prog := range map[string]string{
		"shell function":    `function shell(o) { return 1 }; console.log(shell({command: ['git', 'push', '--force']}))`,
		"apply_patch const": "const apply_patch = s => s.length; apply_patch(\"*** Begin Patch\\n*** Delete File: /etc/passwd\\n*** End Patch\");",
		"exec_command let":  `let exec_command = (o) => 1; exec_command({cmd: 'git push'});`,
		"class":             `class shell { } new shell({command: ['git', 'push']}); shell({command: ['git', 'push']});`,
		"var function expr": `var exec_command = function (o) {}; exec_command({cmd: 'git push'});`,
	} {
		t.Run(name, func(t *testing.T) {
			a := runExec(t, prog, model.InputText, "/w")
			if len(a.Actions) != 0 {
				t.Fatalf("%s", describeActions(a))
			}
		})
	}
	// An alias of the tool, or a destructuring of tools, is the tool.
	reachEq(t, `const exec_command = tools.exec_command; await exec_command({cmd: 'git push'});`, model.GitNetwork, model.ReachAlways)
	reachEq(t, `const {exec_command} = tools; await exec_command({cmd: 'git push'});`, model.GitNetwork, model.ReachAlways)
	// A tools-qualified call is the tool even when a bare name is declared.
	reachEq(t, `function shell(o) {} await tools.shell({command: ['git', 'push']});`, model.GitNetwork, model.ReachAlways)
	// Optional chaining is a direct call.
	for _, prog := range []string{
		`await tools?.exec_command({cmd: 'git push'});`,
		`await tools.exec_command?.({cmd: 'git push'});`,
		`await tools?.exec_command?.({cmd: 'git push'});`,
	} {
		reachEq(t, prog, model.GitNetwork, model.ReachAlways)
		if a := runExec(t, prog, model.InputText, "/w"); len(actionsOf(a, model.ProcessUnresolved)) != 0 {
			t.Fatalf("%q: %s", prog, describeActions(a))
		}
	}
	// Any other use of the tools is an unresolved part, not silence.
	for name, prog := range map[string]string{
		"alias":             `const run = tools.exec_command; await run({cmd: 'git push'});`,
		"call method":       `await tools.exec_command.call(null, {cmd: 'git push'});`,
		"comma call":        `await (0, tools.exec_command)({cmd: 'git push'});`,
		"computed":          `await tools["exec_command"]({cmd: 'git push'});`,
		"computed var":      `await tools[name]({cmd: 'git push'});`,
		"optional computed": `await tools?.["shell"]({command: ['git', 'push']});`,
		"shell alias":       `const s = tools.shell; await s({command: ['git', 'push']});`,
		"patch alias":       `const p = tools.apply_patch; await p("x");`,
		"passed":            `run(tools.exec_command);`,
	} {
		t.Run(name, func(t *testing.T) {
			a := runExec(t, prog, model.InputText, "/w")
			if len(actionsOf(a, model.ProcessUnresolved)) != 1 || !a.Partial || len(gitDirs(a)) != 0 {
				t.Fatalf("partial=%v %s", a.Partial, describeActions(a))
			}
		})
	}
	// Other tools by literal name are not our business.
	a := runExec(t, `await tools["mcp__x"]({a: 1}); await tools.write_stdin({session_id: 1}); await tools.mcp__codex_apps__github_get_user_login({});`, model.InputText, "/w")
	if len(a.Actions) != 0 || len(actionsOf(a, model.ProcessUnresolved)) != 0 {
		t.Fatalf("%s", describeActions(a))
	}
}

func TestGitDashCAbsoluteExpression(t *testing.T) {
	a, err := engine(t).AnalyzeShell(context.Background(), []byte("git -C x -C /abs add a"), ctxWith("/a/b"))
	if err != nil {
		t.Fatal(err)
	}
	st := actionsOf(a, model.GitStage)
	if len(st) != 1 || st[0].Resources[0].Expression != "/abs" || st[0].Resources[0].Resolved != "/abs" {
		t.Fatalf("%s", describeActions(a))
	}
}

func TestCodexExecUnusualProperties(t *testing.T) {
	for name, prog := range map[string]string{
		"getter":    `tools.exec_command({cmd: 'git add a', get workdir() { return '/x'; }})`,
		"method":    `tools.exec_command({cmd: 'git add a', workdir() { return '/x'; }})`,
		"async":     `tools.exec_command({async cmd() { return 'git push'; }})`,
		"__proto__": `tools.exec_command({cmd: 'git add a', __proto__: {workdir: '/x'}})`,
	} {
		t.Run(name, func(t *testing.T) {
			a := runExec(t, prog, model.InputText, "/w")
			if len(actionsOf(a, model.ProcessUnresolved)) != 1 || len(gitDirs(a)) != 0 {
				t.Fatalf("%s", describeActions(a))
			}
		})
	}
	// Ordinary forms are unaffected: trailing commas, comments, nested objects.
	a := runExec(t, `tools.exec_command({ cmd: "git add a" /* c */, /* c */ "workdir": "/cm", extra: {cmd: "git push"}, });`, model.InputText, "/w")
	eq(t, gitDirs(a), "git.stage@/cm")
}

func TestCodexExecToolsObjectAliases(t *testing.T) {
	for name, prog := range map[string]string{
		"alias":           `const t = tools; await t.exec_command({cmd: 'git push'});`,
		"rename":          `const {exec_command: run} = tools; await run({cmd: 'git push'});`,
		"rest":            `const {shell, ...others} = tools; await others.exec_command({cmd: 'git push'});`,
		"spread":          `const all = {...tools}; await all.exec_command({cmd: 'git push'});`,
		"argument":        `await helper(tools, {cmd: 'git push'});`,
		"assignment":      `let t; t = tools; await t.exec_command({cmd: 'git push'});`,
		"destructure mix": `const {exec_command, write_stdin} = tools; await exec_command({cmd: 'git push'});`,
		"returned":        `const get = () => tools; await get().exec_command({cmd: 'git push'});`,
	} {
		t.Run(name, func(t *testing.T) {
			a := runExec(t, prog, model.InputText, "/w")
			if !a.Partial || len(actionsOf(a, model.ProcessUnresolved)) < 1 {
				t.Fatalf("partial=%v %s %v", a.Partial, describeActions(a), a.Diagnostics)
			}
			for _, d := range a.Diagnostics {
				if strings.Contains(d.Message, "nothing to analyze") {
					t.Fatal("reported as nothing to analyze")
				}
			}
		})
	}
	// Following the tools by name is not a reference.
	for name, prog := range map[string]string{
		"shorthand destructure": `const {exec_command} = tools; await exec_command({cmd: 'git push'});`,
		"several tools":         `const {exec_command, shell, apply_patch} = tools; await exec_command({cmd: 'git push'});`,
		"same-name alias":       `const exec_command = tools.exec_command; await exec_command({cmd: 'git push'});`,
		"let alias":             `let shell = tools.shell; await shell({command: ['git', 'push']});`,
		"property key":          `await helper({tools: 1}); await tools.exec_command({cmd: 'git push'});`,
		"own variable":          `function f(x) { const tools2 = 1; } await tools.exec_command({cmd: 'git push'});`,
	} {
		t.Run(name, func(t *testing.T) {
			a := runExec(t, prog, model.InputText, "/w")
			if a.Partial || len(actionsOf(a, model.ProcessUnresolved)) != 0 || len(actionsOf(a, model.GitNetwork)) != 1 {
				t.Fatalf("partial=%v %s %v", a.Partial, describeActions(a), a.Diagnostics)
			}
		})
	}
	// A different name is an alias, not the tool.
	a := runExec(t, `const run = tools.exec_command; await run({cmd: 'git push'});`, model.InputText, "/w")
	if !a.Partial || len(actionsOf(a, model.ProcessUnresolved)) != 1 {
		t.Fatalf("%s", describeActions(a))
	}
}

func TestCodexExecObjectFormReasons(t *testing.T) {
	for want, prog := range map[string]string{
		"a getter, method or other property form": `tools.exec_command({cmd: 'git add a', get workdir() { return '/x'; }})`,
		"a __proto__ property":                    `tools.exec_command({cmd: 'git add a', __proto__: {}})`,
		"a spread":                                `tools.exec_command({...o})`,
		"a computed key":                          `tools.exec_command({["c"]: 'x'})`,
	} {
		a := runExec(t, prog, model.InputText, "/w")
		u := actionsOf(a, model.ProcessUnresolved)
		if len(u) != 1 || !strings.Contains(u[0].Process.Reason, want) {
			t.Fatalf("want %q: %s", want, describeActions(a))
		}
	}
}
