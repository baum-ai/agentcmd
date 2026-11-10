# Changelog

## 0.1.0 — first public release

This is the first public tag. The repository carried an unpublished
"1.0.0" before; it is now numbered 0.1.0, and the Go API may change between
minor versions until 1.0.0. The analyzer version is 1.1.0. The JSON schema
(`model.SchemaVersion`) is unchanged at 1.0.0: no field or action kind was
added or removed, so `docs/versioning.md` requires no schema bump. Analysis
IDs change, as they do whenever the analyzer version or a ruleset changes.

### Changes since the internal baseline

- Module path is now `github.com/baum-ai/agentcmd` (the repository moved
  organisations); the License holder is Baum AI Ltd.
- The flat-records projection package is now `projection/records` (it was
  named after a single consumer); the record shape is unchanged.
- The `go` directive is lowered to 1.26, the real minimum (`mvdan.cc/sh/v3`
  v3.14.1 requires it).
- Embedded Python and JavaScript analysis is opt-in: the frontends moved from
  `internal/languages` to the public package `languages`, selected with
  `Config.Frontend` (`languages.Frontend()`). The root package, rules, native
  bindings, adapters and streams no longer link gotreesitter. Without a
  Frontend, Python and JavaScript programs are reported as unsupported
  ("requires a language frontend") and the result is partial. The reference
  CLI opts in; its behaviour is unchanged. `Frontend` is opaque: only
  `languages.Frontend()` builds one. A build that excludes a grammar (the
  `grammar_subset` tags) now yields a diagnostic and a partial result instead
  of a panic.
- Codex `exec_command` honours `workdir` (binding `std/codex/exec_command`
  version 2): the command is analyzed in that directory. A relative one
  resolves against the invocation's directory and is unknown when that is
  unknown; a non-string or empty one makes it unknown.
- The Codex `shell` binding resolves a relative `workdir` the same way
  instead of reporting resources as resolved against an unknown directory.
- New native binding `std/codex/exec` for Codex code mode: a dependency-free
  lexical scan of the JavaScript program finds `tools.exec_command({cmd,
  workdir})` calls (also `tools.shell`, and `apply_patch`) and analyzes each
  as a nested shell invocation with its own directory. Calls inside blocks,
  after `&&`, `||`, `??` or `?`, in loops or in function bodies are marked
  conditional, repeated or latent. A computed call, a repeated key, a spread
  or computed key is `process.unresolved` and the result is partial; a
  dynamic `workdir` is an unknown directory. See `docs/ingestion.md`.
  `native.Result.Shells`, `native.Result.Parts` and the `Workdir`,
  `WorkdirUnknown`, `Span` and `Reach` fields of `native.ShellSource` carry
  this.
- Code-mode scanning follows JavaScript statement ends (`;`, closing
  brackets, automatic semicolon insertion on a line break), treats `do`,
  object-method and class-method bodies as repeated or latent, accepts
  `tools?.name(...)` and `tools.name?.(...)`, ignores a bare `shell`,
  `exec_command` or `apply_patch` the program declares itself, and reports any
  other reference to those tools (`const run = tools.exec_command`,
  `tools[name](...)`) as an unresolved part. Getters, methods and `__proto__`
  in the argument object make a call unresolved, and an unterminated string,
  template literal or comment is a `syntax_error` and makes the result
  partial. `std/codex/shell` is version 2 (relative `workdir` with an
  unknown base is unknown, an absolute one is cleaned).
- A binding that reports a `limit_reached` or `syntax_error` diagnostic now
  makes the analysis partial (ADR 0007).
- `git -C` composes several options in order and resolves the command's
  operands against the directory it names (rule `std/git` version 2).
- A native node's span now covers the whole input (it used to end at byte 1,
  and was invalid for an empty input).

### Initial feature set (internal, 2026-09-17)

The declared support matrix at the start of the repository history:

- Canonical model (`model`, schema 1.0.0): tagged inputs, execution graph,
  typed actions with opaque round-tripping, resources with namespaces,
  categorical evidence, observations, diagnostics, deterministic IDs.
- Shell analysis for POSIX sh and Bash: structure, scoped state, guards,
  redirections, here-documents, nested launchers.
- Command rule catalog (`docs/rules.md`), native bindings for common harness
  tools, patch parsing as data, MCP annotation hints.
- Embedded Python and JavaScript subsets (`docs/semantics.md`).
- Adapters for canonical, OpenAI Responses, Anthropic Messages and Gemini
  records; bounded stream assembly; observation association.
- Snapshot enrichment for script files and package.json scripts.
- Reference CLI `agentcmd`; OpenTelemetry and flat-records projections.
