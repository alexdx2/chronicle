package mcp

import (
	"strings"
	"testing"
)

func TestStageSystem_DefaultStages(t *testing.T) {
	stages := GetScanStages()
	if len(stages) < 8 {
		t.Fatalf("expected at least 8 default stages, got %d", len(stages))
	}

	// Verify stage types
	types := map[string]int{}
	for _, s := range stages {
		types[s.Type]++
	}
	if types["checkpoint"] < 3 {
		t.Errorf("expected at least 3 checkpoints, got %d", types["checkpoint"])
	}
	if types["agents"] < 2 {
		t.Errorf("expected at least 2 agent stages, got %d", types["agents"])
	}
	if types["action"] < 1 {
		t.Errorf("expected at least 1 action stage, got %d", types["action"])
	}
}

func TestStageSystem_StageOrder(t *testing.T) {
	stages := GetScanStages()
	ids := make([]string, len(stages))
	for i, s := range stages {
		ids[i] = s.ID
	}

	// Verify key ordering
	expected := []string{"discover", "scope", "packs", "create_packs", "scan_mode", "final_review", "finalize_setup", "phase1", "phase2"}
	for i, exp := range expected {
		if i >= len(ids) || ids[i] != exp {
			t.Errorf("stage %d: expected %s, got %v", i, exp, ids)
			break
		}
	}
}

func TestStageSystem_AllAgentStagesHaveAfterAgents(t *testing.T) {
	for _, s := range GetScanStages() {
		if s.Type == "agents" && s.AfterAgents == "" {
			t.Errorf("agent stage %q must have AfterAgents instructions", s.ID)
		}
	}
}

func TestStageSystem_AllAgentStagesHaveModel(t *testing.T) {
	for _, s := range GetScanStages() {
		if s.Type == "agents" && s.AgentModel == "" {
			t.Errorf("agent stage %q must specify AgentModel", s.ID)
		}
	}
}

func TestStageSystem_BuildInstruction(t *testing.T) {
	text := BuildScanStagesInstruction()

	// Must have the orchestrator pattern
	assertContains(t, text, "ORCHESTRATOR PATTERN",
		"must explain the common orchestrator pattern")
	assertContains(t, text, "AFTER AGENTS",
		"must reference after-agents steps")

	// Each checkpoint has STOP
	stopCount := strings.Count(text, "STOP")
	if stopCount < 3 {
		t.Errorf("expected at least 3 STOP markers, got %d", stopCount)
	}

	// Agent stages have AFTER markers
	afterCount := strings.Count(text, "AFTER ALL")
	if afterCount < 2 {
		t.Errorf("expected at least 2 AFTER ALL markers (phase1 + phase2), got %d", afterCount)
	}

	// Numbered checkpoints
	assertContains(t, text, "CHECKPOINT 1", "must have checkpoint 1")
	assertContains(t, text, "CHECKPOINT 2", "must have checkpoint 2")
	assertContains(t, text, "CHECKPOINT 3", "must have checkpoint 3")

	// Numbered steps
	assertContains(t, text, "STEP 1", "must have step 1")
	assertContains(t, text, "STEP 2", "must have step 2")
}

func TestStageSystem_ModelAssignment(t *testing.T) {
	text := BuildScanStagesInstruction()

	// create_packs and phase2 use sonnet
	for _, s := range GetScanStages() {
		if s.ID == "create_packs" || s.ID == "phase2" {
			if s.AgentModel != "sonnet" {
				t.Errorf("stage %s must use sonnet, got %s", s.ID, s.AgentModel)
			}
		}
		if s.ID == "phase1" {
			if s.AgentModel != "haiku" {
				t.Errorf("stage phase1 must use haiku, got %s", s.AgentModel)
			}
		}
	}

	// Text should reflect this
	phase1Section := between(text, "Phase 1", "Phase 2")
	assertContains(t, phase1Section, "haiku", "phase 1 must mention haiku")

	phase2Section := between(text, "Phase 2", "")
	assertContains(t, phase2Section, "sonnet", "phase 2 must mention sonnet")
}

func TestStageSystem_Extensible(t *testing.T) {
	// Save original
	original := make([]ScanStage, len(scanStages))
	copy(original, scanStages)
	defer func() { scanStages = original }()

	// Add a custom stage after scope
	AddScanStage(ScanStage{
		ID:          "custom_check",
		Name:        "Custom validation",
		Type:        "checkpoint",
		Instruction: "Validate custom rules. [yes/no]",
	}, "scope")

	stages := GetScanStages()
	if len(stages) != len(original)+1 {
		t.Fatalf("expected %d stages, got %d", len(original)+1, len(stages))
	}
	if stages[2].ID != "custom_check" {
		t.Errorf("custom stage should be at position 2, got %s", stages[2].ID)
	}

	text := BuildScanStagesInstruction()
	assertContains(t, text, "Custom validation", "must include custom stage")
}

// Legacy compat
func TestStageSystem_LegacyCheckpointAPI(t *testing.T) {
	cps := GetScanCheckpoints()
	if len(cps) < 3 {
		t.Errorf("legacy API should return at least 3 checkpoints, got %d", len(cps))
	}
	for _, cp := range cps {
		if cp.ID == "" || cp.Name == "" {
			t.Errorf("checkpoint missing ID or Name: %+v", cp)
		}
	}
}
