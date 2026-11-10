package agentcmd

import (
	"strings"
	"testing"

	"github.com/baum-ai/agentcmd/model"
)

type want struct {
	kind      model.ActionKind
	resolved  string // resolved of first resource, "" to skip
	knowledge model.Knowledge
	ns        model.NamespaceKind
}

// TestRuleCatalog exercises every standard rule family through the shell
// frontend with cwd /w. Each case lists the actions expected in order.
func TestRuleCatalog(t *testing.T) {
	cases := []struct {
		src  string
		want []want
	}{
		{"cp config.example.json config.json", []want{{model.FSCopy, "/w/config.example.json", model.KnowledgeLiteral, ""}}},
		{"cp -r a b dest/", []want{{model.FSCopy, "/w/a", "", ""}, {model.FSCopy, "/w/b", "", ""}}},
		{"mv old.txt new.txt", []want{{model.FSMove, "/w/old.txt", "", ""}}},
		{"rm -rf build/", []want{{model.FSDelete, "/w/build", "", ""}}},
		{"rm -- -rf", []want{{model.FSDelete, "/w/-rf", model.KnowledgeLiteral, ""}}},
		{"rm -x file", []want{{model.FSDelete, "/w/file", "", ""}}},
		{"mkdir -p out/reports && touch out/reports/.keep", []want{{model.FSWrite, "/w/out/reports", "", ""}, {model.FSWrite, "/w/out/reports/.keep", "", ""}}},
		{"go test ./... 2>&1 | tee test.log", []want{{model.DevTest, "/w/...", model.KnowledgeGlob, ""}, {model.FSWrite, "/w/test.log", "", ""}}},
		{"sed -i 's/a/b/' lib.txt", []want{{model.FSWrite, "/w/lib.txt", "", ""}}},
		{"sed -n '1p' lib.txt", []want{{model.FSRead, "/w/lib.txt", "", ""}}},
		{"head -n 40 internal/user.go", []want{{model.FSRead, "/w/internal/user.go", "", ""}}},
		{"head -n40 a b", []want{{model.FSRead, "/w/a", "", ""}, {model.FSRead, "/w/b", "", ""}}},
		{"rg -n 'TODO' --type go", []want{{model.SearchContent, "/w", "", ""}}},
		{"rg --files src", []want{{model.SearchPath, "/w/src", "", ""}}},
		{"grep -rn \"func main\" . --include='*.go'", []want{{model.SearchContent, "/w", "", ""}}},
		{"grep foo file.txt", []want{{model.SearchContent, "/w/file.txt", "", ""}}},
		{"grep foo", nil},
		{"find . -name '*.test' -delete", []want{{model.SearchPath, "/w", "", ""}, {model.FSDelete, "/w", model.KnowledgeGlob, ""}}},
		{`find src -name '*.go' -exec cat {} ;`, []want{{model.SearchPath, "/w/src", "", ""}, {model.FSRead, "", model.KnowledgeSymbolic, ""}}},
		{"git status && git add -A && git commit -m 'wip'", []want{{model.GitInspect, "/w", "", ""}, {model.GitStage, "/w", "", ""}, {model.GitCommit, "/w", "", ""}}},
		{"git checkout -b feature/x", []want{{model.GitSwitch, "/w", "", ""}}},
		{"git push origin main", []want{{model.GitNetwork, "/w", "", ""}}},
		{"git -C sub log --oneline", []want{{model.GitInspect, "/w/sub", "", ""}}},
		{"git frobnicate", []want{{model.ProcessExecute, "/w", "", ""}}},
		{"npm install && npm run test", []want{{model.PkgInstall, "", "", ""}, {model.PkgRunScript, "", "", ""}, {model.DevTest, "", "", ""}, {model.ProcessUnresolved, "", "", ""}}},
		{"pip install -r requirements.txt", []want{{model.PkgInstall, "", "", ""}, {model.FSRead, "/w/requirements.txt", "", ""}}},
		{"npx prettier --write src/", []want{{model.DevFormat, "/w/src", "", ""}}},
		{"make test", []want{{model.DevTest, "", "", ""}, {model.ProcessUnresolved, "", "", ""}}},
		{"pytest tests/ -x", []want{{model.DevTest, "/w/tests", "", ""}}},
		{"python -m pytest tests/", []want{{model.DevTest, "", "", ""}, {model.ProcessInterpreter, "", "", ""}}},
		{"cargo test --workspace", []want{{model.DevTest, "", "", ""}}},
		{"curl -fsSL https://example.com/install.sh | sh", []want{{model.NetRequest, "", "", ""}, {model.ProcessUnresolved, "", "", ""}}},
		{"curl -o out.tgz https://example.com/x.tgz", []want{{model.NetDownload, "/w/out.tgz", "", ""}}},
		{"curl -X POST -d @body.json https://api.example.com/v1", []want{{model.NetUpload, "/w/body.json", "", ""}}},
		{"curl \"$URL\"", []want{{model.NetUnresolved, "", "", ""}}},
		{"wget -O /tmp/pkg.tgz https://example.com/pkg.tgz", []want{{model.NetDownload, "/tmp/pkg.tgz", "", ""}}},
		{"wget https://example.com/pkg.tgz", []want{{model.NetDownload, "/w/pkg.tgz", model.KnowledgeDerived, ""}}},
		{"sudo rm -rf /var/cache/foo", []want{{model.FSDelete, "/var/cache/foo", "", ""}}},
		{"timeout 30s go build ./cmd/app", []want{{model.DevBuild, "/w/cmd/app", "", ""}}},
		{"env CGO_ENABLED=0 go test -run TestX ./pkg", []want{{model.DevTest, "/w/pkg", "", ""}}},
		{"env -C sub cat x", []want{{model.FSRead, "/w/sub/x", "", ""}}},
		{"nohup nice -n 5 cat x", []want{{model.FSRead, "/w/x", "", ""}}},
		{"ls | xargs rm", []want{{model.FSList, "/w", "", ""}, {model.FSDelete, "", model.KnowledgeSymbolic, ""}}},
		{"ssh deploy@host 'systemctl restart app'", []want{{model.ProcessShell, "", "", ""}, {model.ProcessExecute, "", "", ""}}},
		{"docker exec app cat /etc/hosts", []want{{model.FSRead, "/etc/hosts", "", model.NamespaceContainer}}},
		{"docker run --rm -v \"$PWD\":/src alpine sh -c 'rm -rf /src/build'", []want{{model.ProcessShell, "", "", ""}, {model.FSDelete, "/src/build", "", model.NamespaceContainer}}},
		{"docker run --entrypoint foo img bar", []want{{model.ProcessUnresolved, "", "", ""}}},
		{"docker cp app:/var/log/x.log ./x.log", []want{{model.FSCopy, "/var/log/x.log", "", model.NamespaceContainer}}},
		{"kubectl exec pod-1 -c web -- cat /etc/hostname", []want{{model.FSRead, "/etc/hostname", "", model.NamespaceContainer}}},
		{"chmod +x script.sh", []want{{model.FSMetadata, "/w/script.sh", "", ""}}},
		{"wc -l *.go", []want{{model.FSRead, "/w/*.go", model.KnowledgeGlob, ""}}},
		{"ln -s target link", []want{{model.FSWrite, "/w/link", "", ""}}},
		{"cat file1 file2 | grep foo | wc -l &", []want{{model.FSRead, "/w/file1", "", ""}, {model.FSRead, "/w/file2", "", ""}}},
	}
	for _, c := range cases {
		t.Run(c.src, func(t *testing.T) {
			a := analyzeShell(t, c.src)
			if len(a.Actions) != len(c.want) {
				t.Fatalf("got %d actions, want %d:\n%s", len(a.Actions), len(c.want), describeActions(a))
			}
			for i, w := range c.want {
				got := a.Actions[i]
				if got.Kind != w.kind {
					t.Errorf("action %d: kind %s, want %s\n%s", i, got.Kind, w.kind, describeActions(a))
					continue
				}
				if len(got.Resources) == 0 {
					if w.resolved != "" || w.knowledge != "" || w.ns != "" {
						t.Errorf("action %d has no resources: %s", i, describeActions(a))
					}
					continue
				}
				r := got.Resources[0]
				if w.resolved != "" && r.Resolved != w.resolved {
					t.Errorf("action %d: resolved %q, want %q", i, r.Resolved, w.resolved)
				}
				if w.knowledge != "" && r.Knowledge != w.knowledge {
					t.Errorf("action %d: knowledge %s, want %s", i, r.Knowledge, w.knowledge)
				}
				if w.ns != "" && r.Namespace.Kind != w.ns {
					t.Errorf("action %d: namespace %s, want %s", i, r.Namespace.Kind, w.ns)
				}
			}
		})
	}
}

func describeActions(a model.Analysis) string {
	var b strings.Builder
	for _, act := range a.Actions {
		b.WriteString(string(act.Kind))
		for _, r := range act.Resources {
			b.WriteString(" " + r.Role + "=" + r.Expression + "→" + r.Resolved + "(" + string(r.Knowledge) + "," + string(r.Namespace.Kind) + ")")
		}
		if act.Process != nil {
			b.WriteString(" reason=" + act.Process.Reason)
		}
		b.WriteString("\n")
	}
	return b.String()
}

func TestUnknownOptionReducesPrecision(t *testing.T) {
	a := analyzeShell(t, "rm --frobnicate value file")
	// --frobnicate is unknown: "value" is treated as an operand under a
	// recorded assumption rather than silently consumed or silently kept.
	dels := actionsOf(a, model.FSDelete)
	if len(dels) != 2 {
		t.Fatalf("%s", describeActions(a))
	}
	joined := strings.Join(dels[0].Evidence.Assumptions, ";")
	if !strings.Contains(joined, "--frobnicate") {
		t.Fatalf("assumption must name the unknown option: %v", dels[0].Evidence.Assumptions)
	}
}

func TestIntentIsNotPromoted(t *testing.T) {
	a := analyzeShell(t, "make test")
	if i := actionsOf(a, model.DevTest); len(i) != 1 || i[0].Intent.Verified {
		t.Fatalf("arbitrary target named test must not be verified: %+v", i)
	}
	a = analyzeShell(t, "go test ./...")
	if i := actionsOf(a, model.DevTest); len(i) != 1 || !i[0].Intent.Verified {
		t.Fatalf("%+v", i)
	}
	a = analyzeShell(t, "npm run test")
	for _, act := range a.Actions {
		if act.Kind == model.DevTest && act.Intent.Verified {
			t.Fatal("npm run test is a project script, not a verified runner")
		}
	}
}

func TestNamespacesDoNotConflate(t *testing.T) {
	a := analyzeShell(t, "docker exec app cat /etc/hosts; cat /etc/hosts")
	reads := actionsOf(a, model.FSRead)
	if len(reads) != 2 || reads[0].Resources[0].Namespace.Kind != model.NamespaceContainer || reads[0].Resources[0].Namespace.ID != "app" || reads[1].Resources[0].Namespace.Kind != model.NamespaceHost {
		t.Fatalf("%s", describeActions(a))
	}
	if n := node(a, reads[0].Node); n.Context.Cwd.Knowledge != model.KnowledgeUnknown || len(n.Command.Wrappers) != 1 || n.Command.Wrappers[0] != "docker" {
		t.Fatalf("%+v", n)
	}
	a = analyzeShell(t, "ssh host 'cd /srv && cat app.log'")
	reads = actionsOf(a, model.FSRead)
	if len(reads) != 1 || reads[0].Resources[0].Namespace.Kind != model.NamespaceRemote || reads[0].Resources[0].Namespace.ID != "host" || reads[0].Resources[0].Resolved != "/srv/app.log" {
		t.Fatalf("%s", describeActions(a))
	}
	a = analyzeShell(t, "docker exec app cat rel.txt")
	if r := actionsOf(a, model.FSRead)[0].Resources[0]; r.Resolved != "" {
		t.Fatalf("relative guest path must not resolve against host cwd: %+v", r)
	}
}
