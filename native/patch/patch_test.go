package patch

import (
	"errors"
	"strings"
	"testing"
)

func TestCodexPatch(t *testing.T) {
	text := "*** Begin Patch\n*** Update File: main.go\n@@\n-import os\n+import sys\n*** Add File: new.py\n+import subprocess\n+subprocess.run('rm -rf /', shell=True)\n*** Delete File: old.txt\n*** Update File: a.go\n*** Move to: b.go\n@@\n-x\n+y\n*** End Patch\n"
	p, err := Parse(text)
	if err != nil {
		t.Fatal(err)
	}
	if p.Format != FormatCodex || len(p.Files) != 4 {
		t.Fatalf("%+v", p)
	}
	if p.Files[0].Operation != "update" || p.Files[0].Hunks != 1 || p.Files[1].Operation != "add" || p.Files[2].Operation != "delete" || p.Files[3].Operation != "move" || p.Files[3].MoveTo != "b.go" {
		t.Fatalf("%+v", p.Files)
	}
	for _, bad := range []string{"*** Begin Patch\n*** Update File: x\n", "junk\n*** Begin Patch\n*** End Patch\n", "*** Begin Patch\n@@\n*** End Patch\n", "*** Begin Patch\n*** Move to: x\n*** End Patch\n"} {
		if _, err := Parse(bad); err == nil {
			t.Errorf("expected error for %q", bad)
		}
	}
}

func TestUnifiedDiff(t *testing.T) {
	text := strings.Join([]string{
		"diff --git a/src/a.go b/src/a.go",
		"index 1..2 100644",
		"--- a/src/a.go",
		"+++ b/src/a.go",
		"@@ -1,2 +1,2 @@",
		"-a", "+b",
		"@@ -10,2 +10,2 @@",
		"-c", "+d",
		"diff --git a/new.txt b/new.txt",
		"new file mode 100644",
		"--- /dev/null",
		"+++ b/new.txt",
		"@@ -0,0 +1 @@",
		"+hello",
		"diff --git a/gone.txt b/gone.txt",
		"deleted file mode 100644",
		"--- a/gone.txt",
		"+++ /dev/null",
		"@@ -1 +0,0 @@",
		"-bye",
		"diff --git a/old.go b/moved.go",
		"similarity index 100%",
		"rename from old.go",
		"rename to moved.go",
		"diff --git a/img.png b/img.png",
		"Binary files a/img.png and b/img.png differ",
		"",
	}, "\n")
	p, err := Parse(text)
	if err != nil {
		t.Fatal(err)
	}
	if p.Format != FormatUnified || len(p.Files) != 4 {
		t.Fatalf("%+v", p)
	}
	if p.Files[0].Path != "src/a.go" || p.Files[0].Hunks != 2 || p.Files[1].Operation != "add" || p.Files[1].Path != "new.txt" || p.Files[2].Operation != "delete" || p.Files[2].Path != "gone.txt" || p.Files[3].Operation != "move" || p.Files[3].MoveTo != "moved.go" {
		t.Fatalf("%+v", p.Files)
	}
	if len(p.Binary) != 1 {
		t.Fatalf("binary: %+v", p.Binary)
	}
	plain := "--- a.txt\t2020\n+++ a.txt\t2021\n@@ -1 +1 @@\n-x\n+y\n"
	if p, err := Parse(plain); err != nil || p.Files[0].Path != "a.txt" {
		t.Fatalf("%v %+v", err, p)
	}
	if _, err := Parse("hello world"); !errors.Is(err, ErrUnknownFormat) {
		t.Fatal(err)
	}
}
