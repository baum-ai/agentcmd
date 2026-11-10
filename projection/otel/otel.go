// Package otel projects an analysis onto OpenTelemetry-shaped data using the
// GenAI semantic conventions for tool execution. It depends on no OTel SDK:
// callers map Span values onto their tracer. The projection is deliberately
// thin: the invocation is a span; static actions are span events, never
// child spans with invented durations; observations become attributes.
package otel

import (
	"strings"

	"github.com/baum-ai/agentcmd/model"
)

// SemconvVersion pins the semantic-convention release the attribute names
// follow (gen_ai.* tool execution attributes).
const SemconvVersion = "1.37.0"

// Span is a provider-neutral span description.
type Span struct {
	Name       string
	Attributes map[string]any
	Events     []Event
}

// Event is a point-in-time record on a span. Static events carry no time.
type Event struct {
	Name       string
	Attributes map[string]any
}

// Project builds the execute_tool span for an analysis and its invocation.
// When inv is nil only analysis-derived attributes are set.
func Project(inv *model.Invocation, a *model.Analysis) Span {
	attrs := map[string]any{
		"gen_ai.operation.name":          "execute_tool",
		"agentcmd.analysis.id":           a.ID,
		"agentcmd.analysis.schema":       a.SchemaVersion,
		"agentcmd.analysis.partial":      a.Partial,
		"agentcmd.analysis.action_count": len(a.Actions),
		"agentcmd.semconv.version":       SemconvVersion,
	}
	name := "execute_tool"
	if inv != nil {
		attrs["gen_ai.tool.name"] = inv.Tool.Name
		if inv.Tool.Namespace != "" {
			attrs["agentcmd.tool.namespace"] = inv.Tool.Namespace
		}
		if inv.CallID != "" {
			attrs["gen_ai.tool.call.id"] = inv.CallID
		}
		if inv.Provider != "" {
			attrs["gen_ai.provider.name"] = inv.Provider
		}
		if inv.Session != "" {
			attrs["gen_ai.conversation.id"] = inv.Session
		}
		if inv.Harness != "" {
			attrs["gen_ai.agent.name"] = inv.Harness
		}
		name = "execute_tool " + inv.Tool.Name
	}
	span := Span{Name: name, Attributes: attrs}
	for _, act := range a.Actions {
		ev := Event{Name: "agentcmd.action", Attributes: map[string]any{
			"agentcmd.action.id":    act.ID,
			"agentcmd.action.kind":  string(act.Kind),
			"agentcmd.action.basis": string(act.Evidence.Basis),
			"agentcmd.action.node":  act.Node,
		}}
		if act.Evidence.Rule != "" {
			ev.Attributes["agentcmd.action.rule"] = act.Evidence.Rule + "@" + act.Evidence.RuleVersion
		}
		var resources []string
		for _, r := range act.Resources {
			resources = append(resources, resourceString(r))
		}
		if len(resources) > 0 {
			ev.Attributes["agentcmd.action.resources"] = resources
		}
		span.Events = append(span.Events, ev)
	}
	for _, o := range a.Observations {
		switch o.Kind {
		case model.ObsExitStatus:
			if o.ExitCode != nil {
				attrs["agentcmd.observed.exit_code"] = *o.ExitCode
			}
		case model.ObsToolResult:
			if o.Success != nil && !*o.Success {
				attrs["error.type"] = "tool_error"
			}
		}
		span.Events = append(span.Events, Event{Name: "agentcmd.observation", Attributes: map[string]any{
			"agentcmd.observation.id": o.ID, "agentcmd.observation.kind": string(o.Kind), "agentcmd.observation.basis": string(o.Basis), "agentcmd.observation.subject": o.Subject,
		}})
	}
	var unknown []string
	for _, d := range a.Diagnostics {
		unknown = append(unknown, d.Code)
	}
	if len(unknown) > 0 {
		attrs["agentcmd.diagnostics"] = unknown
	}
	return span
}

func resourceString(r model.Resource) string {
	var b strings.Builder
	b.WriteString(string(r.Namespace.Kind))
	if r.Namespace.ID != "" {
		b.WriteString(":" + r.Namespace.ID)
	}
	b.WriteString(" ")
	switch {
	case r.Resolved != "":
		b.WriteString(r.Resolved)
	case len(r.Alternatives) > 0:
		b.WriteString("{" + strings.Join(r.Alternatives, "|") + "}")
	default:
		b.WriteString(r.Expression)
	}
	b.WriteString(" [" + string(r.Knowledge) + "]")
	return b.String()
}
