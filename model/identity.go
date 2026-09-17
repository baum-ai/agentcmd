package model

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
)

// AnalysisID derives the deterministic analysis identity from the invocation
// identity, versions, an options fingerprint and the execution context.
// Content of the input is included so that re-analysis of an edited record is
// distinguishable, but two distinct invocations with identical content still
// differ through their IDs. See ADR 0004.
func AnalysisID(inv *Invocation, v Versions, options string) string {
	h := sha256.New()
	enc := json.NewEncoder(h)
	for _, part := range []any{inv.Session, inv.ID, inv.CallID, inv.Tool, inv.Input, inv.Context, v, options} {
		_ = enc.Encode(part)
	}
	return "an_" + hex.EncodeToString(h.Sum(nil))[:24]
}

// Fingerprint returns a short digest of any JSON-encodable value. It is used
// for option and schema fingerprints, never as an event ID.
func Fingerprint(v any) string {
	b, _ := json.Marshal(v)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])[:16]
}
