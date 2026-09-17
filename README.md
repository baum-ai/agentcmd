# toolparser (agentcmd)

A pure-Go library and reference CLI that turns coding-agent tool invocations
into a deterministic, evidence-backed account of the operations they request,
the operations their code may perform, and the outcomes actually reported or
observed.

The design is in `agentcmd-implementation-plan.md`; decisions are recorded in
`docs/adr/`. The module path is `github.com/lastpersonlabs/toolparser`; the
reference CLI is `cmd/agentcmd`.

The library never executes input, never reads the host filesystem or
environment, and builds with `CGO_ENABLED=0`.

## Status

| Milestone | State |
|---|---|
| M0 evidence, model decisions, dependency qualification | done |
| M1 canonical model and library skeleton | done |
| M2 shell structure, state, guarded actions | done |
| M3 shell/native rules and CLI | done |
| M4 embedded Python and JavaScript | done |
| M5 session ingestion and result association | done |
| M6 historical snapshot enrichment | done |
| M7 hardening and integration | done (v1.0.0) |

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
e, err := toolparser.New(toolparser.Config{
    Rules:     standard.Rules(),      // rules/standard
    Native:    nativestd.Bindings(),  // native/standard
    Languages: toolparser.Languages{Python: true, JavaScript: true},
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

```sh
go run ./cmd/agentcmd analyze -cwd /workspace/project -structure - <<'EOF2'
cd service && python - <<'PY'
from pathlib import Path
Path("config.json").write_text("x")
PY
EOF2
```

See `docs/cli.md` and `docs/ingestion.md` for record streams:

```sh
agentcmd records -adapter openai -tool-namespace harness -format jsonl session.jsonl
```
