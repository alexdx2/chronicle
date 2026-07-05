package graph

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/alexdx2/chronicle-core/paths"
)

// TestScanCheckoutBatches_ArtifactPathsAbsolute verifies that outbox/work dirs
// handed back to extractor agents are absolute paths even in the default
// (no --project, no --chronicle-dir) configuration, where paths.Dir() alone
// would return a bare relative ".depbot". These paths cross the process
// boundary in MCP tool responses, so relative paths would be ambiguous to
// whatever cwd the reading agent happens to have.
func TestScanCheckoutBatches_ArtifactPathsAbsolute(t *testing.T) {
	// Pin the default config: no project root, default chronicle dir.
	paths.SetProjectRoot("")
	paths.SetChronicleDir("")
	t.Cleanup(func() {
		paths.SetProjectRoot("")
		paths.SetChronicleDir("")
	})

	// Run from a scratch cwd so the test doesn't litter the repo with a
	// .depbot/scan-outbox directory, and restore cwd afterward.
	orig, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	tmp := t.TempDir()
	if err := os.Chdir(tmp); err != nil {
		t.Fatalf("Chdir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(orig) })

	g := setupGraphDefaults(t)
	revID := makeRevision(t, g)

	runID, err := g.store.CreateScanRun(revID, "test-domain")
	if err != nil {
		t.Fatalf("CreateScanRun: %v", err)
	}
	if err := g.store.TransitionScanRun(runID, "phase1_extract", 1); err != nil {
		t.Fatalf("TransitionScanRun: %v", err)
	}
	if _, err := g.store.CreateObligation(revID, "test-domain", "scan_file", "src/a.ts", "test"); err != nil {
		t.Fatalf("CreateObligation: %v", err)
	}

	batches, err := g.ScanCheckoutBatches(revID, "scan_file", 1)
	if err != nil {
		t.Fatalf("ScanCheckoutBatches: %v", err)
	}
	if len(batches) != 1 {
		t.Fatalf("expected 1 batch, got %d", len(batches))
	}
	batch := batches[0]

	if !filepath.IsAbs(batch.OutboxDir) {
		t.Errorf("OutboxDir = %q, want absolute path", batch.OutboxDir)
	}
	if !filepath.IsAbs(batch.WorkDir) {
		t.Errorf("WorkDir = %q, want absolute path", batch.WorkDir)
	}
	if batch.ItemsPath != "" && !filepath.IsAbs(batch.ItemsPath) {
		t.Errorf("ItemsPath = %q, want absolute path", batch.ItemsPath)
	}
}
