package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alexdx2/chronicle-core/internal/wiring"
)

func TestAttachThenDetachRoundtrip(t *testing.T) {
	t.Setenv("CHRONICLE_HOME", t.TempDir())
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, "AGENTS.md"), []byte("# Existing agents doc\n"), 0644)

	var out bytes.Buffer
	if err := runAttach(root, &out); err != nil {
		t.Fatal(err)
	}
	ag, _ := os.ReadFile(filepath.Join(root, "AGENTS.md"))
	if !strings.Contains(string(ag), wiring.AgentsMarkerStart) || !strings.Contains(string(ag), "# Existing agents doc") {
		t.Fatalf("AGENTS.md: %s", ag)
	}
	cl, _ := os.ReadFile(filepath.Join(root, "CLAUDE.md"))
	if !strings.Contains(string(cl), "@AGENTS.md") {
		t.Fatalf("CLAUDE.md: %s", cl)
	}
	settings, _ := os.ReadFile(filepath.Join(root, ".claude", "settings.json"))
	if !strings.Contains(string(settings), "hook fire") {
		t.Fatalf("settings.json: %s", settings)
	}
	if !strings.Contains(out.String(), "chronicle scan") {
		t.Fatalf("missing scan hint:\n%s", out.String())
	}

	out.Reset()
	if err := runDetach(root, &out); err != nil {
		t.Fatal(err)
	}
	ag2, _ := os.ReadFile(filepath.Join(root, "AGENTS.md"))
	if strings.Contains(string(ag2), wiring.AgentsMarkerStart) || !strings.Contains(string(ag2), "# Existing agents doc") {
		t.Fatalf("detached AGENTS.md: %s", ag2)
	}
	if _, err := os.Stat(filepath.Join(root, "CLAUDE.md")); !os.IsNotExist(err) {
		t.Fatal("chronicle-created CLAUDE.md should be deleted on detach")
	}
	settings2, _ := os.ReadFile(filepath.Join(root, ".claude", "settings.json"))
	if strings.Contains(string(settings2), "hook fire") {
		t.Fatalf("hook still present: %s", settings2)
	}
}

func TestDetachWarnsOnInvalidSettingsJSON(t *testing.T) {
	t.Setenv("CHRONICLE_HOME", t.TempDir())
	root := t.TempDir()

	var out bytes.Buffer
	if err := runAttach(root, &out); err != nil {
		t.Fatal(err)
	}
	id := wiring.ProjectID(root, gitRemote(root))

	settingsFile := filepath.Join(root, ".claude", "settings.json")
	if err := os.WriteFile(settingsFile, []byte("{ not valid json"), 0644); err != nil {
		t.Fatal(err)
	}

	out.Reset()
	if err := runDetach(root, &out); err != nil {
		t.Fatalf("runDetach should not abort on invalid settings.json, got: %v", err)
	}
	if !strings.Contains(out.String(), "not valid JSON") || !strings.Contains(out.String(), "left in place") {
		t.Fatalf("missing warning line:\n%s", out.String())
	}

	ag, _ := os.ReadFile(filepath.Join(root, "AGENTS.md"))
	if strings.Contains(string(ag), wiring.AgentsMarkerStart) {
		t.Fatalf("AGENTS.md chronicle section should still be removed: %s", ag)
	}

	settings, _ := os.ReadFile(settingsFile)
	if string(settings) != "{ not valid json" {
		t.Fatalf("corrupt settings.json should be left untouched: %s", settings)
	}

	rec, err := wiring.LoadAttachment(id)
	if err != nil {
		t.Fatal(err)
	}
	if rec != nil {
		t.Fatalf("attachment record should be deleted, got: %+v", rec)
	}
}

func TestAttachAbortsOnInvalidSettingsJSON(t *testing.T) {
	t.Setenv("CHRONICLE_HOME", t.TempDir())
	root := t.TempDir()

	settingsFile := filepath.Join(root, ".claude", "settings.json")
	if err := os.MkdirAll(filepath.Dir(settingsFile), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(settingsFile, []byte("{ not valid json"), 0644); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	err := runAttach(root, &out)
	if err == nil {
		t.Fatal("expected runAttach to abort on invalid settings.json")
	}
	if !strings.Contains(err.Error(), settingsFile) {
		t.Fatalf("error should mention settings path %q, got: %v", settingsFile, err)
	}

	if _, statErr := os.Stat(filepath.Join(root, "AGENTS.md")); !os.IsNotExist(statErr) {
		t.Fatal("AGENTS.md should not have been created — ApplyPlan never ran")
	}
	if _, statErr := os.Stat(filepath.Join(root, "CLAUDE.md")); !os.IsNotExist(statErr) {
		t.Fatal("CLAUDE.md should not have been created — ApplyPlan never ran")
	}
}

func TestReattachPreservesOwnershipRecord(t *testing.T) {
	t.Setenv("CHRONICLE_HOME", t.TempDir())
	root := t.TempDir()

	var out bytes.Buffer
	if err := runAttach(root, &out); err != nil {
		t.Fatal(err)
	}
	id := wiring.ProjectID(root, gitRemote(root))
	rec, err := wiring.LoadAttachment(id)
	if err != nil {
		t.Fatal(err)
	}
	if !rec.Changes.ClaudeFileCreated {
		t.Fatalf("expected ClaudeFileCreated=true after fresh attach, got record: %+v", rec)
	}

	out.Reset()
	if err := runAttach(root, &out); err != nil {
		t.Fatal(err)
	}
	rec2, err := wiring.LoadAttachment(id)
	if err != nil {
		t.Fatal(err)
	}
	if !rec2.Changes.ClaudeFileCreated {
		t.Fatalf("expected ClaudeFileCreated to remain true after re-attach, got record: %+v", rec2)
	}
}

func TestAttachIsIdempotent(t *testing.T) {
	t.Setenv("CHRONICLE_HOME", t.TempDir())
	root := t.TempDir()
	var out bytes.Buffer
	if err := runAttach(root, &out); err != nil {
		t.Fatal(err)
	}
	first, _ := os.ReadFile(filepath.Join(root, "CLAUDE.md"))
	if err := runAttach(root, &out); err != nil {
		t.Fatal(err)
	}
	second, _ := os.ReadFile(filepath.Join(root, "CLAUDE.md"))
	if string(first) != string(second) {
		t.Fatalf("attach not idempotent:\n%s\nvs\n%s", first, second)
	}
}
