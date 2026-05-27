package graph

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/alexdx2/chronicle-core/manifest"
	"github.com/alexdx2/chronicle-core/registry"
	"github.com/alexdx2/chronicle-core/store"
)

// TestScanWorkflowFullCycle tests the complete scan workflow on the tom-and-jerry fixture:
// start_scan → discover → phase1_extract → extract all files → resolve → phase2_select → finalized.
func TestScanWorkflowFullCycle(t *testing.T) {
	fixtureDir := "../../chronicle-pro/fixtures/tom-and-jerry"
	if _, err := os.Stat(fixtureDir); err != nil {
		t.Skip("tom-and-jerry fixture not available")
	}

	// Resolve to absolute path for git operations
	absFixture, err := filepath.Abs(fixtureDir)
	if err != nil {
		t.Fatalf("Abs: %v", err)
	}

	// Setup: fresh DB + graph
	tmpDir := t.TempDir()
	s, err := store.Open(filepath.Join(tmpDir, "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	reg, err := registry.LoadDefaults()
	if err != nil {
		t.Fatalf("LoadDefaults: %v", err)
	}
	g := New(s, reg)

	domainKey := "tom-and-jerry"

	// ─── Step 1: No active run → ScanNextAction returns "start_scan" ───
	action, err := g.ScanNextAction(domainKey)
	if err != nil {
		t.Fatalf("ScanNextAction (no run): %v", err)
	}
	if action.Action != "start_scan" {
		t.Fatalf("expected action=start_scan, got %s", action.Action)
	}
	t.Log("Step 1 OK: no active run → start_scan")

	// ─── Step 2: Create revision + scan run ───
	revID, err := s.CreateRevision(domainKey, "scan_workflow_test", "", "full_scan", "full", "{}")
	if err != nil {
		t.Fatalf("CreateRevision: %v", err)
	}
	runID, err := s.CreateScanRun(revID, domainKey)
	if err != nil {
		t.Fatalf("CreateScanRun: %v", err)
	}

	// Verify setup phase returns discover_files
	action, err = g.ScanNextAction(domainKey)
	if err != nil {
		t.Fatalf("ScanNextAction (setup): %v", err)
	}
	if action.Action != "discover_files" {
		t.Fatalf("expected action=discover_files, got %s", action.Action)
	}
	t.Log("Step 2 OK: setup phase → discover_files")

	// ─── Step 3: Discover files using the fixture directory ───
	testManifest := &manifest.Manifest{
		Domains: []manifest.DomainEntry{
			{
				Name: domainKey,
				Scan: manifest.ScanConfig{
					Include: []string{"**/*.ts", "**/*.prisma", "**/package.json"},
					Exclude: []string{"**/*.test.*", "**/*.spec.*"},
				},
			},
		},
	}

	discovery, err := g.DiscoverFiles(absFixture, domainKey, revID, testManifest)
	if err != nil {
		t.Fatalf("DiscoverFiles: %v", err)
	}
	if discovery.TotalFiles == 0 {
		t.Fatal("DiscoverFiles returned 0 files — fixture may be empty or not a git repo")
	}
	t.Logf("Step 3 OK: discovered %d files", discovery.TotalFiles)

	// ─── Step 4: Transition to phase1_extract ───
	if err := s.TransitionScanRun(runID, "phase1_extract", discovery.TotalFiles); err != nil {
		t.Fatalf("TransitionScanRun to phase1_extract: %v", err)
	}

	// Verify ScanNextAction returns extract_files with pending files
	action, err = g.ScanNextAction(domainKey)
	if err != nil {
		t.Fatalf("ScanNextAction (phase1_extract): %v", err)
	}
	if action.Action != "extract_files" {
		t.Fatalf("expected action=extract_files, got %s", action.Action)
	}
	if len(action.Files) == 0 {
		t.Fatal("expected files in extract batch")
	}
	if action.Progress == nil {
		t.Fatal("expected progress to be set")
	}
	if action.Progress.Total != discovery.TotalFiles {
		t.Errorf("expected progress.total=%d, got %d", discovery.TotalFiles, action.Progress.Total)
	}
	t.Logf("Step 4 OK: phase1_extract → extract_files (batch of %d)", len(action.Files))

	// ─── Step 5: Simulate extraction loop — extract all files ───
	// Process the batch from Step 4 first, then keep claiming new batches
	extractedCount := 0
	for {
		// Process current batch
		for _, filePath := range action.Files {
			facts, _ := json.Marshal([]Fact{
				{Kind: "import", To: "./something"},
			})
			_, err := g.SaveFileExtraction(revID, domainKey, filePath, "extracted", "", string(facts), "")
			if err != nil {
				t.Fatalf("SaveFileExtraction(%s): %v", filePath, err)
			}
			if err := s.SatisfyObligation(revID, "scan_file", filePath); err != nil {
				t.Fatalf("SatisfyObligation(%s): %v", filePath, err)
			}
			if err := s.IncrementScanRunExtracted(runID); err != nil {
				t.Fatalf("IncrementScanRunExtracted: %v", err)
			}
			extractedCount++
		}

		// Claim next batch
		action, err = g.ScanNextAction(domainKey)
		if err != nil {
			t.Fatalf("ScanNextAction (extract loop): %v", err)
		}
		if action.Action != "extract_files" {
			break
		}
	}

	if extractedCount != discovery.TotalFiles {
		t.Fatalf("expected to extract %d files, extracted %d", discovery.TotalFiles, extractedCount)
	}
	t.Logf("Step 5 OK: extracted all %d files", extractedCount)

	// ─── Step 6: All done → ScanNextAction returns "call_resolve_extractions" (blocked) ───
	if action.Action != "call_resolve_extractions" {
		t.Fatalf("expected action=call_resolve_extractions after all extracted, got %s", action.Action)
	}
	if !action.Blocked {
		t.Fatal("expected blocked=true for resolve phase")
	}
	if action.Phase != "phase1_resolve" {
		t.Fatalf("expected phase=phase1_resolve, got %s", action.Phase)
	}
	t.Log("Step 6 OK: all files done → call_resolve_extractions (blocked)")

	// Verify calling ScanNextAction again still returns blocked
	action2, err := g.ScanNextAction(domainKey)
	if err != nil {
		t.Fatalf("ScanNextAction (blocked re-check): %v", err)
	}
	if action2.Action != "call_resolve_extractions" || !action2.Blocked {
		t.Fatalf("expected still blocked, got action=%s blocked=%v", action2.Action, action2.Blocked)
	}

	// ─── Step 7: Call ResolveExtractions ───
	result, err := g.ResolveExtractions(domainKey, revID)
	if err != nil {
		t.Fatalf("ResolveExtractions: %v", err)
	}
	t.Logf("Step 7 OK: ResolveExtractions — files=%d nodes=%d edges=%d evidence=%d",
		result.FilesProcessed, result.NodesCreated, result.EdgesCreated, result.EvidenceCreated)

	if result.FilesProcessed != extractedCount {
		t.Errorf("expected %d files processed, got %d", extractedCount, result.FilesProcessed)
	}

	// ─── Step 8: Transition to phase2_select (as MCP handler does) ───
	run, err := s.GetActiveScanRun(domainKey)
	if err != nil {
		t.Fatalf("GetActiveScanRun: %v", err)
	}
	if run == nil {
		t.Fatal("expected active scan run after resolve")
	}
	if run.Phase != "phase1_resolve" {
		t.Fatalf("expected phase=phase1_resolve before transition, got %s", run.Phase)
	}

	if err := s.SetScanRunResolved(runID, result.NodesCreated+result.EdgesCreated); err != nil {
		t.Fatalf("SetScanRunResolved: %v", err)
	}
	if err := s.TransitionScanRun(runID, "phase2_select", 0); err != nil {
		t.Fatalf("TransitionScanRun to phase2_select: %v", err)
	}

	// ─── Step 9: phase2_select auto-finalizes ───
	action, err = g.ScanNextAction(domainKey)
	if err != nil {
		t.Fatalf("ScanNextAction (phase2_select): %v", err)
	}
	if !action.Done {
		t.Fatalf("expected done=true from phase2_select, got done=%v action=%s", action.Done, action.Action)
	}
	if action.Phase != "finalized" {
		t.Fatalf("expected phase=finalized, got %s", action.Phase)
	}
	t.Log("Step 9 OK: phase2_select → auto-finalized")

	// ─── Step 10: Verify final state in DB ───
	finalRun, err := s.GetScanRun(runID)
	if err != nil {
		t.Fatalf("GetScanRun: %v", err)
	}
	if finalRun.Phase != "finalized" {
		t.Errorf("expected final phase=finalized, got %s", finalRun.Phase)
	}
	if finalRun.Status != "completed" {
		t.Errorf("expected final status=completed, got %s", finalRun.Status)
	}
	if finalRun.TotalFiles != discovery.TotalFiles {
		t.Errorf("expected total_files=%d, got %d", discovery.TotalFiles, finalRun.TotalFiles)
	}
	if finalRun.ExtractedFiles != extractedCount {
		t.Errorf("expected extracted_files=%d, got %d", extractedCount, finalRun.ExtractedFiles)
	}
	t.Logf("Step 10 OK: run finalized — total=%d extracted=%d resolved=%d",
		finalRun.TotalFiles, finalRun.ExtractedFiles, finalRun.Resolved)

	// ─── Verify: calling ScanNextAction after finalized returns done ───
	action, err = g.ScanNextAction(domainKey)
	if err != nil {
		t.Fatalf("ScanNextAction (post-finalize): %v", err)
	}
	// No active run anymore, so it should suggest starting a new scan
	if action.Action != "start_scan" {
		t.Fatalf("expected action=start_scan after finalized run, got %s", action.Action)
	}
	t.Log("DONE: Full scan workflow cycle completed successfully")
}
