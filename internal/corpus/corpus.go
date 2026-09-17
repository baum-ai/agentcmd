// Package corpus loads the fixture corpus under testdata/corpus.
//
// Records carry provenance (origin, redaction, redistribution), stratification
// tags, a canonical invocation and separately reviewed labels. The held-out
// split must not be used by rule tests.
package corpus

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
)

// Record is one corpus fixture.
type Record struct {
	ID             string          `json:"id"`
	Origin         string          `json:"origin"`
	Redaction      string          `json:"redaction"`
	Redistribution string          `json:"redistribution"`
	Tags           []string        `json:"tags"`
	Invocation     json.RawMessage `json:"invocation"`
	Labels         Labels          `json:"labels"`
}

// Labels are reviewed expectations, kept separate per evidence class.
type Labels struct {
	Requests     []string `json:"requests"`
	Inferences   []string `json:"inferences"`
	Observations []string `json:"observations"`
	Unknowns     []string `json:"unknowns"`
}

// Dir returns the corpus directory relative to this source file.
func Dir() string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(file), "..", "..", "testdata", "corpus")
}

// Load reads one JSONL split ("commands" or "heldout").
func Load(split string) ([]Record, error) {
	f, err := os.Open(filepath.Join(Dir(), split+".jsonl"))
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var recs []Record
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for line := 1; sc.Scan(); line++ {
		var r Record
		if err := json.Unmarshal(sc.Bytes(), &r); err != nil {
			return nil, fmt.Errorf("%s.jsonl:%d: %w", split, line, err)
		}
		if r.ID == "" || r.Origin == "" || r.Redaction == "" || r.Redistribution == "" {
			return nil, fmt.Errorf("%s.jsonl:%d: missing provenance fields", split, line)
		}
		recs = append(recs, r)
	}
	return recs, sc.Err()
}

// Distribution counts records per tag prefix ("cmd", "struct", ...).
func Distribution(recs []Record) map[string]map[string]int {
	out := map[string]map[string]int{}
	for _, r := range recs {
		for _, t := range r.Tags {
			group, value, ok := strings.Cut(t, ":")
			if !ok {
				continue
			}
			if out[group] == nil {
				out[group] = map[string]int{}
			}
			out[group][value]++
		}
	}
	return out
}

// Report renders the distribution as Markdown.
func Report(name string, recs []Record) string {
	var b strings.Builder
	fmt.Fprintf(&b, "## %s (%d records)\n\n", name, len(recs))
	dist := Distribution(recs)
	groups := make([]string, 0, len(dist))
	for g := range dist {
		groups = append(groups, g)
	}
	sort.Strings(groups)
	for _, g := range groups {
		fmt.Fprintf(&b, "### %s\n\n| value | count |\n|---|---:|\n", g)
		vals := make([]string, 0, len(dist[g]))
		for v := range dist[g] {
			vals = append(vals, v)
		}
		sort.Strings(vals)
		for _, v := range vals {
			fmt.Fprintf(&b, "| %s | %d |\n", v, dist[g][v])
		}
		b.WriteString("\n")
	}
	return b.String()
}
