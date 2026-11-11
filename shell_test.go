package agentcmd

import (
	"context"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/baum-ai/agentcmd/model"
)

var update = flag.Bool("update", false, "rewrite golden files")

func analyzeShell(t *testing.T, src string) model.Analysis {
	t.Helper()
	a, err := engine(t).AnalyzeShell(context.Background(), []byte(src), ctxWith("/w"))
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func node(a model.Analysis, id string) model.Node {
	for _, n := range a.Nodes {
		if n.ID == id {
			return n
		}
	}
	return model.Node{}
}

func actionsOf(a model.Analysis, kind model.ActionKind) []model.Action {
	var out []model.Action
	for _, act := range a.Actions {
		if act.Kind == kind {
			out = append(out, act)
		}
	}
	return out
}

func commandNodes(a model.Analysis) []model.Node {
	var out []model.Node
	for _, n := range a.Nodes {
		if n.Kind == model.NodeCommand {
			out = append(out, n)
		}
	}
	return out
}

func TestAndListGuardAndCwd(t *testing.T) {
	a := analyzeShell(t, "cd service && cat config.json")
	cmds := commandNodes(a)
	if len(cmds) != 2 || cmds[1].Guard == nil || cmds[1].Guard.Node != cmds[0].ID || cmds[1].Guard.On != model.GuardSuccess || cmds[1].Reach != model.ReachConditional {
		t.Fatalf("%+v", cmds)
	}
	reads := actionsOf(a, model.FSRead)
	if len(reads) != 1 || reads[0].Resources[0].Resolved != "/w/service/config.json" || reads[0].Resources[0].Knowledge != model.KnowledgeLiteral {
		t.Fatalf("%+v", reads)
	}
	if cwd := cmds[1].Context.Cwd; cwd.Knowledge != model.KnowledgeDerived || cwd.Text != "/w/service" || len(cwd.Assumptions) == 0 {
		t.Fatalf("cwd %+v", cwd)
	}
}

func TestOrListGuardIsFailure(t *testing.T) {
	a := analyzeShell(t, "cd nonexistent || exit 1; cat x")
	cmds := commandNodes(a)
	if cmds[1].Guard.On != model.GuardFailure || cmds[1].Command.Executable.Text != "exit" {
		t.Fatalf("%+v", cmds[1])
	}
	// exit is guarded, so cat remains reachable; cwd assumes cd succeeded.
	if cmds[2].Reach != model.ReachAlways || cmds[2].Context.Cwd.Text != "/w/nonexistent" {
		t.Fatalf("%+v", cmds[2])
	}
	a = analyzeShell(t, "exit 0; cat x")
	if cmds := commandNodes(a); cmds[1].Reach != model.ReachUnreachable {
		t.Fatalf("after unconditional exit: %+v", cmds[1])
	}
}

func TestSubshellAndPipelineStateDoesNotLeak(t *testing.T) {
	a := analyzeShell(t, "(cd build && ls) && ls")
	lists := actionsOf(a, model.FSList)
	if len(lists) != 2 || lists[0].Resources[0].Resolved != "/w/build" || lists[1].Resources[0].Resolved != "/w" {
		t.Fatalf("%+v", lists)
	}
	a = analyzeShell(t, "cd x | cat; cat y")
	reads := actionsOf(a, model.FSRead)
	if len(reads) != 1 || reads[0].Resources[0].Resolved != "/w/y" {
		t.Fatalf("%+v", reads)
	}
	var pipe model.Node
	for _, n := range a.Nodes {
		if n.Kind == model.NodePipeline {
			pipe = n
		}
	}
	if pipe.ID == "" || pipe.Detail != "|" {
		t.Fatal("pipeline node missing")
	}
	a = analyzeShell(t, "cd x & cat y")
	if reads := actionsOf(a, model.FSRead); reads[0].Resources[0].Resolved != "/w/y" {
		t.Fatalf("background cd must not leak: %+v", reads)
	}
}

func TestRedirections(t *testing.T) {
	a := analyzeShell(t, "echo hi > out.txt 2>&1; echo more >> out.txt; cat < in.txt; > empty; sort <> both")
	writes := actionsOf(a, model.FSWrite)
	if len(writes) != 4 {
		t.Fatalf("writes %+v", writes)
	}
	if writes[0].Write.Mode != model.WriteTruncate || writes[0].Resources[0].Resolved != "/w/out.txt" || writes[1].Write.Mode != model.WriteAppend {
		t.Fatalf("%+v", writes)
	}
	if writes[2].Resources[0].Resolved != "/w/empty" || writes[3].Write.Mode != model.WriteUnknown {
		t.Fatalf("%+v", writes[2:])
	}
	reads := actionsOf(a, model.FSRead)
	if len(reads) != 2 || reads[0].Resources[0].Resolved != "/w/in.txt" || reads[1].Resources[0].Role != "input" {
		t.Fatalf("%+v", reads)
	}
	for _, w := range writes {
		if node(a, w.Node).Kind != model.NodeRedirect || w.Evidence.Rule != "shell/redirect" {
			t.Fatalf("redirect actions belong to redirect nodes: %+v", node(a, w.Node))
		}
	}
	var dup bool
	for _, n := range a.Nodes {
		if n.Kind == model.NodeRedirect && strings.Contains(n.Detail, "duplicates") {
			dup = true
		}
	}
	if !dup {
		t.Fatal("2>&1 must be a descriptor duplication without a file action")
	}
	// The redirect happens even if the executable does not exist.
	a = analyzeShell(t, "nonexistent-cmd > log.txt")
	if w := actionsOf(a, model.FSWrite); len(w) != 1 || node(a, w[0].Node).Parent == "" {
		t.Fatalf("%+v", w)
	}
}

func TestQuotedHeredocBecomesProgramSource(t *testing.T) {
	src := "cd service && python - <<'PY'\nfrom pathlib import Path\np = Path(\"config.json\")\nPY\n"
	a := analyzeShell(t, src)
	var prog model.Node
	for _, n := range a.Nodes {
		if n.Kind == model.NodeProgram {
			prog = n
		}
	}
	if prog.Program == nil || prog.Program.Language != "python" || prog.Program.Mode != "stdin" || prog.Program.Source == "" {
		t.Fatalf("%+v", prog)
	}
	if prog.Guard == nil || prog.Reach != model.ReachConditional || prog.Context.Cwd.Text != "/w/service" {
		t.Fatalf("program must inherit the cd guard and cwd: %+v", prog)
	}
	var hd model.Source
	for _, s := range a.Sources {
		if s.ID == prog.Program.Source {
			hd = s
		}
	}
	body := "from pathlib import Path\np = Path(\"config.json\")\n"
	if hd.Text != body || hd.Derived == nil || !hd.Derived.Exact || hd.Derived.From != "s0" {
		t.Fatalf("%+v", hd)
	}
	if src[hd.Derived.Segments[0].FromStart:hd.Derived.Segments[0].FromEnd] != body {
		t.Fatal("heredoc span does not map to the body")
	}
	// Python analysis is not enabled in this engine, so the program is
	// explicitly unsupported and the result is partial.
	if !a.Partial || prog.Syntax != model.SyntaxUnsupported {
		t.Fatalf("partial=%v syntax=%s", a.Partial, prog.Syntax)
	}
	if p := actionsOf(a, model.ProcessInterpreter); len(p) != 1 {
		t.Fatalf("%+v", a.Actions)
	}
}

func TestUnquotedHeredocWithExpansionIsUnresolved(t *testing.T) {
	a := analyzeShell(t, "python - <<PY\nprint('$USER')\nPY\n")
	if u := actionsOf(a, model.ProcessUnresolved); len(u) != 1 || !strings.Contains(u[0].Process.Reason, "literally") {
		t.Fatalf("%+v", a.Actions)
	}
	a = analyzeShell(t, "cat <<-EOF > notes.txt\n\thello\n\tEOF\n")
	if w := actionsOf(a, model.FSWrite); len(w) != 1 || w[0].Resources[0].Resolved != "/w/notes.txt" {
		t.Fatalf("%+v", a.Actions)
	}
}

func TestNestedShellString(t *testing.T) {
	a := analyzeShell(t, "bash -c 'cd /tmp && ls' && ls")
	lists := actionsOf(a, model.FSList)
	if len(lists) != 2 || lists[0].Resources[0].Resolved != "/tmp" || lists[1].Resources[0].Resolved != "/w" {
		t.Fatalf("child cwd must not leak: %+v", lists)
	}
	if len(a.Sources) != 2 || a.Sources[1].Kind != "shell" || !a.Sources[1].Derived.Exact || a.Sources[1].Text != "cd /tmp && ls" {
		t.Fatalf("%+v", a.Sources)
	}
	a = analyzeShell(t, `bash -c "cat $FILE"`)
	if u := actionsOf(a, model.ProcessUnresolved); len(u) != 1 {
		t.Fatalf("symbolic -c string must be unresolved: %+v", a.Actions)
	}
	a = analyzeShell(t, `sh -c 'cat "$1"' _ a.txt`)
	if r := actionsOf(a, model.FSRead); len(r) != 1 || r[0].Resources[0].Resolved != "/w/a.txt" || r[0].Resources[0].Knowledge != model.KnowledgeDerived {
		t.Fatalf("positional args: %+v", a.Actions)
	}
}

func TestVariablesAndSymbolicValues(t *testing.T) {
	a := analyzeShell(t, `F=notes.md; cat "$F" "$HOME/.bashrc" *.md ~/x`)
	reads := actionsOf(a, model.FSRead)
	if len(reads) != 4 {
		t.Fatalf("%+v", reads)
	}
	if reads[0].Resources[0].Resolved != "/w/notes.md" || reads[0].Resources[0].Knowledge != model.KnowledgeDerived {
		t.Fatalf("%+v", reads[0].Resources[0])
	}
	if r := reads[1].Resources[0]; r.Knowledge != model.KnowledgeSymbolic || r.Resolved != "" || r.Expression != "$HOME/.bashrc" {
		t.Fatalf("%+v", r)
	}
	if r := reads[2].Resources[0]; r.Knowledge != model.KnowledgeGlob || r.Resolved != "/w/*.md" {
		t.Fatalf("%+v", r)
	}
	if r := reads[3].Resources[0]; r.Knowledge != model.KnowledgeSymbolic {
		t.Fatalf("%+v", r)
	}
	a = analyzeShell(t, `cd "$DIR"; cat x`)
	if r := actionsOf(a, model.FSRead)[0].Resources[0]; r.Resolved != "" || r.Expression != "x" {
		t.Fatalf("unknown cwd must not resolve: %+v", r)
	}
	a = analyzeShell(t, `unset X; cat "a${X}b"`)
	if r := actionsOf(a, model.FSRead)[0].Resources[0]; r.Resolved != "/w/ab" {
		t.Fatalf("unset expands to nothing: %+v", r)
	}
}

func TestBranchesLoopsAndFunctions(t *testing.T) {
	a := analyzeShell(t, "if [ -f .env ]; then cat .env; else cat .env.example; fi")
	reads := actionsOf(a, model.FSRead)
	if len(reads) != 2 {
		t.Fatalf("%+v", reads)
	}
	n1, n2 := node(a, reads[0].Node), node(a, reads[1].Node)
	if n1.Guard == nil || n1.Guard.On != model.GuardSuccess || n1.Reach != model.ReachConditional || n2.Reach != model.ReachConditional {
		t.Fatalf("%+v %+v", n1, n2)
	}
	if node(a, n2.Parent).Guard == nil || node(a, n2.Parent).Guard.On != model.GuardFailure {
		t.Fatalf("else branch guard: %+v", node(a, n2.Parent))
	}

	a = analyzeShell(t, `if x; then cd a; else cd b; fi; cat f`)
	r := actionsOf(a, model.FSRead)[0].Resources[0]
	if r.Resolved != "" || len(r.Alternatives) != 2 || r.Alternatives[0] != "/w/a/f" || r.Knowledge != model.KnowledgeDerived {
		t.Fatalf("branch join: %+v", r)
	}

	a = analyzeShell(t, "for f in *.md; do cat \"$f\"; done")
	reads = actionsOf(a, model.FSRead)
	if len(reads) != 1 || node(a, reads[0].Node).Reach != model.ReachRepeated || reads[0].Resources[0].Knowledge != model.KnowledgeGlob {
		t.Fatalf("%+v %+v", reads, node(a, reads[0].Node))
	}
	a = analyzeShell(t, "for f in a b; do cat $f; done")
	if r := actionsOf(a, model.FSRead)[0].Resources[0]; len(r.Alternatives) != 2 || r.Alternatives[1] != "/w/b" {
		t.Fatalf("%+v", r)
	}
	a = analyzeShell(t, "while read -r l; do cat \"$l\"; done < input.txt")
	if reads := actionsOf(a, model.FSRead); len(reads) != 2 || reads[0].Resources[0].Resolved != "/w/input.txt" || node(a, reads[1].Node).Reach != model.ReachRepeated {
		t.Fatalf("%+v", reads)
	}

	a = analyzeShell(t, "show() { cat \"$1\"; }\nshow a.txt\n")
	var fn model.Node
	for _, n := range a.Nodes {
		if n.Kind == model.NodeFunction {
			fn = n
		}
	}
	if fn.Reach != model.ReachLatent || fn.Detail != "show" {
		t.Fatalf("%+v", fn)
	}
	reads = actionsOf(a, model.FSRead)
	if len(reads) != 1 || reads[0].Resources[0].Resolved != "/w/a.txt" {
		t.Fatalf("%+v", reads)
	}
	inCall := false
	for n := node(a, reads[0].Node); n.Parent != ""; n = node(a, n.Parent) {
		inCall = inCall || n.Detail == "function call"
	}
	if !inCall {
		t.Fatal("expanded body must sit under the call node")
	}
	a = analyzeShell(t, "unused() { rm -rf /; }\necho ok\n")
	if len(a.Actions) != 0 {
		t.Fatalf("uncalled function body produced actions: %+v", a.Actions)
	}
	a = analyzeShell(t, "git() { echo shadow; }\ngit status\n")
	if len(a.Actions) != 0 {
		t.Fatalf("shadowed git must not inherit standard meaning: %+v", a.Actions)
	}
}

func TestSubstitutionsSourceAndEval(t *testing.T) {
	a := analyzeShell(t, "OUT=$(cat rev.txt); echo $OUT > .rev")
	var sub model.Node
	for _, n := range a.Nodes {
		if n.Kind == model.NodeSubstitution {
			sub = n
		}
	}
	if sub.ID == "" || len(actionsOf(a, model.FSRead)) != 1 || node(a, actionsOf(a, model.FSRead)[0].Node).Parent != sub.ID {
		t.Fatalf("%+v", a.Nodes)
	}
	if w := actionsOf(a, model.FSWrite); len(w) != 1 || w[0].Resources[0].Resolved != "/w/.rev" {
		t.Fatalf("%+v", w)
	}

	a = analyzeShell(t, "source ./env.sh; cat x")
	if !a.Partial || actionsOf(a, model.FSRead)[1].Resources[0].Resolved != "" {
		t.Fatalf("sourced code must invalidate cwd: %+v", a.Actions)
	}
	if u := actionsOf(a, model.ProcessUnresolved); len(u) != 1 || !u[0].Process.Shell {
		t.Fatalf("%+v", u)
	}

	a = analyzeShell(t, `eval "cat lit.txt"`)
	if r := actionsOf(a, model.FSRead); len(r) != 1 || r[0].Resources[0].Resolved != "/w/lit.txt" {
		t.Fatalf("literal eval is nested shell: %+v", a.Actions)
	}
	a = analyzeShell(t, `eval "$CMD"`)
	if u := actionsOf(a, model.ProcessUnresolved); len(u) != 1 || !a.Partial {
		t.Fatalf("%+v", a.Actions)
	}
}

func TestNegationFlipsGuard(t *testing.T) {
	a := analyzeShell(t, "! cd x && cat y")
	cmds := commandNodes(a)
	if cmds[1].Guard.On != model.GuardFailure {
		t.Fatalf("%+v", cmds[1].Guard)
	}
}

func TestSyntaxBudget(t *testing.T) {
	e, _ := New(Config{Limits: Limits{SyntaxNodes: 5}})
	a, err := e.AnalyzeShell(context.Background(), []byte("a; b; c; d; e; f; g"), ctxWith("/w"))
	if err != nil || !a.Partial {
		t.Fatalf("%v %v", err, a.Partial)
	}
	found := false
	for _, d := range a.Diagnostics {
		found = found || d.Code == model.DiagLimitReached
	}
	if !found || node(a, a.Nodes[len(a.Nodes)-1].ID).Kind != model.NodeUnknown {
		t.Fatalf("%+v", a.Diagnostics)
	}
}

func TestOtherInterpretersStayVisible(t *testing.T) {
	a := analyzeShell(t, "perl -pi -e 's/a/b/' lib.pm")
	u := actionsOf(a, model.ProcessUnresolved)
	if len(u) != 1 || len(u[0].Process.Args) != 4 || !strings.Contains(u[0].Process.Reason, "perl") {
		t.Fatalf("%+v", a.Actions)
	}
}

// TestGolden keeps full JSON results for representative snippets so a
// reviewer can explain structure and uncertainty from the output alone.
func TestGolden(t *testing.T) {
	cases := map[string]string{
		"and_list":        "cd service && cat config.json",
		"pipeline_bg":     "cat file1 file2 | grep foo | wc -l &",
		"redirects":       "echo 'more' >> greeting.txt 2>&1",
		"heredoc_python":  "cd service && python - <<'PY'\nfrom pathlib import Path\np = Path(\"config.json\")\np.write_text(p.read_text().replace(\"old\", \"new\"))\nPY\n",
		"if_else_join":    "if [ -f .env ]; then cd a; else cd b; fi; cat f",
		"for_glob":        "for f in *.md; do wc -l \"$f\"; done",
		"function_call":   "show() { cat \"$1\"; }\nshow a.txt\n",
		"nested_shell":    "bash -c 'cd /tmp && ls' && ls",
		"syntax_error":    "echo )",
		"redirect_only":   "> empty.log",
		"unsupported_zsh": "echo hi",
	}
	e := engine(t)
	for name, src := range cases {
		t.Run(name, func(t *testing.T) {
			ec := ctxWith("/w")
			if name == "unsupported_zsh" {
				ec.Shell = model.KnownFact("zsh", "reported")
			}
			a, err := e.AnalyzeShell(context.Background(), []byte(src), ec)
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
