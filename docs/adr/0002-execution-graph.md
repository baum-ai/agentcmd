# ADR 0002: Execution representation is a tree of nodes; actions are children

**Status:** Accepted (M0)

## Decision

`Analysis.Nodes` is a flat list of `Node` values forming a tree via `Parent`.
Node kinds cover shell structure (sequence, and-list, or-list, pipeline,
subshell, group, if, loop, function, background, command, substitution,
redirect), embedded programs (`program`), native tool operations (`native`)
and opaque regions (`unknown`).

Each `Action` references the `Node` that produces it. The action list is a
convenience view; the node tree is the source of truth for ordering,
containment, guards and repetition.

Guards are explicit: `Guard{Node, On}` where `On` is `success` or `failure`
of the referenced node. Pipelines are containers with stream edges between
children, not sequences of completed operations.

## Consequences

- A file read inside `if`, a loop or an `&&` right-hand side inherits the
  guard/reachability of its node; it is never flattened into an unconditional
  effect.
- Roll-up: wrappers (`env`, `timeout`, `sudo`, `sh -c`) produce one command
  node with a `Wrappers` ancestry list, so an inner read is counted once.
- Static nodes may run zero, one or many times; observations attach to
  processes, not nodes.
