# agentcmd

A pure-Go library and reference CLI that turns coding-agent tool invocations
into a deterministic, evidence-backed account of the operations they request,
the operations their code may perform, and the outcomes actually reported or
observed.

It can analyze a single shell command, a stream of provider tool-call records
(OpenAI, Anthropic, Gemini), or a coding agent's own on-disk session
transcript (Claude Code, Codex CLI) directly — no export step needed.

The module path is `github.com/baum-ai/agentcmd`; the reference CLI is
`cmd/agentcmd`.

The library never executes input, never reads the host filesystem or
environment, and builds with `CGO_ENABLED=0`.

## Getting started

```sh
go build -o agentcmd ./cmd/agentcmd
```

### Parse a coding agent's own session logs

`records` reads a coding agent's own on-disk session log directly — point it
at the file, no export step needed.

```sh
# Claude Code: ~/.claude/projects/<slug>/<session-id>.jsonl
agentcmd records -adapter claude-code -tool-namespace harness -format jsonl \
  ~/.claude/projects/<slug>/<session-id>.jsonl

# Codex CLI: ~/.codex/sessions/<year>/<month>/<day>/rollout-*.jsonl
agentcmd records -adapter codex -tool-namespace harness -format jsonl \
  ~/.codex/sessions/<year>/<month>/<day>/rollout-*.jsonl
```

**`-tool-namespace harness` is required.** A transcript only records the
transport's own namespace (e.g. `anthropic`), never the harness that issued
the call; without `-tool-namespace harness` to rebind it, every native tool
call (`Bash`, `Read`, `exec_command`, …) is reported as an opaque
`unknown_tool` diagnostic — decoded, but with its input left unanalyzed.

### Parse provider tool-call traffic directly

The same `records` command reads raw API transcripts — one JSON object per
line, whole response objects, or streaming events — for the underlying
providers, if you're capturing traffic yourself rather than reading an
agent's own log:

```sh
# OpenAI Responses API
agentcmd records -adapter openai -tool-namespace harness -format jsonl session.jsonl

# Anthropic Messages API
agentcmd records -adapter anthropic -tool-namespace harness -format jsonl session.jsonl

# Gemini generateContent
agentcmd records -adapter gemini -tool-namespace harness -format jsonl session.jsonl
```

`-tool-namespace harness` applies here too, for the same reason.

### Analyze a single shell command

```sh
go run ./cmd/agentcmd analyze -cwd /workspace/project -structure - <<'EOF2'
cd service && python - <<'PY'
from pathlib import Path
Path("config.json").write_text("x")
PY
EOF2
```

See [`docs/cli.md`](docs/cli.md) and [`docs/ingestion.md`](docs/ingestion.md)
for the full record-stream reference, including the other adapters and
streaming/association semantics.

## Library usage

Build an engine once and reuse it; it holds no per-call state.

```go
e, err := agentcmd.New(agentcmd.Config{
    Rules:     standard.Rules(),      // rules/standard
    Native:    nativestd.Bindings(),  // native/standard
    Languages: agentcmd.Languages{Python: true, JavaScript: true},
})
```

### Analyze a shell command

```go
analysis, err := e.AnalyzeShell(ctx, []byte("cat README.md"), model.ExecutionContext{
    WorkingDir: model.KnownFact("/workspace/project", "reported"),
    Shell:      model.KnownFact("bash", "reported"),
    Namespace:  model.Namespace{Kind: model.NamespaceHost},
})
```

### Analyze a single structured tool call

`Analyze` takes a canonical `model.Invocation` (JSON arguments, text, argv,
inline source, patch or blob) directly, without going through an adapter or
a record stream — useful when you already have one call in hand, e.g. from
your own harness's tool dispatch:

```go
analysis, err := e.Analyze(ctx, model.Invocation{
    ID:      "call-1",
    Tool:    model.ToolIdentity{Namespace: "harness", Name: "Bash"},
    Input:   model.Input{Kind: model.InputJSON, JSON: json.RawMessage(`{"command":"rm -rf /tmp/build"}`)},
    Context: model.ExecutionContext{Namespace: model.Namespace{Kind: model.NamespaceHost}},
})
```

Structured tool inputs go through native bindings, selected by
`Tool.Namespace` + `Tool.Name` (here `std/claude-code/Bash`, from
`native/standard`); text inputs are analyzed as shell only when a binding
says so.

### Analyze a record stream (adapters + `stream`)

For a whole session — a coding agent's own transcript or raw provider
traffic — decode it with an `adapters.Adapter` and feed it through
`stream.Processor`, the same path the `records` CLI command uses:

```go
f, err := os.Open("session.jsonl")
// ...
p, err := stream.New(e, stream.Options{
    Adapter:       adapters.ClaudeCode{}, // or adapters.Codex{}, adapters.OpenAI{}, ...
    ToolNamespace: "harness",             // see the note on -tool-namespace above
})
err = p.Process(ctx, f, func(ev model.Event) error {
    switch ev.Kind {
    case model.EventAnalysis:
        return render.Text(os.Stdout, ev.Analysis, render.TextOptions{})
    case model.EventDiagnostic:
        log.Printf("record %d: %s", ev.Sequence, ev.Diagnostic.Message)
    }
    return nil
})
```

`Process` emits one `model.Event` per completed call, result, or problem, in
input order; the callback's error aborts processing. See
[`docs/ingestion.md`](docs/ingestion.md) for adapter coverage and streaming
bounds, and [`render`](render) for the JSON and text renderers.

Projections for integrations live in `projection/otel` (OpenTelemetry GenAI
attributes, no SDK dependency) and `projection/records` (flat indexable
records). Neither is imported by the root package.

## Documentation

| Topic | File |
|---|---|
| CLI | [`docs/cli.md`](docs/cli.md) |
| Adapters, streaming, observations | [`docs/ingestion.md`](docs/ingestion.md) |
| Parser qualification, limits, certified subsets | [`docs/compatibility.md`](docs/compatibility.md) |
| Shell semantics and assumptions | [`docs/shell-semantics.md`](docs/shell-semantics.md) |
| Python and JavaScript subsets | [`docs/semantics.md`](docs/semantics.md) |
| Command rule catalog | [`docs/rules.md`](docs/rules.md) |
| Snapshot enrichment | [`docs/snapshots.md`](docs/snapshots.md) |
| Benchmarks and regression budget | [`docs/benchmarks.md`](docs/benchmarks.md) |
| Scorecard and corpus report | [`docs/scorecard.md`](docs/scorecard.md), [`docs/corpus-report.md`](docs/corpus-report.md) |
| Versioning, privacy, dependencies | [`docs/versioning.md`](docs/versioning.md) |
| Threat model | [`docs/threat-model.md`](docs/threat-model.md) |
| Contributing | [`docs/contributing.md`](docs/contributing.md) |
| Decisions | [`docs/adr/`](docs/adr/) |

## Development

```sh
CGO_ENABLED=0 go test ./...
go test ./internal/corpus -update   # regenerate docs/corpus-report.md
```
