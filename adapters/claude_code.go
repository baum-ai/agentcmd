package adapters

import (
	"encoding/json"
	"fmt"
)

// ClaudeCode decodes Claude Code's own on-disk session transcript
// (~/.claude/projects/<slug>/<session-id>.jsonl): one JSON object per line
// wrapping a Messages API message under "message", alongside CLI
// bookkeeping (uuid, cwd, sessionId, …). Lines without a "message" — mode,
// permission-mode, attachment, file-history-snapshot, summary and similar —
// carry no call information and are skipped. A "message" whose content is
// plain text (no content-block array) is a chat turn with nothing to
// report and is likewise skipped. Everything else uses the same
// tool_use/tool_result content-block shape as the Messages API, so
// decoding is delegated to Anthropic once the envelope is stripped.
type ClaudeCode struct{}

func (ClaudeCode) Name() string    { return "claude-code.transcript" }
func (ClaudeCode) Version() string { return "cli-session-log" }

func (c ClaudeCode) Decode(line []byte) ([]Record, error) {
	var m map[string]json.RawMessage
	if err := json.Unmarshal(line, &m); err != nil {
		return nil, fmt.Errorf("claude-code: %w", err)
	}
	msg := m["message"]
	if msg == nil {
		return nil, nil
	}
	var mm map[string]json.RawMessage
	if err := json.Unmarshal(msg, &mm); err != nil {
		return nil, ErrUnrecognized
	}
	if content := mm["content"]; len(content) == 0 || content[0] != '[' {
		return nil, nil
	}
	recs, err := (Anthropic{}).Decode(msg)
	if err != nil {
		return nil, err
	}
	session := str(m, "sessionId")
	if session == "" {
		return recs, nil
	}
	for i := range recs {
		switch {
		case recs[i].Call != nil:
			recs[i].Call.Session = session
		case recs[i].Result != nil:
			recs[i].Result.Session = session
		case recs[i].Delta != nil:
			recs[i].Delta.Session = session
		case recs[i].Lifecycle != nil:
			recs[i].Lifecycle.Session = session
		}
	}
	return recs, nil
}
