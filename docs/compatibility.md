# Compatibility and limits

Measured on 17 September 2026, Linux amd64, Go 1.27.1, `CGO_ENABLED=0`.
Re-run `go test ./internal/...` to reproduce the qualification checks; the
benchmarks in `internal/languages/treesitter` and `internal/shell` report the
numbers below.

## Dependency qualification (AC-003)

| Dependency | Version | License | CGo | Verdict |
|---|---|---|---|---|
| `mvdan.cc/sh/v3/syntax` | v3.14.1 | BSD-3-Clause | none | accepted for POSIX/Bash; Zsh/mksh parse only |
| `github.com/odvcencio/gotreesitter` (+ `grammars`) | v0.52.0 | MIT | none | accepted for Python and JavaScript |

### gotreesitter findings

| Check | Result |
|---|---|
| Parse correctness | Python and JavaScript snippets produce upstream tree-sitter node types and field names. |
| Source positions | Byte offsets are exact, including multi-byte UTF-8 (`héllo — ✓` mapped correctly). |
| Error / partial trees | Syntax errors return a complete tree with `ERROR`/`MISSING` nodes; `ParseStrict` returns `err == nil` for them and `root.HasError() == true`. Only early stops (timeout, cancellation, safety limit) return `ErrParseStoppedEarly`. |
| Cancellation / limits | `SetTimeoutMicros` stops within ~1 ms of the budget on a 600 KiB input; `SetCancellationFlag` available. Node count is not enforceable inside the backend, so node limits are checked after parsing. |
| Throughput | 600 KiB / 20k-line Python module parsed in ~1.0 s (~0.6 MiB/s); 100-byte snippets in 0.2–3 ms cold. Deeply nested `((((...))))` × 5000 parsed in 68 ms. |
| Memory / binary | Default build embeds all 206 grammar blobs (35 MiB binary). With `-tags 'grammar_subset grammar_subset_python grammar_subset_javascript'` the binary is 15 MiB. |
| Pinning | `go.sum` pins v0.52.0; grammar blobs are part of the module. |

Consequence: the 1 MiB per-source limit bounds Python/JavaScript parsing at
roughly two seconds of cooperative work; the parser timeout is set from the
remaining context deadline.

### mvdan/sh findings

`syntax.Parser` is pure Go and returns `syntax.ParseError` (value type) with
a byte position for invalid input; `Incomplete` marks truncated input, and the
reported position is then the start of the unclosed construct, not the end of
input. Variant violations (Bash arrays under POSIX) come back as
`syntax.LangError`. The lexer rejects non-UTF-8 bytes as a syntax error at
the offending offset, so byte-oriented shell sources must be treated as
invalid input rather than parsed leniently. Expression statements in the
tree-sitter Python grammar are collapsed by the runtime: a top-level call
appears directly under `module`. Here-document bodies keep their quoting
information (`Redirect.Op` is `Hdoc`/`DashHdoc`; the delimiter word's quoting
is visible on the `Word`). The interpreter and expansion packages are not
imported.

## Semantic coverage

| Area | Certified subset | Not covered |
|---|---|---|
| Shell dialects | POSIX sh, Bash | Zsh, mksh, fish (parse or refuse; semantics unsupported) |
| Shell constructs | simple commands, assignments, lists, pipelines, subshells, groups, if/case, while/until/for, functions, background, redirections, here-docs, command substitution | arithmetic evaluation, arrays beyond literal indexing, traps, `eval`, aliases, coprocesses |
| Python | pathlib/os/shutil/open/subprocess/glob subset in `docs/semantics.md` | classes, dynamic attribute access, other libraries |
| JavaScript | fs/fs.promises/child_process/path subset in `docs/semantics.md` | TypeScript-only syntax, non-builtin modules, computed member access |

## Limits (defaults in `toolparser.DefaultLimits`)

| Limit | Value | Kind |
|---|---|---|
| Input frame | 8 MiB | hard: checked before decoding |
| Individual executable source | 1 MiB | hard: checked before parsing |
| Total derived source per invocation | 4 MiB | hard |
| Nested language/script depth | 8 | hard |
| Syntax nodes per parsed source | 100,000 | cooperative: checked after parsing for tree-sitter, during lowering for shell |
| Emitted actions per invocation | 2,000 | hard |
| Alternatives at an abstract-value join | 8 | hard; widens to symbolic |
| Buffered stream input across active calls | 64 MiB | hard |
| Concurrent incomplete calls | 256 | hard |

"Hard" means the limit is enforced before the work is done. "Cooperative"
means the backend may exceed it briefly; the result is still rejected.

## Baseline benchmarks (AC-004)

Single worker, `CGO_ENABLED=0`, AMD64 desktop, Go 1.27.1. These are the
regression baseline for the parsing paths; end-to-end numbers are added per
milestone in `docs/benchmarks.md`.

| Path | Input | Result |
|---|---|---|
| Shell parse (`internal/shell`) | 1 KiB Bash, 22 lines | 21 µs/op, ~49 MiB/s, ~47k parses/s |
| Python parse (`internal/languages/treesitter`) | 99-byte snippet | 272 µs/op |
| JavaScript parse | 58-byte snippet | 166 µs/op |
| Python parse | 600 KiB module | ~1.0 s |

The per-snippet tree-sitter cost is dominated by parser setup rather than
input size, so the provisional target of ~10,000 ordinary 1 KiB shell calls
per second per worker is credible for shell, and embedded-language analysis is
roughly 10× more expensive per snippet. Regression budget: fail CI if either
parse benchmark regresses by more than 50% against these numbers.
