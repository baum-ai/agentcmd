package model

import (
	"fmt"
	"strings"
)

// Validate checks referential integrity: unique IDs, existing parents and
// guards, actions attached to nodes, spans inside known sources, static
// bases on actions and dynamic bases on observations.
func (a *Analysis) Validate() error {
	if major(a.SchemaVersion) != major(SchemaVersion) {
		return fmt.Errorf("analysis: schema version %q is not compatible with %q", a.SchemaVersion, SchemaVersion)
	}
	if a.ID == "" || a.Invocation == "" {
		return fmt.Errorf("analysis: id and invocation are required")
	}
	sources := map[string]int{}
	for _, s := range a.Sources {
		if _, dup := sources[s.ID]; dup {
			return fmt.Errorf("analysis: duplicate source %s", s.ID)
		}
		sources[s.ID] = s.Length
	}
	for _, s := range a.Sources {
		if s.Derived != nil {
			if _, ok := sources[s.Derived.From]; !ok {
				return fmt.Errorf("source %s: derived from unknown source %s", s.ID, s.Derived.From)
			}
		}
	}
	checkSpan := func(owner string, sp *SourceSpan) error {
		if sp == nil {
			return nil
		}
		n, ok := sources[sp.Source]
		if !ok {
			return fmt.Errorf("%s: span in unknown source %s", owner, sp.Source)
		}
		if sp.Start < 0 || sp.End < sp.Start || sp.End > n {
			return fmt.Errorf("%s: span [%d,%d) outside source %s of length %d", owner, sp.Start, sp.End, sp.Source, n)
		}
		return nil
	}
	nodes := map[string]bool{}
	for _, n := range a.Nodes {
		if n.ID == "" || nodes[n.ID] {
			return fmt.Errorf("node %q: missing or duplicate id", n.ID)
		}
		nodes[n.ID] = true
	}
	for _, n := range a.Nodes {
		if n.Parent != "" && !nodes[n.Parent] {
			return fmt.Errorf("node %s: unknown parent %s", n.ID, n.Parent)
		}
		if n.Guard != nil && !nodes[n.Guard.Node] {
			return fmt.Errorf("node %s: guard references unknown node %s", n.ID, n.Guard.Node)
		}
		if err := checkSpan("node "+n.ID, n.Span); err != nil {
			return err
		}
	}
	ids := map[string]bool{}
	for i := range a.Actions {
		act := &a.Actions[i]
		if err := act.Validate(); err != nil {
			return err
		}
		if ids[act.ID] {
			return fmt.Errorf("action %s: duplicate id", act.ID)
		}
		ids[act.ID] = true
		if !nodes[act.Node] {
			return fmt.Errorf("action %s: unknown node %s", act.ID, act.Node)
		}
		for _, sp := range act.Evidence.Spans {
			if err := checkSpan("action "+act.ID, &sp); err != nil {
				return err
			}
		}
	}
	for _, o := range a.Observations {
		if o.ID == "" || o.Subject == "" {
			return fmt.Errorf("observation %q: id and subject are required", o.ID)
		}
		if o.Basis.Static() || o.Basis == "" {
			return fmt.Errorf("observation %s: basis %q is not dynamic evidence", o.ID, o.Basis)
		}
		if o.Subject != a.Invocation && !nodes[o.Subject] && !ids[o.Subject] {
			return fmt.Errorf("observation %s: unknown subject %s", o.ID, o.Subject)
		}
	}
	for _, d := range a.Diagnostics {
		if d.Code == "" || d.Message == "" {
			return fmt.Errorf("diagnostic: code and message are required")
		}
		if d.Node != "" && !nodes[d.Node] {
			return fmt.Errorf("diagnostic %s: unknown node %s", d.Code, d.Node)
		}
	}
	return nil
}

func major(v string) string {
	m, _, _ := strings.Cut(v, ".")
	return m
}
