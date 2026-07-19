package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alexdx2/chronicle-core/internal/wiring"
)

func testCodexHome(t *testing.T) string {
	t.Helper()
	t.Setenv("CHRONICLE_HOME", t.TempDir())
	codexDir := filepath.Join(t.TempDir(), ".codex")
	os.MkdirAll(codexDir, 0755)
	os.WriteFile(filepath.Join(codexDir, "config.toml"), []byte("existing = true\n"), 0644)
	return codexDir
}

func TestRunSetupDryRunWritesNothing(t *testing.T) {
	codexDir := testCodexHome(t)
	var out bytes.Buffer
	err := runSetup(setupOptions{agents: []string{"codex"}, dryRun: true, codexDirOverride: codexDir}, &out)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "config.toml") {
		t.Fatalf("dry-run must list targets:\n%s", out.String())
	}
	cfg, _ := os.ReadFile(filepath.Join(codexDir, "config.toml"))
	if strings.Contains(string(cfg), "chronicle") {
		t.Fatal("dry-run wrote to config")
	}
	if _, err := os.Stat(wiring.StatePath()); !os.IsNotExist(err) {
		t.Fatal("dry-run wrote state")
	}
}

func TestRunSetupYesAppliesAndRecordsState(t *testing.T) {
	codexDir := testCodexHome(t)
	var out bytes.Buffer
	err := runSetup(setupOptions{agents: []string{"codex"}, yes: true, codexDirOverride: codexDir}, &out)
	if err != nil {
		t.Fatal(err)
	}
	cfg, _ := os.ReadFile(filepath.Join(codexDir, "config.toml"))
	if !strings.Contains(string(cfg), "[mcp_servers.chronicle]") || !strings.Contains(string(cfg), "existing = true") {
		t.Fatalf("config not spliced correctly:\n%s", cfg)
	}
	st, err := wiring.LoadState()
	if err != nil {
		t.Fatal(err)
	}
	ag, ok := st.Agents["codex"]
	if !ok || ag.Installation != string(wiring.HealthInstalled) || len(ag.Artifacts) == 0 {
		t.Fatalf("state = %+v", st)
	}
}

func TestRunSetupUnknownAgent(t *testing.T) {
	t.Setenv("CHRONICLE_HOME", t.TempDir())
	var out bytes.Buffer
	err := runSetup(setupOptions{agents: []string{"emacs"}, yes: true}, &out)
	if err == nil || !strings.Contains(err.Error(), "unknown agent") {
		t.Fatalf("err = %v", err)
	}
}

func decodeSetupReport(t *testing.T, raw []byte) []struct {
	ID string `json:"id"`
} {
	t.Helper()
	var report struct {
		Agents []struct {
			ID string `json:"id"`
		} `json:"agents"`
	}
	if err := json.Unmarshal(raw, &report); err != nil {
		t.Fatalf("stdout not pure JSON: %v\nstdout:\n%s", err, string(raw))
	}
	return report.Agents
}

func TestRunSetupJSONStdoutIsPureJSON(t *testing.T) {
	dir := testCodexHome(t)
	var out, humanBuf bytes.Buffer
	err := runSetup(setupOptions{agents: []string{"codex"}, yes: true, jsonOut: true, codexDirOverride: dir, errOut: &humanBuf}, &out)
	if err != nil {
		t.Fatal(err)
	}
	agents := decodeSetupReport(t, out.Bytes())
	if len(agents) == 0 || agents[0].ID != "codex" {
		t.Fatalf("report.agents[0].id = %+v, want codex", agents)
	}
	if humanBuf.Len() == 0 {
		t.Fatal("expected progress output on errOut, got none")
	}
}

func TestRunSetupJSONDryRunStdoutIsPureJSON(t *testing.T) {
	dir := testCodexHome(t)
	var out, humanBuf bytes.Buffer
	err := runSetup(setupOptions{agents: []string{"codex"}, dryRun: true, jsonOut: true, codexDirOverride: dir, errOut: &humanBuf}, &out)
	if err != nil {
		t.Fatal(err)
	}
	agents := decodeSetupReport(t, out.Bytes())
	if len(agents) == 0 || agents[0].ID != "codex" {
		t.Fatalf("report.agents[0].id = %+v, want codex", agents)
	}
	if humanBuf.Len() == 0 {
		t.Fatal("expected progress output on errOut, got none")
	}
}
