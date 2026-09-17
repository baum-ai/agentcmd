# ADR 0004: Identity and versioning

**Status:** Accepted (M0)

## Decision

- `Invocation.ID` is a caller-supplied local occurrence identity. Provider
  call IDs (`CallID`) are kept but scoped by `Session`; they are never assumed
  globally unique.
- `Analysis.ID` is a SHA-256 based digest of the invocation identity, the
  analyzer/schema/ruleset versions, the engine options fingerprint and the
  execution-context fingerprint. Same inputs produce the same ID; a rule
  upgrade produces a new ID.
- Node and action IDs are assigned in deterministic depth-first traversal
  order (`n1`, `n2`, ..., `a1`, `a2`, ...). They are stable within an analysis
  version and may change across rule upgrades.
- Content fingerprints are not event IDs. Two identical commands in two calls
  are two invocations.

## Consequences

- Cache keys: syntax caches key on (parser version, dialect, source bytes);
  semantic caches key on `Analysis.ID` inputs.
- Enriching context creates a new analysis with `Previous` pointing at the
  old ID.
