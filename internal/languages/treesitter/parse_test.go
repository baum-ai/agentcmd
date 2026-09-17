package treesitter

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestQualification(t *testing.T) {
	cases := []struct {
		lang   Language
		src    string
		status Status
		root   string
	}{
		{Python, "from pathlib import Path\np = Path(\"x\")\n", StatusParsed, "module"},
		{Python, "def f(:\n  open('x'\n", StatusPartial, "module"},
		{Python, "s = \"héllo — ✓\"\nopen(s)\n", StatusParsed, "module"},
		{JavaScript, "const fs = require('fs');\nfs.writeFileSync('a', 'x');\n", StatusParsed, "program"},
		{JavaScript, "const fs = require('fs'\n", StatusPartial, "program"},
	}
	for _, c := range cases {
		tr, err := Parse(c.lang, []byte(c.src), time.Second, 0)
		if err != nil {
			t.Fatal(err)
		}
		defer tr.Release()
		if tr.Status != c.status {
			t.Errorf("%s %q: status %s, want %s", c.lang, c.src, tr.Status, c.status)
		}
		if got := tr.Root.Type(tr.Lang); !strings.HasPrefix(got, c.root) && got != "ERROR" {
			t.Errorf("root %s", got)
		}
		if int(tr.Root.EndByte()) != len(c.src) {
			t.Errorf("root end %d, want %d", tr.Root.EndByte(), len(c.src))
		}
	}
}

func TestUnicodeOffsetsAreBytes(t *testing.T) {
	src := []byte("s = \"héllo\"\nopen(s)\n")
	tr, err := Parse(Python, src, time.Second, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer tr.Release()
	call := tr.Root.NamedChild(1)
	if got := call.Text(src); got != "open(s)" || call.Type(tr.Lang) != "call" {
		t.Fatalf("got %q", got)
	}
	if call.StartByte() != uint32(strings.Index(string(src), "open")) {
		t.Fatalf("byte offset %d", call.StartByte())
	}
}

func TestNodeBudgetAndTimeout(t *testing.T) {
	var sb strings.Builder
	for i := 0; i < 2000; i++ {
		fmt.Fprintf(&sb, "x%d = open('f%d').read()\n", i, i)
	}
	src := []byte(sb.String())
	tr, err := Parse(Python, src, time.Second, 100)
	if err != nil {
		t.Fatal(err)
	}
	tr.Release()
	if tr.Status != StatusLimit {
		t.Fatalf("status %s", tr.Status)
	}
	tr, err = Parse(Python, src, 10*time.Microsecond, 0)
	if err != nil {
		t.Fatal(err)
	}
	tr.Release()
	if tr.Status != StatusLimit || tr.Reason != "timeout" {
		t.Fatalf("status %s reason %q", tr.Status, tr.Reason)
	}
}

func TestUnsupportedLanguage(t *testing.T) {
	if _, err := Parse(Language("ruby"), []byte("puts 1"), 0, 0); err == nil {
		t.Fatal("expected error")
	}
}

func BenchmarkParsePythonSnippet(b *testing.B) {
	src := []byte("from pathlib import Path\np = Path(\"config.json\")\np.write_text(p.read_text().replace(\"old\", \"new\"))\n")
	b.SetBytes(int64(len(src)))
	for b.Loop() {
		tr, err := Parse(Python, src, time.Second, 0)
		if err != nil {
			b.Fatal(err)
		}
		tr.Release()
	}
}

func BenchmarkParseJavaScriptSnippet(b *testing.B) {
	src := []byte("const fs = require('fs');\nfs.writeFileSync('a.txt', 'x');\n")
	b.SetBytes(int64(len(src)))
	for b.Loop() {
		tr, err := Parse(JavaScript, src, time.Second, 0)
		if err != nil {
			b.Fatal(err)
		}
		tr.Release()
	}
}
