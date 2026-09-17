# Changelog

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
- Reference CLI `agentcmd`; OpenTelemetry and Recountable projections.
