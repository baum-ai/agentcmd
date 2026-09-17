package toolparser

import (
	"context"
	"strings"
	"testing"

	"github.com/lastpersonlabs/toolparser/model"
	nstd "github.com/lastpersonlabs/toolparser/native/standard"
	rstd "github.com/lastpersonlabs/toolparser/rules/standard"
	"github.com/lastpersonlabs/toolparser/snapshot"
)

func snapEngine(t *testing.T, r snapshot.Resolver) *Engine {
	t.Helper()
	e, err := New(Config{Rules: rstd.Rules(), Native: nstd.Bindings(), Languages: Languages{Python: true, JavaScript: true}, Resolver: r})
	if err != nil {
		t.Fatal(err)
	}
	return e
}

func hasDiag(a model.Analysis, code, substr string) bool {
	for _, d := range a.Diagnostics {
		if d.Code == code && strings.Contains(d.Message, substr) {
			return true
		}
	}
	return false
}

func TestScriptFileResolution(t *testing.T) {
	files := snapshot.Map{Snapshot: "commit-1", Files: map[string][]byte{
		"/w/scripts/migrate.py": []byte("import os\nos.remove('old.db')\n"),
		"/w/build.sh":           []byte("rm -rf dist && mkdir dist\n"),
		"/w/tool.js":            []byte("require('fs').writeFileSync('out.txt', 'x')\n"),
	}}
	e := snapEngine(t, files)
	ec := ctxWith("/w")
	ec.Snapshot = "commit-1"

	a, err := e.AnalyzeShell(context.Background(), []byte("python scripts/migrate.py --dry-run"), ec)
	if err != nil {
		t.Fatal(err)
	}
	dels := actionsOf(a, model.FSDelete)
	if len(dels) != 1 || dels[0].Resources[0].Resolved != "/w/old.db" || a.Partial {
		t.Fatalf("%s %+v", describeActions(a), a.Diagnostics)
	}
	for _, as := range dels[0].Evidence.Assumptions {
		if strings.Contains(as, "approximate") {
			t.Fatal("exact snapshot must not be labeled approximate")
		}
	}
	var ext model.Source
	for _, s := range a.Sources {
		if s.Origin != nil {
			ext = s
		}
	}
	if ext.Origin == nil || ext.Origin.Path != "/w/scripts/migrate.py" || ext.Origin.Snapshot != "commit-1" || ext.Origin.Approximate || !strings.HasPrefix(ext.Origin.Digest, "sha256:") {
		t.Fatalf("%+v", ext)
	}
	if hasDiag(a, model.DiagMissingSnapshot, "") {
		t.Fatalf("%+v", a.Diagnostics)
	}

	a, _ = e.AnalyzeShell(context.Background(), []byte("bash build.sh && node tool.js"), ec)
	if len(actionsOf(a, model.FSDelete)) != 1 || len(actionsOf(a, model.FSWrite)) != 2 {
		t.Fatalf("%s", describeActions(a))
	}
}

func TestApproximateAndStaleContent(t *testing.T) {
	files := snapshot.Map{Snapshot: "live", Files: map[string][]byte{"/w/gen.py": []byte("open('made.txt', 'w')\n")}}
	e := snapEngine(t, files)

	// Snapshot unknown: content is approximate and every derived action says so.
	a, _ := e.AnalyzeShell(context.Background(), []byte("python gen.py"), ctxWith("/w"))
	w := actionsOf(a, model.FSWrite)
	if len(w) != 1 || !strings.Contains(strings.Join(w[0].Evidence.Assumptions, ";"), "approximate") || !hasDiag(a, model.DiagStaleContent, "not known to match") {
		t.Fatalf("%s %+v", describeActions(a), a.Diagnostics)
	}
	// The file was written earlier in the invocation: snapshot content is not used.
	a, _ = e.AnalyzeShell(context.Background(), []byte("echo 'x' > gen.py; python gen.py"), ctxWith("/w"))
	if len(actionsOf(a, model.FSWrite)) != 1 || !hasDiag(a, model.DiagStaleContent, "modified earlier") || !a.Partial {
		t.Fatalf("%s %+v", describeActions(a), a.Diagnostics)
	}
	// Missing file: explicit diagnostic, nothing invented.
	a, _ = e.AnalyzeShell(context.Background(), []byte("python other.py"), ctxWith("/w"))
	if len(a.Actions) != 2 || !hasDiag(a, model.DiagMissingSnapshot, "not present") {
		t.Fatalf("%s %+v", describeActions(a), a.Diagnostics)
	}
	// No resolver: the core never reads host files.
	plain := engine(t)
	a, _ = plain.AnalyzeShell(context.Background(), []byte("python gen.py"), ctxWith("/w"))
	if len(a.Actions) != 2 || !hasDiag(a, model.DiagMissingSnapshot, "no snapshot resolver") {
		t.Fatalf("%s %+v", describeActions(a), a.Diagnostics)
	}
	// Unknown cwd: relative script path cannot be requested.
	a, _ = e.AnalyzeShell(context.Background(), []byte("python gen.py"), model.ExecutionContext{})
	if len(actionsOf(a, model.FSWrite)) != 0 || !hasDiag(a, model.DiagMissingSnapshot, "not resolvable") {
		t.Fatalf("%s %+v", describeActions(a), a.Diagnostics)
	}
}

func TestPackageScripts(t *testing.T) {
	manifest := `{"name":"app","scripts":{"prebuild":"rm -rf dist","build":"tsc && cp -r assets dist","test":"jest --ci","postbuild":"node tool.js"}}`
	files := snapshot.Map{Snapshot: "c1", Files: map[string][]byte{"/w/web/package.json": []byte(manifest), "/w/web/tool.js": []byte("require('fs').unlinkSync('tmp')")}}
	e := snapEngine(t, files)
	ec := ctxWith("/w")
	ec.Snapshot = "c1"
	a, err := e.AnalyzeShell(context.Background(), []byte("cd web && npm run build"), ec)
	if err != nil {
		t.Fatal(err)
	}
	got := kinds(a)
	want := []model.ActionKind{model.PkgRunScript, model.DevBuild, model.FSRead, model.ProcessShell, model.FSDelete, model.ProcessShell, model.DevLint, model.FSCopy, model.ProcessShell, model.FSRead, model.ProcessInterpreter, model.FSDelete}
	if len(got) != len(want) {
		t.Fatalf("%s\n%+v", describeActions(a), a.Diagnostics)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("action %d: %s want %s\n%s", i, got[i], want[i], describeActions(a))
		}
	}
	if d := actionsOf(a, model.FSDelete); d[0].Resources[0].Resolved != "/w/web/dist" || d[1].Resources[0].Resolved != "/w/web/tmp" {
		t.Fatalf("%s", describeActions(a))
	}
	if len(actionsOf(a, model.ProcessUnresolved)) != 0 {
		t.Fatalf("resolved scripts must not remain unresolved: %s", describeActions(a))
	}
	// pnpm does not run lifecycle scripts by default.
	a, _ = e.AnalyzeShell(context.Background(), []byte("cd web && pnpm run build"), ec)
	if len(actionsOf(a, model.FSDelete)) != 0 || len(actionsOf(a, model.FSCopy)) != 1 {
		t.Fatalf("%s", describeActions(a))
	}
	// Undeclared script.
	a, _ = e.AnalyzeShell(context.Background(), []byte("cd web && npm run deploy"), ec)
	if len(actionsOf(a, model.ProcessUnresolved)) != 1 || !hasDiag(a, model.DiagMissingSnapshot, "not declared") {
		t.Fatalf("%s %+v", describeActions(a), a.Diagnostics)
	}
	// Without a resolver the script stays unresolved (unchanged behaviour).
	a, _ = engine(t).AnalyzeShell(context.Background(), []byte("npm run build"), ctxWith("/w"))
	if u := actionsOf(a, model.ProcessUnresolved); len(u) != 1 || !strings.Contains(u[0].Process.Reason, "not resolved") {
		t.Fatalf("%s", describeActions(a))
	}
}
