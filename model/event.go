package model

// EventKind classifies stream output.
type EventKind string

const (
	// EventAnalysis carries a completed invocation and its analysis.
	EventAnalysis EventKind = "analysis"
	// EventObservation carries dynamic evidence about an invocation emitted
	// earlier (a tool result, exit status or lifecycle change).
	EventObservation EventKind = "observation"
	// EventOrphan carries a result whose call was never seen or was evicted.
	EventOrphan EventKind = "orphan_result"
	// EventDiagnostic reports a record that could not be processed.
	EventDiagnostic EventKind = "diagnostic"
)

// Event is one output of stream processing. Sequence is the input record
// number that produced it, so consumers can restore input order.
type Event struct {
	Kind        EventKind        `json:"kind"`
	Sequence    int              `json:"sequence"`
	Source      *SourceRef       `json:"source,omitempty"`
	Invocation  string           `json:"invocation,omitempty"`
	Analysis    *Analysis        `json:"analysis,omitempty"`
	Observation *Observation     `json:"observation,omitempty"`
	Lifecycle   []LifecycleEvent `json:"lifecycle,omitempty"`
	Diagnostic  *Diagnostic      `json:"diagnostic,omitempty"`
}
