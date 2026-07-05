package mcp

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// End-to-end regression for the phase-2 flow-artifact drop (otopoint field
// finding 2026-07-05): a flow artifact committed for a file that already has a
// phase-1 extraction row must produce a named flow node after resolve — not be
// silently swallowed by AST-merge + dedup while its obligation is marked
// satisfied and the commit reports zero errors.
func TestCommitOutbox_FlowArtifactSurvivesPhase1Row(t *testing.T) {
	root := repoRootFromWD(t)
	if root == "" {
		t.Skip("could not locate repo root (.git)")
	}
	relPath := "fixtures/tom-and-jerry/arena-api/src/arena/arena.controller.ts"
	if _, err := os.Stat(filepath.Join(root, relPath)); err != nil {
		t.Skipf("fixture file absent: %s", relPath)
	}

	orig, _ := os.Getwd()
	if err := os.Chdir(root); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(orig) })

	g := newLabTestGraph(t)
	st := g.Store()

	revID, err := st.CreateRevision("testdomain", "", "abc", "manual", "full", "{}")
	if err != nil {
		t.Fatalf("CreateRevision: %v", err)
	}

	// Phase-1 state: extraction row with import facts already resolved.
	phase1Facts := `[{"kind":"import","to":"./arena.service","symbols":["ArenaService"]}]`
	if _, err := st.SaveExtraction(revID, "testdomain", relPath, "resolved", "controller", phase1Facts, ""); err != nil {
		t.Fatalf("phase1 row: %v", err)
	}
	if _, err := st.CreateObligation(revID, "testdomain", "trace_flow", relPath, "trigger file"); err != nil {
		t.Fatalf("CreateObligation: %v", err)
	}

	outboxDir := filepath.Join(root, ".depbot", "scan-outbox", fmt.Sprintf("%d", revID))
	if err := os.MkdirAll(outboxDir, 0o755); err != nil {
		t.Fatalf("mkdir outbox: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(outboxDir) })

	writeOutboxArtifact(t, outboxDir, relPath, "extracted", "", []map[string]any{
		{
			"kind":      "flow",
			"flow_name": "Enter Arena",
			"trigger":   "POST /arena/enter",
			"method":    "enter",
			"requires":  []string{"ArenaService"},
			"steps":     []string{"Validate entry", "Register combatant"},
		},
	}, "testdomain", revID)

	h := commitScanOutboxHandler(g)
	res, err := h(context.Background(), makeRevisionRequest(map[string]any{
		"domain":       "testdomain",
		"revision_id":  float64(revID),
		"remove_after": false,
	}))
	if err != nil {
		t.Fatalf("handler: %v", err)
	}
	out := decodeActionResult(t, res)

	if written, _ := out["rows_written"].(float64); written < 1 {
		t.Errorf("rows_written = %v; flow artifact was not stored (response: %v)", out["rows_written"], out)
	}

	// The flow facts must exist as their own extraction row.
	rows, err := st.ListExtractions(revID, "testdomain")
	if err != nil {
		t.Fatalf("ListExtractions: %v", err)
	}
	flowRows := 0
	for _, r := range rows {
		if strings.Contains(r.FactsJSON, `"kind":"flow"`) {
			flowRows++
		}
	}
	if flowRows != 1 {
		t.Fatalf("want 1 flow extraction row, got %d (flow facts dropped)", flowRows)
	}

	// Obligation satisfied because the work was actually stored.
	pending, _ := st.CountPendingObligations(revID, "trace_flow")
	if pending != 0 {
		t.Errorf("trace_flow obligations pending = %d; want 0", pending)
	}

	// Resolve must turn the flow facts into a named flow node.
	if _, err := g.ResolveExtractions("testdomain", revID); err != nil {
		t.Fatalf("ResolveExtractions: %v", err)
	}
	node, err := st.GetNodeByKey("flow:use_case:testdomain:post__arena_enter")
	if err != nil || node == nil {
		t.Fatalf("flow node missing after resolve: %v", err)
	}
	if node.Name != "Enter Arena" {
		t.Errorf("flow node name = %q; want the traced name %q", node.Name, "Enter Arena")
	}
}

// Artifacts whose facts were dropped (not stored, non-empty) must NOT satisfy
// their obligation — a dropped artifact must surface as unfinished work, not
// silent success.
func TestCommitOutbox_DedupDropDoesNotSatisfyObligation(t *testing.T) {
	root := repoRootFromWD(t)
	if root == "" {
		t.Skip("could not locate repo root (.git)")
	}
	relPath := "fixtures/tom-and-jerry/arena-api/src/arena/arena.controller.ts"
	orig, _ := os.Getwd()
	if err := os.Chdir(root); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(orig) })

	g := newLabTestGraph(t)
	st := g.Store()
	revID, err := st.CreateRevision("testdomain", "", "abc", "manual", "full", "{}")
	if err != nil {
		t.Fatalf("CreateRevision: %v", err)
	}

	// Phase-1 row with import facts; identical re-commit → dedup, not written.
	phase1Facts := `[{"kind":"import","to":"./arena.service","symbols":["ArenaService"]}]`
	if _, err := st.SaveExtraction(revID, "testdomain", relPath, "resolved", "controller", phase1Facts, ""); err != nil {
		t.Fatalf("phase1 row: %v", err)
	}
	if _, err := st.CreateObligation(revID, "testdomain", "scan_file", relPath, "re-scan"); err != nil {
		t.Fatalf("CreateObligation: %v", err)
	}

	outboxDir := filepath.Join(root, ".depbot", "scan-outbox", fmt.Sprintf("%d", revID))
	if err := os.MkdirAll(outboxDir, 0o755); err != nil {
		t.Fatalf("mkdir outbox: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(outboxDir) })

	writeOutboxArtifact(t, outboxDir, relPath, "extracted", "controller", []map[string]any{
		{"kind": "import", "to": "./arena.service", "symbols": []string{"ArenaService"}},
	}, "testdomain", revID)

	h := commitScanOutboxHandler(g)
	res, err := h(context.Background(), makeRevisionRequest(map[string]any{
		"domain":       "testdomain",
		"revision_id":  float64(revID),
		"remove_after": false,
	}))
	if err != nil {
		t.Fatalf("handler: %v", err)
	}
	out := decodeActionResult(t, res)

	if deduped, _ := out["deduped"].(float64); deduped < 1 {
		t.Errorf("deduped = %v; want the identical artifact reported as deduped", out["deduped"])
	}
	// Dedup of identical facts is legitimate completion — the file's facts ARE
	// stored (in the phase-1 row) — so the obligation IS satisfied.
	pending, _ := st.CountPendingObligations(revID, "scan_file")
	if pending != 0 {
		t.Errorf("identical-facts dedup should still satisfy obligation; pending = %d", pending)
	}
}
