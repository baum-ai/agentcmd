# Threat model

The analyzer consumes hostile data: agent transcripts, tool arguments, code,
tool descriptions and tool output. Attackers controlling that data must not
be able to make the analyzer execute anything, read the host, exhaust
resources, or emit confident false history.

## Prohibited host access

Production analysis performs no process execution, network access, module
import, or ambient filesystem/environment inspection. The only filesystem
access path is a caller-supplied `snapshot.Resolver`, which is root-confined
and budgeted by the resolver implementation.

Source-provided text never changes analyzer behaviour: there is no rule
language, plugin loader or directive parsing over input.

## Denial of service

Bounded by the limits in `docs/compatibility.md`. Decoders refuse oversized
frames before allocating them. Recursion into nested programs shares one
budget (bytes, depth, actions) per invocation and detects cycles by
(source digest, language, context) identity, not depth alone.

Cancelling a Go context stops the analyzer between steps and sets the
tree-sitter timeout; it cannot pre-empt a backend mid-parse. Applications
that need hard wall-clock guarantees run analysis in a separate worker
process.

## False confidence

Every action carries a basis and assumptions; every unmodelled region is a
diagnostic or `unknown` node. Observations are separate records. Tests assert
that no static inference is ever serialized with a dynamic basis.

## Privacy

Redaction is upstream of parsing. Results retain source text only below a
small size threshold and the CLI never prints raw arguments by default.
Fingerprints exposed to consumers are derived from analysis inputs, not from
raw secrets.
