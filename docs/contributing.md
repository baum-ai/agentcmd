# Contributing

## Adding a command rule

1. Write the rule in `rules/standard/` as a `rules.Rule` value with a stable
   `ID` (`std/<name>`), a `Version`, the `Executables` it handles and a `Doc`
   that names the option grammar you implemented.
2. Parse options with `rules.ParseOptions` and a spec listing every option
   that takes an argument. Unknown options are recorded as assumptions so an
   option argument is never silently promoted to a path.
3. Build actions with `rules.NewAction` and `cmd.Resource(value, role)`.
   State assumptions in `Evidence.Assumptions`; never produce a
   `tool_report` or `runtime_observation` basis from a rule.
4. Register the rule in `Rules()` and add cases to `rules_test.go` in the
   root package: positive, negative, `--` end-of-options, unknown flag and a
   non-literal operand. Regenerate `docs/rules.md` with
   `go test ./rules/standard -update`.

The engine does not change: rules only see `rules.Command` and return
`rules.Result`. Wrappers return `Wrapped`; launchers return `Program`;
package managers return `Script`.

## Adding a native binding

Add a `native.Binding` in `native/standard/` keyed by namespace and tool
name (and schema fingerprint when the input schema is pinned). Decode the
declared input, build actions with `tool_contract` basis, and return
`Shell`/`Exec` when the input carries a command (`Shells` for several
independent shell texts, each with its own `Workdir`). Patch text goes through
`native.PatchResult`. Keep `native/standard` free of heavy dependencies: the
root package and the standard packages must not import tree-sitter
(`just check-deps`).

## Adding a fixture

Append a record to `testdata/corpus/commands.jsonl` (or `heldout.jsonl` for
evaluation only) with provenance fields, stratification tags and separately
reviewed labels. `go test ./internal/corpus -update` regenerates the
distribution report; `go test . -run TestScorecard -update` the scorecard.
Golden shell outputs live in `testdata/golden` (`go test . -update`).

## Invariants every change must keep

- No execution, host filesystem, environment or network access in
  production packages (`CGO_ENABLED=0 go test ./...` is the gate).
- Every action has a static basis, provenance spans and stated assumptions.
- Observations are separate records and never rewrite actions.
- Limits produce diagnostics, never silently smaller output.
- Results are deterministic and validate (`FuzzAnalyzeShell` and friends
  assert this; run them for a few minutes before a release).
