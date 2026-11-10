// Package snapshot defines the read-only content resolver the analyzer may
// use to see script files and package manifests as they were when an
// invocation ran. The core has no ambient filesystem access: without a
// Resolver nothing is read.
//
// A resolver answers with content plus provenance (path, snapshot identity,
// digest) and says whether the content is known to be contemporaneous with
// the invocation. Approximate content is analyzed but labeled, and never
// presented as a historical fact.
package snapshot

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"strings"
	"sync"

	"github.com/baum-ai/agentcmd/model"
)

// Request asks for one file.
type Request struct {
	// Path is the lexically resolved path in the namespace.
	Path      string
	Namespace model.Namespace
	// Snapshot is the identity the invocation ran against ("" when unknown).
	Snapshot string
	// MaxBytes bounds the content; larger files fail with ErrTooLarge.
	MaxBytes int
}

// Content is a resolver answer.
type Content struct {
	Data     []byte
	Path     string
	Snapshot string
	Digest   string
	// Approximate is true when the snapshot is not known to match the
	// invocation's (unknown snapshot, or a live working tree).
	Approximate bool
}

// Errors a resolver may return.
var (
	ErrNotFound    = errors.New("snapshot: not found")
	ErrOutsideRoot = errors.New("snapshot: path outside the resolver root")
	ErrTooLarge    = errors.New("snapshot: content exceeds the byte limit")
	ErrBudget      = errors.New("snapshot: resolver budget exhausted")
	ErrNamespace   = errors.New("snapshot: namespace not served by this resolver")
)

// Resolver serves file contents. Implementations must enforce root
// confinement and symlink policy themselves, not through lexical checks.
type Resolver interface {
	Resolve(ctx context.Context, req Request) (Content, error)
}

// Digest returns the content digest used in provenance.
func Digest(data []byte) string {
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// Map is an in-memory, content-addressed resolver for one snapshot. It
// serves the host namespace unless Namespace is set.
type Map struct {
	Snapshot  string
	Namespace model.Namespace
	Files     map[string][]byte
}

// Resolve implements Resolver. Content is exact only when the request names
// the same snapshot.
func (m Map) Resolve(_ context.Context, req Request) (Content, error) {
	ns := m.Namespace
	if ns.Kind == "" {
		ns.Kind = model.NamespaceHost
	}
	if req.Namespace.Kind != "" && req.Namespace != ns {
		return Content{}, ErrNamespace
	}
	data, ok := m.Files[path.Clean(req.Path)]
	if !ok {
		return Content{}, ErrNotFound
	}
	if req.MaxBytes > 0 && len(data) > req.MaxBytes {
		return Content{}, ErrTooLarge
	}
	return Content{Data: append([]byte(nil), data...), Path: path.Clean(req.Path), Snapshot: m.Snapshot, Digest: Digest(data), Approximate: req.Snapshot == "" || req.Snapshot != m.Snapshot}, nil
}

// Dir serves a live directory tree confined to Root through os.Root, which
// rejects traversal and symlinks that escape the root at the OS level.
// Content from a live tree is always approximate unless the caller states
// the tree is checked out at the invocation's snapshot.
type Dir struct {
	Root string
	// Snapshot is the identity the tree is checked out at, if known.
	Snapshot string
	// Namespace defaults to host.
	Namespace model.Namespace
	// Mount is the path prefix in the invocation's namespace that
	// corresponds to Root (default "/"); paths outside it are not served.
	Mount string
	// MaxTotalBytes and MaxRequests bound aggregate work; zero means the
	// defaults 16 MiB and 256.
	MaxTotalBytes int
	MaxRequests   int

	mu       sync.Mutex
	total    int
	requests int
}

// Resolve implements Resolver.
func (d *Dir) Resolve(ctx context.Context, req Request) (Content, error) {
	if err := ctx.Err(); err != nil {
		return Content{}, err
	}
	ns := d.Namespace
	if ns.Kind == "" {
		ns.Kind = model.NamespaceHost
	}
	if req.Namespace.Kind != "" && req.Namespace != ns {
		return Content{}, ErrNamespace
	}
	maxTotal, maxReq := d.MaxTotalBytes, d.MaxRequests
	if maxTotal <= 0 {
		maxTotal = 16 << 20
	}
	if maxReq <= 0 {
		maxReq = 256
	}
	d.mu.Lock()
	d.requests++
	over := d.requests > maxReq
	d.mu.Unlock()
	if over {
		return Content{}, ErrBudget
	}
	mount := d.Mount
	if mount == "" {
		mount = "/"
	}
	clean := path.Clean(req.Path)
	if !path.IsAbs(clean) {
		return Content{}, fmt.Errorf("%w: relative path %q", ErrOutsideRoot, req.Path)
	}
	rel := strings.TrimPrefix(clean, strings.TrimSuffix(mount, "/"))
	if rel == clean && mount != "/" {
		return Content{}, ErrOutsideRoot
	}
	rel = strings.TrimPrefix(rel, "/")
	if rel == "" {
		rel = "."
	}
	root, err := os.OpenRoot(d.Root)
	if err != nil {
		return Content{}, err
	}
	defer root.Close()
	info, err := root.Lstat(rel)
	switch {
	case errors.Is(err, os.ErrNotExist):
		return Content{}, ErrNotFound
	case err != nil:
		if strings.Contains(err.Error(), "escapes") || strings.Contains(err.Error(), "outside") {
			return Content{}, ErrOutsideRoot
		}
		return Content{}, err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		// os.Root follows symlinks only inside the root; we still refuse to
		// serve a link as the file the invocation named.
		return Content{}, fmt.Errorf("%w: %q is a symbolic link", ErrOutsideRoot, req.Path)
	}
	if !info.Mode().IsRegular() {
		return Content{}, fmt.Errorf("snapshot: %q is not a regular file", req.Path)
	}
	if req.MaxBytes > 0 && info.Size() > int64(req.MaxBytes) {
		return Content{}, ErrTooLarge
	}
	f, err := root.Open(rel)
	if err != nil {
		return Content{}, err
	}
	defer f.Close()
	limit := int64(req.MaxBytes)
	if limit <= 0 {
		limit = int64(maxTotal)
	}
	data, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil {
		return Content{}, err
	}
	if int64(len(data)) > limit {
		return Content{}, ErrTooLarge
	}
	d.mu.Lock()
	d.total += len(data)
	exceeded := d.total > maxTotal
	d.mu.Unlock()
	if exceeded {
		return Content{}, ErrBudget
	}
	return Content{Data: data, Path: clean, Snapshot: d.Snapshot, Digest: Digest(data), Approximate: d.Snapshot == "" || req.Snapshot != d.Snapshot}, nil
}
