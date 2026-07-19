package cli

// Command-level tests for `chronicle setup` / `chronicle setup codex`.
// Content and splice-logic tests (marker sections, codex sentinel blocks,
// SessionStart hook, prompts) moved to internal/wiring/sections_test.go and
// internal/wiring/codex_content_test.go as pure planner tests.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ─── ensureDepbotDir writes project AGENTS.md ───────────────────────────────

func TestEnsureDepbotDir_WritesProjectAgentsMD(t *testing.T) {
	tmp := t.TempDir()
	t.Chdir(tmp)

	oldDB, oldManifest := dbPath, manifestPath
	t.Cleanup(func() { dbPath, manifestPath = oldDB, oldManifest })
	dbPath = filepath.Join(tmp, ".depbot", "chronicle.db")
	manifestPath = filepath.Join(tmp, ".depbot", "chronicle.domain.yaml")

	ensureDepbotDir()

	got := readFileOrFail(t, filepath.Join(tmp, "AGENTS.md"))
	if !strings.Contains(got, "<!-- chronicle:start -->") {
		t.Errorf("AGENTS.md missing chronicle markers:\n%s", got)
	}
	if !strings.Contains(got, "chronicle_command") {
		t.Errorf("AGENTS.md missing entry-point guidance:\n%s", got)
	}

	// CLAUDE.md behavior unchanged
	if _, err := os.Stat(filepath.Join(tmp, "CLAUDE.md")); err != nil {
		t.Errorf("CLAUDE.md not created: %v", err)
	}
}

// ─── setup command registration ─────────────────────────────────────────────

func TestSetupCommandRegistered(t *testing.T) {
	root := NewRootCmd()
	for _, c := range root.Commands() {
		if c.Name() == "setup" {
			for _, sub := range c.Commands() {
				if sub.Name() == "codex" {
					return
				}
			}
			t.Fatal("setup command has no codex subcommand")
		}
	}
	t.Fatal("setup command not registered on root")
}

// ─── helpers ────────────────────────────────────────────────────────────────

func readFileOrFail(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(b)
}
