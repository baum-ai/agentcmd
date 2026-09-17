# ADR 0001: Separate requests, inferences, reports, and observations

**Status:** Accepted (M0)

## Decision

Every semantic statement carries a `Basis` from a closed set:

| Basis | Meaning |
|---|---|
| `tool_contract` | The tool's schema says this is what the tool does with the given input. |
| `syntax` | Follows directly from parsed structure (a redirection, an argv literal). |
| `bounded_value` | Follows from bounded value inference (concatenation, known variable). |
| `heuristic_intent` | Command-family intent label (this looks like a test run). |
| `tool_report` | The tool or harness reported an outcome. |
| `runtime_observation` | An instrumented runtime recorded the event. |

Static bases (`tool_contract`, `syntax`, `bounded_value`, `heuristic_intent`)
live on `Action.Evidence`. Dynamic bases (`tool_report`,
`runtime_observation`) live on `Observation` records that point at the entity
they cover. An `Action` is never created from an observation and an
observation never rewrites an action's basis.

## Consequences

- Renderers must qualify each statement ("requested", "inferred", "reported",
  "observed") and may not merge them into one verb.
- A missing observation is silence, not a failed or absent effect.
- Consumers that need probabilities calibrate them downstream; the model does
  not carry a numeric confidence.
