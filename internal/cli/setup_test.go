package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ─── upsertMarkedSection ────────────────────────────────────────────────────

func TestUpsertMarkedSection_CreatesFileWhenMissing(t *testing.T) {
	path := filepath.Join(t.TempDir(), "AGENTS.md")

	changed, err := upsertMarkedSection(path, "hello section")
	if err != nil {
		t.Fatalf("upsertMarkedSection: %v", err)
	}
	if !changed {
		t.Error("changed = false; want true for new file")
	}

	got := readFileOrFail(t, path)
	if !strings.Contains(got, agentsMarkerStart) || !strings.Contains(got, agentsMarkerEnd) {
		t.Errorf("missing markers in:\n%s", got)
	}
	if !strings.Contains(got, "hello section") {
		t.Errorf("missing content in:\n%s", got)
	}
}

func TestUpsertMarkedSection_AppendsPreservingUserContent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "AGENTS.md")
	userContent := "# My project rules\n\nAlways use tabs.\n"
	writeFileOrFail(t, path, userContent)

	changed, err := upsertMarkedSection(path, "chronicle stuff")
	if err != nil {
		t.Fatalf("upsertMarkedSection: %v", err)
	}
	if !changed {
		t.Error("changed = false; want true when section is added")
	}

	got := readFileOrFail(t, path)
	if !strings.Contains(got, "Always use tabs.") {
		t.Errorf("user content lost:\n%s", got)
	}
	if !strings.Contains(got, "chronicle stuff") {
		t.Errorf("section not appended:\n%s", got)
	}
	if strings.Index(got, "Always use tabs.") > strings.Index(got, agentsMarkerStart) {
		t.Errorf("section should be appended after user content:\n%s", got)
	}
}

func TestUpsertMarkedSection_ReplacesExistingSection(t *testing.T) {
	path := filepath.Join(t.TempDir(), "AGENTS.md")
	writeFileOrFail(t, path,
		"# Before\n\n"+agentsMarkerStart+"\nOLD CONTENT\n"+agentsMarkerEnd+"\n\n# After\n")

	changed, err := upsertMarkedSection(path, "NEW CONTENT")
	if err != nil {
		t.Fatalf("upsertMarkedSection: %v", err)
	}
	if !changed {
		t.Error("changed = false; want true when section content differs")
	}

	got := readFileOrFail(t, path)
	if strings.Contains(got, "OLD CONTENT") {
		t.Errorf("old section not replaced:\n%s", got)
	}
	if !strings.Contains(got, "NEW CONTENT") {
		t.Errorf("new section missing:\n%s", got)
	}
	if !strings.Contains(got, "# Before") || !strings.Contains(got, "# After") {
		t.Errorf("surrounding user content lost:\n%s", got)
	}
	if strings.Count(got, agentsMarkerStart) != 1 {
		t.Errorf("want exactly one start marker:\n%s", got)
	}
}

func TestUpsertMarkedSection_IdempotentWhenUnchanged(t *testing.T) {
	path := filepath.Join(t.TempDir(), "AGENTS.md")

	if _, err := upsertMarkedSection(path, "same content"); err != nil {
		t.Fatalf("first upsert: %v", err)
	}
	first := readFileOrFail(t, path)

	changed, err := upsertMarkedSection(path, "same content")
	if err != nil {
		t.Fatalf("second upsert: %v", err)
	}
	if changed {
		t.Error("changed = true on identical content; want false")
	}
	if got := readFileOrFail(t, path); got != first {
		t.Errorf("content drifted between identical upserts:\n%s\nvs\n%s", first, got)
	}
}

// ─── upsertCodexMCPConfig ───────────────────────────────────────────────────

func TestUpsertCodexMCPConfig_CreatesConfigWhenMissing(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")

	action, err := upsertCodexMCPConfig(path, "/usr/local/bin/chronicle")
	if err != nil {
		t.Fatalf("upsertCodexMCPConfig: %v", err)
	}
	if action != setupCreated {
		t.Errorf("action = %q; want %q", action, setupCreated)
	}

	got := readFileOrFail(t, path)
	for _, want := range []string{
		"[mcp_servers.chronicle]",
		`command = "/usr/local/bin/chronicle"`,
		`args = ["mcp", "serve"]`,
		"startup_timeout_sec",
		codexSentinelStart,
		codexSentinelEnd,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("config missing %q:\n%s", want, got)
		}
	}
}

func TestUpsertCodexMCPConfig_AppendsPreservingExistingServers(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	writeFileOrFail(t, path, "[mcp_servers.playwright]\ncommand = \"npx\"\n")

	action, err := upsertCodexMCPConfig(path, "/bin/chronicle")
	if err != nil {
		t.Fatalf("upsertCodexMCPConfig: %v", err)
	}
	if action != setupUpdated {
		t.Errorf("action = %q; want %q", action, setupUpdated)
	}

	got := readFileOrFail(t, path)
	if !strings.Contains(got, "[mcp_servers.playwright]") {
		t.Errorf("existing server lost:\n%s", got)
	}
	if !strings.Contains(got, "[mcp_servers.chronicle]") {
		t.Errorf("chronicle server not added:\n%s", got)
	}
}

func TestUpsertCodexMCPConfig_ReplacesSentinelBlock(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	writeFileOrFail(t, path, "[mcp_servers.other]\ncommand = \"x\"\n\n"+
		codexSentinelStart+"\n[mcp_servers.chronicle]\ncommand = \"/old/path\"\n"+codexSentinelEnd+"\n")

	action, err := upsertCodexMCPConfig(path, "/new/path")
	if err != nil {
		t.Fatalf("upsertCodexMCPConfig: %v", err)
	}
	if action != setupUpdated {
		t.Errorf("action = %q; want %q", action, setupUpdated)
	}

	got := readFileOrFail(t, path)
	if strings.Contains(got, "/old/path") {
		t.Errorf("old binary path not replaced:\n%s", got)
	}
	if !strings.Contains(got, `command = "/new/path"`) {
		t.Errorf("new binary path missing:\n%s", got)
	}
	if strings.Count(got, "[mcp_servers.chronicle]") != 1 {
		t.Errorf("want exactly one chronicle section:\n%s", got)
	}
	if !strings.Contains(got, "[mcp_servers.other]") {
		t.Errorf("other server lost:\n%s", got)
	}
}

func TestUpsertCodexMCPConfig_SkipsUserManagedSection(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	userConfig := "[mcp_servers.chronicle]\ncommand = \"my-custom-wrapper\"\n"
	writeFileOrFail(t, path, userConfig)

	action, err := upsertCodexMCPConfig(path, "/bin/chronicle")
	if err != nil {
		t.Fatalf("upsertCodexMCPConfig: %v", err)
	}
	if action != setupSkipped {
		t.Errorf("action = %q; want %q", action, setupSkipped)
	}
	if got := readFileOrFail(t, path); got != userConfig {
		t.Errorf("user-managed config modified:\n%s", got)
	}
}

func TestUpsertCodexMCPConfig_UnchangedOnSecondRun(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")

	if _, err := upsertCodexMCPConfig(path, "/bin/chronicle"); err != nil {
		t.Fatalf("first run: %v", err)
	}
	action, err := upsertCodexMCPConfig(path, "/bin/chronicle")
	if err != nil {
		t.Fatalf("second run: %v", err)
	}
	if action != setupUnchanged {
		t.Errorf("action = %q; want %q", action, setupUnchanged)
	}
}

// ─── instruction content ────────────────────────────────────────────────────

func TestProjectAgentsSection_CoversEntryPointAndScanGating(t *testing.T) {
	content := projectAgentsSection()

	for _, want := range []string{
		"chronicle_command",      // the workflow entry point
		"chronicle_node_search",  // query surface
		"chronicle_impact",       // query surface
		"chronicle_query_deps",   // query surface
		"chronicle_scan_",        // scan pipeline warning must name the prefix
		"Claude Code",            // scan gating: where scans run today
	} {
		if !strings.Contains(content, want) {
			t.Errorf("project AGENTS section missing %q", want)
		}
	}
	if strings.Contains(content, agentsMarkerStart) {
		t.Error("section body must not embed its own markers")
	}
}

func TestGlobalAgentsSection_IsProjectAgnostic(t *testing.T) {
	content := globalAgentsSection()
	if !strings.Contains(content, "chronicle_command") {
		t.Error("global AGENTS section must name chronicle_command as entry point")
	}
	if !strings.Contains(content, ".depbot") {
		t.Error("global AGENTS section should tell agents how to recognize a Chronicle project (.depbot)")
	}
}

// ─── Codex custom prompts ───────────────────────────────────────────────────

func TestWriteCodexPrompts_CreatesPromptFiles(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "prompts")

	written, err := writeCodexPrompts(dir)
	if err != nil {
		t.Fatalf("writeCodexPrompts: %v", err)
	}
	if written == 0 {
		t.Fatal("no prompt files written")
	}

	for _, name := range []string{"chronicle-scan.md", "chronicle-status.md", "chronicle-impact.md", "chronicle-help.md"} {
		content := readFileOrFail(t, filepath.Join(dir, name))
		if !strings.Contains(content, "chronicle_command") {
			t.Errorf("%s must route through chronicle_command:\n%s", name, content)
		}
	}
}

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
	if !strings.Contains(got, agentsMarkerStart) {
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

func writeFileOrFail(t *testing.T, path string, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
