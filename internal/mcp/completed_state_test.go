package mcp

import (
	"strings"
	"testing"
)

// chronicle_scan_pool_status after a completed scan must return a success
// payload whose fields satisfy the wave-loop exit condition (wave_complete,
// claimable_now=0, in_progress=0) — not an error. Agents polling the pool
// after finalize otherwise see "no active scan run" and can't tell a finished
// scan from a missing one. (Field-tested with Codex 2026-07-04.)
func TestPoolStatus_AfterCompletedScan(t *testing.T) {
	g := newLabTestGraph(t)
	revID, err := g.Store().CreateRevision("testapp", "", "abc", "manual", "full", "{}")
	if err != nil {
		t.Fatalf("CreateRevision: %v", err)
	}
	runID, err := g.Store().CreateScanRun(revID, "testapp")
	if err != nil {
		t.Fatalf("CreateScanRun: %v", err)
	}
	if err := g.Store().CompleteScanRun(runID); err != nil {
		t.Fatalf("CompleteScanRun: %v", err)
	}

	out := callToolText(t, scanPoolStatusHandler(g), map[string]any{"domain": "testapp"})

	for _, want := range []string{
		`"state":"scan_complete"`,
		`"wave_complete":true`,
		`"claimable_now":0`,
		`"in_progress":0`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("pool status after completed scan missing %s:\n%s", want, out)
		}
	}
}

// Never-scanned domains keep the error, but it must point at the fix.
func TestPoolStatus_NeverScanned(t *testing.T) {
	g := newLabTestGraph(t)

	h := scanPoolStatusHandler(g)
	var req = makeRevisionRequest(map[string]any{"domain": "ghost"})
	res, err := h(t.Context(), req)
	if err != nil {
		t.Fatalf("handler: %v", err)
	}
	if !res.IsError {
		t.Fatal("expected error result for never-scanned domain")
	}
}
