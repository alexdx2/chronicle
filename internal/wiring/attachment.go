package wiring

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

const (
	ClaudeMarkerStart = "<!-- chronicle:claude:start -->"
	ClaudeMarkerEnd   = "<!-- chronicle:claude:end -->"
)

type AttachmentChanges struct {
	AgentsSectionAdded bool `json:"agentsSectionAdded"`
	ClaudeImportAdded  bool `json:"claudeImportAdded"`
	ClaudeFileCreated  bool `json:"claudeFileCreated"`
	HookEntryAdded     bool `json:"hookEntryAdded"`
}

type AttachmentRecord struct {
	SchemaVersion int               `json:"schemaVersion"`
	ProjectPath   string            `json:"projectPath"`
	GitRemote     string            `json:"gitRemote"`
	AttachedAt    string            `json:"attachedAt"`
	Changes       AttachmentChanges `json:"changes"`
}

// ProjectID identifies a project by hashing remote + path together. Path is
// part of the hash, so a relocated checkout gets a new ID — the old
// attachment record is orphaned, and detach falls back to conservative mode
// (see runDetach) rather than finding it. The remote only disambiguates
// distinct clones that happen to share a path prefix; it does not make the
// ID survive a move.
func ProjectID(root, gitRemote string) string {
	sum := sha256.Sum256([]byte(gitRemote + "|" + filepath.Clean(root)))
	return hex.EncodeToString(sum[:12])
}

func AttachmentPath(id string) string {
	return filepath.Join(Home(), "projects", id, "attachment.json")
}

func LoadAttachment(id string) (*AttachmentRecord, error) {
	data, err := os.ReadFile(AttachmentPath(id))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var r AttachmentRecord
	if err := json.Unmarshal(data, &r); err != nil {
		return nil, err
	}
	return &r, nil
}

func SaveAttachment(id string, r *AttachmentRecord) error {
	data, _ := json.MarshalIndent(r, "", "  ")
	return AtomicWrite(AttachmentPath(id), append(data, '\n'), 0644)
}

func DeleteAttachment(id string) error {
	return os.RemoveAll(filepath.Dir(AttachmentPath(id)))
}

func claudeBridgeBlock() string {
	return ClaudeMarkerStart + "\n@AGENTS.md\n" + ClaudeMarkerEnd + "\n"
}

// PlanClaudeBridge wires CLAUDE.md → AGENTS.md per the officially recommended
// import pattern. Canonical content lives ONLY in AGENTS.md — never duplicate
// the section here.
func PlanClaudeBridge(existing []byte, fileExists bool) (string, []byte, bool, bool) {
	if !fileExists {
		return ActionCreate, []byte(claudeBridgeBlock()), true, true
	}
	text := string(existing)
	for _, line := range strings.Split(text, "\n") {
		if strings.TrimSpace(line) == "@AGENTS.md" {
			return ActionUnchanged, nil, false, false
		}
	}
	updated := text
	if updated != "" && !strings.HasSuffix(updated, "\n") {
		updated += "\n"
	}
	updated += "\n" + claudeBridgeBlock()
	return ActionUpdate, []byte(updated), false, true
}

func PlanClaudeBridgeRemoval(existing []byte, fileExists bool, rec AttachmentChanges) (string, []byte) {
	if !fileExists || !rec.ClaudeImportAdded {
		return ActionUnchanged, nil
	}
	text := string(existing)
	start := strings.Index(text, ClaudeMarkerStart)
	end := strings.Index(text, ClaudeMarkerEnd)
	if start < 0 || end <= start {
		return ActionUnchanged, nil // user removed/rewrote it — leave alone
	}
	tail := strings.TrimPrefix(text[end+len(ClaudeMarkerEnd):], "\n")
	stripped := strings.TrimRight(text[:start], "\n")
	var remaining string
	switch {
	case stripped == "":
		remaining = tail
	case tail == "":
		remaining = stripped + "\n"
	default:
		remaining = stripped + "\n\n" + tail
	}
	if rec.ClaudeFileCreated && strings.TrimSpace(remaining) == "" {
		return ActionDelete, nil
	}
	return ActionUpdate, []byte(remaining)
}
