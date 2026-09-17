# ADR 0007: Unknown is a first-class value

**Status:** Accepted (M0)

## Decision

- `Fact` values in `ExecutionContext` distinguish `Known: false` from an
  empty value. A missing working directory is unknown, not `""`.
- Abstract `Value`s carry `Knowledge`; unset variables are `unset`, unknown
  variables are `symbolic`.
- Every region of input the analyzer cannot model produces a `Node` of kind
  `unknown` (or a `Diagnostic` when no node applies) with a reason code:
  `syntax_error`, `unsupported`, `limit_reached`, `unresolved_binding`,
  `missing_snapshot`, `truncated`, `budget_exhausted`.
- Limits never silently shrink output: hitting one yields `Partial: true` plus
  a diagnostic.
- Unknown native tools produce a `native` node with no actions and a
  diagnostic; an empty action list is not a safety claim.

## Consequences

Renderers show unknown regions next to inferred actions so a reader can tell
"nothing was found" from "nothing could be analyzed".
