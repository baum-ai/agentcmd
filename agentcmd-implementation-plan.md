# agentcmd — Go implementation plan

**Status:** Proposed design, not an existing package or implemented API.  
**Prepared:** 16 September 2026.  
**Purpose:** Turn coding-agent tool invocations into a deterministic, evidence-backed account of the operations they request, the operations their code may perform, and the outcomes actually reported or observed.

## 1. Decisions and scope

Build a standalone Go library and reference CLI. The library must not depend on any consumer. The library must be usable on one shell command without an agent transcript, and on a normalized native tool invocation without a shell parser.

Use a pure-Go production dependency graph: no Python or Node installation, CGo, external interpreter, subprocess execution, model API, database, telemetry backend, or network access is required for analysis. Test-only reference implementations may use other runtimes in isolated CI jobs.

Use `mvdan.cc/sh/v3/syntax` for shell syntax. Its upstream documentation describes a pure-Go implementation and support for POSIX shell, Bash, Zsh, and mksh [S1, S2]. Initially certify semantic analysis for a documented POSIX/Bash subset; parser support is not a promise of equivalent semantic coverage for every dialect.

Preferred embedded-language backend: evaluate `odvcencio/gotreesitter` for Python and JavaScript/TypeScript, behind a private interface. It documents a pure-Go runtime and those grammars, but that is not independent proof of completeness or resource safety [S3]. The official Go Tree-sitter bindings use C-allocated objects, so they do not satisfy this production constraint [S4]. Adoption is conditional on milestone M0. Do not make Python/JavaScript correctness depend on a README claim.

The earlier suggestion that a small registry would cover 80–90% of agent behavior was not measured. This project must establish coverage from a corpus and distinguish command recognition from meaningful semantic analysis.

### Deliverables

1. A Go module with a stable, versioned action representation and synchronous analysis API.
2. A command registry, shell analyzer, native-tool bindings, and qualified embedded-language analyzers.
3. Optional transport/session adapters, streaming assembly, snapshot enrichment, and observation reconciliation.
4. A reference CLI, reproducible corpus, benchmarks, compatibility manifest, and contributor documentation.

### Non-goals for v1

Do not build a shell interpreter, general program verifier, agent runtime, universal deobfuscator, security authorization boundary, repository-wide call-graph engine, or tracing backend. Do not execute input to determine its meaning. Do not claim that an emitted effect inventory is exhaustive for arbitrary programs.

Runtime filesystem/process/network collection is a separate integration. The library may consume such evidence, but it must remain useful without it. An optional prose summarizer belongs downstream and must not manufacture factual actions.

## 2. What the library promises

For each invocation, answer: what was requested, which operations can be inferred under stated assumptions, which inputs/resources are involved, what controls their execution, and what evidence exists for the outcome.

These are distinct statements:

- “The tool was asked to read this file.”
- “The embedded source contains a reachable-looking file-read operation under these assumptions.”
- “The tool reported a successful read.”
- “An instrumented runtime recorded a successful read associated with this invocation.”

None may be silently upgraded to the next.

A syntactically valid command can be opaque. A failed command can have effects before failure. A successful outer shell invocation does not prove that every child command ran or that every inferred effect completed. Bash lists and redirection semantics make control structure and execution phases important [S5, S6].

**Primary invariant:** every semantic assertion has provenance, a basis, an execution context, and a statement of its uncertainty. Missing information is represented explicitly rather than guessed.

## 3. Architecture

```text
Source records or an already-normalized invocation
    -> optional source adapter and event assembler
    -> canonical invocation + separate lifecycle observations
    -> versioned tool-contract binding
    -> shell source / executable + argv / embedded source / native operation
    -> language frontends
    -> owned execution representation
    -> bounded semantic analysis and command rules
    -> actions + resource expressions + unknown regions + diagnostics
    -> optional snapshot enrichment
    -> optional observation reconciliation
    -> JSON / text / application integration / telemetry projection
```

Separate three dimensions that are often conflated:

**Provider transport** describes how a call is encoded. **Harness/tool contract** describes what a named tool accepts and does. **Language/command semantics** describes the code within that tool invocation. A model name does not determine shell persistence, argument shape, or the meaning of a tool called `exec`.

Retain both an execution representation and semantic actions. A flat action list is a convenience view, not the source of truth. Preserve sequential composition, success/failure guards, pipelines, subshells, background execution, functions, loops, and nested language boundaries. Model stream/data dependencies separately from ordering and containment.

Keep actual process observations distinct from static execution nodes: a static node may execute zero times, once, or repeatedly.

## 4. Canonical model

### 4.1 Invocation

An invocation contains source identity; provider, harness, and version metadata; session/run identity; namespaced tool identity and schema fingerprint; provider call ID and local occurrence identity; input; execution context; and provenance references.

Input is a tagged union: JSON arguments, free-form text, executable plus literal argv, inline language source, patch text, or a blob reference. Preserve the original input form. OpenAI supports both JSON-schema function inputs and free-form custom-tool inputs, so JSON arguments cannot be the only representation [S7].

The context includes the reported working directory, path flavor, shell/dialect when known, explicit environment facts, execution namespace, snapshot identity, and any persistent shell/process identity. Each fact carries its origin. Unknown is not the same as an empty value.

Lifecycle information is append-only evidence: requested, approved, denied, started, reported completed, cancelled, timed out, or unknown. Missing completion is not failure. Distinguish provider model-output completion from actual tool execution completion.

### 4.2 Actions

Start with these operation families:

| Family | Initial operations |
|---|---|
| Filesystem | read, write, append, truncate, delete, copy, move, list, metadata |
| Search | content search and path search |
| Process | execute, shell execution, interpreter execution, unresolved execution |
| Git | inspect, stage, commit, switch, merge, reset, network operation |
| Development | test intent, build intent, lint intent, format intent |
| Packages | install, remove, run script |
| Network | request intent, upload/download intent, unresolved network operation |
| Native tools | patch/edit request, structured file operation, subagent delegation |

A command may produce several actions. Keep the command execution as context and the inferred operations as children; define roll-up rules so a file read is not counted once per wrapper.

Use typed operation payloads, not a public `map[string]any`. Use a discriminated JSON representation with validation. Unknown future action kinds must remain round-trippable as opaque payloads by compatible readers; reject contradictory known payloads.

For writes, represent known mode separately: append, overwrite/truncate, create-exclusive, in-place edit, or unknown. A requested deletion need not imply a deleted file. Paths are resource expressions, not automatically canonical filesystem identities.

### 4.3 Evidence, conditions, and knowledge

Use categorical evidence rather than invented probabilities:

| Dimension | Example values |
|---|---|
| Basis | tool contract, syntax inference, bounded value inference, heuristic intent, tool report, runtime observation |
| Resource knowledge | literal, derived under assumptions, symbolic, glob, redacted, unknown |
| Reachability | conditional, statically unreachable, potentially repeated, unknown |
| Syntax status | parsed, partial, invalid, unsupported, limit reached |

Store assumptions such as “standard `git` executable,” “this binding denotes the standard library module,” or “working directory is the successful `cd` target.” Store rule ID, rule version, parser version, and source spans with each inference.

Do not attach a universal “confidence 0.98” field. If a future consumer needs calibrated probabilities, train and validate those separately against labeled outcomes.

Observations refer to the specific entity they cover. A process exit code belongs to that process; a patch result belongs to that patch application; a before/after snapshot establishes a net change over an interval, not necessarily exclusive causation by one tool call.

### 4.4 Resource identity

Preserve the original expression, optional lexical resolution, root/namespace, snapshot, and knowledge state. A relative file in a Docker container is not a path on the host. A lexical absolute path is not a symlink-resolved identity. Do not collapse `..` through unknown symlinks and claim physical equivalence.

For globs or search roots, report scope, not an invented list of matching files. Keep byte-oriented path values recoverable when they cannot be represented directly as valid Unicode JSON strings.

### 4.5 Provenance and source maps

Give each source buffer a source ID and half-open byte spans. Track derivation across raw event fields, JSON decoding, shell quoting, here-documents, and embedded string literals. Mapping can be many-to-one or approximate; represent that explicitly.

No third-party AST escapes into the public API. Keep original source references rather than printing an AST and pretending the printed text is original. Large bodies may live behind caller-owned blob references. The default result must not retain the entire transcript.

### 4.6 Identity and versioning

Provider call IDs are scoped by their actual source/session domain. Distinct occurrences of the same command remain distinct invocations. A content fingerprint is not an event ID.

Create deterministic analysis IDs from invocation identity, analyzer/schema/ruleset versions, options, and relevant context/snapshot fingerprints. Within an analysis version, assign stable node/action IDs from deterministic traversal and structure. Do not promise identical action IDs across rule upgrades.

Use separate syntax-cache keys and semantic-analysis keys. Semantic keys must include context and configuration, not just command text. Context enrichment creates a new analysis revision linked to the old one.

## 5. Go package design and API

Use one repository and initially one Go module. Optional packages must not be imported by the root package merely to register themselves.

```text
agentcmd/
  analyze.go                 # Small root API
  options.go
  model/                     # Public versioned model, validation, serialization
  rules/                     # Registration contracts and metadata
  rules/standard/            # Explicitly selected built-in command rules
  native/                    # Namespaced tool-contract bindings
  adapters/                  # Optional transport/session adapters
  stream/                    # Bounded assembly and result association
  snapshot/                  # Read-only snapshot contracts
  observe/                   # Optional evidence reconciliation
  render/                    # JSON and deterministic human summaries
  internal/shell/            # mvdan frontend and shell state lowering
  languages/                 # Optional embedded Python/JavaScript frontends (tree-sitter)
  internal/analysis/         # Abstract values, scopes, guards, rule dispatch
  internal/source/           # Provenance, source maps, decoding
  internal/limits/            # Shared work/allocation accounting
  cmd/agentcmd/              # Reference CLI
  testdata/                  # Redacted fixtures and golden results
  docs/                      # Semantics, compatibility, ADRs, contribution guide
```

The following signatures describe the proposed API; they are not calls to an existing package:

```go
// Engine configuration is frozen by New. An Engine may be shared by callers.
func New(config Config) (*Engine, error)

// Analyze never executes the invocation or reads the host environment.
func (e *Engine) Analyze(
    ctx context.Context,
    input model.Invocation,
) (model.Analysis, error)

// AnalyzeShell is a convenience entry point with an explicit execution context.
func (e *Engine) AnalyzeShell(
    ctx context.Context,
    source []byte,
    execution model.ExecutionContext,
) (model.Analysis, error)

// Transport processing is separate from single-invocation semantic analysis.
func (p *Processor) Process(
    ctx context.Context,
    input io.Reader,
    emit func(model.Event) error,
) error
```

Define `Config` with typed limits, explicitly registered command rules/native contracts, language support policy, and optional read-only resolver. Zero-value/default behavior must be documented and validated. No implicit provider autodetection that silently picks a mismatched schema.

Expected incompleteness returns an analysis with diagnostics. Invalid caller configuration, cancellation, and I/O errors use Go errors. A partial analysis returned alongside an error must be clearly identified. Never treat a parser returning a partial tree as a complete successful parse.

Caller-owned input must not be mutated. By default, results either own small retained data or refer to an explicit external source store; borrowed-memory APIs are deferred. Parser instances are worker-local until thread safety is established. Publish ownership and concurrency tests before optimization.

Custom rule registrations contain a stable ID, version, supported executable/tool identities, platform profile, documentation references, and explicit matching/effect logic. Resolve collisions deterministically or reject them. Do not create a rule language, dynamic plugin loader, or auto-registering global registry in v1.

## 6. Shell analysis

### 6.1 Frontend

Parse shell source with `mvdan.cc/sh/v3/syntax` and lower it into owned nodes [S1, S2]. Never substitute whitespace splitting or a shell-word tokenizer for a shell AST.

Preserve simple commands, words and expansions, assignments, redirections, lists, pipelines, conditional lists, grouped commands, subshells, command substitution, process substitution, functions, branches, loops, and background jobs. Unsupported semantic constructs remain visible with unknown regions.

Do not run the package's interpreter or host-dependent expansion helpers. The analyzer must not expand globs against its own filesystem, source startup files, execute command substitutions, or inspect its own environment.

### 6.2 Bounded state analysis

Maintain abstract values for working directory, selected shell variables, positional arguments where supplied, file-descriptor routing, defined functions/aliases when modeled, and options that materially affect behavior.

Support literals, concatenations, explicitly known variables, limited alternatives at branch joins, and symbolic unknowns. Distinguish unset from unknown. Preserve quote context and the possibility of word splitting or globbing; a string expression is not automatically one argv element.

Apply changes in the correct scope. A successful directory change can affect later commands in that shell, but a subshell's state does not automatically escape. Unknown sourced code invalidates affected parent-shell facts; an external executable does not itself change the parent shell's working directory. These rules must be qualified by actual command identity and documented shell assumptions.

Bound alternatives and total analysis steps. Widen to symbolic unknowns when a bound is reached. Do not enumerate arbitrary loop iterations or recursively inline unrestricted functions. Known function bodies may receive bounded summaries after call-site analysis; an uncalled function body is not an observed action.

### 6.3 Control flow

For `a && b`, the guard on `b` refers to successful completion of `a`. For `a || b`, it refers to nonzero completion of `a`. Pipelines have stream edges and shell-dependent status behavior; they are not a sequential list of completed operations [S5].

Represent loops structurally and effects as potentially repeated. Unknown shell options, traps, startup behavior, and aliases reduce certainty; do not silently use one global default.

### 6.4 Redirections

Model file opening, truncation/append intent, input files, descriptor duplication, and ordering separately from the command body. Redirections occur before command execution and their order matters [S6].

Required cases include redirection-only commands, descriptors redirected to other descriptors rather than paths, failed executable lookup after redirection, and multiple redirects to one destination. Avoid converting every redirect into a generic completed write.

### 6.5 Embedded source extraction

Recognize shell command-string execution; Python command, stdin, script, and module invocation; and Node inline evaluation and script invocation. Python documents distinct command-line modes, while Node documents its evaluation options [S8, S9]. Parse their option grammars; do not find the source by substring search.

Here-document extraction must retain whether the delimiter was quoted and whether tab stripping or shell expansion applies. These distinctions change the actual source supplied to the child [S6]. If expansion cannot be resolved, retain symbolic segments and avoid pretending that the visible body is the exact Python/JavaScript program.

Retain positional arguments, environment changes, shell dialect, and wrapper ancestry while descending. A direct argv input must not be reparsed as a shell program merely because an argument contains shell punctuation.

## 7. Command registry

### 7.1 Initial coverage

| Group | Initial implementation scope | Boundary |
|---|---|---|
| Reading/listing/search | Common file readers, directory listing, content/path search | Scope is not proof every file was accessed |
| File modification | Copy/move/remove/create; output redirection; supported patch/edit forms | Modes, options, and conditional outcomes preserved |
| Git | Common inspection and mutation commands | Config, aliases, hooks, and extra processes remain assumptions/unknowns |
| Development | Go test/build, known test runners, formatters and linters | Classify intent; arbitrary project tasks remain opaque |
| Package managers | Installation and script execution recognition | Script name alone does not establish its contents or effects |
| Network tools | Literal request endpoints and file transfers for documented forms | Config, redirects, proxies, and response content may be unknown |
| Wrappers | Selected environment/timeout/shell/launcher wrappers | Preserve execution constraints and transformations |

Start with a small, explicitly tested set informed by corpus frequency: `cat`, `head`, `tail`, `ls`, `rg`, `grep`, selected `find` forms, `cp`, `mv`, `rm`, `mkdir`, `touch`, `tee`, `git`, `go`, common package managers, and common interpreter launchers. These are planned recognizers, not a claim that every option is supported.

Each command rule needs a documented option grammar and tests for end-of-options handling, option operands, platform differences, unknown flags, and special modes. Unknown syntax must reduce the precision of extraction rather than reinterpret an option argument as a file path.

Represent assumptions about executable identity. A function called `git`, a locally shadowed binary, or an unresolved alias must not inherit an unqualified standard-command meaning.

### 7.2 Semantic layering

Run structural rules first, then wrapper rules, then command semantics, then bounded argument/value inference. Intent labels enrich an execution; they do not replace it or claim exhaustive effects.

Keep “execute a project target named test” distinct from “invoke a known test runner.” When project scripts are resolved later, preserve lifecycle scripts, shell selection, environment, and working-directory changes; otherwise leave their additional behavior unknown.

For unsupported interpreters and scripts, emit a meaningful unresolved-execution node with executable, known argv, source location, and reason. Preserve Perl, Ruby, awk, PowerShell, and other programs instead of dropping them or misclassifying their source as Bash.

### 7.3 Remote and container execution

Recognize namespace boundaries before resolving resources. Initially classify remote/container invocation and preserve the command payload without asserting host-side file effects. More detailed expansion requires a verified wrapper profile and corresponding remote/container context.

Do not assume that an SSH argument vector, a Docker command, and an entrypoint-controlled container have identical command-string semantics. Dedicated rule tests must establish each supported case.

## 8. Python and JavaScript analysis

### 8.1 Parser qualification

M0 evaluates `gotreesitter` against real snippets and upstream grammar fixtures. Required evidence includes parse correctness, source positions, error/partial-tree handling, scanner behavior, cancellation/limit behavior, memory use, binary size, dependency licensing, and reproducible pinning. Import only the languages actually needed where practical [S3].

Alternative pure-Go candidates for the evaluation are `tamnd/gopapy` for Python and `tdewolff/parse` for JavaScript. Their upstream documentation describes Python parsing and a Go JavaScript parser respectively; neither is presumed to satisfy this project's complete compatibility and safety contract without testing [S10, S11].

Select a backend per language if necessary. Do not expose its AST or assume that all adapters need a single universal syntax-tree abstraction. If no candidate qualifies, keep extraction and opaque execution available, mark that language unsupported, and make the parser gap explicit. Shell/native releases may proceed; advertised deep Python/JavaScript support may not.

### 8.2 Semantic subset

Implement lexical binding/import tracking, literal constants and simple assignments, bounded concatenation, selected path construction, selected function aliases, known calls, and conditional summaries. Start intraprocedurally, adding small bounded local summaries only where the corpus justifies them.

For Python, target qualified standard-library file/path operations, file opening modes, and subprocess construction. For JavaScript, target qualified Node filesystem calls, filesystem promises, and child-process construction. These operation families are documented by Python and Node [S12–S15]. Version the modeled API behavior.

Example target:

```python
from pathlib import Path
base = Path("internal")
p = base / "user.go"
p.write_text("replacement")
```

Produce an inferred write with a lexically derived relative resource and explicit assumptions about binding/module identity, successful execution of preceding code, and the execution directory. Do not assert that the file exists afterward or that writing was the program's only effect.

Shadowing matters: a local `open` or a user object named `fs` invalidates simple name matching. Dynamic imports, rebinding, reflective calls, unknown attributes, and unknown call effects introduce uncertainty. Imported modules can have behavior outside the snippet, so a successful syntax parse cannot establish a closed-world effect inventory.

Do not walk every function or callback body and mark it executed. Preserve latent bodies; distinguish eager top-level expressions from code that requires later invocation. Unsupported control flow yields guarded or unknown summaries, not fictional linear execution.

### 8.3 Nested process execution

Distinguish subprocess APIs that execute a shell string from those that execute a program with arguments. Python's shell selection and Node's exec/execFile/spawn distinctions require separate models [S14, S15].

When a literal child command and its execution mode are known, recursively analyze it under the same global budget. Otherwise emit an unresolved child execution. Preserve the wrapper, child scope, stdin relationship, and source derivation.

No evaluation of arbitrary constants through Python/Node. Any implemented folding is a bounded Go operation over a documented subset, not an embedded interpreter.

## 9. Provider, harness, and native-tool adapters

### 9.1 Transport adapters

Implement canonical input first, then the source formats actually present in the corpus. Initial targets are OpenAI Responses/function/custom-tool records, Anthropic tool-use/result blocks, and Gemini function-call/response parts. Their documentation confirms that these are different message representations [S7, S16, S17].

Adapters normalize identity, input forms, lifecycle evidence, and source references. They do not interpret a tool's business meaning. Keep unrecognized provider metadata as an extension/reference without requiring it in the core semantic schema.

For on-disk agent sessions, prefer documented exports when available. Every support claim needs a pinned schema/source version and golden fixtures. Treat undocumented log formats as versioned, best-effort adapters. Do not claim Claude Code, Codex, Pi/OMP, Entire, or another harness format is stable without checking the specific format during implementation.

### 9.2 Streaming and correlation

Assemble interleaved argument fragments per source/session/call identity and sequence. Handle completed argument snapshots separately from deltas. Preserve duplicate/conflicting events, retries, cancellation, orphan results, and incomplete end-of-stream state.

For a stream with missing fragments, do not analyze the assembled prefix as a final complete tool call. Provisional analysis is an explicit later feature. Never match a result to a call purely because their names or command strings coincide.

Bound active calls, buffered bytes, record size, output inspection, and reordering windows. Apply backpressure when the result consumer is slow. Flush incomplete calls with diagnostics at EOF. Sequential input order remains available even if worker completion order differs.

### 9.3 Native tool bindings

Bind by namespace, tool identity, schema fingerprint, and harness profile. Support known read/write/edit/search/patch operations directly from structured inputs. Do not re-encode them as fictitious shell commands.

Treat patch contents as data. Code inserted by a patch is not executed by the patch request. Support declared patch formats with dedicated fixtures for added/deleted/moved files, hunk boundaries, malformed patches, and unsupported binary forms. Do not apply patches during analysis.

MCP annotations are declared hints, not authoritative effect guarantees; the protocol explicitly warns that they need not faithfully describe behavior [S18]. Keep hint-derived classifications separate from verified tool-contract bindings.

Unknown native tools remain visible as opaque invocations. Do not infer safety from an empty action list or an appealing tool name.

## 10. Snapshot enrichment and execution evidence

### 10.1 Read-only snapshot resolver

Define a capability-limited resolver for content at a specified snapshot and execution namespace. It may retrieve script contents or selected project configuration; it does not execute anything. The core has no ambient filesystem fallback.

A resolver answer must include content/source identity and snapshot provenance. Apply per-file and aggregate byte limits, traversal limits, recursion/cycle detection, and cancellation. Enforce root confinement and symlink policy in the resolver implementation, not only through lexical path checks.

Never analyze today's `script.py` as though it were the script that ran yesterday. When an exact contemporaneous snapshot is unavailable, mark content as approximate and keep it out of claims about historical execution. If earlier commands in the same invocation could alter a referenced file, do not blindly use the invocation's starting snapshot as its later contents.

Start with explicitly referenced script files and selected package script declarations. Avoid recursive repository crawling, dependency downloads, and arbitrary build-system evaluation. A deterministic content-addressed snapshot is preferable to live working-tree reads.

### 10.2 Observation reconciliation

Normalize externally supplied process starts/exits, direct tool results, structured test results, patch outcomes, and optionally file/network observations. Link by explicit call/process/session identity whenever possible, with the collector and namespace recorded.

Retain the original inference and attach confirming or conflicting evidence. A file diff can show net change but not all reads or intermediate writes. A process exit code does not prove every internal operation succeeded. Missing observations do not prove absence of effects.

For downstream consumers, retain invocation IDs, source spans, resource expressions, rule versions, and explicit observation links. Session/commit causation remains a higher-level evidence problem; this library should not infer exclusive commit ownership from a filename overlap.

## 11. Security and privacy boundaries

Treat source records, code, tool descriptions, and output as hostile data. Production analysis performs no execution, network access, import loading, or ambient filesystem inspection. Source-provided instructions never change analyzer behavior.

Require finite limits on raw frame size, decoded source size, total derived-source bytes, parsing/analysis work, recursive expansion depth, retained result size, active stream state, and output inspection. Detect cycles by source/context identity in addition to depth.

Proposed starting limits, to validate during M0:

| Limit | Initial value |
|---|---:|
| Input frame | 8 MiB |
| Individual executable source | 1 MiB |
| Total derived source per invocation | 4 MiB |
| Nested language/script depth | 8 |
| Syntax nodes per parsed source | 100,000, where enforceable inside the backend |
| Emitted actions per invocation | 2,000 |
| Alternatives at an abstract-value join | 8 |
| Buffered stream input across active calls | 64 MiB |
| Concurrent incomplete calls | 256, also subject to the byte limit |

These are proposed policy values, not existing dependency options. Do not claim an allocation limit is hard because it is checked after parsing. If a parser cannot honor required limits/cancellation, document the limitation and use separately isolated analysis workers at the application boundary for hard process-level limits. Cancelling a Go context does not itself terminate an uncooperative parser.

Keep redaction separate from parsing. Accept already-redacted content with redaction spans where available; unresolved secret substitutions remain unknown. For integrations that analyze raw content inside a trusted boundary, redact derived values, excerpts, diagnostics, and exported fingerprints as well as the original record before persistence or upload.

Default CLI output should not dump raw tool arguments or source. Do not log complete environments, credentials, URL secrets, or file contents. Limit and escape untrusted text in human renderers. Exposed fingerprints should be derived from redacted content or use an appropriate private keyed scheme; do not leak sensitive low-entropy values through public raw hashes.

## 12. Performance and operational behavior

Support single-invocation analysis and bounded record streaming. Never decode an entire session into an in-memory transcript structure. Reject or explicitly skip oversized frames without first allocating them in full. Large outputs and binary artifacts stay in caller-owned storage unless the selected analysis needs a bounded portion.

Use explicit worker pools, immutable rule metadata, worker-local parser state, and bounded caches. Preserve deterministic output regardless of worker count. Avoid introducing pooling until profiles demonstrate a benefit; memory retention from large exceptional records must be tested.

Benchmark four paths independently: transport normalization, shell parsing/lowering, semantic rules, and embedded-language analysis. Also benchmark end-to-end processing including decoding, source maps, result serialization, and any configured redaction boundary.

Use redacted real-session corpora at approximately 100 MiB and 1 GiB, plus synthetic adversarial records. Publish hardware, dependency versions, worker count, grammar set, cache policy, input/output sizes, allocation statistics, peak memory, and syntax/semantic coverage with every result. A run that marks Python opaque is not equivalent to a run that analyzes it deeply.

Provisional engineering targets, not measured results: single-worker shell analysis around 10,000 ordinary 1 KiB calls/second; low-millisecond tail latency for ordinary small snippets; and bounded retained memory as total stream length grows. M0 must establish whether these are credible and replace them with a documented baseline and regression budget. Do not borrow throughput expectations from secret scanning or simple JSON ingestion.

Limit hits produce structured reasons, never silently smaller action lists. Distinguish a syntax error, unsupported language, unresolved binding, missing snapshot, truncation, and exhausted resource budget.

## 13. Validation and test strategy

### 13.1 Corpus first

Before writing a large rule catalog, collect permissioned, redacted representative sessions across the harnesses in use. Sample by tool family, command family, shell structure, embedded language, platform, and record size. Keep a held-out evaluation split. Store fixture origin, version, redaction policy, and redistribution permission.

Label invocation decoding, execution structure, possible actions, resource expressions, guards, assumptions, unknown regions, and observed outcomes separately. Human review must distinguish what is inferable from what happened in one particular run.

Every rule ships with positive cases, negative cases, option parsing cases, shadowing cases where applicable, and unknown/dynamic cases. Add regression fixtures for every production failure.

### 13.2 Required adversarial and semantic cases

Include nested quoting, escaped JSON, Unicode offsets, CRLF, non-UTF-8 bytes, quoted/unquoted here-documents, command substitution, unknown variables, globs, redirection order, pipeline status, shell options, background execution, failed directory changes, subshell scope, aliases/functions, and missing startup context.

Include Python/JavaScript binding shadowing, uncalled functions, callbacks, dynamic imports, nested subprocesses, syntactically partial programs, supported new syntax, and different interpreter modes. Include patches containing code-like text, redacted operands, script files modified before execution, and remote/container namespace changes.

Include interleaved streamed calls, duplicate IDs across sessions, duplicate fragments, conflicting completion snapshots, out-of-order results, orphan results, retries, denied calls, and interrupted streams. Distinct identical commands must never be collapsed.

### 13.3 Differential and property testing

Use parser reference implementations only in CI: compare supported syntax and source positions with upstream fixtures, CPython AST parsing, and JavaScript reference syntax tooling. Python documents `ast.parse` and its grammar model [S19]. Do not execute imported agent transcripts as a test oracle.

A separate tiny curated execution suite may run hand-written benign examples inside isolated temporary environments to check claimed shell/file semantics. It has no network or credentials and is not exposed as a runtime feature.

Fuzz record framing, JSON union decoding, shell extraction, embedded decoding, source-map composition, analyzers, and serializers. Require deterministic IDs/results, valid references, bounded growth, and no fabricated runtime observations. Metamorphic tests should cover safe quoting changes and equivalent native/shell operations without assuming broader semantic equivalence than established.

Run pure-Go build/test jobs and separate race-detection jobs. `CGO_ENABLED` is a documented Go build setting; the pure-Go gate uses `CGO_ENABLED=0` [S20]. Test target platforms independently of the execution language/platform represented by the input.

### 13.4 Release scorecard

Do not publish one “accuracy” number. Report adapter acceptance, syntax success, specific semantic action coverage, resource resolution, unknown-action rate, effect precision by evidence class, result-association correctness, and resource-limit incidence.

Required invariants: every input is accounted for; every asserted action has valid provenance; no static inference becomes a runtime observation; all supported fixtures pass; deterministic replay is stable; and source formats outside the compatibility matrix fail explicitly.

A proposed initial evaluation gate is at least 99% precision for the narrowly defined, deterministic action/resource classifications on the held-out labeled corpus, with sample counts and uncertainty intervals reported. This is not a security guarantee and must not be met by hiding low-coverage categories. Set a separate primary-intent coverage target after the corpus baseline, and publish the unknown tail by command family.

## 14. Milestones and executable backlog

No dates or throughput claims below imply that work has already been performed. Each milestone has an artifact and a release gate.

### M0 — Evidence, model, and dependency qualification

**AC-001: Build the representative corpus.** Define redaction/consent rules, select source formats, stratify examples, and freeze a held-out split. Done when the corpus has a distribution report and separately reviewed labels for requests, inferences, and observations.

**AC-002: Freeze core semantic decisions.** Write ADRs for effect/evidence separation, graph structure, source mapping, identity, path namespaces, pure-Go scope, and unknown handling. Done when a golden example exercises all of these concepts without ambiguous fields.

**AC-003: Qualify parser dependencies.** Pin shell and candidate embedded parsers, measure their real behavior, audit licenses, and publish the compatibility/limit matrix. Done when each accepted backend has reproducible tests and failures are explicitly classified.

**AC-004: Establish benchmarks and threat model.** Record baseline decoding/parsing costs and resource behavior; document host-access prohibitions and denial-of-service boundaries. Done when performance targets and hard versus cooperative limits are justified by measurements.

**Dependencies:** none. **Gate:** do not advertise Python/JavaScript support before AC-003 passes for those languages.

### M1 — Canonical model and library skeleton

**AC-005: Implement model and JSON contract.** Add tagged inputs/actions, graph entities, resource expressions, evidence, observations, diagnostics, and validation. Done when malformed unions fail and supported JSON round trips without losing semantics.

**AC-006: Implement identity and source provenance.** Add deterministic analysis/node IDs, scoped call identities, source stores, and composable byte-span maps. Done when decoding nested escaped input retains an explainable path to the original field.

**AC-007: Implement Engine and extension contracts.** Add explicit immutable configuration, command/native registration, error policy, and minimal standard rule set. Done when canonical shell and native input can be analyzed without adapters or telemetry packages.

**Dependencies:** M0 semantic decisions. **Gate:** every result is validated; unknown input remains representable.

### M2 — Shell structure, state, and guarded actions

**AC-008: Lower shell syntax.** Preserve syntax structure and positions while keeping mvdan types private. Done when golden cases cover lists, pipes, redirects, compound commands, and unsupported regions.

**AC-009: Add bounded state/value analysis.** Implement literal/derived/symbolic values, scoped cwd/environment facts, and bounded branch joins. Done when subshell changes do not leak and unknown values are never replaced with host values.

**AC-010: Add control flow and execution phases.** Model conditions, possible repetition, substitutions, descriptor routing, and redirection-before-body behavior. Done when failed or skipped commands cannot become completed actions.

**AC-011: Add shell/interpreter extraction.** Parse supported invocation options and here-documents while retaining source maps and shared budgets. Done when nested source is either correctly derived or explicitly unresolved.

**Dependencies:** M1. **Gate:** a reviewer can explain the structure and uncertainty of each supported shell fixture using only the output.

### M3 — Useful shell and native-tool release

**AC-012: Implement file/search rules.** Add the initial high-frequency filesystem/search catalog with platform and option coverage. Done when negative and ambiguous examples cannot produce precise but wrong paths.

**AC-013: Implement Git/development/package/network rules.** Add conservative intent and documented command forms, retaining unknown additional behavior. Done when an arbitrary target named test is not promoted to a verified test execution.

**AC-014: Implement wrapper and namespace rules.** Support selected wrappers; preserve remote/container scope even when deeper behavior is opaque. Done when host and guest resource identities cannot be conflated.

**AC-015: Implement native file/edit/patch bindings.** Bind against captured schemas and parse patch formats as data. Done when inserted source is not classified as code execution and tool result evidence is separate.

**AC-016: Ship the reference CLI.** Accept a shell source file/stdin or canonical record stream and produce deterministic text/JSON with privacy-safe defaults. All CLI names and flags are to be specified by this project, not inferred from another tool. Done when each documented CLI workflow has an integration test.

**Dependencies:** M2; native bindings may proceed after M1. **Release:** v0.1, useful independently of embedded-language support.

### M4 — Embedded Python and JavaScript semantics

**AC-017: Integrate qualified language frontends.** Add worker-local parsers, diagnostics, source positions, lifecycle handling, and limits. Done when both valid and partial/error inputs match the compatibility matrix.

**AC-018: Implement Python semantic subset.** Add binding-aware selected file/path/subprocess rules and bounded values. Done when shadowed standard names and uncalled functions do not produce unconditional effects.

**AC-019: Implement JavaScript semantic subset.** Add qualified filesystem and child-process models with supported module modes. Done when callbacks and arbitrary objects named fs remain correctly qualified.

**AC-020: Implement cross-language recursion.** Reuse execution context, source maps, and one global budget through nested shell/interpreter calls. Done when nesting and cycles cannot bypass limits or lose ancestry.

**Dependencies:** M0 qualified backend, M2, M3 rule contracts. **Release:** v0.2 with explicit per-language semantic coverage.

### M5 — Session ingestion and result association

**AC-021: Implement provider/source adapters.** Add corpus-prioritized formats with pinned source/schema metadata and fixtures. Done when unsupported versions are explicit and no recognized call disappears silently.

**AC-022: Implement bounded stream assembly.** Handle interleaving, deltas, snapshots, replay, orphan events, EOF, and backpressure. Done when shuffled valid inputs associate correctly and invalid/conflicting inputs are diagnosed.

**AC-023: Implement observation association.** Attach structured results and process/test/patch evidence to the correct entity without upgrading unrelated actions. Done when identical commands in different calls remain distinct and outer exit status stays scoped.

**Dependencies:** M1; can run in parallel with M3/M4. **Release gate:** corpus-level accounting and replay invariants hold.

### M6 — Historical snapshot enrichment

**AC-024: Implement constrained resolver contracts.** Add snapshot/namespace identities, content provenance, budgets, and traversal policy. Done when the core never falls back to host files.

**AC-025: Resolve selected scripts/configuration.** Analyze explicit script files and selected package declarations with cycles and modification timing handled conservatively. Done when stale or invalidated contents are labeled rather than presented as historical facts.

**Dependencies:** M4 for deep script analysis, M1 for identities. **Release:** v0.3 enrichment, independently optional.

### M7 — Hardening and integration

**AC-026: Complete fuzzing and adversarial suites.** Exercise decoder, parser, source maps, action rules, and serializers with resource watchdogs. Done when known failures are fixed or unsupported behavior is explicit and tested.

**AC-027: Profile and optimize.** Measure 100 MiB/1 GiB end-to-end corpora, investigate allocation and cache retention, and set regression thresholds. Done when benchmarks state actual coverage and retained memory does not grow with processed session length.

**AC-028: Add records and telemetry projections.** Keep these optional; connect stable evidence IDs and namespace/snapshot facts. OpenTelemetry GenAI conventions are an external projection, not the internal action model [S21]. Done when disabling the integration changes neither core analysis nor dependencies required by the root package.

**AC-029: Publish compatibility and contributor documentation.** Document supported syntax/tool versions, every rule's assumptions, unknown handling, privacy, upgrade policy, and how to add a fixture/rule. Done when a contributor can implement a small rule without changing the engine.

**AC-030: Stabilize v1.** Review the schema/API against a real consumer; verify scorecard, platform builds, dependency pins/license notices, migration policy, deterministic replay, and privacy defaults. Done when all release gates are reproducible in CI.

**Dependencies:** preceding release-specific milestones. **Release:** v1.0 only for the explicitly declared support matrix; later languages and command forms remain additive.

## 15. Integration and presentation contract

Human output should lead with useful operations, qualify their basis, and show unknowns. A deterministic renderer can describe “requested write,” “inferred test invocation,” “reported failure,” and “unresolved project script” without a model.

Do not render static order as observed timestamps. Provide source locations, wrapper ancestry, guards, and evidence drill-down. A summary such as “modified three files” requires an explicit policy: requested targets, reported modifications, or observed net changes. The UI must not use those definitions interchangeably.

Use telemetry as a projection: original invocation spans and actual observed execution spans when available; inferred static actions as attributes/events or linked analysis data, not invented runtime spans with fabricated durations. Pin the external semantic-convention version in the exporter [S21].

For downstream indexers, initially integrate analysis over already-normalized calls rather than duplicating all ingestion logic. Store raw/redacted source references and analysis version so changes can be replayed. Index action kinds, namespaced resources, known test/build intents, unknown reasons, and evidence links. Keep source matching and causal conclusions outside the parser.

## 16. First end-to-end acceptance scenario

Use a canonical invocation with known cwd `/workspace/project` and Bash context, containing:

```sh
cd service && python - <<'PY'
from pathlib import Path
p = Path("config.json")
p.write_text(p.read_text().replace("old", "new"))
PY
```

The result must retain the directory-change node, the guard on Python execution, the quoted here-document source and mapping, and inferred read/write operations on `config.json` in the successful directory-change context. The paths may be lexically resolved but are not asserted to be symlink-canonical. The Python bindings remain qualified by the semantic assumptions.

With input alone, the renderer must not say that the file was changed. A supplied outer exit code is attached to that shell invocation, not automatically to every inferred operation. A later direct write observation or relevant snapshot comparison may add evidence with its own scope and limitations.

Repeat with an unknown directory, an unquoted here-document containing expansion, a shadowed `Path`, a script exceeding the budget, and a patch that merely contains the same Python source. Each must degrade in the documented way without producing a confidently incorrect history.

## 17. Final recommendation

Prioritize the model, corpus, and shell/native vertical slice. The lasting value is not the number of recognized executable names: it is a stable representation that retains control flow, resource context, provenance, and explicit unknowns.

The system should reliably tell a consumer the difference between “the agent requested this,” “this code appears capable of doing this,” and “there is evidence this happened.” That contract is more important than a large initial command catalog or an attractive summary.

## References

Dependency and external capability checks were made against these primary sources on 16 September 2026. Backend qualification and performance testing are planned work, not completed experiments. Third-party repository documentation is used to establish documented interfaces/claims, not as independent proof of performance or correctness.

- **S1:** mvdan/sh upstream README — https://github.com/mvdan/sh
- **S2:** mvdan shell syntax API — https://pkg.go.dev/mvdan.cc/sh/v3/syntax
- **S3:** gotreesitter upstream documentation — https://github.com/odvcencio/gotreesitter
- **S4:** Official Go Tree-sitter bindings, including C object lifetime requirements — https://github.com/tree-sitter/go-tree-sitter
- **S5:** GNU Bash reference manual, command lists and execution environment — https://www.gnu.org/s/bash/manual/bash.html
- **S6:** GNU Bash redirections and here-documents — https://www.gnu.org/s/bash/manual/html_node/Redirections.html
- **S7:** OpenAI function calling and free-form custom tools — https://developers.openai.com/api/docs/guides/function-calling
- **S8:** Python command-line modes — https://docs.python.org/3/using/cmdline.html
- **S9:** Node command-line API — https://nodejs.org/api/cli.html
- **S10:** gopapy upstream documentation — https://github.com/tamnd/gopapy
- **S11:** tdewolff JavaScript parser documentation — https://raw.githubusercontent.com/tdewolff/parse/master/js/README.md
- **S12:** Python pathlib — https://docs.python.org/3/library/pathlib.html
- **S13:** Node filesystem API — https://nodejs.org/api/fs.html
- **S14:** Python subprocess semantics — https://docs.python.org/3/library/subprocess.html
- **S15:** Node child-process semantics — https://nodejs.org/api/child_process.html
- **S16:** Anthropic tool use — https://platform.claude.com/docs/en/agents-and-tools/tool-use/overview
- **S17:** Gemini function calling — https://ai.google.dev/gemini-api/docs/function-calling
- **S18:** MCP schema and ToolAnnotations caveat — https://modelcontextprotocol.io/specification/2025-11-25/schema
- **S19:** Python AST documentation — https://docs.python.org/3/library/ast.html
- **S20:** Go command documentation — https://pkg.go.dev/cmd/go
- **S21:** OpenTelemetry GenAI semantic conventions — https://github.com/open-telemetry/semantic-conventions-genai
