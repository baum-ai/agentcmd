# Benchmarks

Measured 17 September 2026 on the development machine (AMD64, 24 hardware
threads, Go 1.27.1, `CGO_ENABLED=0`, single worker). Re-run with:

```sh
CGO_ENABLED=0 go test -run xxx -bench . -benchtime 3s . ./stream ./internal/shell ./internal/treesitter
```

| Path | Input | Result | Allocations |
|---|---|---|---|
| Shell parse only | 1 KiB Bash | 14 µs/op (74 MiB/s) | – |
| Shell analysis end to end | 1 KiB, 14 commands incl. rules | 121 µs/op (~8,300 calls/s) | 189 KiB, 1,578 allocs |
| Python here-document (acceptance scenario) | 117 bytes | 310 µs/op | 353 KiB, 1,523 allocs |
| Native Edit binding | one JSON record | 10.8 µs/op | 9 KiB, 72 allocs |
| Stream, canonical records | 1 MiB synthetic | 107 ms (9.8 MiB/s) | 104 MiB total |
| Stream, canonical records | 100 MiB synthetic | 7.8 s (13.4 MiB/s) | 10 GiB total, 87.7 M allocs |
| tree-sitter Python snippet | 99 bytes | 273 µs/op | – |
| tree-sitter JavaScript snippet | 58 bytes | 167 µs/op | – |

Coverage of the runs: the shell path recognizes every command in the 1 KiB
input; the stream corpus is a rotation of a shell command, a native Read, a
multi-command shell line and an exit-status observation. Python and
JavaScript are analyzed deeply in the heredoc benchmark; the stream benchmark
does not include embedded languages.

Retained memory: `TestRetainedMemoryDoesNotGrowWithStreamLength` processes a
2 MiB and a 20 MiB stream and requires the live heap after each to be within
a factor of two plus 4 MiB; retained state is the bounded active-call and
completion-window maps.

Provisional targets from the plan versus measurements: shell analysis
reaches ~8k ordinary 1 KiB calls per second on one worker (target ~10k);
tail latency for small snippets is well under a millisecond for shell and
about 0.3 ms with an embedded Python program. The 1 GiB corpus run was not
executed on this machine; extrapolating the 100 MiB figure gives ~80 s.
Regression budget: CI fails when `BenchmarkAnalyzeShell1KiB` or
`BenchmarkStream1MiB` regress by more than 50%.

Optimization notes: the dominant costs are tree-sitter parser setup per
embedded program and per-record JSON decoding in the stream path. No pooling
was introduced; profiles did not show retention issues, and pooling would
complicate the ownership contract.
