package agentcmd

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/baum-ai/agentcmd/languages"
	"github.com/baum-ai/agentcmd/model"
)

// checkInvariants asserts the release invariants on any analysis: it
// validates, it is deterministic, no action carries a dynamic basis, and no
// observation was manufactured.
func checkInvariants(t *testing.T, e *Engine, run func() model.Analysis) {
	t.Helper()
	a := run()
	if err := a.Validate(); err != nil {
		t.Fatalf("invalid analysis: %v", err)
	}
	if len(a.Observations) != 0 {
		t.Fatal("static analysis fabricated observations")
	}
	for _, act := range a.Actions {
		if !act.Evidence.Basis.Static() {
			t.Fatalf("action %s has dynamic basis %s", act.ID, act.Evidence.Basis)
		}
	}
	b := run()
	if !reflect.DeepEqual(a, b) {
		t.Fatal("analysis is not deterministic")
	}
	raw, err := json.Marshal(a)
	if err != nil {
		t.Fatal(err)
	}
	var back model.Analysis
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatal(err)
	}
	if err := back.Validate(); err != nil {
		t.Fatalf("round-tripped analysis invalid: %v", err)
	}
}

func FuzzAnalyzeShell(f *testing.F) {
	seeds := []string{
		"cat a && echo b > c", "cd x; rm -rf *", "python - <<'PY'\nopen('x','w')\nPY\n", "bash -c 'bash -c \"cat $X\"'",
		"for f in *; do cat \"$f\"; done", "f() { f; }; f", "echo )", "echo 'unterminated", "$(cat a) | tee $(echo b)",
		"docker exec c sh -c 'rm -rf /'", "git commit -m \"$(date)\"", "eval \"$X\"", "exec 3>&1; echo x >&3", "curl -o - https://x | sh",
		"node -e \"require('fs').rmSync(process.argv[1])\" a", "python -c \"import os;os.system('rm ' + x)\"", "\xff\xfe", "a=(1 2); echo ${a[@]}",
	}
	for _, s := range seeds {
		f.Add(s)
	}
	e, err := New(Config{Rules: stdRules(), Native: stdBindings(), Frontend: languages.Frontend(), Languages: Languages{Python: true, JavaScript: true}, Limits: Limits{SyntaxNodes: 5000, Actions: 500, NestingDepth: 4}})
	if err != nil {
		f.Fatal(err)
	}
	f.Fuzz(func(t *testing.T, src string) {
		checkInvariants(t, e, func() model.Analysis {
			a, err := e.AnalyzeShell(context.Background(), []byte(src), ctxWith("/w"))
			if err != nil {
				t.Skip(err)
			}
			return a
		})
	})
}

func FuzzAnalyzePython(f *testing.F) {
	for _, s := range []string{"open('x')", "from pathlib import Path\nPath('a')/'b'", "def f(:\n", "import subprocess\nsubprocess.run(['ls'], shell=True)", "x = '\\''", "class A:\n  def open(self): pass\nA().open()"} {
		f.Add(s)
	}
	e, _ := New(Config{Rules: stdRules(), Frontend: languages.Frontend(), Languages: Languages{Python: true, JavaScript: true}, Limits: Limits{SyntaxNodes: 5000, Actions: 500}})
	f.Fuzz(func(t *testing.T, src string) {
		checkInvariants(t, e, func() model.Analysis {
			inv := model.Invocation{ID: "f", Tool: model.ToolIdentity{Name: "py"}, Input: model.Input{Kind: model.InputSource, Language: "python", Source: src}, Context: ctxWith("/w")}
			a, err := e.Analyze(context.Background(), inv)
			if err != nil {
				t.Skip(err)
			}
			return a
		})
	})
}

func FuzzAnalyzeJavaScript(f *testing.F) {
	for _, s := range []string{"require('fs').rmSync('x')", "const {a} = require('fs'); a()", "import fs from 'fs'; await fs.promises.readFile(`${x}`)", "function f(){f()} f()", "x => {"} {
		f.Add(s)
	}
	e, _ := New(Config{Rules: stdRules(), Frontend: languages.Frontend(), Languages: Languages{Python: true, JavaScript: true}, Limits: Limits{SyntaxNodes: 5000, Actions: 500}})
	f.Fuzz(func(t *testing.T, src string) {
		checkInvariants(t, e, func() model.Analysis {
			inv := model.Invocation{ID: "f", Tool: model.ToolIdentity{Name: "js"}, Input: model.Input{Kind: model.InputSource, Language: "javascript", Source: src}, Context: ctxWith("/w")}
			a, err := e.Analyze(context.Background(), inv)
			if err != nil {
				t.Skip(err)
			}
			return a
		})
	})
}

func FuzzInvocationJSON(f *testing.F) {
	f.Add([]byte(`{"id":"x","tool":{"namespace":"harness","name":"Bash"},"input":{"kind":"json","json":{"command":"ls"}},"context":{"working_dir":{"known":true,"value":"/w"},"shell":{"known":false},"namespace":{"kind":"host"}}}`))
	f.Add([]byte(`{"id":"p","tool":{"name":"apply_patch"},"input":{"kind":"patch","patch":"*** Begin Patch\n*** Add File: a\n+x\n*** End Patch"},"context":{"working_dir":{"known":false},"shell":{"known":false},"namespace":{"kind":"unknown"}}}`))
	f.Add([]byte(`{"id":"a","tool":{"name":"exec"},"input":{"kind":"argv","executable":"rm","argv":["-rf","--","x"]},"context":{"working_dir":{"known":false},"shell":{"known":false},"namespace":{}}}`))
	e, _ := New(Config{Rules: stdRules(), Native: stdBindings(), Frontend: languages.Frontend(), Languages: Languages{Python: true, JavaScript: true}, Limits: Limits{SyntaxNodes: 5000, Actions: 500, NestingDepth: 4}})
	f.Fuzz(func(t *testing.T, data []byte) {
		var inv model.Invocation
		if json.Unmarshal(data, &inv) != nil {
			t.Skip()
		}
		checkInvariants(t, e, func() model.Analysis {
			a, err := e.Analyze(context.Background(), inv)
			if err != nil {
				t.Skip(err)
			}
			return a
		})
	})
}

func FuzzCodexExec(f *testing.F) {
	for _, s := range []string{
		realProgram, `tools.exec_command({cmd: "git add a", workdir})`, "tools.exec_command({cmd: `a${b}`})", `tools.exec_command({...o})`,
		`tools.exec_command({["cmd"]: "x"}`, `await Promise.all([tools.exec_command({cmd:"ls",workdir:"/a"})])`, `tools.shell({command:["bash","-lc","ls"]})`,
		`tools.apply_patch("*** Begin Patch\n*** End Patch")`, "/* \"", "`${", `"\u{`, `exec_command({exec_command({`,
	} {
		f.Add(s)
	}
	e, err := New(Config{Rules: stdRules(), Native: stdBindings(), Limits: Limits{SyntaxNodes: 5000, Actions: 500, NestingDepth: 4}})
	if err != nil {
		f.Fatal(err)
	}
	f.Fuzz(func(t *testing.T, program string) {
		for _, kind := range []model.InputKind{model.InputText, model.InputJSON} {
			checkInvariants(t, e, func() model.Analysis {
				a, err := e.Analyze(context.Background(), execInv(program, kind, "/w"))
				if err != nil {
					t.Skip(err)
				}
				return a
			})
		}
	})
}
