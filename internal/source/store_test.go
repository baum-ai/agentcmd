package source

import (
	"testing"

	"github.com/lastpersonlabs/toolparser/model"
)

func TestNestedDerivationMapsToRoot(t *testing.T) {
	// Root: JSON field text containing a shell command with a heredoc.
	root := []byte(`{"command":"python - <<'PY'\nopen('x')\nPY"}`)
	s := New("json_field", root)
	shell := []byte("python - <<'PY'\nopen('x')\nPY")
	// JSON unescaping makes the map approximate: \n is two bytes in JSON.
	sh := s.DeriveWhole("shell", shell, "s0", 12, len(root)-2)
	body := []byte("open('x')\n")
	start := 16
	py := s.Derive("python", body, sh, true, []model.Segment{{Start: 0, End: len(body), FromStart: start, FromEnd: start + len(body)}})

	got, exact := s.ToRoot(model.SourceSpan{Source: py, Start: 0, End: 4})
	if got.Source != "s0" || exact {
		t.Fatalf("got %+v exact=%v", got, exact)
	}
	if got.Start < 12 || got.End > len(root)-2 {
		t.Fatalf("mapped span %+v escapes the JSON field", got)
	}
	// One exact hop stays exact and precise.
	mid, exact := mapSpan(model.SourceSpan{Source: py, Start: 0, End: 4}, &model.Derivation{From: sh, Exact: true, Segments: []model.Segment{{Start: 0, End: len(body), FromStart: start, FromEnd: start + len(body)}}})
	if !exact || mid.Start != start || mid.End != start+4 {
		t.Fatalf("exact hop: %+v %v", mid, exact)
	}
	if s.DerivedBytes() != len(shell)+len(body) {
		t.Fatalf("derived bytes %d", s.DerivedBytes())
	}
	exp := s.Export(16)
	if exp[0].Text != "" || exp[2].Text != string(body) {
		t.Fatalf("retention: %+v", exp)
	}
}

func TestSpanAcrossSegmentsIsApproximate(t *testing.T) {
	s := New("shell", []byte(`echo "a"'b'`))
	id := s.Derive("word", []byte("ab"), "s0", true, []model.Segment{{Start: 0, End: 1, FromStart: 6, FromEnd: 7}, {Start: 1, End: 2, FromStart: 9, FromEnd: 10}})
	got, exact := s.ToRoot(model.SourceSpan{Source: id, Start: 0, End: 2})
	if exact || got.Start != 6 || got.End != 10 {
		t.Fatalf("%+v %v", got, exact)
	}
	if s.Text("s9") != nil || s.Text("zz") != nil {
		t.Fatal("unknown ids must return nil")
	}
}
