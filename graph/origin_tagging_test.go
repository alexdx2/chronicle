package graph

import (
	"strings"
	"testing"

	"github.com/alexdx2/chronicle-core/store"
)

// Confidence redesign Task 1: fact origin tagging in unionFacts and the
// origin → evidence ExtractorID mapping in resolveOneFact.
// AST-derived/corroborated facts must emit "chronicle-ast" evidence;
// pure-LLM facts keep "chronicle-scan".

// --- Unit: unionFacts origin tagging (all three cases) ---

func TestUnionFacts_OriginTagging(t *testing.T) {
	astFacts := []map[string]any{
		{"kind": "endpoint", "to": "status", "method": "GET"}, // dedup-matches the LLM endpoint below
		{"kind": "consumes", "to": "battle.result"},           // AST-only
	}
	llmFacts := []map[string]any{
		{"kind": "endpoint", "to": "/jerry/status", "method": "GET"}, // matches AST endpoint (trailing segment)
		{"kind": "http_call", "target": "http://tom-api/attack"},     // pure LLM
	}

	result := unionFacts(astFacts, llmFacts)
	if len(result) != 3 {
		t.Fatalf("expected 3 merged facts, got %d: %v", len(result), result)
	}

	byKind := map[string]map[string]any{}
	for _, f := range result {
		kind, _ := f["kind"].(string)
		byKind[kind] = f
	}

	// Case 1: AST-only fact → origin "ast"
	if got, _ := byKind["consumes"]["origin"].(string); got != "ast" {
		t.Errorf("AST-only fact origin = %q, want %q", got, "ast")
	}

	// Case 2: LLM fact that dedup-matched an AST fact → surviving fact tagged "ast+llm"
	ep := byKind["endpoint"]
	if got, _ := ep["origin"].(string); got != "ast+llm" {
		t.Errorf("AST-corroborated LLM fact origin = %q, want %q", got, "ast+llm")
	}
	// The surviving fact must still be the LLM one (richer "to").
	if to, _ := ep["to"].(string); to != "/jerry/status" {
		t.Errorf("surviving endpoint fact to = %q, want LLM value %q", to, "/jerry/status")
	}

	// Case 3: pure LLM fact → untagged (llm is the default representation)
	if _, tagged := byKind["http_call"]["origin"]; tagged {
		t.Errorf("pure LLM fact should stay untagged, got origin=%v", byKind["http_call"]["origin"])
	}
}

// --- Unit: origin → extractor identity mapping ---

func TestFactExtractorID(t *testing.T) {
	cases := []struct {
		origin string
		want   string
	}{
		{"ast", "chronicle-ast"},
		{"ast+llm", "chronicle-ast"},
		{"llm", "chronicle-scan"},
		{"", "chronicle-scan"},
	}
	for _, c := range cases {
		if got := factExtractorID(Fact{Origin: c.origin}); got != c.want {
			t.Errorf("factExtractorID(origin=%q) = %q, want %q", c.origin, got, c.want)
		}
	}
}

// --- Resolve-level: origin-tagged facts produce origin-matched evidence rows ---

func TestResolveExtractions_OriginDrivesEvidenceExtractorID(t *testing.T) {
	g, s, revID := setupTestGraph(t)

	// Three import facts from one controller: AST-derived, AST-corroborated, pure-LLM.
	// Relative specifiers are DepInternal, so each creates an edge + evidence.
	facts := `[
		{"kind":"import","to":"./tom.service","symbols":["TomService"],"origin":"ast"},
		{"kind":"import","to":"./jerry.service","symbols":["JerryService"],"origin":"ast+llm"},
		{"kind":"import","to":"./cheese.service","symbols":["CheeseService"]}
	]`
	g.SaveFileExtraction(revID, "testapp", "src/tom.controller.ts", "extracted", "controller", facts, "")

	if _, err := g.ResolveExtractions("testapp", revID); err != nil {
		t.Fatalf("ResolveExtractions: %v", err)
	}

	// Collect the import-evidence extractor ID per target service.
	wantByStem := map[string]string{
		"tom.service":    "chronicle-ast",  // origin "ast"
		"jerry.service":  "chronicle-ast",  // origin "ast+llm"
		"cheese.service": "chronicle-scan", // untagged → llm default
	}
	gotByStem := map[string]string{}

	edges, err := s.ListEdges(store.EdgeFilter{})
	if err != nil {
		t.Fatalf("ListEdges: %v", err)
	}
	for _, e := range edges {
		evs, err := s.ListEvidenceByEdge(e.EdgeID)
		if err != nil {
			t.Fatalf("ListEvidenceByEdge(%s): %v", e.EdgeKey, err)
		}
		for stem := range wantByStem {
			if !strings.Contains(e.ToNodeKey, stem) {
				continue
			}
			for _, ev := range evs {
				if ev.AssertionKind == "import_specifier" {
					gotByStem[stem] = ev.ExtractorID
				}
			}
		}
	}

	for stem, want := range wantByStem {
		got, ok := gotByStem[stem]
		if !ok {
			t.Errorf("no import_specifier evidence found for edge targeting %s", stem)
			continue
		}
		if got != want {
			t.Errorf("evidence ExtractorID for %s = %q, want %q", stem, got, want)
		}
	}
}
