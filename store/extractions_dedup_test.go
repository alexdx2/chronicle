package store

import "testing"

// A scan's reading of a file must not be deduped against the structural
// phase's standing row. The phase records an import fact for nearly every file
// it reads, so an unscoped match hit its row for any scan row whose first fact
// was an import: the scan's facts were discarded and the id handed back
// belonged to another writer.
func TestSaveExtractionDoesNotDedupAScanAgainstTheStructuralRow(t *testing.T) {
	s := openTestStore(t)
	rev, err := s.CreateRevision("d", "", "abc", "manual", "full", "{}")
	if err != nil {
		t.Fatal(err)
	}

	// The structural phase's standing row. It records an import for nearly
	// every file it reads.
	structuralID, err := s.SaveStructuralExtraction(rev, "d", "src/a.ts", "extracted", "provider",
		`[{"kind":"import","to":"./b"}]`, "", "")
	if err != nil {
		t.Fatal(err)
	}

	// The agent's first reading of the same file — a different primary kind, so
	// it inserts and becomes the row the next save dedups against.
	scanID, written, err := s.SaveExtractionWithOutcome(rev, "d", "src/a.ts", "extracted", "provider",
		`[{"kind":"endpoint","to":"get:/a"}]`, "", "single", "", 0)
	if err != nil || !written {
		t.Fatalf("first scan save: id=%d written=%v err=%v", scanID, written, err)
	}

	// A second agent save whose FIRST fact is an import. Unscoped, the same-kind
	// lookup matched the structural row: these facts were dropped and the id
	// handed back belonged to another writer.
	gotID, written, err := s.SaveExtractionWithOutcome(rev, "d", "src/a.ts", "extracted", "provider",
		`[{"kind":"import","to":"./c"}]`, "", "single", "", 0)
	if err != nil {
		t.Fatal(err)
	}
	if gotID == structuralID {
		t.Errorf("the scan was handed the structural row (id %d); written=%v", structuralID, written)
	}

	// The structural row is untouched: it is another writer's standing answer.
	rows, err := s.ListStandingExtractionsByRole("d", StructuralExtractionRole)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].ExtractionID != structuralID {
		t.Fatalf("structural rows = %d, want just id %d", len(rows), structuralID)
	}
	if rows[0].FactsJSON != `[{"kind":"import","to":"./b"}]` {
		t.Errorf("the structural row's facts were rewritten: %s", rows[0].FactsJSON)
	}
}

// Two readings from the SAME conversation still dedup — that is what the rule
// is for.
func TestSaveExtractionStillDedupsWithinOneConversation(t *testing.T) {
	s := openTestStore(t)
	rev, err := s.CreateRevision("d", "", "abc", "manual", "full", "{}")
	if err != nil {
		t.Fatal(err)
	}
	first, written, err := s.SaveExtractionWithOutcome(rev, "d", "src/a.ts", "extracted", "provider",
		`[{"kind":"import","to":"./b"}]`, "", "single", "", 0)
	if err != nil || !written {
		t.Fatalf("first save: id=%d written=%v err=%v", first, written, err)
	}
	second, written, err := s.SaveExtractionWithOutcome(rev, "d", "src/a.ts", "extracted", "provider",
		`[{"kind":"import","to":"./c"}]`, "", "single", "", 0)
	if err != nil {
		t.Fatal(err)
	}
	if written || second != first {
		t.Errorf("second save: id=%d written=%v, want the first row %d reused", second, written, first)
	}
}
