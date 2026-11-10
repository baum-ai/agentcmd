# Versioning, compatibility and privacy defaults

## Schema

`model.SchemaVersion` follows semantic versioning. Readers accept documents
with the same major version. Additive changes (new action kinds, new
optional fields) bump the minor version; unknown action kinds already round
trip as opaque payloads, so a 1.x reader can carry 1.y documents without
loss. Removing or renaming a field is a major bump.

## API

The Go module follows semantic import versioning. `Config`, `Engine`,
`model`, `rules`, `native`, `snapshot`, `stream`, `adapters`, `observe`,
`render`, `languages` and `projection/*` are public. `internal/*` is not.

## Analysis identity

`Analysis.ID` incorporates the analyzer version, ruleset fingerprint and
options. Upgrading rules changes IDs; node and action IDs are stable only
within one analysis version. Consumers store the raw input reference and
the versions so results can be replayed.

## Support matrix

See `docs/compatibility.md` for parser dependencies and certified language
subsets, `docs/rules.md` for command rules, `docs/ingestion.md` for
transport schemas. Anything outside those tables is reported, not guessed.

## Privacy defaults

- Results retain source text only up to `RetainSourceBytes` (4 KiB).
- The CLI never prints source or raw arguments unless `-show-source` is
  given; renderers escape control characters and truncate long values.
- Projections (`projection/*`) never include source text.
- Fingerprints are derived from invocation identity, versions and options;
  raw content is hashed only together with those inputs and never exposed as
  a content fingerprint of secrets.
- Redaction is upstream: analyze already-redacted records where possible.

## Third-party dependencies

| Module | Version | License |
|---|---|---|
| mvdan.cc/sh/v3 | v3.14.1 | BSD-3-Clause |
| github.com/odvcencio/gotreesitter | v0.52.0 | MIT |
| golang.org/x/sync (indirect) | as pinned in go.sum | BSD-3-Clause |
| gopkg.in/yaml.v3 (indirect) | as pinned in go.sum | MIT / Apache-2.0 |
