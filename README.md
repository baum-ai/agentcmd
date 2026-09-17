# agentcmd

A pure-Go library and reference CLI that turns coding-agent tool invocations
into a deterministic, evidence-backed account of the operations they request,
the operations their code may perform, and the outcomes actually reported or
observed.

It can analyze a single shell command, a stream of provider tool-call records
(OpenAI, Anthropic, Gemini), or a coding agent's own on-disk session
transcript (Claude Code, Codex CLI) directly — no export step needed.

The module path is `github.com/lastpersonlabs/agentcmd`; the reference CLI is
`cmd/agentcmd`.

The library never executes input, never reads the host filesystem or
environment, and builds with `CGO_ENABLED=0`.

## Documentation

| Topic | File |
|---|---|
| Decisions | `docs/adr/` |
| Parser qualification, limits, certified subsets | `docs/compatibility.md` |
| Shell semantics and assumptions | `docs/shell-semantics.md` |
| Python and JavaScript subsets | `docs/semantics.md` |
| Command rule catalog | `docs/rules.md` |
| Adapters, streaming, observations | `docs/ingestion.md` |
| Snapshot enrichment | `docs/snapshots.md` |
| CLI | `docs/cli.md` |
| Benchmarks and regression budget | `docs/benchmarks.md` |
| Scorecard and corpus report | `docs/scorecard.md`, `docs/corpus-report.md` |
| Versioning, privacy, dependencies | `docs/versioning.md` |
| Threat model | `docs/threat-model.md` |
| Contributing | `docs/contributing.md` |

Projections for integrations live in `projection/otel` (OpenTelemetry GenAI
attributes, no SDK dependency) and `projection/recountable` (flat indexable
records). Neither is imported by the root package.

## Development

```sh
CGO_ENABLED=0 go test ./...
go test ./internal/corpus -update   # regenerate docs/corpus-report.md
```

## Usage

```go
e, err := agentcmd.New(agentcmd.Config{
    Rules:     standard.Rules(),      // rules/standard
    Native:    nativestd.Bindings(),  // native/standard
    Languages: agentcmd.Languages{Python: true, JavaScript: true},
})
analysis, err := e.AnalyzeShell(ctx, []byte("cat README.md"), model.ExecutionContext{
    WorkingDir: model.KnownFact("/workspace/project", "reported"),
    Shell:      model.KnownFact("bash", "reported"),
    Namespace:  model.Namespace{Kind: model.NamespaceHost},
})
```

`Analyze` takes a canonical `model.Invocation` (JSON arguments, text, argv,
inline source, patch or blob). Structured tool inputs go through native
bindings; text inputs are analyzed as shell only when a binding says so.

## CLI

Analyze a single shell command:

```sh
go run ./cmd/agentcmd analyze -cwd /workspace/project -structure - <<'EOF2'
cd service && python - <<'PY'
from pathlib import Path
Path("config.json").write_text("x")
PY
EOF2
```

Analyze a coding agent's own session transcript, in place:

```sh
# Claude Code: ~/.claude/projects/<slug>/<session-id>.jsonl
agentcmd records -adapter claude-code -tool-namespace harness -format jsonl \
  ~/.claude/projects/<slug>/<session-id>.jsonl

# Codex CLI: ~/.codex/sessions/<year>/<month>/<day>/rollout-*.jsonl
agentcmd records -adapter codex -tool-namespace harness -format jsonl \
  ~/.codex/sessions/<year>/<month>/<day>/rollout-*.jsonl
```

**`-tool-namespace harness` is required** for `claude-code`, `codex`, and the
raw provider adapters (`openai`, `anthropic`, `gemini`). A transcript only
records the transport's own namespace (e.g. `anthropic`), never the harness
that issued the call; without `-tool-namespace harness` to rebind it, every
native tool call (`Bash`, `Read`, `exec_command`, …) is reported as an opaque
`unknown_tool` diagnostic — decoded, but with its input left unanalyzed.

See `docs/cli.md` and `docs/ingestion.md` for the full record-stream
reference, including the other adapters and streaming/association semantics.
