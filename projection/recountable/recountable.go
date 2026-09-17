// Package recountable projects an analysis into flat, indexable records for
// the Recountable integration: stable evidence IDs, namespaced resources,
// intents, unknown reasons and observation links. It performs no causal
// inference: matching sources to commits stays outside this library.
package recountable

import "github.com/lastpersonlabs/toolparser/model"

// ProjectionVersion changes when the record shape changes.
const ProjectionVersion = "1"

// Record is the indexable view of one analysis.
type Record struct {
	Projection   string          `json:"projection"`
	Invocation   string          `json:"invocation"`
	Analysis     string          `json:"analysis"`
	Previous     string          `json:"previous,omitempty"`
	Versions     model.Versions  `json:"versions"`
	Partial      bool            `json:"partial"`
	Actions      []ActionRecord  `json:"actions"`
	Intents      []IntentRecord  `json:"intents,omitempty"`
	Unknown      []UnknownRecord `json:"unknown,omitempty"`
	Observations []LinkRecord    `json:"observations,omitempty"`
	Sources      []SourceRecord  `json:"sources"`
}

// ActionRecord is one action with its resources flattened.
type ActionRecord struct {
	ID          string             `json:"id"`
	Kind        model.ActionKind   `json:"kind"`
	Basis       model.Basis        `json:"basis"`
	Rule        string             `json:"rule,omitempty"`
	Node        string             `json:"node"`
	Reach       model.Reachability `json:"reachability"`
	Guarded     bool               `json:"guarded"`
	Resources   []ResourceRecord   `json:"resources,omitempty"`
	Spans       []model.SourceSpan `json:"spans,omitempty"`
	Assumptions []string           `json:"assumptions,omitempty"`
}

// ResourceRecord is a namespaced resource identity.
type ResourceRecord struct {
	Role         string          `json:"role,omitempty"`
	Namespace    model.Namespace `json:"namespace"`
	Snapshot     string          `json:"snapshot,omitempty"`
	Path         string          `json:"path,omitempty"`
	Alternatives []string        `json:"alternatives,omitempty"`
	Expression   string          `json:"expression,omitempty"`
	Knowledge    model.Knowledge `json:"knowledge"`
}

// IntentRecord indexes development intents separately from effects.
type IntentRecord struct {
	Action   string           `json:"action"`
	Kind     model.ActionKind `json:"kind"`
	Tool     string           `json:"tool"`
	Target   string           `json:"target,omitempty"`
	Verified bool             `json:"verified"`
}

// UnknownRecord indexes why something could not be analyzed.
type UnknownRecord struct {
	Code    string `json:"code"`
	Node    string `json:"node,omitempty"`
	Message string `json:"message"`
}

// LinkRecord links an observation to the entity it covers.
type LinkRecord struct {
	ID       string                `json:"id"`
	Subject  string                `json:"subject"`
	Kind     model.ObservationKind `json:"kind"`
	Basis    model.Basis           `json:"basis"`
	Relation model.Relation        `json:"relation"`
}

// SourceRecord keeps source identity without text.
type SourceRecord struct {
	ID       string               `json:"id"`
	Kind     string               `json:"kind"`
	Length   int                  `json:"length"`
	From     string               `json:"from,omitempty"`
	Exact    bool                 `json:"exact,omitempty"`
	Origin   *model.ContentOrigin `json:"origin,omitempty"`
	Redacted bool                 `json:"redacted"`
}

// Project flattens an analysis. Source text is never included.
func Project(a *model.Analysis) Record {
	nodes := map[string]model.Node{}
	for _, n := range a.Nodes {
		nodes[n.ID] = n
	}
	rec := Record{Projection: ProjectionVersion, Invocation: a.Invocation, Analysis: a.ID, Previous: a.Previous, Versions: a.Versions, Partial: a.Partial, Actions: []ActionRecord{}, Sources: []SourceRecord{}}
	for _, act := range a.Actions {
		ar := ActionRecord{ID: act.ID, Kind: act.Kind, Basis: act.Evidence.Basis, Node: act.Node, Spans: act.Evidence.Spans, Assumptions: act.Evidence.Assumptions}
		if act.Evidence.Rule != "" {
			ar.Rule = act.Evidence.Rule + "@" + act.Evidence.RuleVersion
		}
		for n, ok := nodes[act.Node]; ok; n, ok = nodes[n.Parent] {
			if ar.Reach == "" {
				ar.Reach = n.Reach
			}
			if n.Guard != nil {
				ar.Guarded = true
			}
			if n.Parent == "" {
				break
			}
		}
		for _, r := range act.Resources {
			ar.Resources = append(ar.Resources, ResourceRecord{Role: r.Role, Namespace: r.Namespace, Snapshot: r.Snapshot, Path: r.Resolved, Alternatives: r.Alternatives, Expression: r.Expression, Knowledge: r.Knowledge})
		}
		rec.Actions = append(rec.Actions, ar)
		if act.Intent != nil {
			rec.Intents = append(rec.Intents, IntentRecord{Action: act.ID, Kind: act.Kind, Tool: act.Intent.Tool, Target: act.Intent.Target, Verified: act.Intent.Verified})
		}
	}
	for _, d := range a.Diagnostics {
		rec.Unknown = append(rec.Unknown, UnknownRecord{Code: d.Code, Node: d.Node, Message: d.Message})
	}
	for _, o := range a.Observations {
		rec.Observations = append(rec.Observations, LinkRecord{ID: o.ID, Subject: o.Subject, Kind: o.Kind, Basis: o.Basis, Relation: o.Relation})
	}
	for _, s := range a.Sources {
		sr := SourceRecord{ID: s.ID, Kind: s.Kind, Length: s.Length, Origin: s.Origin, Redacted: true}
		if s.Derived != nil {
			sr.From, sr.Exact = s.Derived.From, s.Derived.Exact
		}
		rec.Sources = append(rec.Sources, sr)
	}
	return rec
}
