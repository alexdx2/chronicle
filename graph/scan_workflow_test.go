package graph

import (
	"testing"
)

func TestScanNextAction_NoActiveRun(t *testing.T) {
	g := setupGraphDefaults(t)

	action, err := g.ScanNextAction("myapp")
	if err != nil {
		t.Fatalf("ScanNextAction: %v", err)
	}
	if action.Action != "start_scan" {
		t.Errorf("expected action=start_scan, got %s", action.Action)
	}
}

func TestScanNextAction_Phase1ExtractPending(t *testing.T) {
	g := setupGraphDefaults(t)

	revID := makeRevision(t, g)
	runID, err := g.store.CreateScanRun(revID, "test-domain")
	if err != nil {
		t.Fatalf("CreateScanRun: %v", err)
	}
	// Transition to phase1_extract with 3 files
	if err := g.store.TransitionScanRun(runID, "phase1_extract", 3); err != nil {
		t.Fatalf("TransitionScanRun: %v", err)
	}

	// Create scan_file obligations (open)
	for _, f := range []string{"src/a.ts", "src/b.ts", "src/c.ts"} {
		if _, err := g.store.CreateObligation(revID, "test-domain", "scan_file", f, "test"); err != nil {
			t.Fatalf("CreateObligation: %v", err)
		}
	}

	action, err := g.ScanNextAction("test-domain")
	if err != nil {
		t.Fatalf("ScanNextAction: %v", err)
	}
	if action.Action != "extract_files" {
		t.Errorf("expected action=extract_files, got %s", action.Action)
	}
	if len(action.Files) != 3 {
		t.Errorf("expected 3 files, got %d", len(action.Files))
	}
	if action.ScanRunID != runID {
		t.Errorf("expected scan_run_id=%d, got %d", runID, action.ScanRunID)
	}
	if action.Progress == nil {
		t.Fatal("expected progress to be set")
	}
	if action.Progress.Total != 3 {
		t.Errorf("expected progress.total=3, got %d", action.Progress.Total)
	}
}

func TestScanNextAction_Phase1ExtractAllDone(t *testing.T) {
	g := setupGraphDefaults(t)

	revID := makeRevision(t, g)
	runID, err := g.store.CreateScanRun(revID, "test-domain")
	if err != nil {
		t.Fatalf("CreateScanRun: %v", err)
	}
	if err := g.store.TransitionScanRun(runID, "phase1_extract", 2); err != nil {
		t.Fatalf("TransitionScanRun: %v", err)
	}

	// Create obligations and satisfy them all
	for _, f := range []string{"src/a.ts", "src/b.ts"} {
		if _, err := g.store.CreateObligation(revID, "test-domain", "scan_file", f, "test"); err != nil {
			t.Fatalf("CreateObligation: %v", err)
		}
		if err := g.store.SatisfyObligation(revID, "scan_file", f); err != nil {
			t.Fatalf("SatisfyObligation: %v", err)
		}
	}

	action, err := g.ScanNextAction("test-domain")
	if err != nil {
		t.Fatalf("ScanNextAction: %v", err)
	}
	if action.Action != "call_resolve_extractions" {
		t.Errorf("expected action=call_resolve_extractions, got %s", action.Action)
	}
	if !action.Blocked {
		t.Error("expected blocked=true")
	}
	// Should have transitioned to phase1_resolve
	if action.Phase != "phase1_resolve" {
		t.Errorf("expected phase=phase1_resolve, got %s", action.Phase)
	}
}

func TestScanNextAction_Phase2SelectAutoFinalizes(t *testing.T) {
	g := setupGraphDefaults(t)

	revID := makeRevision(t, g)
	runID, err := g.store.CreateScanRun(revID, "test-domain")
	if err != nil {
		t.Fatalf("CreateScanRun: %v", err)
	}
	if err := g.store.TransitionScanRun(runID, "phase2_select", 0); err != nil {
		t.Fatalf("TransitionScanRun: %v", err)
	}

	action, err := g.ScanNextAction("test-domain")
	if err != nil {
		t.Fatalf("ScanNextAction: %v", err)
	}
	if !action.Done {
		t.Error("expected done=true")
	}

	// Verify it was finalized in DB
	row, err := g.store.GetScanRun(runID)
	if err != nil {
		t.Fatalf("GetScanRun: %v", err)
	}
	if row.Phase != "finalized" {
		t.Errorf("expected phase=finalized, got %s", row.Phase)
	}
	if row.Status != "completed" {
		t.Errorf("expected status=completed, got %s", row.Status)
	}
}
