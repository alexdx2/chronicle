package mcp

import (
	"encoding/json"
	"testing"
)

func TestExtractionGuideCompactIsValidJSON(t *testing.T) {
	guide := ExtractionGuide("")
	var parsed map[string]any
	if err := json.Unmarshal([]byte(guide), &parsed); err != nil {
		t.Fatalf("guide is not valid JSON: %v", err)
	}

	for _, key := range []string{"workflow", "key_rules", "layers", "edge_types"} {
		if _, ok := parsed[key]; !ok {
			t.Errorf("missing top-level key: %s", key)
		}
	}

	if len(guide) > 5000 {
		t.Errorf("compact guide is %d bytes, want < 5000", len(guide))
	}
	t.Logf("Compact guide: %d bytes (~%d tokens)", len(guide), len(guide)/4)
}

func TestExtractionGuideDetailedNestJS(t *testing.T) {
	guide := ExtractionGuide("nestjs")
	var parsed map[string]any
	if err := json.Unmarshal([]byte(guide), &parsed); err != nil {
		t.Fatalf("not valid JSON: %v", err)
	}
	if _, ok := parsed["controllers"]; !ok {
		t.Error("missing controllers section")
	}
	if _, ok := parsed["common_mistakes"]; !ok {
		t.Error("missing common_mistakes")
	}
}

func TestExtractionGuideDetailedPrisma(t *testing.T) {
	guide := ExtractionGuide("prisma")
	var parsed map[string]any
	if err := json.Unmarshal([]byte(guide), &parsed); err != nil {
		t.Fatalf("not valid JSON: %v", err)
	}
	if _, ok := parsed["models"]; !ok {
		t.Error("missing models section")
	}
}

func TestExtractionGuideSizeDifference(t *testing.T) {
	compact := ExtractionGuide("")
	detailed := ExtractionGuide("nestjs")

	t.Logf("Compact: %d bytes (~%d tokens)", len(compact), len(compact)/4)
	t.Logf("NestJS:  %d bytes (~%d tokens)", len(detailed), len(detailed)/4)

	// Both should be under 3KB
	if len(compact) > 5000 {
		t.Errorf("compact too large: %d bytes", len(compact))
	}
	if len(detailed) > 3000 {
		t.Errorf("detailed too large: %d bytes", len(detailed))
	}
}
