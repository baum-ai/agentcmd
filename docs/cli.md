# agentcmd CLI

`agentcmd` is the reference command-line interface. It never executes input.
By default it prints no raw source: `-show-source` opts in.

```
agentcmd analyze [-cwd DIR] [-shell DIALECT] [-namespace KIND[:ID]] [-format text|json] [-structure] [-show-source] [FILE|-]
agentcmd records [-adapter canonical|openai|anthropic|gemini|claude-code|codex] [-tool-namespace NS] [-format text|jsonl] [-cwd DIR] [-shell DIALECT] [-structure] [-show-source] [FILE|-]
agentcmd version
```

## analyze

Reads one shell program from `FILE` or stdin (`-`) and analyzes it with the
standard rules and bindings.

| Flag | Meaning |
|---|---|
| `-cwd DIR` | reported working directory; unknown when omitted, so relative paths stay unresolved |
| `-shell DIALECT` | `bash` (default), `sh`, `zsh`, `mksh`; only `bash`/`sh` semantics are certified |
| `-namespace KIND[:ID]` | `host` (default), `container:ID`, `remote:ID`, `unknown` |
| `-format` | `text` (default) or `json` (the `model.Analysis` document) |
| `-structure` | append the execution tree with guards and reachability |
| `-show-source` | include retained source text |

Exit status: `0` complete, `2` partial (a diagnostic explains why), `1` usage
or I/O error.

## records

Reads newline-delimited records in the format named by `-adapter` (see
`docs/ingestion.md`) and emits one `model.Event` per completed call, result
or problem. Provider records need `-tool-namespace harness` to bind against
the standard harness bindings; without it calls stay opaque. `-cwd` and
`-shell` supply the execution context for records that carry none. Text
output prints `record N:` blocks; `-format jsonl` emits the events. Records
over the frame limit (8 MiB) are skipped without being buffered. Exit status
is `2` when any event was a diagnostic, orphan or partial analysis.

## Reading the text output

```
operations:
  a1   inferred  filesystem.read   /etc/hosts [literal,container:app]  [via docker]
  a4   intent    development.test  tool=npm target=test unverified
```

The second column is the evidence verb: `requested` (tool contract),
`inferred` (syntax or bounded values), `intent` (heuristic label), `reported`
(tool report) and `observed` (runtime observation). Static verbs never say a
file was changed. Resources show expression → lexical resolution with
knowledge and namespace; qualifiers in the trailing brackets show guards,
wrappers, repetition and namespace. `unknown regions` lists what could not be
analyzed and `diagnostics` says why.
