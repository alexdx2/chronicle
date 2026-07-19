package wiring

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func testCodexAdapter(t *testing.T, dir string, found bool) *CodexAdapter {
	t.Helper()
	a := NewCodexAdapter()
	a.CodexDir = dir
	a.BinaryPath = "/opt/chronicle/bin/chronicle"
	a.LookPath = func(string) (string, error) {
		if found {
			return "/usr/bin/codex", nil
		}
		return "", errors.New("not found")
	}
	return a
}

func TestCodexDetectTiers(t *testing.T) {
	empty := t.TempDir()
	if got := testCodexAdapter(t, filepath.Join(empty, "nope"), false).Detect().Confidence; got != DetectionAbsent {
		t.Fatalf("absent: %s", got)
	}
	if got := testCodexAdapter(t, empty, false).Detect().Confidence; got != DetectionStale {
		t.Fatalf("stale (empty dir): %s", got)
	}
	withCfg := t.TempDir()
	os.WriteFile(filepath.Join(withCfg, "config.toml"), []byte("x = 1\n"), 0644)
	if got := testCodexAdapter(t, withCfg, false).Detect().Confidence; got != DetectionProbable {
		t.Fatalf("probable: %s", got)
	}
	if got := testCodexAdapter(t, withCfg, true).Detect().Confidence; got != DetectionConfirmed {
		t.Fatalf("confirmed: %s", got)
	}
}

func TestCodexPlanFreshInstall(t *testing.T) {
	a := testCodexAdapter(t, filepath.Join(t.TempDir(), ".codex"), true)
	p, err := a.Plan()
	if err != nil {
		t.Fatal(err)
	}
	// config.toml (MCP block + SessionStart hook merged into one write, since
	// both target the same file) + global AGENTS.md + 5 prompts = 7 changes
	if len(p.Changes) != 7 {
		t.Fatalf("changes = %d: %+v", len(p.Changes), p.Changes)
	}
	var sawConfig, sawPrompt bool
	for _, c := range p.Changes {
		if strings.HasSuffix(c.Target, "config.toml") && c.Ownership != OwnershipUser {
			t.Fatal("config.toml must be user-owned")
		}
		if strings.HasSuffix(c.Target, "config.toml") {
			sawConfig = true
			if c.Action == ActionCreate && !strings.Contains(string(c.NewContent), a.BinaryPath) {
				t.Fatal("config must reference canonical binary")
			}
		}
		if strings.Contains(c.Target, "prompts") {
			sawPrompt = true
			if c.Ownership != OwnershipChronicle {
				t.Fatal("prompts are chronicle-owned")
			}
		}
	}
	if !sawConfig || !sawPrompt {
		t.Fatalf("missing targets: %+v", p.Changes)
	}
}

func TestCodexPlanIdempotentAfterApply(t *testing.T) {
	t.Setenv("CHRONICLE_HOME", t.TempDir())
	a := testCodexAdapter(t, filepath.Join(t.TempDir(), ".codex"), true)
	p, _ := a.Plan()
	if _, err := ApplyPlan(p); err != nil {
		t.Fatal(err)
	}
	p2, _ := a.Plan()
	for _, c := range p2.Changes {
		if c.Mutating() && c.Ownership == OwnershipUser {
			t.Fatalf("second plan still mutates user file %s (%s)", c.Target, c.Action)
		}
	}
}

func TestCodexVerifyTiers(t *testing.T) {
	a := testCodexAdapter(t, filepath.Join(t.TempDir(), ".codex"), true)
	if v := a.Verify(); v.Level != VerifyNone || v.Health != HealthNotInstalled {
		t.Fatalf("pre-install verify: %+v", v)
	}
	t.Setenv("CHRONICLE_HOME", t.TempDir())
	p, _ := a.Plan()
	ApplyPlan(p)
	v := a.Verify()
	if v.Level != VerifyFile { // BinaryPath doesn't exist in tests → not reachable
		t.Fatalf("post-install verify: %+v", v)
	}
	if v.Health != HealthInstalled {
		t.Fatalf("health: %+v", v)
	}
}

func TestCodexRemovePlan(t *testing.T) {
	t.Setenv("CHRONICLE_HOME", t.TempDir())
	a := testCodexAdapter(t, filepath.Join(t.TempDir(), ".codex"), true)
	p, _ := a.Plan()
	ApplyPlan(p)
	rp, err := a.Remove()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ApplyPlan(rp); err != nil {
		t.Fatal(err)
	}
	cfg, _ := os.ReadFile(filepath.Join(a.CodexDir, "config.toml"))
	if strings.Contains(string(cfg), "chronicle") {
		t.Fatalf("config still references chronicle: %s", cfg)
	}
	if _, err := os.Stat(filepath.Join(a.CodexDir, "prompts", "chronicle-scan.md")); !os.IsNotExist(err) {
		t.Fatal("prompt not deleted")
	}
}
