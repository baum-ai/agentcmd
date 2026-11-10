# Session ingestion: adapters, streaming and result association

## Adapters (`adapters/`)

An adapter decodes one transport record into calls, argument fragments,
results and lifecycle updates. It never interprets what a tool does; tool
meaning comes from `native` bindings selected by namespace.

| Adapter | Pinned schema | Records recognized |
|---|---|---|
| `canonical` | `model.SchemaVersion` | a `model.Invocation` object; `{"observation":{...},"session","call_id"}`; `{"lifecycle":{...},"session","call_id"}` |
| `openai` | Responses API, 2025-03 | `function_call`, `custom_tool_call`, `function_call_output`, `custom_tool_call_output` items; whole response objects; streaming `response.output_item.added/done`, `response.function_call_arguments.delta/done`, `response.custom_tool_call_input.delta/done` |
| `anthropic` | Messages API 2023-06-01 | `tool_use` and `tool_result` blocks, bare or inside a message; streaming `content_block_start/delta/stop` with `input_json_delta` |
| `gemini` | v1beta 2025 | `functionCall` and `functionResponse` parts, bare, in `content.parts` or in `candidates[].content.parts` |
| `claude-code` | CLI session log | Claude Code's own `~/.claude/projects/*/*.jsonl` transcript: `tool_use`/`tool_result` blocks nested under a line's `"message"`, decoded via `anthropic` once the CLI envelope (`type`, `uuid`, `sessionId`, `cwd`, …) is stripped |
| `codex` | CLI session log | Codex CLI's own `~/.codex/sessions/**/rollout-*.jsonl` log: `function_call`/`custom_tool_call` items and their outputs nested under a `"response_item"` line's `"payload"`, decoded via `openai` once the envelope (`session_meta`, `event_msg`, `token_usage_record`, `turn_context`, …) is stripped |

Records outside a schema are reported as `unsupported` diagnostics; malformed
JSON as `invalid_record`. Nothing recognized disappears silently: a call with
arguments that are not valid JSON keeps them as text input, which the binding
then refuses explicitly. `claude-code` and `codex` are the exception: their
CLI-only bookkeeping lines (Claude Code's `mode`, `permission-mode`,
`attachment`, `file-history-snapshot`; Codex's `session_meta`, `event_msg`,
`token_usage_record`, `turn_context`) and plain conversation turns (text-only
messages, `reasoning`) are expected and skipped rather than diagnosed, since
most of a session's lines are not tool traffic. A payload or message that
looks like it should carry a call but does not decode still reports
`unsupported`, so an unimplemented item kind (Codex's `local_shell_call`, for
example) is never silently dropped.

## Codex shell tools (`native/standard`)

The `codex` adapter yields these calls; with `-tool-namespace harness` the
standard bindings interpret them:

| Tool | Input | Analysis |
|---|---|---|
| `shell` | `{command: [argv], workdir?}` | the argv, run in `workdir` |
| `exec_command` | `{cmd, workdir?, ...}` | `cmd` as shell text, run in `workdir`; other fields are ignored |
| `exec` (code mode) | a JavaScript program: free text, a JSON string, or `{input}` | see below |

For `exec_command`, a non-empty string `workdir` is the working directory of
the command. A relative `workdir` resolves against the invocation's working
directory and is unknown when that is unknown. A `workdir` that is present
but not a string makes the directory unknown (resources then keep their
relative expressions and are not resolved). Without `workdir` the invocation's
directory applies.

### Code mode (`exec`)

Code mode wraps tool calls in a JavaScript program, for example
`text(await tools.exec_command({cmd: "git add a", workdir: "/wt"}))`. The
binding never runs the program and does not use a JavaScript parser: a small
lexical scanner (strings, template literals, comments and regular
expressions are tokenized, so text inside them is not code) finds the calls.
Each recognized call is analyzed as a nested shell invocation with its own
working directory and shell state, so a `cd` in one call does not reach the
next.

| Recognized | Result |
|---|---|
| `tools.exec_command({...})` or `exec_command({...})` whose first argument is an object literal with a static `cmd` (string, or template literal without substitutions; JS escapes are decoded; quoted or bare keys) | the shell analysis of `cmd` in `workdir` |
| `tools.shell({command: [...], workdir})` with a static string array (`bash -lc script` runs the script; other argv are single-quoted words) | as for `shell` |
| `tools.apply_patch("...")` with a static string | the patch binding |
| `cmd` is dynamic (identifier, shorthand, concatenation, template with `${}`), or the object has a spread or computed key | a `process.unresolved` action; nothing is guessed |
| `workdir` is present but dynamic (shorthand, identifier, template with `${}`, concatenation) | the command is analyzed with an unknown working directory |
| objects not passed directly as the first argument (`const steps = [{cmd: ...}]`, `{opts: {cmd: ...}}`, `exec_command(variable)`), `write_stdin`, `wait`, MCP calls, other JavaScript | ignored |

A derived `command` field stored next to `input` is ignored. A program whose
brackets do not pair up gets a `syntax_error` warning (the calls found before
the damage are still analyzed); a program with no recognized call gets an
`unsupported` info diagnostic; more than 256 commands in one program are
capped with a `limit_reached` warning. The scan is bounded to a step budget
linear in the program size. Evidence spans of the nested shell text point at
the command's string literal in the program when the input is text, and at
the whole input when it is JSON (the decoded program does not map byte for
byte).

Provider tool calls carry the provider as namespace (`openai`, `anthropic`,
`gemini`). Bindings are registered per harness namespace, so the caller states
which harness produced the records with `ToolNamespace` (CLI:
`-tool-namespace harness`). Without it, calls stay opaque. A model name never
selects a schema.

Gemini calls may have no identifier. They get a positional key
`<name>#<n>` per session, the matching response uses the same rule, and the
observation says "associated positionally by name and order". This is the
only place a name participates in association, and it is stated.

## Streaming (`stream/`)

`stream.Processor.Process` reads newline-delimited records sequentially and
emits `model.Event`s in input order (`Sequence` is the record number):

- `analysis` when a call completes (snapshot, or final fragment);
- `observation` for results and lifecycle updates of a known call;
- `orphan_result` for results whose call was never seen or was evicted;
- `diagnostic` for skipped, malformed, unrecognized or incomplete records.

Bounds: records over `FrameBytes` (8 MiB) are skipped without buffering;
fragments across active calls are capped by `BufferBytes` (64 MiB) and
`ActiveCalls` (256), evicting the oldest incomplete call with a
`budget_exhausted` diagnostic; completed calls stay associable within a
`Window` (4096) of completions. Incomplete calls at EOF are flushed as
`truncated` diagnostics and are never analyzed as complete. Results that
arrive before their call completes are held and emitted after the analysis.

Identity: an invocation ID is `<session>/<call>#<sequence>`, so two calls
with the same provider id in different sessions, or a reused id in one
session, are distinct. A completion delivered twice with identical arguments
(OpenAI `arguments.done` followed by `output_item.done`) is one invocation; a
conflicting second snapshot is diagnosed and analyzed as a new occurrence.

Backpressure is the synchronous `emit` callback: its error aborts processing.
Processing is single-worker, so output is deterministic by construction.

## Observation association (`observe/`)

`observe.Attach` adds dynamic evidence to an analysis and picks the entity it
covers: exit statuses and tool results cover the invocation; a file change
confirms filesystem actions on the same resolved path in the same namespace
or, if none matches, informs the invocation; a test or patch result covers
the single matching action if there is exactly one. Observations never
change an action's basis, and a missing observation proves nothing.
