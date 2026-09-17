# ADR 0005: Resources are expressions in a namespace, not filesystem identities

**Status:** Accepted (M0)

## Decision

A `Resource` keeps the original `Expression`, an optional lexical `Resolved`
form (joined with the abstract working directory, `.` and `..` collapsed
lexically only), a `Knowledge` state (`literal`, `derived`, `symbolic`,
`glob`, `redacted`, `unknown`), and the `Namespace` (`host`, `container`,
`remote`, `unknown`) it belongs to.

Lexical resolution never claims symlink-canonical identity. Globs and search
roots are reported as scope with `Knowledge: glob`; no matching file list is
invented. Non-UTF-8 expressions are carried as base64 in `ExpressionBytes`.

Remote/container wrappers (`ssh`, `docker exec`, `docker run`, `kubectl exec`)
switch the namespace of everything inside the payload; host paths and guest
paths cannot be compared by string equality.

## Consequences

- Two resources are the same only if namespace, snapshot, and resolved
  expression agree and both are `literal`/`derived`.
- Consumers that need physical identity must supply snapshot evidence.
