# Shell semantics: certified subset and assumptions

This documents what `internal/shell` models for POSIX sh and Bash and the
assumptions every result carries. Zsh and mksh parse but their semantics are
reported as unsupported (`Partial: true`).

## Structure

| Construct | Node kind | Notes |
|---|---|---|
| `a; b` / newlines | children of the enclosing node in order | after an unconditional `exit`/`return`, later statements are `unreachable` |
| `a && b` | `and` with two children | `b` carries `Guard{a, success}` and `conditional` reachability |
| `a \|\| b` | `or` | `b` carries `Guard{a, failure}` |
| `! a && b` | `and` | guard condition is flipped |
| `a \| b \| c` | `pipeline` with flattened children | every element runs in a subshell: no state escapes |
| `( ... )` | `subshell` | cloned state |
| `{ ...; }` | `group` | shared state |
| `if/elif/else`, `case` | `if` (nested `if` with detail `else`) | branches are `conditional`; states are joined afterwards |
| `while`, `until`, `for` | `loop` | bodies are `repeated`; the loop variable holds bounded alternatives, a glob match, or a symbolic value |
| `f() { ... }` | `function`, `latent` | body is lowered only under a `function call` node when `f` is invoked; recursion is expanded once |
| `cmd &` | `background` | subshell semantics |
| `$(...)`, `<(...)` | `substitution` placed before the command that uses it | value is symbolic |
| redirections | `redirect` nodes under the command, in order | `>` truncate, `>>` append, `<` read, `<>` read + write(unknown), `n>&m` duplication without file action, `>&file` truncate |
| `<<EOF` | `redirect` "stdin from here-document" | quoted delimiter → literal body with its own derived source; unquoted → expanded, symbolic if any expansion is unknown; `<<-` strips leading tabs |
| `> file` alone | `command` "redirection only" | the write happens without a command |
| `(( ))`, `[[ ]]`, `let` | `command` with rule `builtin` | no external effects |
| coproc, unknown constructs | `unknown` + `unsupported` diagnostic | result is partial |

## State

The abstract state tracks the working directory, variables assigned in the
program, unset variables, functions and aliases defined in the program.

- **Working directory.** `cd`/`pushd` with a concrete operand derive the new
  directory lexically under the recorded assumption "directory change to X
  succeeded". This optimistic assumption is deliberate: agents chain
  `cd dir && cmd`, and the guard on `cmd` already encodes the dependency. A
  symbolic operand makes cwd symbolic; `popd`, `source` and unknown `eval`
  make it unknown.
- **Variables.** Plain assignments and `export NAME=value` are tracked.
  Expansions of tracked variables are `derived` (assumption "$X holds its
  assigned value"). Unset variables expand to nothing. Everything else is
  `symbolic` and rendered as its source text (`$HOME/.bashrc`).
- **Branch joins.** After `if`/`case`/loops the states are joined: equal
  values stay; up to `JoinAlternatives` distinct concrete values become a
  `derived` value with `alternatives`; more become `symbolic`.
- **Scope.** Subshells, pipelines, background jobs and command substitutions
  operate on clones. External executables never change the parent shell's
  directory.
- **Invalidation.** `source file`, `.`, and `eval` of a non-literal string
  invalidate the state (cwd unknown, variables forgotten) and mark the result
  partial. `eval "literal"` is analyzed as nested shell.

## Words

Unquoted literals honour backslash escapes; `*?[` mark the word as a `glob`
(scope, never an enumerated match list); a leading `~` is symbolic. Double
quotes suppress globbing of literal metacharacters and word splitting; an
unquoted expansion of a non-literal value records "may split into several
words". Arithmetic, brace expansion and parameter operators are symbolic.

## Executable identity

Every rule application assumes the name denotes the standard executable.
A function or alias defined in the program with the same name overrides that:
the call is a `function call` node or a symbolic executable, never the
standard command's meaning. Startup files, `PATH` and external aliases are
not modeled; that is stated as the assumption on each command.

## Nested programs

`sh|bash|dash|zsh -c string`, `python -c|-|-m|script`, `node -e|-p|-|script`
and `eval` are extracted with their positional arguments and stdin
here-document. A literal source becomes a derived source with an exact byte
map; a symbolic source becomes `process.unresolved`. Script files become
`process.interpreter` plus a `missing_snapshot` diagnostic: contents are not
part of the invocation. Perl, Ruby, awk, PowerShell and similar programs are
preserved as `process.unresolved` with their argv.
