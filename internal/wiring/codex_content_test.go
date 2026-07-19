package wiring

import (
	"strings"
	"testing"
)

// ─── planCodexMCPConfig ─────────────────────────────────────────────────────

func TestPlanCodexMCPConfigCreateAndUserManagedSkip(t *testing.T) {
	action, content := planCodexMCPConfig(nil, false, "/opt/bin/chronicle")
	if action != ActionCreate || !strings.Contains(string(content), `command = "/opt/bin/chronicle"`) {
		t.Fatalf("action=%s content=%s", action, content)
	}
	user := []byte("[mcp_servers.chronicle]\ncommand = \"mine\"\n")
	action2, content2 := planCodexMCPConfig(user, true, "/opt/bin/chronicle")
	if action2 != ActionSkip || content2 != nil {
		t.Fatalf("user-managed block must be skipped: %s", action2)
	}
}

// TestPlanCodexMCPConfigCreatesConfigWhenMissing ports
// TestUpsertCodexMCPConfig_CreatesConfigWhenMissing.
func TestPlanCodexMCPConfigCreatesConfigWhenMissing(t *testing.T) {
	action, content := planCodexMCPConfig(nil, false, "/usr/local/bin/chronicle")
	if action != ActionCreate {
		t.Errorf("action = %q; want %q", action, ActionCreate)
	}

	got := string(content)
	for _, want := range []string{
		"[mcp_servers.chronicle]",
		`command = "/usr/local/bin/chronicle"`,
		`args = ["mcp", "serve"]`,
		"startup_timeout_sec",
		// Without auto-approval Codex cancels every MCP call in non-interactive
		// runs ("user cancelled MCP tool call") — field-tested 2026-07-04.
		`default_tools_approval_mode = "approve"`,
		codexSentinelStart,
		codexSentinelEnd,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("config missing %q:\n%s", want, got)
		}
	}
}

// TestPlanCodexMCPConfigAppendsPreservingExistingServers ports
// TestUpsertCodexMCPConfig_AppendsPreservingExistingServers.
func TestPlanCodexMCPConfigAppendsPreservingExistingServers(t *testing.T) {
	existing := []byte("[mcp_servers.playwright]\ncommand = \"npx\"\n")

	action, content := planCodexMCPConfig(existing, true, "/bin/chronicle")
	if action != ActionUpdate {
		t.Errorf("action = %q; want %q", action, ActionUpdate)
	}

	got := string(content)
	if !strings.Contains(got, "[mcp_servers.playwright]") {
		t.Errorf("existing server lost:\n%s", got)
	}
	if !strings.Contains(got, "[mcp_servers.chronicle]") {
		t.Errorf("chronicle server not added:\n%s", got)
	}
}

// TestPlanCodexMCPConfigReplacesSentinelBlock ports
// TestUpsertCodexMCPConfig_ReplacesSentinelBlock.
func TestPlanCodexMCPConfigReplacesSentinelBlock(t *testing.T) {
	existing := []byte("[mcp_servers.other]\ncommand = \"x\"\n\n" +
		codexSentinelStart + "\n[mcp_servers.chronicle]\ncommand = \"/old/path\"\n" + codexSentinelEnd + "\n")

	action, content := planCodexMCPConfig(existing, true, "/new/path")
	if action != ActionUpdate {
		t.Errorf("action = %q; want %q", action, ActionUpdate)
	}

	got := string(content)
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

// TestPlanCodexMCPConfigSkipsUserManagedSection ports
// TestUpsertCodexMCPConfig_SkipsUserManagedSection.
func TestPlanCodexMCPConfigSkipsUserManagedSection(t *testing.T) {
	userConfig := []byte("[mcp_servers.chronicle]\ncommand = \"my-custom-wrapper\"\n")

	action, content := planCodexMCPConfig(userConfig, true, "/bin/chronicle")
	if action != ActionSkip {
		t.Errorf("action = %q; want %q", action, ActionSkip)
	}
	if content != nil {
		t.Errorf("content = %v; want nil on skip (user-managed config must not be touched)", content)
	}
}

// TestPlanCodexMCPConfigUnchangedOnSecondRun ports
// TestUpsertCodexMCPConfig_UnchangedOnSecondRun.
func TestPlanCodexMCPConfigUnchangedOnSecondRun(t *testing.T) {
	_, first := planCodexMCPConfig(nil, false, "/bin/chronicle")

	action, _ := planCodexMCPConfig(first, true, "/bin/chronicle")
	if action != ActionUnchanged {
		t.Errorf("action = %q; want %q", action, ActionUnchanged)
	}
}

// ─── planCodexSessionHook ───────────────────────────────────────────────────

func TestPlanCodexSessionHookIdempotent(t *testing.T) {
	action, content := planCodexSessionHook(nil, false)
	if action != ActionCreate {
		t.Fatalf("action=%s", action)
	}
	action2, _ := planCodexSessionHook(content, true)
	if action2 != ActionUnchanged {
		t.Fatalf("second run action=%s", action2)
	}
}

// TestPlanCodexSessionHookCreatesBlock ports
// TestUpsertCodexSessionHook_CreatesBlock.
func TestPlanCodexSessionHookCreatesBlock(t *testing.T) {
	existing := []byte("[mcp_servers.other]\ncommand = \"x\"\n")

	action, content := planCodexSessionHook(existing, true)
	if action != ActionUpdate {
		t.Errorf("action = %q; want %q", action, ActionUpdate)
	}

	got := string(content)
	for _, want := range []string{
		"[[hooks.SessionStart]]",
		`matcher = "startup|resume|clear|compact"`,
		"chronicle_command", // the reminder must re-prime the entry point
		codexHookSentinelStart,
		codexHookSentinelEnd,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("hook block missing %q:\n%s", want, got)
		}
	}
	if !strings.Contains(got, "[mcp_servers.other]") {
		t.Errorf("existing config lost:\n%s", got)
	}
	// TOML single-quoted literal — the command must contain no single quotes.
	for _, line := range strings.Split(got, "\n") {
		if strings.HasPrefix(line, "command = '") && strings.Count(line, "'") != 2 {
			t.Errorf("hook command must not contain inner single quotes: %s", line)
		}
	}
}

// TestPlanCodexSessionHookIdempotentOnRealConfig ports
// TestUpsertCodexSessionHook_Idempotent.
func TestPlanCodexSessionHookIdempotentOnRealConfig(t *testing.T) {
	_, first := planCodexSessionHook(nil, false)

	action, _ := planCodexSessionHook(first, true)
	if action != ActionUnchanged {
		t.Errorf("action = %q; want %q", action, ActionUnchanged)
	}
	got := string(first)
	if strings.Count(got, "[[hooks.SessionStart]]") != 1 {
		t.Errorf("want exactly one hook block:\n%s", got)
	}
}

// ─── removeSentinelBlock ────────────────────────────────────────────────────

func TestRemoveSentinelBlock(t *testing.T) {
	text := "keep\n\n" + codexSentinelStart + "\n[mcp_servers.chronicle]\ncommand = \"x\"\n" + codexSentinelEnd + "\nkeep2\n"
	got := removeSentinelBlock(text, codexSentinelStart, codexSentinelEnd)
	if strings.Contains(got, codexSentinelStart) || strings.Contains(got, "[mcp_servers.chronicle]") {
		t.Fatalf("sentinel block not removed:\n%s", got)
	}
	if !strings.Contains(got, "keep") || !strings.Contains(got, "keep2") {
		t.Fatalf("surrounding content lost:\n%s", got)
	}
}

func TestRemoveSentinelBlockNoMatchReturnsUnchanged(t *testing.T) {
	text := "no sentinels here\n"
	got := removeSentinelBlock(text, codexSentinelStart, codexSentinelEnd)
	if got != text {
		t.Fatalf("expected unchanged text, got %q", got)
	}
}

// ─── CodexPrompts ───────────────────────────────────────────────────────────

// TestCodexPrompts_CoversPromptFiles ports
// TestWriteCodexPrompts_CreatesPromptFiles (content-only now; the CLI layer
// handles writing the files to disk).
func TestCodexPrompts_CoversPromptFiles(t *testing.T) {
	prompts := CodexPrompts()
	if len(prompts) == 0 {
		t.Fatal("no prompts defined")
	}

	for _, name := range []string{"chronicle-scan.md", "chronicle-status.md", "chronicle-impact.md", "chronicle-help.md"} {
		content, ok := prompts[name]
		if !ok {
			t.Fatalf("missing prompt %s", name)
		}
		if !strings.Contains(content, "chronicle_command") {
			t.Errorf("%s must route through chronicle_command:\n%s", name, content)
		}
	}
}
