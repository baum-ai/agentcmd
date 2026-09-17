// Package treesitter wraps the pure-Go tree-sitter runtime for the embedded
// languages the analyzer supports. The wrapped node types stay private to
// internal/languages.
package treesitter

import (
	"errors"
	"fmt"
	"time"

	ts "github.com/odvcencio/gotreesitter"
	"github.com/odvcencio/gotreesitter/grammars"
)

// Version is the pinned runtime version recorded in results.
const Version = "github.com/odvcencio/gotreesitter@v0.52.0"

// Language identifies a qualified grammar.
type Language string

const (
	Python     Language = "python"
	JavaScript Language = "javascript"
)

func grammar(l Language) (*ts.Language, error) {
	switch l {
	case Python:
		return grammars.PythonLanguage(), nil
	case JavaScript:
		return grammars.JavascriptLanguage(), nil
	}
	return nil, fmt.Errorf("unsupported language %q", l)
}

// Status classifies the parse outcome.
type Status string

const (
	StatusParsed  Status = "parsed"
	StatusPartial Status = "partial"       // tree contains ERROR or MISSING nodes
	StatusLimit   Status = "limit_reached" // stopped early: timeout or node budget
)

// Tree is an owned handle over a parsed source.
type Tree struct {
	Lang   *ts.Language
	Root   *ts.Node
	Source []byte
	Status Status
	Nodes  int
	Reason string
	tree   *ts.Tree
}

// Release returns backend memory. The tree must not be used afterwards.
func (t *Tree) Release() {
	if t.tree != nil {
		t.tree.Release()
	}
}

// Parse parses src with a cooperative timeout and a post-parse node budget.
// Parsers are created per call, so Parse is safe for concurrent use.
func Parse(l Language, src []byte, timeout time.Duration, maxNodes int) (*Tree, error) {
	lang, err := grammar(l)
	if err != nil {
		return nil, err
	}
	p := ts.NewParser(lang)
	if timeout > 0 {
		p.SetTimeoutMicros(uint64(timeout / time.Microsecond))
	}
	tree, err := p.ParseStrict(src)
	out := &Tree{Lang: lang, Source: src, Status: StatusParsed, tree: tree}
	if err != nil {
		if tree == nil || !errors.Is(err, ts.ErrParseStoppedEarly) {
			return nil, err
		}
		out.Status = StatusLimit
		out.Reason = string(tree.ParseStopReason())
	}
	out.Root = tree.RootNode()
	out.Nodes = count(out.Root, maxNodes)
	if maxNodes > 0 && out.Nodes > maxNodes {
		out.Status = StatusLimit
		out.Reason = fmt.Sprintf("more than %d syntax nodes", maxNodes)
	} else if out.Status == StatusParsed && out.Root.HasErrorOrMissing() {
		out.Status = StatusPartial
	}
	return out, nil
}

// count counts nodes, stopping once the limit is exceeded.
func count(n *ts.Node, limit int) int {
	total := 1
	for i := 0; i < n.ChildCount(); i++ {
		total += count(n.Child(i), limit)
		if limit > 0 && total > limit {
			return total
		}
	}
	return total
}
