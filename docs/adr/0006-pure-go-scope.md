# ADR 0006: Pure-Go production dependency graph, no execution of input

**Status:** Accepted (M0)

## Decision

Production packages build with `CGO_ENABLED=0` and depend on:

- `mvdan.cc/sh/v3/syntax` (BSD-3) for shell syntax.
- `github.com/odvcencio/gotreesitter` (MIT) and its `grammars` package for
  Python and JavaScript, behind `internal/languages`.

The analyzer never executes input, never runs the mvdan interpreter or
expansion helpers, never reads the host filesystem or environment, and makes
no network calls. Test-only reference implementations may use other runtimes
in isolated CI jobs.

Python and JavaScript support is conditional on the qualification recorded in
`docs/compatibility.md` and may be disabled per language via `Config`.

## Consequences

- Binary size: the default `grammars` build embeds all grammar blobs
  (~16 MiB); consumers that care use the upstream `grammar_subset` build tags.
- A parser that cannot honor cancellation is bounded by size limits and its
  own timeout; process-level isolation is the application's job.
