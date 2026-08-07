package store

import (
	"testing"
)

func makeEvidence(targetKind string, targetID int64, line int) EvidenceRow {
	e := EvidenceRow{
		TargetKind:       targetKind,
		SourceKind:       "ast",
		RepoName:         "myrepo",
		FilePath:         "src/orders.go",
		LineStart:        line,
		LineEnd:          line + 5,
		ExtractorID:      "go-calls",
		ExtractorVersion: "1.0.0",
		Confidence:       0.9,
		Metadata:         "{}",
	}
	if targetKind == "node" {
		e.NodeID = targetID
	} else {
		e.EdgeID = targetID
	}
	return e
}

func TestAddEvidenceForEdge(t *testing.T) {
	s := openTestStore(t)
	revID, n1, n2 := seedNodes(t, s)
	edgeID, err := s.UpsertEdge(makeEdgeRow("edge:calls:oc:os", n1, n2, revID))
	if err != nil {
		t.Fatalf("UpsertEdge: %v", err)
	}

	evID, err := s.AddEvidence(makeEvidence("edge", edgeID, 10))
	if err != nil {
		t.Fatalf("AddEvidence: %v", err)
	}
	if evID <= 0 {
		t.Fatalf("expected positive id, got %d", evID)
	}
}

func TestAddEvidenceDedup(t *testing.T) {
	s := openTestStore(t)
	revID, n1, n2 := seedNodes(t, s)
	edgeID, _ := s.UpsertEdge(makeEdgeRow("edge:calls:oc:os", n1, n2, revID))

	ev := makeEvidence("edge", edgeID, 10)
	id1, err := s.AddEvidence(ev)
	if err != nil {
		t.Fatalf("AddEvidence first: %v", err)
	}

	// Same location — should update, not insert.
	ev.Confidence = 0.75
	ev.CommitSHA = "abc123"
	id2, err := s.AddEvidence(ev)
	if err != nil {
		t.Fatalf("AddEvidence dedup: %v", err)
	}
	if id1 != id2 {
		t.Errorf("expected same evidence_id on dedup, got %d vs %d", id1, id2)
	}

	// Verify count is still 1.
	rows, err := s.ListEvidenceByEdge(edgeID)
	if err != nil {
		t.Fatalf("ListEvidenceByEdge: %v", err)
	}
	if len(rows) != 1 {
		t.Errorf("expected 1 evidence row after dedup, got %d", len(rows))
	}
}

func TestAddEvidenceDifferentLine(t *testing.T) {
	s := openTestStore(t)
	revID, n1, n2 := seedNodes(t, s)
	edgeID, _ := s.UpsertEdge(makeEdgeRow("edge:calls:oc:os", n1, n2, revID))

	_, err := s.AddEvidence(makeEvidence("edge", edgeID, 10))
	if err != nil {
		t.Fatalf("AddEvidence line 10: %v", err)
	}
	_, err = s.AddEvidence(makeEvidence("edge", edgeID, 20))
	if err != nil {
		t.Fatalf("AddEvidence line 20: %v", err)
	}

	rows, err := s.ListEvidenceByEdge(edgeID)
	if err != nil {
		t.Fatalf("ListEvidenceByEdge: %v", err)
	}
	if len(rows) != 2 {
		t.Errorf("expected 2 evidence rows for different lines, got %d", len(rows))
	}
}

func TestListEvidenceByNode(t *testing.T) {
	s := openTestStore(t)
	revID, n1, n2 := seedNodes(t, s)
	_ = revID

	s.AddEvidence(makeEvidence("node", n1, 5))
	s.AddEvidence(makeEvidence("node", n1, 15))
	s.AddEvidence(makeEvidence("node", n2, 5))

	rows, err := s.ListEvidenceByNode(n1)
	if err != nil {
		t.Fatalf("ListEvidenceByNode: %v", err)
	}
	if len(rows) != 2 {
		t.Errorf("expected 2 evidence rows for n1, got %d", len(rows))
	}
}

// TestListEvidenceByAssertionKind verifies the selector filters by
// assertion_kind across target kinds/domains (no domain filter, unlike
// ListRejectedEvidence) and that the returned rows carry NodeID/EdgeID —
// pro's package index needs EdgeID populated to attach identities to edges.
func TestListEvidenceByAssertionKind(t *testing.T) {
	s := openTestStore(t)
	revID, n1, n2 := seedNodes(t, s)
	edgeID, err := s.UpsertEdge(makeEdgeRow("edge:calls:oc:os", n1, n2, revID))
	if err != nil {
		t.Fatalf("UpsertEdge: %v", err)
	}

	withAssertion := makeEvidence("edge", edgeID, 10)
	withAssertion.Assertion = `{"module":"@okeep/auth-client"}`
	withAssertion.AssertionKind = "import_specifier"
	if _, err := s.AddEvidence(withAssertion); err != nil {
		t.Fatalf("AddEvidence withAssertion: %v", err)
	}

	// Different assertion_kind — must not be picked up.
	otherKind := makeEvidence("node", n1, 5)
	otherKind.Assertion = `{"foo":"bar"}`
	otherKind.AssertionKind = "other_kind"
	if _, err := s.AddEvidence(otherKind); err != nil {
		t.Fatalf("AddEvidence otherKind: %v", err)
	}

	// No assertion at all — must not be picked up.
	if _, err := s.AddEvidence(makeEvidence("node", n2, 5)); err != nil {
		t.Fatalf("AddEvidence plain: %v", err)
	}

	rows, err := s.ListEvidenceByAssertionKind("import_specifier")
	if err != nil {
		t.Fatalf("ListEvidenceByAssertionKind: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 row for import_specifier, got %d", len(rows))
	}
	if rows[0].Assertion != `{"module":"@okeep/auth-client"}` {
		t.Errorf("Assertion = %q, want the seeded payload", rows[0].Assertion)
	}
	if rows[0].EdgeID != edgeID {
		t.Errorf("EdgeID = %d, want %d", rows[0].EdgeID, edgeID)
	}

	none, err := s.ListEvidenceByAssertionKind("does_not_exist")
	if err != nil {
		t.Fatalf("ListEvidenceByAssertionKind (empty): %v", err)
	}
	if len(none) != 0 {
		t.Errorf("expected 0 rows for unknown assertion_kind, got %d", len(none))
	}
}
