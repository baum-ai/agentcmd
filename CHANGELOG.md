# Changelog

## Unreleased

- Embedded Python and JavaScript analysis is opt-in: the frontends moved from
  `internal/languages` to the public package `languages`, selected with
  `Config.Frontend` (`languages.Frontend()`). The root package, rules, native
  bindings, adapters and streams no longer link gotreesitter (a shell-only
  consumer is about 2 MB larger than an empty program instead of tens of MB).
  Without a Frontend, Python and JavaScript programs are reported as
  unsupported. The reference CLI opts in; its behaviour is unchanged.
- Codex `exec_command` now honours `workdir`: the command is analyzed in that
  directory (a relative one resolves against the invocation's directory; a
  non-string one makes it unknown).
- New native binding `std/codex/exec` for Codex code mode: a dependency-free
  lexical scan of the JavaScript program finds `tools.exec_command({cmd,
  workdir})` calls (and `tools.shell`, static `apply_patch`) and analyzes each
  as a nested shell invocation with its own directory; dynamic commands are
  `process.unresolved` and dynamic directories unknown. See
  `docs/ingestion.md`. `native.Result.Shells` and the `Workdir`,
  `WorkdirUnknown` and `Span` fields of `native.ShellSource` carry this.
- A native node's span now covers the whole input (it used to end at byte 1);
  analysis IDs and golden files change because the binding set changed.

- Module path is now `github.com/baum-ai/agentcmd` (was the former organisation).
- The flat-records projection package is now `projection/records` (it was
  named after a single consumer); the record shape is unchanged.
- The `go` directive is lowered to 1.26, the real minimum (`mvdan.cc/sh/v3`
  v3.14.1 requires it).
- License holder is now Baum AI Ltd.

## 1.0.0 — 2026-09-17

First stable release of the declared support matrix.

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
