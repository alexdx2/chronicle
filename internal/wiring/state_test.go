package wiring

import (
	"path/filepath"
	"testing"
)

func TestHomeRespectsEnvOverride(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CHRONICLE_HOME", dir)
	if Home() != dir {
		t.Fatalf("Home() = %q, want %q", Home(), dir)
	}
	if StatePath() != filepath.Join(dir, "install-state.json") {
		t.Fatalf("StatePath() = %q", StatePath())
	}
}

func TestLoadStateMissingFileReturnsEmptyState(t *testing.T) {
	t.Setenv("CHRONICLE_HOME", t.TempDir())
	st, err := LoadState()
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}
	if st.SchemaVersion != 1 || st.Agents == nil || len(st.Agents) != 0 {
		t.Fatalf("unexpected empty state: %+v", st)
	}
}

func TestStateRoundtrip(t *testing.T) {
	t.Setenv("CHRONICLE_HOME", t.TempDir())
	st := &State{SchemaVersion: 1, ChronicleVersion: "test-fp", Agents: map[string]AgentState{
		"codex": {
			AdapterVersion: 1, InstalledAt: "2026-07-18T00:00:00Z",
			Detection: string(DetectionConfirmed), Installation: string(HealthInstalled),
			Verification: string(VerifyFile), Health: string(HealthInstalled),
			Artifacts:     []Artifact{{Path: "/x/prompts/a.md", Ownership: "chronicle", Hash: "abc"}},
			ConfigChanges: []ConfigChange{{Path: "/x/config.toml", Key: "mcp_servers.chronicle", Marker: "# >>> chronicle mcp >>>"}},
		},
	}}
	if err := SaveState(st); err != nil {
		t.Fatalf("SaveState: %v", err)
	}
	got, err := LoadState()
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}
	if got.Agents["codex"].Artifacts[0].Hash != "abc" || got.ChronicleVersion != "test-fp" {
		t.Fatalf("roundtrip mismatch: %+v", got)
	}
}
