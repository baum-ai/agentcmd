package snapshot

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/lastpersonlabs/agentcmd/model"
)

func TestMap(t *testing.T) {
	m := Map{Snapshot: "abc", Files: map[string][]byte{"/w/s.py": []byte("open('x')")}}
	c, err := m.Resolve(context.Background(), Request{Path: "/w/./s.py", Snapshot: "abc"})
	if err != nil || c.Approximate || c.Digest == "" || string(c.Data) != "open('x')" {
		t.Fatalf("%v %+v", err, c)
	}
	if c, _ := m.Resolve(context.Background(), Request{Path: "/w/s.py", Snapshot: "other"}); !c.Approximate {
		t.Fatal("different snapshot must be approximate")
	}
	if c, _ := m.Resolve(context.Background(), Request{Path: "/w/s.py"}); !c.Approximate {
		t.Fatal("unknown snapshot must be approximate")
	}
	if _, err := m.Resolve(context.Background(), Request{Path: "/w/none"}); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	if _, err := m.Resolve(context.Background(), Request{Path: "/w/s.py", MaxBytes: 3}); !errors.Is(err, ErrTooLarge) {
		t.Fatal(err)
	}
	if _, err := m.Resolve(context.Background(), Request{Path: "/w/s.py", Namespace: model.Namespace{Kind: model.NamespaceContainer, ID: "c"}}); !errors.Is(err, ErrNamespace) {
		t.Fatal(err)
	}
}

func TestDirConfinement(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	os.MkdirAll(filepath.Join(root, "scripts"), 0o755)
	os.WriteFile(filepath.Join(root, "scripts", "a.sh"), []byte("cat x"), 0o644)
	os.WriteFile(filepath.Join(outside, "secret"), []byte("nope"), 0o644)
	os.Symlink(filepath.Join(outside, "secret"), filepath.Join(root, "link"))
	os.WriteFile(filepath.Join(root, "big"), make([]byte, 100), 0o644)

	d := &Dir{Root: root, Mount: "/w", Snapshot: "live"}
	c, err := d.Resolve(context.Background(), Request{Path: "/w/scripts/a.sh", Snapshot: "live"})
	if err != nil || string(c.Data) != "cat x" || c.Approximate || c.Path != "/w/scripts/a.sh" {
		t.Fatalf("%v %+v", err, c)
	}
	if c, _ := d.Resolve(context.Background(), Request{Path: "/w/scripts/a.sh", Snapshot: "commit-1"}); !c.Approximate {
		t.Fatal("live tree at another snapshot must be approximate")
	}
	for _, p := range []string{"/w/../etc/passwd", "/w/scripts/../../x", "/other/a.sh", "relative"} {
		if _, err := d.Resolve(context.Background(), Request{Path: p}); !errors.Is(err, ErrOutsideRoot) && !errors.Is(err, ErrNotFound) {
			t.Fatalf("%s: %v", p, err)
		}
	}
	if _, err := d.Resolve(context.Background(), Request{Path: "/w/link"}); !errors.Is(err, ErrOutsideRoot) {
		t.Fatalf("symlink must be refused: %v", err)
	}
	if _, err := d.Resolve(context.Background(), Request{Path: "/w/big", MaxBytes: 10}); !errors.Is(err, ErrTooLarge) {
		t.Fatal(err)
	}
	if _, err := d.Resolve(context.Background(), Request{Path: "/w/missing"}); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	small := &Dir{Root: root, Mount: "/w", MaxRequests: 1}
	small.Resolve(context.Background(), Request{Path: "/w/big"})
	if _, err := small.Resolve(context.Background(), Request{Path: "/w/big"}); !errors.Is(err, ErrBudget) {
		t.Fatal(err)
	}
	tiny := &Dir{Root: root, Mount: "/w", MaxTotalBytes: 50}
	if _, err := tiny.Resolve(context.Background(), Request{Path: "/w/big", MaxBytes: 200}); !errors.Is(err, ErrBudget) {
		t.Fatal(err)
	}
}
