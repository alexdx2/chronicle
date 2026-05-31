package mcp

import (
	"strings"
	"testing"
)

// Tests that the assembled scan command contains required elements.
// These test the FINAL output after stage injection.

func TestScanCommand_HasPackCreationFlow(t *testing.T) {
	cmd := CommandInstructions["scan"]

	assertContains(t, cmd, "chronicle_instruction_packs",
		"must call chronicle_instruction_packs")
	assertContains(t, cmd, "MISSING",
		"must flag missing packs")
	assertContains(t, cmd, "guide/pack_authoring",
		"must reference pack authoring guide")
	assertContains(t, cmd, "chronicle_save_custom_pack",
		"must use save_custom_pack tool")
}

func TestScanCommand_OrchestratorAfterAgents(t *testing.T) {
	cmd := CommandInstructions["scan"]

	// The stage system generates "AFTER ALL AGENTS FINISH"
	afterCount := strings.Count(cmd, "AFTER ALL AGENTS FINISH")
	if afterCount < 3 {
		t.Errorf("expected at least 3 'AFTER ALL AGENTS FINISH' markers (phase1 + reconcile + phase2), got %d", afterCount)
	}

	// Must include resolve + check pattern in after-agents
	assertContains(t, cmd, "chronicle_resolve_extractions",
		"after-agents must call resolve")
	assertContains(t, cmd, "check next phase",
		"after phase 1 must check next phase")
}

func TestScanCommand_DomainPassing(t *testing.T) {
	cmd := CommandInstructions["scan"]
	assertContains(t, cmd, "domain_key",
		"must reference per-file domain_key")
	assertContains(t, cmd, "domains",
		"must reference multi-domain manifest format")
}

func TestScanCommand_PackCreationUsesStrongModel(t *testing.T) {
	cmd := CommandInstructions["scan"]
	assertContains(t, cmd, "STRONG MODEL",
		"pack creation must specify strong model role")
	assertContains(t, cmd, "NOT a fast model",
		"must explicitly exclude fast model for pack creation")
}

func TestScanCommand_SequenceIsCorrect(t *testing.T) {
	cmd := CommandInstructions["scan"]

	// Verify order: checkpoints → pack creation → scan mode → finalize → extraction → reconcile → flows
	steps := []string{
		"CHECKPOINT 1",          // scope
		"CHECKPOINT 2",          // packs
		"STEP 1",                // create_packs (strong)
		"CHECKPOINT 3",          // scan mode
		"Finalize setup",        // save manifest + discover
		"STEP 2",                // phase1 extraction (fast)
		"STEP 3",                // phase1.5 reconciliation (strong)
		"STEP 4",                // phase2 flow tracing (strong)
	}

	lastIdx := -1
	for _, step := range steps {
		idx := strings.Index(cmd, step)
		if idx < 0 {
			t.Errorf("step %q not found in scan command", step)
			continue
		}
		if idx <= lastIdx {
			t.Errorf("step %q is out of order (at %d, previous was %d)", step, idx, lastIdx)
		}
		lastIdx = idx
	}
}

func TestScanCommand_SeparateCheckpoints(t *testing.T) {
	cmd := CommandInstructions["scan"]

	for _, cp := range []string{"CHECKPOINT 1", "CHECKPOINT 2", "CHECKPOINT 3"} {
		idx := strings.Index(cmd, cp)
		if idx < 0 {
			t.Errorf("%s not found", cp)
			continue
		}
		afterCP := cmd[idx:]
		stopIdx := strings.Index(afterCP, "STOP")
		if stopIdx < 0 || stopIdx > 1200 {
			t.Errorf("%s must have a STOP within 1200 chars (got %d)", cp, stopIdx)
		}
	}
}

func TestScanCommand_StructuredCheckpoints(t *testing.T) {
	cmd := CommandInstructions["scan"]

	if strings.Contains(cmd, "Does this look right?") {
		t.Error("should ask specific questions, not 'does this look right?'")
	}

	assertContains(t, cmd, "CHECKPOINT 1: Scope", "checkpoint 1 must be about scope")
	assertContains(t, cmd, "CHECKPOINT 2: Instruction packs", "checkpoint 2 must be about packs")
	assertContains(t, cmd, "CHECKPOINT 3: Scan quality", "checkpoint 3 must be about scan quality")
	assertContains(t, cmd, "Agents", "checkpoint 3 must show agents column")
	assertContains(t, cmd, "Reads", "checkpoint 3 must show reads column")
	assertContains(t, cmd, "How many agents per file", "checkpoint 3 must ask for a number")
}

func TestScanCommand_OrchestratorPattern(t *testing.T) {
	cmd := CommandInstructions["scan"]
	assertContains(t, cmd, "ORCHESTRATOR PATTERN",
		"must explain the common pattern for all agent stages")
}

// --- Helpers ---

func assertContains(t *testing.T, text, substr, msg string) {
	t.Helper()
	if !strings.Contains(text, substr) {
		t.Errorf("%s — %q not found", msg, substr)
	}
}

func between(text, start, end string) string {
	startIdx := strings.Index(text, start)
	if startIdx < 0 {
		return ""
	}
	sub := text[startIdx:]
	if end == "" {
		return sub
	}
	endIdx := strings.Index(sub, end)
	if endIdx < 0 {
		return sub
	}
	return sub[:endIdx]
}
