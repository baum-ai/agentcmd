# ADR 0003: Source buffers, half-open byte spans, and derivation maps

**Status:** Accepted (M0)

## Decision

Every text the analyzer parses is a `Source` with an ID (`s0`, `s1`, ...).
`s0` is the invocation input. Derived sources (a here-document body, a
`python -c` argument, a JSON string field) record a `Derivation` with the
parent source ID, `Exact bool`, and a list of segment maps from derived byte
ranges to parent byte ranges.

Spans are half-open `[Start, End)` byte offsets into one source. Mapping a
derived span to the root walks derivations; when any hop is approximate the
result is flagged `Exact: false`.

Third-party AST types never appear in the public model. The retained text of
a source is included only when it is small (default 4 KiB); otherwise only
its length and derivation are kept and the caller owns the bytes.

## Consequences

- Every action and diagnostic can be pointed at original bytes.
- Quoted/unquoted here-documents, `<<-` tab stripping and shell escapes are
  recorded as approximate maps instead of pretending the body is verbatim.
- Results never retain the transcript.
