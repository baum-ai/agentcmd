package agentcmd

import (
	"context"
	"strings"
	"testing"

	"github.com/baum-ai/agentcmd/languages"
	"github.com/baum-ai/agentcmd/model"
	nstd "github.com/baum-ai/agentcmd/native/standard"
	rstd "github.com/baum-ai/agentcmd/rules/standard"
)

func langEngine(t *testing.T) *Engine {
	t.Helper()
	e, err := New(Config{Rules: rstd.Rules(), Native: nstd.Bindings(), Frontend: languages.Frontend(), Languages: Languages{Python: true, JavaScript: true}})
	if err != nil {
		t.Fatal(err)
	}
	return e
}

func analyzeWith(t *testing.T, e *Engine, src string) model.Analysis {
	t.Helper()
	a, err := e.AnalyzeShell(context.Background(), []byte(src), ctxWith("/w"))
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func python(t *testing.T, e *Engine, code string) model.Analysis {
	return analyzeWith(t, e, "python - <<'PY'\n"+code+"\nPY\n")
}

func nodejs(t *testing.T, e *Engine, code string) model.Analysis {
	return analyzeWith(t, e, "node - <<'JS'\n"+code+"\nJS\n")
}

func TestAcceptanceScenario(t *testing.T) {
	e := langEngine(t)
	a := analyzeWith(t, e, "cd service && python - <<'PY'\nfrom pathlib import Path\np = Path(\"config.json\")\np.write_text(p.read_text().replace(\"old\", \"new\"))\nPY\n")
	if a.Partial {
		t.Fatalf("unexpected partial: %+v", a.Diagnostics)
	}
	reads, writes := actionsOf(a, model.FSRead), actionsOf(a, model.FSWrite)
	if len(reads) != 1 || len(writes) != 1 {
		t.Fatalf("%s", describeActions(a))
	}
	for _, act := range []model.Action{reads[0], writes[0]} {
		r := act.Resources[0]
		if r.Resolved != "/w/service/config.json" || r.Knowledge != model.KnowledgeDerived || act.Evidence.Basis != model.BasisBoundedValue {
			t.Fatalf("%+v %+v", r, act.Evidence)
		}
		n := node(a, act.Node)
		if n.Reach != model.ReachConditional {
			t.Fatalf("python calls must inherit conditional reachability from the guarded launcher: %+v", n)
		}
		var prog model.Node
		for cur := n; cur.Parent != ""; cur = node(a, cur.Parent) {
			if cur.Kind == model.NodeProgram {
				prog = cur
			}
		}
		if prog.Guard == nil || prog.Guard.On != model.GuardSuccess || prog.Context.Cwd.Text != "/w/service" {
			t.Fatalf("program node must carry the cd guard and cwd: %+v", prog)
		}
		joined := strings.Join(act.Evidence.Assumptions, ";")
		if !strings.Contains(joined, "standard library") || !strings.Contains(joined, "executed without raising") {
			t.Fatalf("assumptions: %v", act.Evidence.Assumptions)
		}
	}
	if writes[0].Write.Mode != model.WriteTruncate {
		t.Fatalf("%+v", writes[0].Write)
	}
	// The read precedes the write in program order and the write's node is
	// the pathlib call, not the shell command.
	if reads[0].ID != "a2" || writes[0].ID != "a3" || a.Actions[0].Kind != model.ProcessInterpreter {
		t.Fatalf("%s", describeActions(a))
	}
	if node(a, writes[0].Node).Command.Executable.Text != "pathlib.Path.write_text" {
		t.Fatalf("%+v", node(a, writes[0].Node).Command)
	}
	// Source map: the write call's span maps into the heredoc body.
	sp := writes[0].Evidence.Spans[0]
	var hd model.Source
	for _, s := range a.Sources {
		if s.ID == sp.Source {
			hd = s
		}
	}
	if hd.Kind != "heredoc" || hd.Text[sp.Start:sp.End] != "p.write_text(p.read_text().replace(\"old\", \"new\"))" {
		t.Fatalf("%q", hd.Text[sp.Start:sp.End])
	}
}

func TestPythonDegradations(t *testing.T) {
	e := langEngine(t)
	// Unknown directory: relative paths stay unresolved.
	a, _ := e.AnalyzeShell(context.Background(), []byte("python -c \"open('x.txt','w')\""), model.ExecutionContext{})
	if w := actionsOf(a, model.FSWrite); len(w) != 1 || w[0].Resources[0].Resolved != "" || w[0].Resources[0].Expression != "x.txt" {
		t.Fatalf("%s", describeActions(a))
	}
	// Shadowed Path.
	a = python(t, e, "from pathlib import Path\nPath = make_thing()\nPath('x').write_text('y')")
	if len(actionsOf(a, model.FSWrite)) != 0 {
		t.Fatalf("shadowed Path must not write: %s", describeActions(a))
	}
	found := false
	for _, d := range a.Diagnostics {
		found = found || d.Code == model.DiagShadowed
	}
	if !found {
		t.Fatalf("expected shadowed diagnostic: %+v", a.Diagnostics)
	}
	// Local open shadows the builtin.
	a = python(t, e, "def open(p, m):\n    return None\nopen('x', 'w')")
	if len(actionsOf(a, model.FSWrite)) != 0 {
		t.Fatalf("%s", describeActions(a))
	}
	// Uncalled function bodies are latent; calls expand them.
	a = python(t, e, "def f(p):\n    open(p, 'w').write('x')\nprint('hi')")
	if len(actionsOf(a, model.FSWrite)) != 0 {
		t.Fatalf("%s", describeActions(a))
	}
	a = python(t, e, "def f(p):\n    open(p, 'w').write('x')\nf('out.txt')")
	if w := actionsOf(a, model.FSWrite); len(w) != 1 || w[0].Resources[0].Resolved != "/w/out.txt" {
		t.Fatalf("%s", describeActions(a))
	}
	// Budget: a big script is reported, not silently truncated.
	small, _ := New(Config{Rules: rstd.Rules(), Frontend: languages.Frontend(), Languages: Languages{Python: true}, Limits: Limits{SyntaxNodes: 30}})
	a, _ = small.AnalyzeShell(context.Background(), []byte("python - <<'PY'\n"+strings.Repeat("open('a')\n", 50)+"PY\n"), ctxWith("/w"))
	if !a.Partial {
		t.Fatal("budget exhaustion must be partial")
	}
	// Syntax errors: partial, with well-formed statements still analyzed.
	a = python(t, e, "open('good.txt')\ndef broken(:\n    pass")
	if !a.Partial || len(actionsOf(a, model.FSRead)) != 1 {
		t.Fatalf("partial=%v %s %+v", a.Partial, describeActions(a), a.Diagnostics)
	}
	// A patch containing the same Python source is data.
	inv := model.Invocation{ID: "p", Tool: model.ToolIdentity{Name: "apply_patch"}, Input: model.Input{Kind: model.InputPatch, Patch: "*** Begin Patch\n*** Add File: s.py\n+from pathlib import Path\n+Path('config.json').write_text('x')\n*** End Patch\n"}, Context: ctxWith("/w")}
	a, _ = e.Analyze(context.Background(), inv)
	for _, act := range a.Actions {
		if act.Resources != nil && act.Resources[0].Expression == "config.json" {
			t.Fatal("patch content analyzed as code")
		}
	}
}

func TestPythonSubset(t *testing.T) {
	e := langEngine(t)
	cases := []struct {
		code string
		want []want
	}{
		{"open('r.txt')", []want{{model.FSRead, "/w/r.txt", "", ""}}},
		{"open('a.txt', 'a')", []want{{model.FSWrite, "/w/a.txt", "", ""}}},
		{"open('x.txt', mode='x')", []want{{model.FSWrite, "/w/x.txt", "", ""}}},
		{"open('rw.txt', 'r+')", []want{{model.FSRead, "/w/rw.txt", "", ""}, {model.FSWrite, "/w/rw.txt", "", ""}}},
		{"m = get_mode()\nopen('m.txt', m)", []want{{model.FSRead, "/w/m.txt", "", ""}, {model.FSWrite, "/w/m.txt", "", ""}}},
		{"with open('w.txt', 'w') as f:\n    f.write('x')", []want{{model.FSWrite, "/w/w.txt", "", ""}}},
		{"import os\nbase = 'out'\nos.remove(os.path.join(base, 'x.log'))", []want{{model.FSDelete, "/w/out/x.log", model.KnowledgeDerived, ""}}},
		{"import shutil\nshutil.rmtree('build')\nshutil.copy('a', 'b')\nshutil.move('b', 'c')", []want{{model.FSDelete, "/w/build", "", ""}, {model.FSCopy, "/w/a", "", ""}, {model.FSMove, "/w/b", "", ""}}},
		{"import os\nos.chdir('sub')\nopen('f')", []want{{model.FSRead, "/w/sub/f", "", ""}}},
		{"import os\nos.makedirs('a/b', exist_ok=True)\nos.listdir('.')", []want{{model.FSWrite, "/w/a/b", "", ""}, {model.FSList, "/w", "", ""}}},
		{"from pathlib import Path\nbase = Path('internal')\np = base / 'user.go'\np.write_text('replacement')", []want{{model.FSWrite, "/w/internal/user.go", model.KnowledgeDerived, ""}}},
		{"from pathlib import Path\nfor p in Path('.').glob('*.pyc'):\n    p.unlink()", []want{{model.SearchPath, "/w", "", ""}, {model.FSDelete, "", model.KnowledgeSymbolic, ""}}},
		{"name = 'a'\nopen(f'{name}.txt')", []want{{model.FSRead, "/w/a.txt", "", ""}}},
		{"import sys\nopen(f'{sys.argv[1]}.txt')", []want{{model.FSRead, "", model.KnowledgeSymbolic, ""}}},
		{"import subprocess\nsubprocess.run(['ls', '-l'], cwd='/tmp')", []want{{model.FSList, "/tmp", "", ""}}},
		{"import subprocess\nsubprocess.run('rm -rf build', shell=True)", []want{{model.ProcessShell, "", "", ""}, {model.FSDelete, "/w/build", "", ""}}},
		{"import subprocess\nsubprocess.check_output(cmd)", []want{{model.ProcessUnresolved, "", "", ""}}},
		{"import subprocess as sp\nsp.Popen('./script.sh')", []want{{model.ProcessExecute, "", "", ""}}},
		{"import os\nos.system('cat ' + 'a.txt')", []want{{model.ProcessShell, "", "", ""}, {model.FSRead, "/w/a.txt", "", ""}}},
		{"import os\nos.system('cat ' + target)", []want{{model.ProcessUnresolved, "", "", ""}}},
		{"if cond:\n    open('c')", []want{{model.FSRead, "/w/c", "", ""}}},
		{"try:\n    open('t')\nexcept OSError:\n    open('fallback')", []want{{model.FSRead, "/w/t", "", ""}, {model.FSRead, "/w/fallback", "", ""}}},
		{"import json\njson.dumps({})", nil},
	}
	for _, c := range cases {
		t.Run(c.code, func(t *testing.T) {
			a := python(t, e, c.code)
			acts := a.Actions[1:] // skip the launcher's process.interpreter
			if len(acts) != len(c.want) {
				t.Fatalf("got %d actions, want %d:\n%s%+v", len(acts), len(c.want), describeActions(a), a.Diagnostics)
			}
			for i, w := range c.want {
				got := acts[i]
				if got.Kind != w.kind {
					t.Errorf("action %d: %s, want %s\n%s", i, got.Kind, w.kind, describeActions(a))
					continue
				}
				if len(got.Resources) == 0 {
					continue
				}
				r := got.Resources[0]
				if w.resolved != "" && r.Resolved != w.resolved {
					t.Errorf("action %d: resolved %q, want %q", i, r.Resolved, w.resolved)
				}
				if w.knowledge != "" && r.Knowledge != w.knowledge {
					t.Errorf("action %d: knowledge %s, want %s", i, r.Knowledge, w.knowledge)
				}
			}
		})
	}
	a := python(t, e, "if cond:\n    open('c')\nfor x in xs:\n    open('d')")
	reads := actionsOf(a, model.FSRead)
	if node(a, reads[0].Node).Reach != model.ReachConditional || node(a, reads[1].Node).Reach != model.ReachRepeated {
		t.Fatalf("%+v %+v", node(a, reads[0].Node), node(a, reads[1].Node))
	}
}

func TestJavaScriptSubset(t *testing.T) {
	e := langEngine(t)
	cases := []struct {
		code string
		want []want
	}{
		{"require('fs').writeFileSync('out.json', '{}')", []want{{model.FSWrite, "/w/out.json", "", ""}}},
		{"const fs = require('node:fs');\nfs.appendFileSync('log.txt', 'x');\nfs.writeFileSync('a', 'b', { flag: 'a' });", []want{{model.FSWrite, "/w/log.txt", "", ""}, {model.FSWrite, "/w/a", "", ""}}},
		{"import { rmSync as rm } from 'node:fs';\nrm('dist', { recursive: true, force: true });", []want{{model.FSDelete, "/w/dist", "", ""}}},
		{"import fs from 'fs/promises';\nawait fs.writeFile('x', 'y');\nawait fs.readFile('z');", []want{{model.FSWrite, "/w/x", "", ""}, {model.FSRead, "/w/z", "", ""}}},
		{"const fs = require('fs');\nawait fs.promises.readFile('p');", []want{{model.FSRead, "/w/p", "", ""}}},
		{"const path = require('path');\nconst fs = require('fs');\nfs.readFileSync(path.join('a', 'b'));\nfs.readFileSync(path.join(__dirname, 'c'));", []want{{model.FSRead, "/w/a/b", model.KnowledgeDerived, ""}, {model.FSRead, "", model.KnowledgeSymbolic, ""}}},
		{"const fs = { writeFileSync() {} };\nfs.writeFileSync('x', 'y');", nil},
		{"const cp = require('child_process');\ncp.exec('rm -rf build');", []want{{model.ProcessShell, "", "", ""}, {model.FSDelete, "/w/build", "", ""}}},
		{"const { execFile, spawn } = require('child_process');\nexecFile('git', ['status']);\nspawn('ls', ['-l'], { shell: true, cwd: '/tmp' });", []want{{model.GitInspect, "/w", "", ""}, {model.ProcessShell, "", "", ""}, {model.FSList, "/tmp", "", ""}}},
		{"const cp = require('child_process');\ncp.execSync(cmd);", []want{{model.ProcessUnresolved, "", "", ""}}},
		{"const fs = require('fs');\nfunction g(q) { fs.unlinkSync(q); }\nconsole.log('x');", nil},
		{"const fs = require('fs');\nfunction g(q) { fs.unlinkSync(q); }\ng('z');", []want{{model.FSDelete, "/w/z", "", ""}}},
		{"const fs = require('fs');\nfs.mkdirSync('d', { recursive: true });\nfs.readdirSync('.');\nfs.existsSync('e');\nfs.copyFileSync('a', 'b');\nfs.renameSync('b', 'c');", []want{{model.FSWrite, "/w/d", "", ""}, {model.FSList, "/w", "", ""}, {model.FSMetadata, "/w/e", "", ""}, {model.FSCopy, "/w/a", "", ""}, {model.FSMove, "/w/b", "", ""}}},
		{"const fs = require('fs');\nprocess.chdir('sub');\nfs.readFileSync('f');", []want{{model.FSRead, "/w/sub/f", "", ""}}},
		{"const fs = require('fs');\nfs.readFileSync(`${process.env.HOME}/.npmrc`);", []want{{model.FSRead, "", model.KnowledgeSymbolic, ""}}},
	}
	for _, c := range cases {
		t.Run(c.code, func(t *testing.T) {
			a := nodejs(t, e, c.code)
			acts := a.Actions[1:]
			if len(acts) != len(c.want) {
				t.Fatalf("got %d actions, want %d:\n%s%+v", len(acts), len(c.want), describeActions(a), a.Diagnostics)
			}
			for i, w := range c.want {
				got := acts[i]
				if got.Kind != w.kind {
					t.Errorf("action %d: %s, want %s\n%s", i, got.Kind, w.kind, describeActions(a))
					continue
				}
				if len(got.Resources) == 0 {
					continue
				}
				r := got.Resources[0]
				if w.resolved != "" && r.Resolved != w.resolved {
					t.Errorf("action %d: resolved %q, want %q", i, r.Resolved, w.resolved)
				}
				if w.knowledge != "" && r.Knowledge != w.knowledge {
					t.Errorf("action %d: knowledge %s, want %s", i, r.Knowledge, w.knowledge)
				}
			}
		})
	}
}

func TestJavaScriptCallbacksAreConditional(t *testing.T) {
	e := langEngine(t)
	a := nodejs(t, e, "const fs = require('fs');\nfs.readFile('a', (err, d) => { fs.writeFileSync('b', d); });\nfs.readFile('c').then(() => fs.unlinkSync('d'));")
	reads := actionsOf(a, model.FSRead)
	if len(reads) != 2 || node(a, reads[0].Node).Reach != model.ReachAlways {
		t.Fatalf("%s", describeActions(a))
	}
	w := actionsOf(a, model.FSWrite)
	if len(w) != 1 || node(a, w[0].Node).Reach != model.ReachConditional || node(a, node(a, w[0].Node).Parent).Detail != "callback" {
		t.Fatalf("%s %+v", describeActions(a), node(a, w[0].Node))
	}
	if d := actionsOf(a, model.FSDelete); len(d) != 1 || node(a, d[0].Node).Reach != model.ReachConditional {
		t.Fatalf("%s", describeActions(a))
	}
	a = nodejs(t, e, "const fs = require('fs');\nconst fs2 = fs;\nfs2.unlinkSync('x');\nsomething(() => fs.unlinkSync('y'));")
	// fs2 aliases the module; the callback to an unknown call is analyzed
	// as a conditional body.
	d := actionsOf(a, model.FSDelete)
	if len(d) != 2 || d[0].Resources[0].Resolved != "/w/x" || node(a, d[0].Node).Reach != model.ReachAlways || d[1].Resources[0].Resolved != "/w/y" || node(a, d[1].Node).Reach != model.ReachConditional {
		t.Fatalf("%s", describeActions(a))
	}
}

func TestCrossLanguageRecursionSharesBudget(t *testing.T) {
	e := langEngine(t)
	// shell -> python -> shell -> node -> shell: ancestry and cwd preserved.
	a := analyzeWith(t, e, "cd app && python - <<'PY'\nimport subprocess\nsubprocess.run(\"node -e 'require(\\\"child_process\\\").execSync(\\\"cat deep.txt\\\")'\", shell=True)\nPY\n")
	reads := actionsOf(a, model.FSRead)
	if len(reads) != 1 || reads[0].Resources[0].Resolved != "/w/app/deep.txt" {
		t.Fatalf("%s\n%+v", describeActions(a), a.Diagnostics)
	}
	n := node(a, reads[0].Node)
	langs := []string{}
	for cur := n; cur.Parent != ""; cur = node(a, cur.Parent) {
		if cur.Kind == model.NodeProgram {
			langs = append(langs, cur.Program.Language)
		}
	}
	if strings.Join(langs, ",") != "shell,javascript,shell,python" {
		t.Fatalf("ancestry %v", langs)
	}
	if n.Reach != model.ReachConditional {
		t.Fatal("deepest command must still be conditional on the cd through nesting")
	}
	// Depth limit.
	deep := "cat leaf"
	for i := 0; i < 10; i++ {
		deep = "sh -c '" + strings.ReplaceAll(deep, "'", "'\\''") + "'"
	}
	a = analyzeWith(t, e, deep)
	if !a.Partial {
		t.Fatal("nesting beyond the limit must be partial")
	}
	limited := false
	for _, d := range a.Diagnostics {
		limited = limited || (d.Code == model.DiagLimitReached && strings.Contains(d.Message, "nesting"))
	}
	if !limited {
		t.Fatalf("%+v", a.Diagnostics)
	}
	// Derived-byte budget shared across nested sources.
	tiny, _ := New(Config{Rules: rstd.Rules(), Frontend: languages.Frontend(), Languages: Languages{Python: true}, Limits: Limits{DerivedBytes: 40}})
	a, _ = tiny.AnalyzeShell(context.Background(), []byte("python -c \"import os; os.system('cat a'); os.system('cat b'); os.system('cat c')\""), ctxWith("/w"))
	if !a.Partial {
		t.Fatalf("derived byte budget must be enforced: %+v", a.Diagnostics)
	}
}

// Without a Frontend the root package does not link tree-sitter: embedded
// programs are reported as unsupported, whatever Languages says.
func TestLanguagesNeedFrontend(t *testing.T) {
	e, err := New(Config{Rules: rstd.Rules(), Native: nstd.Bindings(), Languages: Languages{Python: true, JavaScript: true}})
	if err != nil {
		t.Fatal(err)
	}
	a, err := e.AnalyzeShell(context.Background(), []byte(`python3 -c "import os; os.remove('x')"; node -e "require('fs').rmSync('y')"`), ctxWith("/w"))
	if err != nil {
		t.Fatal(err)
	}
	if !a.Partial || !hasDiag(a, model.DiagUnsupported, "python analysis requires a language frontend") || !hasDiag(a, model.DiagUnsupported, "javascript analysis requires a language frontend") {
		t.Fatalf("%v", a.Diagnostics)
	}
	if len(actionsOf(a, model.FSDelete)) != 0 || a.Versions.LanguageRuntime != "" {
		t.Fatalf("%s %q", describeActions(a), a.Versions.LanguageRuntime)
	}
}
