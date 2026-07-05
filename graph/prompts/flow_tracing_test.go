package prompts

import (
	"strings"
	"testing"
)

// Field finding (otopoint, 2026-07-05): Codex generated flow facts with a
// regex script — templated steps ("Receive X request / Return X result"),
// constructor-regex requires. The schema itself must forbid scripted
// extraction; the extraction guide's no-scripts rule never reaches phase-2
// extractors.
func TestFlowTracing_ForbidsScriptedExtraction(t *testing.T) {
	lower := strings.ToLower(FlowTracing)
	if !strings.Contains(lower, "script") {
		t.Error("flow schema must explicitly forbid generating flows with scripts")
	}
	if !strings.Contains(FlowTracing, "READ") {
		t.Error("flow schema must demand actually reading the trigger file")
	}
	if !strings.Contains(lower, "steps must") {
		t.Error("flow schema must require steps to reflect what the code actually does")
	}
}
