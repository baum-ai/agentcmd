package agentcmd

import (
	"context"
	"testing"

	"github.com/baum-ai/agentcmd/model"
)

func TestResolveWorkdir(t *testing.T) {
	lit := model.Literal
	none := model.Unknown()
	for _, tc := range []struct {
		name    string
		base    model.Value
		workdir string
		flavor  model.PathFlavor
		want    string // "" means unknown
	}{
		{"posix absolute", lit("/w"), "/a/../b", "", "/b"},
		{"posix relative", lit("/w"), "sub", model.PathPOSIX, "/w/sub"},
		{"posix relative, unknown base", none, "sub", "", ""},
		// Without the Windows flavor a drive path is relative, as before.
		{"posix keeps drive relative", lit("/w"), `C:\x`, "", `/w/C:\x`},

		{"drive backslash", lit("/w"), `C:\Users\me\repo`, model.PathWindows, "C:/Users/me/repo"},
		{"drive slash", none, "C:/Users/me/repo/", model.PathWindows, "C:/Users/me/repo"},
		{"drive mixed and dots", none, `d:\a/b\..\c`, model.PathWindows, "d:/a/c"},
		{"drive cannot climb above root", none, `C:\..\..`, model.PathWindows, "C:/"},
		{"drive root", none, `C:\`, model.PathWindows, "C:/"},
		{"UNC", none, `\\host\share\dir\..\x`, model.PathWindows, "//host/share/x"},
		{"UNC cannot climb above share", none, `\\host\share\..`, model.PathWindows, "//host/share/"},
		{"relative to drive base", lit(`C:\Users\me`), `repo\sub`, model.PathWindows, "C:/Users/me/repo/sub"},
		{"relative to slash drive base", lit("C:/Users/me"), "../you", model.PathWindows, "C:/Users/you"},
		{"relative, unknown base", none, `repo`, model.PathWindows, ""},
		{"rooted takes base drive", lit("C:/Users/me"), `\tmp\x`, model.PathWindows, "C:/tmp/x"},
		{"rooted without base drive", none, `/tmp/x`, model.PathWindows, "/tmp/x"},
		{"drive-relative on base drive", lit("C:/Users/me"), "c:repo", model.PathWindows, "C:/Users/me/repo"},
		{"drive-relative on other drive", lit("C:/Users/me"), "D:repo", model.PathWindows, ""},
		{"drive-relative, unknown base", none, "C:repo", model.PathWindows, ""},
		{"relative to POSIX base", lit("/w"), "sub", model.PathWindows, "/w/sub"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := resolveWorkdir(tc.base, tc.workdir, tc.flavor)
			if tc.want == "" {
				if got.Concrete() {
					t.Fatalf("got %q, want unknown", got.Text)
				}
				return
			}
			if !got.Concrete() || got.Text != tc.want {
				t.Fatalf("got %+v, want %q", got, tc.want)
			}
		})
	}
}

// TestCodexWindowsWorkdir runs Codex calls whose workdir is a Windows drive
// path through the engine: exec_command, shell and code-mode exec.
func TestCodexWindowsWorkdir(t *testing.T) {
	e := engine(t)
	windows := func(inv model.Invocation) model.Invocation {
		inv.Context.PathFlavor = model.PathWindows
		inv.Context.WorkingDir = model.KnownFact("C:/Users/me", "reported")
		return inv
	}
	run := func(inv model.Invocation) model.Analysis {
		t.Helper()
		a, err := e.Analyze(context.Background(), inv)
		if err != nil {
			t.Fatal(err)
		}
		return a
	}
	a := run(windows(nativeInv("c", "exec_command", `{"cmd":"git add a","workdir":"C:\\Users\\me\\repo"}`)))
	eq(t, gitDirs(a), "git.stage@C:/Users/me/repo")
	a = run(windows(nativeInv("c", "exec_command", `{"cmd":"git add a","workdir":"D:/wt"}`)))
	eq(t, gitDirs(a), "git.stage@D:/wt")
	a = run(windows(nativeInv("c", "exec_command", `{"cmd":"git add a","workdir":"repo"}`)))
	eq(t, gitDirs(a), "git.stage@C:/Users/me/repo")
	a = run(windows(nativeInv("c", "shell", `{"command":["git","add","a"],"workdir":"C:\\wt"}`)))
	eq(t, gitDirs(a), "git.stage@C:/wt")

	prog := "text(await tools.exec_command({cmd: \"git add a\", workdir: \"C:\\\\Users\\\\me\\\\wt\"}));\n" +
		"text(await tools.exec_command({cmd: \"git add b\", workdir: 'E:/other'}));\n"
	for _, kind := range []model.InputKind{model.InputText, model.InputJSON} {
		a = run(windows(execInv(prog, kind, "/ignored")))
		eq(t, gitDirs(a), "git.stage@C:/Users/me/wt", "git.stage@E:/other")
	}

	// Without the Windows flavor the drive path stays relative to the
	// invocation's directory, as before.
	a = run(nativeInv("c", "exec_command", `{"cmd":"git add a","workdir":"C:/wt"}`))
	eq(t, gitDirs(a), "git.stage@/w/C:/wt")
}
