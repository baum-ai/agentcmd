# Seed corpus

`commands.jsonl` and `heldout.jsonl` are synthetic, hand-written records that
imitate the tool invocations coding agents emit. No real session data is
included: no permissioned real-session corpus was available when the project
started, so this seed exists to make the fixture format, stratification and
labels concrete. Replace or extend it with redacted real sessions before
publishing any coverage numbers.

Each line is one JSON object:

| Field | Meaning |
|---|---|
| `id` | fixture identity |
| `origin` | `synthetic` for every record here |
| `redaction` | `none-required` for synthetic records |
| `redistribution` | `allowed` |
| `tags` | stratification tags: tool family, command family, shell structure, language, platform, size |
| `invocation` | a canonical `model.Invocation` |
| `labels` | separately reviewed expectations: `requests`, `inferences`, `observations`, `unknowns` (strings) |

The held-out split is never used by rule tests. `internal/corpus` loads both
files and `docs/corpus-report.md` is regenerated with
`go test ./internal/corpus -update`.
