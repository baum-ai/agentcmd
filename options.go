package toolparser

import "github.com/lastpersonlabs/toolparser/internal/analysis"

// Limits bound the work of one analysis. See docs/compatibility.md for the
// meaning of each limit and whether it is hard or cooperative.
type Limits = analysis.Limits

// DefaultLimits are the documented starting values.
func DefaultLimits() Limits {
	return Limits{
		InputFrameBytes:   8 << 20,
		SourceBytes:       1 << 20,
		DerivedBytes:      4 << 20,
		NestingDepth:      8,
		SyntaxNodes:       100_000,
		Actions:           2000,
		JoinAlternatives:  8,
		StreamBufferBytes: 64 << 20,
		StreamActiveCalls: 256,
	}
}

// Languages selects which embedded languages receive semantic analysis.
// Disabled languages are extracted and reported as unsupported programs.
type Languages struct {
	Python     bool `json:"python"`
	JavaScript bool `json:"javascript"`
}

// fillLimits replaces zero limits with defaults.
func fillLimits(l Limits) Limits {
	d := DefaultLimits()
	pick := func(v, def int) int {
		if v <= 0 {
			return def
		}
		return v
	}
	return Limits{
		InputFrameBytes:   pick(l.InputFrameBytes, d.InputFrameBytes),
		SourceBytes:       pick(l.SourceBytes, d.SourceBytes),
		DerivedBytes:      pick(l.DerivedBytes, d.DerivedBytes),
		NestingDepth:      pick(l.NestingDepth, d.NestingDepth),
		SyntaxNodes:       pick(l.SyntaxNodes, d.SyntaxNodes),
		Actions:           pick(l.Actions, d.Actions),
		JoinAlternatives:  pick(l.JoinAlternatives, d.JoinAlternatives),
		StreamBufferBytes: pick(l.StreamBufferBytes, d.StreamBufferBytes),
		StreamActiveCalls: pick(l.StreamActiveCalls, d.StreamActiveCalls),
	}
}
