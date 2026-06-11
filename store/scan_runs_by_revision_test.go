package store

import (
	"path/filepath"
	"testing"
)

// Regression: checkout used GetScanRun(revisionID) which looks up by run_id.
// The IDs coincide on fresh projects, then drift (revisions without runs) —
// after which checkout claimed obligations and errored, silently burning the
// wave's attempt budget. Found by the multilang scan lab 2026-06-11.
func TestGetScanRunByRevisionWhenIDsDrift(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	// Three revisions, only the LAST gets a run → run_id=1, revision_id=3.
	if _, err := s.CreateRevision("dom", "", "sha1", "manual", "full", "{}"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateRevision("dom", "", "sha2", "manual", "full", "{}"); err != nil {
		t.Fatal(err)
	}
	rev3, err := s.CreateRevision("dom", "", "sha3", "manual", "full", "{}")
	if err != nil {
		t.Fatal(err)
	}
	runID, err := s.CreateScanRun(rev3, "dom", 1)
	if err != nil {
		t.Fatal(err)
	}
	if runID == rev3 {
		t.Fatalf("test setup broken: run_id %d == revision_id %d (no drift)", runID, rev3)
	}

	run, err := s.GetScanRunByRevision(rev3)
	if err != nil {
		t.Fatalf("GetScanRunByRevision: %v", err)
	}
	if run.RunID != runID || run.RevisionID != rev3 {
		t.Errorf("got run_id=%d revision_id=%d, want %d/%d", run.RunID, run.RevisionID, runID, rev3)
	}
}
