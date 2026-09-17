package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func exec(t *testing.T, stdin string, args ...string) (int, string, string) {
	t.Helper()
	var out, errb bytes.Buffer
	code := run(args, strings.NewReader(stdin), &out, &errb)
	return code, out.String(), errb.String()
}

func TestAnalyzeStdinText(t *testing.T) {
	code, out, _ := exec(t, "cd service && cat config.json\n", "analyze", "-cwd", "/workspace/project", "-")
	if code != 0 {
		t.Fatalf("code %d: %s", code, out)
	}
	if !strings.Contains(out, "inferred  filesystem.read") || !strings.Contains(out, "/workspace/project/service/config.json") || !strings.Contains(out, "if n4 success") {
		t.Fatalf("%s", out)
	}
	if strings.Contains(out, "sources:") {
		t.Fatal("source must not be printed by default")
	}
	code, out, _ = exec(t, "cat secret.txt\n", "analyze", "-show-source", "-structure", "-")
	if code != 0 || !strings.Contains(out, "| cat secret.txt") || !strings.Contains(out, "structure:") {
		t.Fatalf("%d %s", code, out)
	}
}

func TestAnalyzeFileJSONAndPartialStatus(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cmd.sh")
	if err := os.WriteFile(path, []byte("python - <<'PY'\nprint(1)\nPY\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	code, out, _ := exec(t, "", "analyze", "-format", "json", "-cwd", "/w", path)
	if code != 2 {
		t.Fatalf("partial analysis must exit 2, got %d", code)
	}
	var a map[string]any
	if err := json.Unmarshal([]byte(out), &a); err != nil || a["partial"] != true {
		t.Fatalf("%v %s", err, out)
	}
	code, out, _ = exec(t, "echo )", "analyze", "-")
	if code != 2 || !strings.Contains(out, "syntax_error") {
		t.Fatalf("%d %s", code, out)
	}
}

func TestRecordsStream(t *testing.T) {
	lines := []string{
		`{"id":"c1","tool":{"namespace":"harness","name":"bash"},"input":{"kind":"text","text":"ls src"},"context":{"working_dir":{"known":true,"value":"/w"},"shell":{"known":true,"value":"bash"},"namespace":{"kind":"host"}}}`,
		`not json`,
		``,
		`{"id":"c2","tool":{"namespace":"harness","name":"Read"},"input":{"kind":"json","json":{"file_path":"/w/a.go"}},"context":{"working_dir":{"known":false},"shell":{"known":false},"namespace":{"kind":"host"}}}`,
	}
	code, out, _ := exec(t, strings.Join(lines, "\n")+"\n", "records", "-format", "jsonl", "-")
	if code != 2 {
		t.Fatalf("invalid record must make exit status 2, got %d\n%s", code, out)
	}
	outLines := strings.Split(strings.TrimSpace(out), "\n")
	if len(outLines) != 3 {
		t.Fatalf("expected 3 result lines, got %d:\n%s", len(outLines), out)
	}
	var first, second, third map[string]any
	json.Unmarshal([]byte(outLines[0]), &first)
	json.Unmarshal([]byte(outLines[1]), &second)
	json.Unmarshal([]byte(outLines[2]), &third)
	if first["invocation"] != "c1" || first["analysis"] == nil || second["error"] == nil || third["invocation"] != "c2" {
		t.Fatalf("%s", out)
	}
	code, out, _ = exec(t, lines[0]+"\n", "records", "-")
	if code != 0 || !strings.Contains(out, "record 1:") || !strings.Contains(out, "filesystem.list") {
		t.Fatalf("%d %s", code, out)
	}
}

func TestOversizedRecordIsSkippedNotBuffered(t *testing.T) {
	big := strings.Repeat("x", 9<<20)
	code, out, _ := exec(t, big+"\n"+`{"id":"ok","tool":{"name":"bash","namespace":"harness"},"input":{"kind":"text","text":"ls"},"context":{"working_dir":{"known":false},"shell":{"known":false},"namespace":{"kind":"host"}}}`+"\n", "records", "-format", "jsonl", "-")
	if code != 2 || !strings.Contains(out, "exceeds") || !strings.Contains(out, `"invocation":"ok"`) {
		t.Fatalf("%d %s", code, out[:min(len(out), 300)])
	}
}

func TestUsageAndVersion(t *testing.T) {
	if code, _, errOut := exec(t, "", "bogus"); code != 1 || !strings.Contains(errOut, "usage") {
		t.Fatal(errOut)
	}
	if code, out, _ := exec(t, "", "version"); code != 0 || !strings.HasPrefix(out, "agentcmd ") {
		t.Fatal(out)
	}
}
