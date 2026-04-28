package store

import "testing"

func TestVersionedEvidenceStaleCreatesNewVersion(t *testing.T) {
	s := openTestDB(t)
	ctxID, _ := s.CreateContext("dom", "main", "main", 0, 0)
	rev1, _ := s.CreateRevisionWithContext("dom", "", "ev-aaa", "manual", "full", "{}", ctxID)
	rev2, _ := s.CreateRevisionWithContext("dom", "ev-aaa", "ev-bbb", "manual", "incremental", "{}", ctxID)

	// Create node and evidence
	nodeID, _ := s.UpsertNode(NodeRow{
		NodeKey: "code:svc:dom:evx", Layer: "code", NodeType: "provider",
		DomainKey: "dom", Name: "X", Status: "active",
		FirstSeenRevisionID: rev1, LastSeenRevisionID: rev1,
		Confidence: 1.0, Freshness: 1.0, TrustScore: 1.0, Metadata: "{}",
	})

	_, err := s.AddEvidence(EvidenceRow{
		TargetKind: "node", NodeID: nodeID,
		SourceKind: "file", FilePath: "src/evx.ts",
		ExtractorID: "claude", ExtractorVersion: "1.0",
		Confidence: 0.99, EvidenceStatus: "valid", EvidencePolarity: "positive",
		ValidFromRevisionID: rev1, ContextID: ctxID, Metadata: "{}",
	})
	if err != nil {
		t.Fatalf("AddEvidence: %v", err)
	}

	staleCount, nodeIDs, _, err := s.MarkEvidenceStaleByFilesVersioned([]string{"src/evx.ts"}, rev2, ctxID)
	if err != nil {
		t.Fatalf("MarkEvidenceStaleByFilesVersioned: %v", err)
	}
	if staleCount == 0 {
		t.Fatal("expected stale count > 0")
	}
	if len(nodeIDs) == 0 {
		t.Fatal("expected affected node IDs")
	}

	// Query ALL evidence for this node (including closed versions)
	var totalRows int
	s.db.QueryRow(`SELECT COUNT(*) FROM graph_evidence WHERE node_id = ?`, nodeID).Scan(&totalRows)
	if totalRows < 2 {
		t.Errorf("expected >= 2 evidence rows (old closed + new stale), got %d", totalRows)
	}

	// Old row should have valid_to set
	var closedCount int
	s.db.QueryRow(`SELECT COUNT(*) FROM graph_evidence WHERE node_id = ? AND valid_to_revision_id IS NOT NULL AND valid_to_revision_id > 0`, nodeID).Scan(&closedCount)
	if closedCount == 0 {
		t.Error("expected old evidence row to have valid_to set")
	}

	// New stale row should exist
	var staleRows int
	s.db.QueryRow(`SELECT COUNT(*) FROM graph_evidence WHERE node_id = ? AND evidence_status = 'stale' AND (valid_to_revision_id IS NULL OR valid_to_revision_id = 0)`, nodeID).Scan(&staleRows)
	if staleRows == 0 {
		t.Error("expected new stale evidence version")
	}
}

func TestVersionedEvidenceEmptyFiles(t *testing.T) {
	s := openTestDB(t)
	count, _, _, err := s.MarkEvidenceStaleByFilesVersioned([]string{}, 1, 1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if count != 0 {
		t.Errorf("expected 0 stale, got %d", count)
	}
}
