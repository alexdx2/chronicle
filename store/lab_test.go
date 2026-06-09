package store

import (
	"path/filepath"
	"testing"
)

func newLabTestStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestLabConfig_RoundTrip(t *testing.T) {
	s := newLabTestStore(t)
	revID, err := s.CreateRevision("tom-and-jerry__lab1", "", "abc", "manual", "full",
		`{"lab":{"autopilot":true,"answers":{"phase1_summary":"skip flows"},"base_domain":"tom-and-jerry"}}`)
	if err != nil {
		t.Fatalf("CreateRevision: %v", err)
	}
	cfg, err := s.LabConfigForRevision(revID)
	if err != nil {
		t.Fatalf("LabConfigForRevision: %v", err)
	}
	if !cfg.Autopilot || cfg.BaseDomain != "tom-and-jerry" || cfg.Answers["phase1_summary"] != "skip flows" {
		t.Errorf("bad config: %+v", cfg)
	}
}

func TestLabConfig_AbsentIsZero(t *testing.T) {
	s := newLabTestStore(t)
	revID, _ := s.CreateRevision("d", "", "abc", "manual", "full", "{}")
	cfg, err := s.LabConfigForRevision(revID)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if cfg.Autopilot || cfg.BaseDomain != "" {
		t.Errorf("expected zero config, got %+v", cfg)
	}
}

func TestScanRun_AutopilotMarker(t *testing.T) {
	s := newLabTestStore(t)
	revID, _ := s.CreateRevision("d", "", "abc", "manual", "full", "{}")
	runID, err := s.CreateScanRun(revID, "d")
	if err != nil {
		t.Fatalf("CreateScanRun: %v", err)
	}
	if err := s.SetScanRunAutopilot(runID); err != nil {
		t.Fatalf("SetScanRunAutopilot: %v", err)
	}
	if err := s.AppendScanRunAutoConfirm(runID, "scope", "yes"); err != nil {
		t.Fatalf("AppendScanRunAutoConfirm: %v", err)
	}
	if err := s.AppendScanRunAutoConfirm(runID, "phase1_review", "proceed"); err != nil {
		t.Fatalf("AppendScanRunAutoConfirm 2: %v", err)
	}
	run, err := s.GetScanRun(runID)
	if err != nil {
		t.Fatalf("GetScanRun: %v", err)
	}
	if run.Autopilot != true {
		t.Error("autopilot flag not set")
	}
	if run.AutoConfirms != `[{"checkpoint":"scope","answer":"yes"},{"checkpoint":"phase1_review","answer":"proceed"}]` {
		t.Errorf("auto_confirms = %s", run.AutoConfirms)
	}
}
