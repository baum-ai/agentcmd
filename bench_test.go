package agentcmd

import (
	"context"
	"testing"
)

func BenchmarkAnalyzeShell1KiB(b *testing.B) {
	e, _ := New(Config{Rules: stdRules(), Native: stdBindings()})
	src := []byte("cd service && go test ./... 2>&1 | tee out.log; git add -A && git commit -m wip; for f in *.md; do wc -l \"$f\"; done; rm -rf build && mkdir -p build/out && cp -r assets build/out; grep -rn TODO src --include='*.go'; curl -fsSL https://example.com/x -o /tmp/x; python scripts/x.py; sed -i 's/a/b/' README.md; find . -name '*.tmp' -delete; docker exec app cat /etc/hosts; cat a b c > d; echo done\n")
	for len(src) < 1024 {
		src = append(src, "echo padding-line-to-reach-one-kilobyte-of-shell-input\n"...)
	}
	ec := ctxWith("/w")
	b.SetBytes(int64(len(src)))
	b.ReportAllocs()
	for b.Loop() {
		if _, err := e.AnalyzeShell(context.Background(), src, ec); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkAnalyzePythonHeredoc(b *testing.B) {
	e, _ := New(Config{Rules: stdRules(), Native: stdBindings(), Languages: Languages{Python: true}})
	src := []byte("cd service && python - <<'PY'\nfrom pathlib import Path\np = Path(\"config.json\")\np.write_text(p.read_text().replace(\"old\", \"new\"))\nPY\n")
	ec := ctxWith("/w")
	b.SetBytes(int64(len(src)))
	b.ReportAllocs()
	for b.Loop() {
		if _, err := e.AnalyzeShell(context.Background(), src, ec); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkAnalyzeNative(b *testing.B) {
	e, _ := New(Config{Rules: stdRules(), Native: stdBindings()})
	inv := nativeInv("n", "Edit", `{"file_path":"/w/main.go","old_string":"a","new_string":"b"}`)
	b.ReportAllocs()
	for b.Loop() {
		if _, err := e.Analyze(context.Background(), inv); err != nil {
			b.Fatal(err)
		}
	}
}
