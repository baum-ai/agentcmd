// Package source owns source buffers, their derivations and span mapping.
// See ADR 0003.
package source

import (
	"fmt"

	"github.com/lastpersonlabs/toolparser/model"
)

// Store holds the sources of one analysis. s0 is the invocation input.
type Store struct {
	sources []model.Source
	text    [][]byte
	derived int
}

// New creates a store whose root source is text.
func New(kind string, text []byte) *Store {
	s := &Store{}
	s.add(kind, text, nil)
	return s
}

func (s *Store) add(kind string, text []byte, d *model.Derivation) string {
	id := fmt.Sprintf("s%d", len(s.sources))
	s.sources = append(s.sources, model.Source{ID: id, Kind: kind, Length: len(text), Derived: d})
	s.text = append(s.text, text)
	if d != nil {
		s.derived += len(text)
	}
	return id
}

// Derive adds a source derived from parent. Segments map derived byte ranges
// to parent ranges; exact says whether the map is byte-precise.
func (s *Store) Derive(kind string, text []byte, parent string, exact bool, segs []model.Segment) string {
	return s.add(kind, text, &model.Derivation{From: parent, Exact: exact, Segments: segs})
}

// DeriveWhole adds a derived source that maps entirely onto [start,end) of
// the parent, byte-precise only when the lengths agree.
func (s *Store) DeriveWhole(kind string, text []byte, parent string, start, end int) string {
	exact := end-start == len(text)
	return s.Derive(kind, text, parent, exact, []model.Segment{{Start: 0, End: len(text), FromStart: start, FromEnd: end}})
}

// Text returns the bytes of a source.
func (s *Store) Text(id string) []byte {
	i, ok := s.index(id)
	if !ok {
		return nil
	}
	return s.text[i]
}

// DerivedBytes is the total size of derived sources, for budgeting.
func (s *Store) DerivedBytes() int { return s.derived }

// Len returns the number of sources.
func (s *Store) Len() int { return len(s.sources) }

func (s *Store) index(id string) (int, bool) {
	var i int
	if _, err := fmt.Sscanf(id, "s%d", &i); err != nil || i < 0 || i >= len(s.sources) {
		return 0, false
	}
	return i, true
}

// ToRoot maps a span to the root source. exact is false when any hop was
// approximate or the span straddles segments.
func (s *Store) ToRoot(sp model.SourceSpan) (root model.SourceSpan, exact bool) {
	exact = true
	for {
		i, ok := s.index(sp.Source)
		if !ok {
			return sp, false
		}
		d := s.sources[i].Derived
		if d == nil {
			return sp, exact
		}
		mapped, hopExact := mapSpan(sp, d)
		exact = exact && hopExact
		sp = mapped
	}
}

func mapSpan(sp model.SourceSpan, d *model.Derivation) (model.SourceSpan, bool) {
	out := model.SourceSpan{Source: d.From, Start: -1, End: -1}
	exact := d.Exact
	covering := 0
	for _, seg := range d.Segments {
		if seg.End <= sp.Start || seg.Start >= sp.End {
			if !(seg.Start == seg.End && seg.Start == sp.Start && sp.Start == sp.End) {
				continue
			}
		}
		covering++
		from, to := seg.FromStart, seg.FromEnd
		if seg.End-seg.Start == seg.FromEnd-seg.FromStart {
			// Linear segment: map precisely within it.
			from = seg.FromStart + max(sp.Start, seg.Start) - seg.Start
			to = seg.FromStart + min(sp.End, seg.End) - seg.Start
		} else {
			exact = false
		}
		if out.Start < 0 || from < out.Start {
			out.Start = from
		}
		if to > out.End {
			out.End = to
		}
	}
	if covering == 0 {
		// No segment: fall back to the whole parent range of the derivation.
		for _, seg := range d.Segments {
			if out.Start < 0 || seg.FromStart < out.Start {
				out.Start = seg.FromStart
			}
			if seg.FromEnd > out.End {
				out.End = seg.FromEnd
			}
		}
		if out.Start < 0 {
			out.Start, out.End = 0, 0
		}
		return out, false
	}
	return out, exact && covering == 1
}

// Export returns the sources, retaining text only up to retain bytes.
func (s *Store) Export(retain int) []model.Source {
	out := make([]model.Source, len(s.sources))
	for i, src := range s.sources {
		if len(s.text[i]) <= retain {
			src.Text = string(s.text[i])
		}
		out[i] = src
	}
	return out
}
