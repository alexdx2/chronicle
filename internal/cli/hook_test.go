package cli

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestMergeHookIntoSettings_Empty(t *testing.T) {
	out, changed, err := mergeHookIntoSettings(nil, "Grep|Glob|Read", "chronicle hook fire")
	if err != nil {
		t.Fatalf("merge: %v", err)
	}
	if !changed {
		t.Fatal("expected changed=true on empty settings")
	}
	var parsed map[string]any
	if err := json.Unmarshal(out, &parsed); err != nil {
		t.Fatalf("output not valid JSON: %v\n%s", err, out)
	}
	hooks, ok := parsed["hooks"].(map[string]any)
	if !ok {
		t.Fatalf("hooks key missing: %v", parsed)
	}
	pre, ok := hooks["PreToolUse"].([]any)
	if !ok || len(pre) != 1 {
		t.Fatalf("expected one PreToolUse entry, got %v", hooks["PreToolUse"])
	}
	entry := pre[0].(map[string]any)
	if entry["matcher"] != "Grep|Glob|Read" {
		t.Errorf("matcher mismatch: %v", entry["matcher"])
	}
}

func TestMergeHookIntoSettings_Idempotent(t *testing.T) {
	out1, _, err := mergeHookIntoSettings(nil, "Grep|Glob|Read", "chronicle hook fire")
	if err != nil {
		t.Fatal(err)
	}
	out2, changed, err := mergeHookIntoSettings(out1, "Grep|Glob|Read", "chronicle hook fire")
	if err != nil {
		t.Fatal(err)
	}
	if changed {
		t.Fatal("second install must be a no-op (changed=false)")
	}
	if string(out1) != string(out2) {
		t.Errorf("idempotent merge changed bytes:\n%s\nvs\n%s", out1, out2)
	}
}

func TestMergeHookIntoSettings_PreservesOtherKeys(t *testing.T) {
	existing := []byte(`{"model":"opus","permissions":{"allow":["Bash"]},"hooks":{"PostToolUse":[{"matcher":"Edit","hooks":[]}]}}`)
	out, changed, err := mergeHookIntoSettings(existing, "Grep|Glob|Read", "chronicle hook fire")
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Fatal("expected changed=true")
	}
	var parsed map[string]any
	if err := json.Unmarshal(out, &parsed); err != nil {
		t.Fatal(err)
	}
	if parsed["model"] != "opus" {
		t.Error("model key lost")
	}
	if parsed["permissions"] == nil {
		t.Error("permissions key lost")
	}
	hooks := parsed["hooks"].(map[string]any)
	if hooks["PostToolUse"] == nil {
		t.Error("existing PostToolUse hook lost")
	}
	if hooks["PreToolUse"] == nil {
		t.Error("PreToolUse not added")
	}
}

func TestRemoveHookFromSettings(t *testing.T) {
	installed, _, _ := mergeHookIntoSettings(
		[]byte(`{"model":"opus"}`), "Grep|Glob|Read", "chronicle hook fire")
	out, changed, err := removeHookFromSettings(installed, "chronicle hook fire")
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Fatal("expected removal to change settings")
	}
	if strings.Contains(string(out), "chronicle hook fire") {
		t.Errorf("chronicle hook not removed: %s", out)
	}
	var parsed map[string]any
	if err := json.Unmarshal(out, &parsed); err != nil {
		t.Fatal(err)
	}
	if parsed["model"] != "opus" {
		t.Error("unrelated key lost during removal")
	}
}

func TestRemoveHookFromSettings_PreservesForeignPreToolUse(t *testing.T) {
	existing := []byte(`{"hooks":{"PreToolUse":[{"matcher":"Bash","hooks":[{"type":"command","command":"other-tool"}]}]}}`)
	installed, _, _ := mergeHookIntoSettings(existing, "Grep|Glob|Read", "chronicle hook fire")
	out, _, err := removeHookFromSettings(installed, "chronicle hook fire")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "other-tool") {
		t.Errorf("foreign PreToolUse hook must survive removal: %s", out)
	}
	if strings.Contains(string(out), "chronicle hook fire") {
		t.Errorf("chronicle hook should be gone: %s", out)
	}
}
