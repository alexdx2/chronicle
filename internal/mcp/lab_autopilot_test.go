package mcp

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	mcplib "github.com/mark3labs/mcp-go/mcp"
)

// decodeActionResult decodes a handler result's JSON content into a map.
func decodeActionResult(t *testing.T, res *mcplib.CallToolResult) map[string]any {
	t.Helper()
	if res.IsError {
		t.Fatalf("handler returned error result: %v", res.Content)
	}
	if len(res.Content) == 0 {
		t.Fatal("no content in result")
	}
	text, ok := res.Content[0].(mcplib.TextContent)
	if !ok {
		t.Fatalf("expected TextContent, got %T", res.Content[0])
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(text.Text), &out); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	return out
}

// TestScanNextFile_AutopilotSkipsScopeCheckpoint verifies that autopilot runs
// automatically answer the "scope" checkpoint and transition to phase1_extract,
// rather than blocking and returning the checkpoint to the caller.
func TestScanNextFile_AutopilotSkipsScopeCheckpoint(t *testing.T) {
	g := newLabTestGraph(t)
	st := g.Store()

	revID, err := st.CreateRevision("lab", "", "abc", "manual", "full",
		`{"lab":{"autopilot":true,"answers":{}}}`)
	if err != nil {
		t.Fatalf("CreateRevision: %v", err)
	}
	runID, err := st.CreateScanRun(revID, "lab")
	if err != nil {
		t.Fatalf("CreateScanRun: %v", err)
	}
	if err := st.SetScanRunAutopilot(runID); err != nil {
		t.Fatalf("SetScanRunAutopilot: %v", err)
	}
	// Create obligations so that after confirming scope the run stays in phase1_extract.
	for _, f := range []string{"a.ts", "b.ts", "c.ts"} {
		if _, err := st.CreateObligation(revID, "lab", "scan_file", f, "test"); err != nil {
			t.Fatalf("CreateObligation: %v", err)
		}
	}
	// Transition to confirm_scope with 3 total files so the checkpoint is triggered.
	if err := st.TransitionScanRun(runID, "confirm_scope", 3); err != nil {
		t.Fatalf("TransitionScanRun: %v", err)
	}

	h := scanNextFileHandler(g)
	res, err := h(context.Background(), makeRevisionRequest(map[string]any{"domain": "lab"}))
	if err != nil {
		t.Fatalf("handler: %v", err)
	}
	out := decodeActionResult(t, res)

	// Autopilot must not surface the checkpoint.
	if cp, ok := out["checkpoint"]; ok && cp != nil {
		t.Fatalf("autopilot returned a checkpoint: %+v", cp)
	}

	// The run must have transitioned to phase1_extract.
	run, err := st.GetScanRun(runID)
	if err != nil {
		t.Fatalf("GetScanRun: %v", err)
	}
	if run.Phase != "phase1_extract" {
		t.Errorf("run phase = %s, want phase1_extract", run.Phase)
	}

	// The auto_confirms log must record the scope checkpoint answer.
	if !strings.Contains(run.AutoConfirms, `"checkpoint":"scope"`) {
		t.Errorf("auto_confirms missing scope entry: %s", run.AutoConfirms)
	}
}

// TestScanNextFile_NonAutopilotStillBlocks verifies that interactive (non-autopilot)
// scans still block and surface the scope checkpoint to the caller.
func TestScanNextFile_NonAutopilotStillBlocks(t *testing.T) {
	g := newLabTestGraph(t)
	st := g.Store()

	revID, err := st.CreateRevision("lab2", "", "abc", "manual", "full", "{}")
	if err != nil {
		t.Fatalf("CreateRevision: %v", err)
	}
	runID, err := st.CreateScanRun(revID, "lab2")
	if err != nil {
		t.Fatalf("CreateScanRun: %v", err)
	}
	if err := st.TransitionScanRun(runID, "confirm_scope", 3); err != nil {
		t.Fatalf("TransitionScanRun: %v", err)
	}

	h := scanNextFileHandler(g)
	res, err := h(context.Background(), makeRevisionRequest(map[string]any{"domain": "lab2"}))
	if err != nil {
		t.Fatalf("handler: %v", err)
	}
	out := decodeActionResult(t, res)

	// Interactive scan must still return the checkpoint.
	cp, _ := out["checkpoint"].(map[string]any)
	if cp == nil {
		t.Fatalf("interactive scan must return checkpoint, got %+v", out)
	}
	if id, _ := cp["id"].(string); id != "scope" {
		t.Fatalf("interactive scan must return scope checkpoint, got id=%q", id)
	}
	_ = runID
}

// TestScanNextFile_AutopilotSkipFlowsCompletes verifies that autopilot runs with
// answers {"phase1_summary":"skip flows"} automatically complete the scan when in
// phase2_confirm (which yields the phase1_summary checkpoint).
func TestScanNextFile_AutopilotSkipFlowsCompletes(t *testing.T) {
	g := newLabTestGraph(t)
	st := g.Store()

	revID, err := st.CreateRevision("lab3", "", "abc", "manual", "full",
		`{"lab":{"autopilot":true,"answers":{"phase1_summary":"skip flows"}}}`)
	if err != nil {
		t.Fatalf("CreateRevision: %v", err)
	}
	runID, err := st.CreateScanRun(revID, "lab3")
	if err != nil {
		t.Fatalf("CreateScanRun: %v", err)
	}
	if err := st.SetScanRunAutopilot(runID); err != nil {
		t.Fatalf("SetScanRunAutopilot: %v", err)
	}
	// phase2_confirm is the phase that yields the phase1_summary checkpoint.
	if err := st.TransitionScanRun(runID, "phase2_confirm", 2); err != nil {
		t.Fatalf("TransitionScanRun: %v", err)
	}

	h := scanNextFileHandler(g)
	res, err := h(context.Background(), makeRevisionRequest(map[string]any{"domain": "lab3"}))
	if err != nil {
		t.Fatalf("handler: %v", err)
	}
	out := decodeActionResult(t, res)

	// Expect done=true and no checkpoint.
	if done, _ := out["done"].(bool); !done {
		t.Errorf("expected done=true, got %+v", out)
	}
	if cp, ok := out["checkpoint"]; ok && cp != nil {
		t.Errorf("expected no checkpoint, got %+v", cp)
	}

	// Scan run must be completed.
	run, err := st.GetScanRun(runID)
	if err != nil {
		t.Fatalf("GetScanRun: %v", err)
	}
	if run.Status != "completed" {
		t.Errorf("run status = %q, want completed", run.Status)
	}
}
