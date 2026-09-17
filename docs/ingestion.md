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

Records outside a schema are reported as `unsupported` diagnostics; malformed
JSON as `invalid_record`. Nothing recognized disappears silently: a call with
arguments that are not valid JSON keeps them as text input, which the binding
then refuses explicitly.

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
