package graph

import (
	"strings"
	"testing"
)

// After a scan completes, agents polling the workflow must get an explicit
// "scan_complete" — returning "start_scan" makes them begin a second scan.
// (Field-tested with Codex 2026-07-04: it reasoned around the confusion, but
// only because the driving model was strong.)

func TestScanNextAction_AfterCompletedScan(t *testing.T) {
	g, s, revID := setupTestGraph(t)

	runID, err := s.CreateScanRun(revID, "testapp")
	if err != nil {
		t.Fatalf("CreateScanRun: %v", err)
	}
	if err := s.CompleteScanRun(runID); err != nil {
		t.Fatalf("CompleteScanRun: %v", err)
	}

	action, err := g.ScanNextAction("testapp")
	if err != nil {
		t.Fatalf("ScanNextAction: %v", err)
	}
	if action.Action != "scan_complete" {
		t.Errorf("action = %q; want scan_complete after a finished scan", action.Action)
	}
	if !action.Done {
		t.Error("Done must be true after a completed scan")
	}
	if !strings.Contains(action.Reason, "complete") {
		t.Errorf("Reason should explain the scan completed, got %q", action.Reason)
	}
}

func TestScanNextAction_NeverScanned(t *testing.T) {
	g, _, _ := setupTestGraph(t)

	action, err := g.ScanNextAction("testapp")
	if err != nil {
		t.Fatalf("ScanNextAction: %v", err)
	}
	if action.Action != "start_scan" {
		t.Errorf("action = %q; want start_scan when the domain was never scanned", action.Action)
	}
}

// file_groups is the first tool a scan touches — on a non-git directory the
// raw "exit status 128" tells the agent nothing actionable.
func TestGroupFilesByDirectory_NotAGitRepo(t *testing.T) {
	_, _, err := GroupFilesByDirectory(t.TempDir())
	if err == nil {
		t.Fatal("expected error on non-git directory")
	}
	if !strings.Contains(err.Error(), "not a git repository") {
		t.Errorf("error must say 'not a git repository', got: %v", err)
	}
	if !strings.Contains(err.Error(), "git init") {
		t.Errorf("error should tell the agent how to fix it (git init), got: %v", err)
	}
}
