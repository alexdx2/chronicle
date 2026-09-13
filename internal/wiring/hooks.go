package wiring

// The PreToolUse hook entry in .claude/settings.json — planned as a pure byte
// transform so every host that installs it (chronicle attach, chronicle hook
// install, and embedders that wire a root of their own) writes the same shape
// and shares the idempotency rule.

import (
	"encoding/json"
	"fmt"
	"strings"
)

// HookMatcher is the tool matcher the freshness nudge fires on: the file-first
// tools an agent reaches for when it should be asking the graph instead.
const HookMatcher = "Grep|Glob|Read"

// hookFireMarker is the stable substring identifying a chronicle hook command
// across the absolute-path variations baked in at install time.
const hookFireMarker = "hook fire"

// MergeHookIntoSettings adds the PreToolUse command hook to a settings.json
// body, preserving everything else in the file. It is idempotent: a settings
// file that already carries a chronicle hook entry comes back unchanged
// (changed=false), whatever binary path that entry names.
func MergeHookIntoSettings(existing []byte, matcher, command string) ([]byte, bool, error) {
	root := map[string]any{}
	if len(existing) > 0 {
		if err := json.Unmarshal(existing, &root); err != nil {
			return nil, false, fmt.Errorf("settings.json is not valid JSON: %w", err)
		}
	}
	hooks, _ := root["hooks"].(map[string]any)
	if hooks == nil {
		hooks = map[string]any{}
	}
	pre, _ := hooks["PreToolUse"].([]any)

	// Idempotency: a chronicle hook fire entry already present → no-op.
	for _, e := range pre {
		if EntryHasChronicleHook(e) {
			return existing, false, nil
		}
	}

	pre = append(pre, map[string]any{
		"matcher": matcher,
		"hooks": []any{
			map[string]any{"type": "command", "command": command},
		},
	})
	hooks["PreToolUse"] = pre
	root["hooks"] = hooks

	out, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		return nil, false, err
	}
	return append(out, '\n'), true, nil
}

// EntryHasChronicleHook reports whether a PreToolUse entry contains a command
// hook pointing at "<some chronicle binary> hook fire".
func EntryHasChronicleHook(e any) bool {
	m, ok := e.(map[string]any)
	if !ok {
		return false
	}
	inner, _ := m["hooks"].([]any)
	for _, h := range inner {
		hm, ok := h.(map[string]any)
		if !ok {
			continue
		}
		if cmd, _ := hm["command"].(string); strings.Contains(cmd, hookFireMarker) {
			return true
		}
	}
	return false
}
