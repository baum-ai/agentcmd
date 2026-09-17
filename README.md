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
| M1 canonical model and library skeleton | planned |
| M2 shell structure, state, guarded actions | planned |
| M3 shell/native rules and CLI | planned |
| M4 embedded Python and JavaScript | planned |
| M5 session ingestion and result association | planned |
| M6 historical snapshot enrichment | planned |
| M7 hardening and integration | planned |

## Development

```sh
CGO_ENABLED=0 go test ./...
go test ./internal/corpus -update   # regenerate docs/corpus-report.md
```
