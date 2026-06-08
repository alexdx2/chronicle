package store

import (
	"path/filepath"
	"testing"
)

func testStoreWithRevision(t *testing.T) (*Store, int64) {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")
	s, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	// Create a revision so FK is satisfied
	revID, err := s.CreateRevision("test-domain", "", "abc123", "full_scan", "full", "{}")
	if err != nil {
		t.Fatalf("CreateRevision: %v", err)
	}
	return s, revID
}

func TestScanRunCreate(t *testing.T) {
	s, revID := testStoreWithRevision(t)
	defer s.Close()

	runID, err := s.CreateScanRun(revID, "test-domain")
	if err != nil {
		t.Fatalf("CreateScanRun: %v", err)
	}
	if runID < 1 {
		t.Fatalf("expected runID >= 1, got %d", runID)
	}

	row, err := s.GetScanRun(runID)
	if err != nil {
		t.Fatalf("GetScanRun: %v", err)
	}
	if row.Phase != "setup" {
		t.Errorf("expected phase=setup, got %s", row.Phase)
	}
	if row.Status != "running" {
		t.Errorf("expected status=running, got %s", row.Status)
	}
	if row.DomainKey != "test-domain" {
		t.Errorf("expected domain_key=test-domain, got %s", row.DomainKey)
	}
}

func TestScanRunTransition(t *testing.T) {
	s, revID := testStoreWithRevision(t)
	defer s.Close()

	runID, _ := s.CreateScanRun(revID, "test-domain")

	err := s.TransitionScanRun(runID, "phase1_extract", 10)
	if err != nil {
		t.Fatalf("TransitionScanRun: %v", err)
	}

	row, _ := s.GetScanRun(runID)
	if row.Phase != "phase1_extract" {
		t.Errorf("expected phase=phase1_extract, got %s", row.Phase)
	}
	if row.TotalFiles != 10 {
		t.Errorf("expected total_files=10, got %d", row.TotalFiles)
	}

	// Transition without changing total_files
	err = s.TransitionScanRun(runID, "phase1_resolve", 0)
	if err != nil {
		t.Fatalf("TransitionScanRun: %v", err)
	}
	row, _ = s.GetScanRun(runID)
	if row.Phase != "phase1_resolve" {
		t.Errorf("expected phase=phase1_resolve, got %s", row.Phase)
	}
	if row.TotalFiles != 10 {
		t.Errorf("expected total_files still 10, got %d", row.TotalFiles)
	}
}

func TestScanRunIncrement(t *testing.T) {
	s, revID := testStoreWithRevision(t)
	defer s.Close()

	runID, _ := s.CreateScanRun(revID, "test-domain")

	for i := 0; i < 3; i++ {
		if err := s.IncrementScanRunExtracted(runID); err != nil {
			t.Fatalf("IncrementScanRunExtracted: %v", err)
		}
	}

	row, _ := s.GetScanRun(runID)
	if row.ExtractedFiles != 3 {
		t.Errorf("expected extracted_files=3, got %d", row.ExtractedFiles)
	}

	if err := s.SetScanRunResolved(runID, 5); err != nil {
		t.Fatalf("SetScanRunResolved: %v", err)
	}
	row, _ = s.GetScanRun(runID)
	if row.Resolved != 5 {
		t.Errorf("expected resolved=5, got %d", row.Resolved)
	}
}

func TestScanRunConfirmScopePhase(t *testing.T) {
	s, revID := testStoreWithRevision(t)
	defer s.Close()

	runID, _ := s.CreateScanRun(revID, "test-domain")
	if err := s.TransitionScanRun(runID, "confirm_scope", 129); err != nil {
		t.Fatalf("TransitionScanRun confirm_scope: %v", err)
	}
	row, _ := s.GetScanRun(runID)
	if row.Phase != "confirm_scope" {
		t.Fatalf("expected confirm_scope, got %s", row.Phase)
	}
	if row.TotalFiles != 129 {
		t.Fatalf("expected total_files=129, got %d", row.TotalFiles)
	}
	if err := s.TransitionScanRun(runID, "phase1_extract", 0); err != nil {
		t.Fatalf("TransitionScanRun phase1_extract: %v", err)
	}
	row, _ = s.GetScanRun(runID)
	if row.TotalFiles != 129 {
		t.Fatalf("expected total_files preserved at 129, got %d", row.TotalFiles)
	}
}

func TestScanRunEndpointReconcilePhase(t *testing.T) {
	s, revID := testStoreWithRevision(t)
	defer s.Close()

	runID, _ := s.CreateScanRun(revID, "test-domain")
	for _, phase := range []string{"confirm_scope", "phase1_extract", "phase1_resolve", "endpoint_reconcile", "phase2_select"} {
		total := 0
		if phase == "confirm_scope" {
			total = 10
		}
		if err := s.TransitionScanRun(runID, phase, total); err != nil {
			t.Fatalf("TransitionScanRun %s: %v", phase, err)
		}
	}
}

func TestScanRunGetActive(t *testing.T) {
	s, revID := testStoreWithRevision(t)
	defer s.Close()

	// No active run yet
	row, err := s.GetActiveScanRun("test-domain")
	if err != nil {
		t.Fatalf("GetActiveScanRun: %v", err)
	}
	if row != nil {
		t.Fatalf("expected nil for no active run, got %+v", row)
	}

	// Create a run
	runID, _ := s.CreateScanRun(revID, "test-domain")

	row, err = s.GetActiveScanRun("test-domain")
	if err != nil {
		t.Fatalf("GetActiveScanRun: %v", err)
	}
	if row == nil {
		t.Fatal("expected active run, got nil")
	}
	if row.RunID != runID {
		t.Errorf("expected run_id=%d, got %d", runID, row.RunID)
	}

	// Complete it
	if err := s.CompleteScanRun(runID); err != nil {
		t.Fatalf("CompleteScanRun: %v", err)
	}

	row, err = s.GetActiveScanRun("test-domain")
	if err != nil {
		t.Fatalf("GetActiveScanRun after complete: %v", err)
	}
	if row != nil {
		t.Fatalf("expected nil after completion, got %+v", row)
	}

	// Verify completed run state
	completed, _ := s.GetScanRun(runID)
	if completed.Phase != "finalized" {
		t.Errorf("expected phase=finalized, got %s", completed.Phase)
	}
	if completed.Status != "completed" {
		t.Errorf("expected status=completed, got %s", completed.Status)
	}
}
