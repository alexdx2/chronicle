package mcp

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestExtractionGuideUniversalIsValidJSON(t *testing.T) {
	guide := ExtractionGuide("")
	var parsed map[string]any
	if err := json.Unmarshal([]byte(guide), &parsed); err != nil {
		t.Fatalf("guide is not valid JSON: %v", err)
	}

	// Must have these core sections
	for _, key := range []string{"workflow", "key_rules", "layer_guide", "edge_intent", "schema_reference"} {
		if _, ok := parsed[key]; !ok {
			t.Errorf("missing top-level key: %s", key)
		}
	}

	// Must NOT enumerate full edge types (that's schema's job)
	if _, ok := parsed["edge_types"]; ok {
		t.Error("guide should NOT contain edge_types — use chronicle_schema() instead")
	}
	if _, ok := parsed["flow_edge_types"]; ok {
		t.Error("guide should NOT contain flow_edge_types — use chronicle_schema() instead")
	}

	if len(guide) > 7000 {
		t.Errorf("guide is %d bytes, want < 7000", len(guide))
	}
	t.Logf("Universal guide: %d bytes (~%d tokens)", len(guide), len(guide)/4)
}

func TestExtractionGuideIgnoresTechnologyParam(t *testing.T) {
	universal := ExtractionGuide("")
	nestjs := ExtractionGuide("nestjs")
	flow := ExtractionGuide("flow")

	// All should return the same universal guide
	if universal != nestjs {
		t.Error("technology='nestjs' should return the same guide as no technology")
	}
	if universal != flow {
		t.Error("technology='flow' should return the same guide as no technology")
	}
}

func TestExtractionGuideHasFlowRules(t *testing.T) {
	guide := ExtractionGuide("")
	var parsed map[string]any
	if err := json.Unmarshal([]byte(guide), &parsed); err != nil {
		t.Fatalf("guide is not valid JSON: %v", err)
	}

	// Must have flow extraction rules
	if _, ok := parsed["flow_extraction_rules"]; !ok {
		t.Error("missing flow_extraction_rules section")
	}

	// Workflow must include flows as a scan pass
	workflow, ok := parsed["workflow"].([]any)
	if !ok {
		t.Fatal("workflow is not an array")
	}
	foundFlows := false
	for _, step := range workflow {
		s, _ := step.(string)
		if strings.Contains(s, "flows") {
			foundFlows = true
			break
		}
	}
	if !foundFlows {
		t.Error("workflow should mention flows as a scan pass")
	}
}

func TestExtractionGuideHasDryRunInfo(t *testing.T) {
	guide := ExtractionGuide("")
	if !strings.Contains(guide, "dry_run") {
		t.Error("guide should mention dry_run mode")
	}
}

func TestExtractionHintsNestJS(t *testing.T) {
	hints := ExtractionHints("nestjs")
	var parsed map[string]any
	if err := json.Unmarshal([]byte(hints), &parsed); err != nil {
		t.Fatalf("hints is not valid JSON: %v", err)
	}

	if parsed["technology"] != "nestjs" {
		t.Errorf("expected technology=nestjs, got %v", parsed["technology"])
	}
	if _, ok := parsed["hints"]; !ok {
		t.Error("missing hints section")
	}
	if _, ok := parsed["note"]; !ok {
		t.Error("missing note about chronicle_schema()")
	}
}

func TestExtractionHintsUnknown(t *testing.T) {
	hints := ExtractionHints("cobol")
	var parsed map[string]any
	if err := json.Unmarshal([]byte(hints), &parsed); err != nil {
		t.Fatalf("hints is not valid JSON: %v", err)
	}

	if _, ok := parsed["message"]; !ok {
		t.Error("unknown technology should return a message")
	}
}
