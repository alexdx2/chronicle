package mcp

import (
	"strings"
	"testing"
)

// The solo scan workflow serves clients without a subagent/Task tool (Codex,
// Cursor, Gemini CLI): one agent does checkout → read files → write outbox
// artifacts → commit itself. Same pool, same checkpoints, no spawning.

func TestSoloScanStages_NoSpawning(t *testing.T) {
	solo := BuildSoloScanStagesInstruction()

	lower := strings.ToLower(solo)
	if strings.Contains(lower, "spawn") {
		t.Errorf("solo instructions must not tell the agent to spawn anything:\n%s", solo)
	}
	if !strings.Contains(solo, "sole extractor") {
		t.Errorf("solo instructions must frame the agent as the sole extractor")
	}
	// The artifact-pool mechanics stay identical.
	for _, want := range []string{
		"chronicle_scan_pool_status",
		"chronicle_scan_checkout_batch",
		"chronicle_commit_scan_outbox",
	} {
		if !strings.Contains(solo, want) {
			t.Errorf("solo instructions missing pool mechanic %q", want)
		}
	}
}

// Solo extractors are tempted to script extraction at scale (Codex regex'd
// 6.3k files in 21 min on otopoint — junk endpoints, templated flows). The
// solo instructions must forbid it where the extractor actually reads them.
func TestSoloScanStages_ForbidsScriptedExtraction(t *testing.T) {
	solo := strings.ToLower(BuildSoloScanStagesInstruction())
	if !strings.Contains(solo, "script") {
		t.Error("solo instructions must explicitly forbid script-generated facts")
	}
}

func TestSoloScanStages_KeepsCheckpoints(t *testing.T) {
	solo := BuildSoloScanStagesInstruction()
	orchestrator := BuildScanStagesInstruction()

	soloCPs := strings.Count(solo, "CHECKPOINT")
	orchCPs := strings.Count(orchestrator, "CHECKPOINT")
	if soloCPs != orchCPs {
		t.Errorf("solo has %d CHECKPOINT mentions, orchestrator has %d — checkpoints must not be dropped", soloCPs, orchCPs)
	}
}

func TestSoloScanCommand_Assembled(t *testing.T) {
	cmd := soloScanCommand()

	for _, leftover := range []string{"__MCP_PREFLIGHT__", "__STAGES__"} {
		if strings.Contains(cmd, leftover) {
			t.Errorf("solo scan command has unexpanded placeholder %s", leftover)
		}
	}
	if !strings.Contains(cmd, "chronicle_mcp_identity") {
		t.Error("solo scan command must keep the MCP preflight")
	}
	if !strings.Contains(cmd, "NEVER call chronicle_import_all during a scan") {
		t.Error("solo scan command must keep the import_all prohibition")
	}
}

// chronicle_command(scan) picks the workflow by detected client.
func TestScanCommand_SelectsWorkflowByClient(t *testing.T) {
	h := commandHandler(nil)

	setConnectedClientForTest(t, "codex")
	soloOut := callToolText(t, h, map[string]any{"command": "scan"})
	if !strings.Contains(soloOut, "sole extractor") {
		t.Errorf("codex client should get the solo scan workflow, got:\n%.400s", soloOut)
	}

	setConnectedClientForTest(t, "claude-code")
	orchOut := callToolText(t, h, map[string]any{"command": "scan"})
	if !strings.Contains(strings.ToLower(orchOut), "spawn") {
		t.Errorf("claude-code client should get the orchestrator scan workflow, got:\n%.400s", orchOut)
	}
}
