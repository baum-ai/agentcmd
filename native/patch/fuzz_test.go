package patch

import "testing"

func FuzzParse(f *testing.F) {
	f.Add("*** Begin Patch\n*** Update File: a\n@@\n-x\n+y\n*** End Patch\n")
	f.Add("diff --git a/x b/x\n--- a/x\n+++ b/x\n@@ -1 +1 @@\n-a\n+b\n")
	f.Add("--- /dev/null\n+++ b/new\n@@ -0,0 +1 @@\n+hi\n")
	f.Fuzz(func(t *testing.T, s string) {
		p, err := Parse(s)
		if err != nil {
			return
		}
		for _, f := range p.Files {
			if f.Path == "" || f.Operation == "" {
				t.Fatalf("parsed file without path/operation: %+v", f)
			}
		}
	})
}
