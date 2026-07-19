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
