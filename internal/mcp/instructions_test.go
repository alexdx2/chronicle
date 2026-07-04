package mcp

import (
	"strings"
	"testing"
)

// Codex (and clients following OpenAI's MCP guidance) weight the first 512
// characters of server instructions when deciding how to use a server — the
// entry point and the pipeline-tool warning must land inside that window.
func TestServerInstructions_FrontLoadsEntryPoint(t *testing.T) {
	head := serverInstructions
	if len(head) > 512 {
		head = head[:512]
	}

	if !strings.Contains(head, "chronicle_command") {
		t.Errorf("first 512 chars must name chronicle_command as the entry point; got:\n%s", head)
	}
	if !strings.Contains(head, "chronicle_scan_") {
		t.Errorf("first 512 chars must warn against calling scan-pipeline tools cold; got:\n%s", head)
	}
}

// Chronicle serves any MCP client (Claude Code, Codex, Cursor, ...) — text the
// model or user sees must not assume a specific client.
func TestInstructions_ClientNeutral(t *testing.T) {
	for name, instr := range CommandInstructions {
		if strings.Contains(instr, "Cursor") {
			t.Errorf("command %q instructions mention Cursor; use client-neutral wording", name)
		}
	}

	for _, tool := range []struct {
		name string
		desc string
	}{
		{"chronicle_evidence_verify", evidenceVerifyTool().Description},
		{"chronicle_invalidate_changed", invalidateChangedTool().Description},
		{"chronicle_finalize_incremental_scan", finalizeIncrementalScanTool().Description},
		{"chronicle_save_manifest", saveManifestTool().Description},
	} {
		if strings.Contains(tool.desc, "Claude") {
			t.Errorf("%s description mentions Claude; use client-neutral wording: %s", tool.name, tool.desc)
		}
	}
}

func TestServerInstructions_KeepsGraphRepairGuidance(t *testing.T) {
	if !strings.Contains(serverInstructions, "source of truth") {
		t.Error("graph-repair guidance (graph is the source of truth) must be retained")
	}
	if !strings.Contains(serverInstructions, "chronicle_import_all") {
		t.Error("graph-repair guidance must keep the fix-the-graph tool reference")
	}
}
