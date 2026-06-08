package mcp

import (
	"encoding/json"
	"fmt"
)

// outboxArtifact is the on-disk JSON format subagents write to scan-outbox.
// facts may be a JSON array (per fact_schema.md) or a JSON-encoded string.
type outboxArtifact struct {
	FilePath     string          `json:"file_path"`
	Status       string          `json:"status"`
	FromType     string          `json:"from_type"`
	Facts        json.RawMessage `json:"facts"`
	ErrorMessage string          `json:"error_message"`
	Error        string          `json:"error"`
	RevisionID   int64           `json:"revision_id"`
	Domain       string          `json:"domain"`
	ObligationID int64           `json:"obligation_id"`
	VoteGroup    string          `json:"vote_group"`
	VoteIndex    int    `json:"vote_index"`
}

func parseOutboxArtifact(data []byte) (outboxArtifact, error) {
	var ext outboxArtifact
	if err := json.Unmarshal(data, &ext); err != nil {
		return ext, err
	}
	return ext, nil
}

// factsJSONString normalizes facts for DB storage (always a JSON text blob).
func factsJSONString(raw json.RawMessage) (string, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return "", nil
	}
	switch raw[0] {
	case '"':
		var s string
		if err := json.Unmarshal(raw, &s); err != nil {
			return "", fmt.Errorf("facts string: %w", err)
		}
		return s, nil
	case '[', '{':
		if !json.Valid(raw) {
			return "", fmt.Errorf("facts: invalid JSON")
		}
		return string(raw), nil
	default:
		return "", fmt.Errorf("facts: unexpected JSON type")
	}
}
