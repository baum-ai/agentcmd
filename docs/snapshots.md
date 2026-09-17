# Snapshot enrichment

The analyzer never reads the host filesystem on its own. A caller may supply
a `snapshot.Resolver` that serves file contents at a named snapshot and in a
named namespace; the engine then uses it for exactly two things:

1. **Script files** named by a launcher (`python scripts/x.py`, `bash
   build.sh`, `node tool.js`). The content becomes an external source with
   `origin` provenance (path, namespace, snapshot, digest) and is analyzed
   under the `program` node.
2. **Package scripts** (`npm|yarn|pnpm|bun run NAME`). `package.json` in the
   working directory is read as data; the named script and, for npm/yarn,
   its `pre`/`post` lifecycle scripts are analyzed as nested shell in the
   package directory. pnpm and bun skip lifecycle scripts by default.

No recursive crawling, dependency downloading or build-system evaluation
takes place; Makefiles, Cargo and pyproject tasks stay unresolved.

## Staleness rules

- Content whose snapshot is unknown or differs from the invocation's is
  **approximate**: it is analyzed, a `stale_content` info diagnostic is
  recorded, the program node says so, and every derived action carries the
  assumption "derived from approximate snapshot content; not a claim about
  what ran historically".
- If an earlier command in the same invocation may have written, moved,
  deleted or patched the file, the snapshot is **not used**: the analysis
  is partial with a `stale_content` warning.
- Missing files, unresolvable (symbolic) paths, missing resolvers and
  resolver errors produce `missing_snapshot` diagnostics; size and budget
  failures produce `limit_reached`.

## Resolvers

`snapshot.Map` serves an in-memory, content-addressed set of files for one
snapshot. `snapshot.Dir` serves a live directory through `os.Root`, so
traversal and symlinks that escape the root are rejected by the operating
system, not by lexical checks; symlinks are refused outright; per-file,
aggregate-byte and request budgets apply. Its `Mount` maps the invocation's
namespace path (for example the reported working directory) onto the root.

CLI: `agentcmd analyze -cwd /w -snapshot-root ./checkout -snapshot <commit>`.
