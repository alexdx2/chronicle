package store

import (
	"path/filepath"
	"strings"
	"testing"
)

// Phase-2 flow artifacts land on files that already have a phase-1 extraction
// row. The dedup must keep them as a separate role='flow' row — the 2026-07-05
// otopoint scans lost 77/77 (codex) and 162/198 (claude) flow artifacts because
// AST-merge made the artifact's primary kind "import" and dedup returned the
// phase-1 row, silently discarding the flow facts.

func openExtractionTestStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestSaveExtraction_FlowRoleCreatesSeparateRow(t *testing.T) {
	s := openExtractionTestStore(t)

	phase1Facts := `[{"kind":"import","to":"./arena.service","symbols":["ArenaService"]}]`
	p1ID, err := s.SaveExtraction(1, "dom", "src/arena.controller.ts", "resolved", "controller", phase1Facts, "")
	if err != nil {
		t.Fatalf("phase1 save: %v", err)
	}

	flowFacts := `[{"kind":"flow","flow_name":"Enter Arena","trigger":"POST /arena/enter"}]`
	flowID, written, err := s.SaveExtractionWithOutcome(1, "dom", "src/arena.controller.ts", "extracted", "", flowFacts, "", "flow", "", 0)
	if err != nil {
		t.Fatalf("flow save: %v", err)
	}
	if !written {
		t.Error("written = false; flow facts were dropped")
	}
	if flowID == p1ID {
		t.Errorf("flow row reused phase-1 row %d; want separate row", p1ID)
	}

	// Both rows must exist with their own facts.
	rows, err := s.ListExtractions(1, "dom")
	if err != nil {
		t.Fatalf("ListExtractions: %v", err)
	}
	var hasP1, hasFlow bool
	for _, r := range rows {
		if strings.Contains(r.FactsJSON, `"kind":"import"`) {
			hasP1 = true
		}
		if strings.Contains(r.FactsJSON, `"kind":"flow"`) {
			hasFlow = true
		}
	}
	if !hasP1 || !hasFlow {
		t.Errorf("want both phase-1 and flow rows; hasP1=%v hasFlow=%v", hasP1, hasFlow)
	}
}

func TestSaveExtraction_FlowRoleRecommitUpdatesInPlace(t *testing.T) {
	s := openExtractionTestStore(t)

	first := `[{"kind":"flow","flow_name":"V1","trigger":"POST /x"}]`
	id1, _, err := s.SaveExtractionWithOutcome(1, "dom", "src/x.controller.ts", "extracted", "", first, "", "flow", "", 0)
	if err != nil {
		t.Fatalf("first: %v", err)
	}

	second := `[{"kind":"flow","flow_name":"V2","trigger":"POST /x"}]`
	id2, written, err := s.SaveExtractionWithOutcome(1, "dom", "src/x.controller.ts", "extracted", "", second, "", "flow", "", 0)
	if err != nil {
		t.Fatalf("second: %v", err)
	}
	if id2 != id1 {
		t.Errorf("re-commit created new row %d; want in-place update of %d", id2, id1)
	}
	if !written {
		t.Error("written = false on refresh; want true")
	}

	rows, _ := s.ListExtractions(1, "dom")
	flowRows := 0
	for _, r := range rows {
		if strings.Contains(r.FactsJSON, `"kind":"flow"`) {
			flowRows++
			if !strings.Contains(r.FactsJSON, "V2") {
				t.Errorf("flow row not refreshed: %s", r.FactsJSON)
			}
		}
	}
	if flowRows != 1 {
		t.Errorf("want exactly 1 flow row, got %d", flowRows)
	}
}

func TestSaveExtraction_DefaultDedupIgnoresFlowRows(t *testing.T) {
	s := openExtractionTestStore(t)

	phase1Facts := `[{"kind":"import","to":"./y.service","symbols":["YService"]}]`
	p1ID, err := s.SaveExtraction(1, "dom", "src/y.controller.ts", "resolved", "controller", phase1Facts, "")
	if err != nil {
		t.Fatalf("phase1: %v", err)
	}
	if _, _, err := s.SaveExtractionWithOutcome(1, "dom", "src/y.controller.ts", "extracted", "", `[{"kind":"flow","flow_name":"F","trigger":"GET /y"}]`, "", "flow", "", 0); err != nil {
		t.Fatalf("flow: %v", err)
	}

	// A phase-1-style re-save must dedup against the phase-1 row, not the
	// (newer) flow row.
	id, written, err := s.SaveExtractionWithOutcome(1, "dom", "src/y.controller.ts", "extracted", "controller", phase1Facts, "", "single", "", 0)
	if err != nil {
		t.Fatalf("re-save: %v", err)
	}
	if written {
		t.Error("identical phase-1 facts re-save reported written=true; want dedup")
	}
	if id != p1ID {
		t.Errorf("dedup matched row %d; want phase-1 row %d", id, p1ID)
	}
}
