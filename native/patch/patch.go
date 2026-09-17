// Package patch parses patch text as data. It never applies a patch, and the
// code inside hunks is content, not something the patch executes.
package patch

import (
	"bufio"
	"errors"
	"fmt"
	"strings"

	"github.com/lastpersonlabs/toolparser/model"
)

// Format identifies a supported patch syntax.
type Format string

const (
	// FormatCodex is the `*** Begin Patch` format used by the OpenAI Codex
	// apply_patch tool.
	FormatCodex Format = "codex-apply-patch"
	// FormatUnified is a unified diff, including git's extended headers.
	FormatUnified Format = "unified-diff"
)

// Patch is the declared content of a patch.
type Patch struct {
	Format Format
	Files  []model.PatchFile
	// Binary lists files whose content is binary and therefore unsupported.
	Binary []string
}

// ErrUnknownFormat is returned when the text matches no supported format.
var ErrUnknownFormat = errors.New("patch: unrecognized format")

// Parse detects the format and extracts the declared file operations.
func Parse(text string) (Patch, error) {
	trim := strings.TrimLeft(text, " \t\r\n")
	switch {
	case strings.HasPrefix(trim, "*** Begin Patch"):
		return parseCodex(text)
	case strings.HasPrefix(trim, "diff --git"), strings.HasPrefix(trim, "--- "), strings.HasPrefix(trim, "Index: "), strings.Contains(text, "\n--- ") && strings.Contains(text, "\n+++ "):
		return parseUnified(text)
	}
	return Patch{}, ErrUnknownFormat
}

func parseCodex(text string) (Patch, error) {
	p := Patch{Format: FormatCodex}
	sc := bufio.NewScanner(strings.NewReader(text))
	sc.Buffer(make([]byte, 1<<20), 16<<20)
	var cur *model.PatchFile
	began, ended := false, false
	for line := 1; sc.Scan(); line++ {
		l := sc.Text()
		switch {
		case strings.HasPrefix(l, "*** Begin Patch"):
			if began {
				return p, fmt.Errorf("patch: line %d: nested Begin Patch", line)
			}
			began = true
		case l == "*** End Patch":
			ended = true
		case strings.HasPrefix(l, "*** Add File: "):
			p.Files = append(p.Files, model.PatchFile{Path: strings.TrimPrefix(l, "*** Add File: "), Operation: "add"})
			cur = &p.Files[len(p.Files)-1]
		case strings.HasPrefix(l, "*** Delete File: "):
			p.Files = append(p.Files, model.PatchFile{Path: strings.TrimPrefix(l, "*** Delete File: "), Operation: "delete"})
			cur = &p.Files[len(p.Files)-1]
		case strings.HasPrefix(l, "*** Update File: "):
			p.Files = append(p.Files, model.PatchFile{Path: strings.TrimPrefix(l, "*** Update File: "), Operation: "update"})
			cur = &p.Files[len(p.Files)-1]
		case strings.HasPrefix(l, "*** Move to: "):
			if cur == nil || cur.Operation != "update" {
				return p, fmt.Errorf("patch: line %d: Move to without Update File", line)
			}
			cur.Operation, cur.MoveTo = "move", strings.TrimPrefix(l, "*** Move to: ")
		case strings.HasPrefix(l, "@@"):
			if cur == nil {
				return p, fmt.Errorf("patch: line %d: hunk outside a file section", line)
			}
			cur.Hunks++
		case !began:
			return p, fmt.Errorf("patch: line %d: content before Begin Patch", line)
		case ended && strings.TrimSpace(l) != "":
			return p, fmt.Errorf("patch: line %d: content after End Patch", line)
		case cur == nil && strings.TrimSpace(l) != "":
			return p, fmt.Errorf("patch: line %d: content outside a file section", line)
		}
	}
	if err := sc.Err(); err != nil {
		return p, err
	}
	if !ended {
		return p, errors.New("patch: missing End Patch")
	}
	for i := range p.Files {
		if p.Files[i].Operation == "add" && p.Files[i].Hunks == 0 {
			p.Files[i].Hunks = 1 // an added file is one implicit hunk
		}
	}
	return p, nil
}

func parseUnified(text string) (Patch, error) {
	p := Patch{Format: FormatUnified}
	sc := bufio.NewScanner(strings.NewReader(text))
	sc.Buffer(make([]byte, 1<<20), 16<<20)
	var cur *model.PatchFile
	var oldPath, newPath string
	flush := func() {
		if oldPath == "" && newPath == "" {
			return
		}
		f := model.PatchFile{Operation: "update"}
		switch {
		case oldPath == "/dev/null":
			f.Path, f.Operation = newPath, "add"
		case newPath == "/dev/null":
			f.Path, f.Operation = oldPath, "delete"
		default:
			f.Path = newPath
			if cur != nil && cur.Operation == "move" {
				f.Operation, f.Path, f.MoveTo = "move", cur.Path, cur.MoveTo
			}
		}
		if cur != nil && cur.Path == f.Path && cur.Operation == f.Operation {
			return
		}
		p.Files = append(p.Files, f)
		cur = &p.Files[len(p.Files)-1]
		oldPath, newPath = "", ""
	}
	for line := 1; sc.Scan(); line++ {
		l := sc.Text()
		switch {
		case strings.HasPrefix(l, "diff --git "):
			flush()
			cur = nil
		case strings.HasPrefix(l, "rename from "):
			if cur == nil {
				p.Files = append(p.Files, model.PatchFile{Operation: "move"})
				cur = &p.Files[len(p.Files)-1]
			}
			cur.Path = strings.TrimPrefix(l, "rename from ")
		case strings.HasPrefix(l, "rename to "):
			if cur != nil {
				cur.MoveTo = strings.TrimPrefix(l, "rename to ")
			}
		case strings.HasPrefix(l, "deleted file mode"):
			if cur == nil {
				p.Files = append(p.Files, model.PatchFile{Operation: "delete"})
				cur = &p.Files[len(p.Files)-1]
			}
		case strings.HasPrefix(l, "new file mode"):
			if cur == nil {
				p.Files = append(p.Files, model.PatchFile{Operation: "add"})
				cur = &p.Files[len(p.Files)-1]
			}
		case strings.HasPrefix(l, "--- "):
			oldPath = stripPrefix(strings.TrimPrefix(l, "--- "))
		case strings.HasPrefix(l, "+++ "):
			newPath = stripPrefix(strings.TrimPrefix(l, "+++ "))
			if cur != nil && cur.Path == "" {
				switch cur.Operation {
				case "add":
					cur.Path = newPath
				case "delete":
					cur.Path = oldPath
				}
				oldPath, newPath = "", ""
			} else {
				flush()
			}
		case strings.HasPrefix(l, "@@"):
			if cur == nil {
				return p, fmt.Errorf("patch: line %d: hunk before file header", line)
			}
			cur.Hunks++
		case strings.HasPrefix(l, "Binary files "), strings.HasPrefix(l, "GIT binary patch"):
			name := "unknown"
			if cur != nil {
				name = cur.Path
			}
			p.Binary = append(p.Binary, name)
		}
	}
	if err := sc.Err(); err != nil {
		return p, err
	}
	flush()
	if len(p.Files) == 0 {
		return p, errors.New("patch: no file headers found")
	}
	for _, f := range p.Files {
		if f.Path == "" {
			return p, errors.New("patch: file section without a path")
		}
	}
	return p, nil
}

// stripPrefix removes git's a/ b/ prefixes and trailing timestamps.
func stripPrefix(s string) string {
	if i := strings.IndexByte(s, '\t'); i >= 0 {
		s = s[:i]
	}
	s = strings.TrimSpace(s)
	if s == "/dev/null" {
		return s
	}
	if strings.HasPrefix(s, "a/") || strings.HasPrefix(s, "b/") {
		s = s[2:]
	}
	return strings.Trim(s, `"`)
}
