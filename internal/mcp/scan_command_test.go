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
	assertContains(t, cmd, "strong-model",
		"pack creation must specify strong model agent type")
}

func TestScanCommand_SequenceIsCorrect(t *testing.T) {
	cmd := CommandInstructions["scan"]

	// Verify order: discovery → manifest → packs → create → quality → finalize → extraction → reconcile → flows
	steps := []string{
		"── Discovery ──",
		"CHECKPOINT 1: Manifest",
		"CHECKPOINT 2: Instruction packs",
		"STEP 1 — Create missing",
		"CHECKPOINT 3: Scan quality",
		"── Finalize setup ──",
		"STEP 2 — Phase 1",
		"STEP 3 — Phase 1.5",
		"STEP 4 — Phase 2",
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

	assertContains(t, cmd, "CHECKPOINT 1: Manifest confirmation", "checkpoint 1 must be manifest confirmation")
	assertContains(t, cmd, "CHECKPOINT 2: Instruction packs", "checkpoint 2 must be about packs")
	assertContains(t, cmd, "CHECKPOINT 3: Scan quality", "checkpoint 3 must be about scan quality")
	assertContains(t, cmd, "fast model", "checkpoint 3 must show fast model option")
	assertContains(t, cmd, "strong model", "checkpoint 3 must show strong model option")
	assertContains(t, cmd, "RECOMMENDED", "checkpoint 3 must recommend a profile")
}

func TestScanCommand_OrchestratorPattern(t *testing.T) {
	cmd := CommandInstructions["scan"]
	assertContains(t, cmd, "WORKER POOL PATTERN",
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
