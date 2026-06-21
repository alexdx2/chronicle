package store

import "testing"

// seedGraph creates two code nodes (under a domain) joined by one edge, with one
// evidence row on the edge. Returns the node IDs and edge ID.
func seedGraph(t *testing.T, s *Store) (fromID, toID, edgeID int64) {
	t.Helper()
	var err error
	fromID, err = s.UpsertNode(NodeRow{
		NodeKey: "code:provider:d:a", Layer: "code", NodeType: "provider", DomainKey: "d",
		Name: "A", FilePath: "svc/a.ts", Status: "active", Confidence: 1, Freshness: 1, TrustScore: 1, Metadata: "{}",
	})
	if err != nil {
		t.Fatal(err)
	}
	toID, err = s.UpsertNode(NodeRow{
		NodeKey: "code:provider:d:b", Layer: "code", NodeType: "provider", DomainKey: "d",
		Name: "B", FilePath: "svc/b.ts", Status: "active", Confidence: 1, Freshness: 1, TrustScore: 1, Metadata: "{}",
	})
	if err != nil {
		t.Fatal(err)
	}
	edgeID, err = s.UpsertEdge(EdgeRow{
		EdgeKey: "code:provider:d:a->code:provider:d:b:INJECTS", FromNodeID: fromID, ToNodeID: toID,
		EdgeType: "INJECTS", DerivationKind: "hard", Active: true, Confidence: 1, Freshness: 1, TrustScore: 1, Metadata: "{}",
	})
	if err != nil {
		t.Fatal(err)
	}
	return
}

func TestEvidence_QueriesAndCounts(t *testing.T) {
	s := openTestStore(t)
	fromID, _, edgeID := seedGraph(t, s)

	// Node-targeted role evidence (exercises ListEvidenceBySourceKind).
	if _, err := s.AddEvidence(EvidenceRow{
		TargetKind: "node", NodeID: fromID, SourceKind: "role_classification",
		FilePath: "svc/a.ts", ExtractorID: "t", ExtractorVersion: "1", Confidence: 0.9,
		EvidenceStatus: "valid", EvidencePolarity: "positive", Assertion: `{"role":"helper"}`,
	}); err != nil {
		t.Fatalf("add node evidence: %v", err)
	}
	// Edge-targeted file evidence.
	evID, err := s.AddEvidence(EvidenceRow{
		TargetKind: "edge", EdgeID: edgeID, SourceKind: "file",
		FilePath: "svc/a.ts", ExtractorID: "t", ExtractorVersion: "1", Confidence: 0.8,
		EvidenceStatus: "valid", EvidencePolarity: "positive", AssertionKind: "import_specifier", Assertion: `{}`,
	})
	if err != nil {
		t.Fatalf("add edge evidence: %v", err)
	}

	if rows, err := s.ListEvidenceBySourceKind("role_classification"); err != nil || len(rows) != 1 {
		t.Fatalf("ListEvidenceBySourceKind: %d err=%v", len(rows), err)
	}
	if counts, err := s.CountEvidenceByStatus("d"); err != nil || counts["valid"] < 1 {
		t.Fatalf("CountEvidenceByStatus: %+v err=%v", counts, err)
	}
	if _, err := s.CountRecentlyVerifiedEvidence("d", 0); err != nil {
		t.Fatalf("CountRecentlyVerifiedEvidence: %v", err)
	}
	if _, err := s.ListRejectedEvidence("d"); err != nil {
		t.Fatalf("ListRejectedEvidence: %v", err)
	}
	if rows, err := s.ListStaleEvidenceByFile("svc/a.ts"); err != nil {
		t.Fatalf("ListStaleEvidenceByFile: %v rows=%d", err, len(rows))
	}

	// Verify update + then stale marking.
	if err := s.UpdateEvidenceVerification(evID, "valid", "verified", "matched", 1, 2, 0); err != nil {
		t.Fatalf("UpdateEvidenceVerification: %v", err)
	}
	staleCount, edges, nodes, err := s.MarkEvidenceStaleByFiles([]string{"svc/a.ts"})
	if err != nil {
		t.Fatalf("MarkEvidenceStaleByFiles: %v", err)
	}
	if staleCount < 1 {
		t.Errorf("expected stale evidence, got %d (edges=%v nodes=%v)", staleCount, edges, nodes)
	}
	if paths, err := s.StaleFilePaths(); err != nil {
		t.Fatalf("StaleFilePaths: %v paths=%v", err, paths)
	}
}

func TestEdgeAndNodeTrustUpdates(t *testing.T) {
	s := openTestStore(t)
	fromID, _, edgeID := seedGraph(t, s)

	if err := s.UpdateEdgeTrust(edgeID, 0.7, 0.9, 0.6, "active"); err != nil {
		t.Fatalf("UpdateEdgeTrust: %v", err)
	}
	if err := s.UpdateEdgeVerificationStatus("code:provider:d:a->code:provider:d:b:INJECTS", "verified"); err != nil {
		t.Fatalf("UpdateEdgeVerificationStatus: %v", err)
	}
	if err := s.UpdateNodeTrust(fromID, 0.7, 0.9, 0.6, "active"); err != nil {
		t.Fatalf("UpdateNodeTrust: %v", err)
	}
	// Edges with no valid evidence (this edge has none) → should include it.
	if _, err := s.GetEdgesWithNoValidEvidence("d"); err != nil {
		t.Fatalf("GetEdgesWithNoValidEvidence: %v", err)
	}
	if _, err := s.MarkStaleEdges("d", 0); err != nil {
		t.Fatalf("MarkStaleEdges: %v", err)
	}
}

func TestAliases_Lookup(t *testing.T) {
	s := openTestStore(t)
	fromID, _, _ := seedGraph(t, s)
	if _, err := s.AddAlias(AliasRow{NodeID: fromID, Alias: "AliasA", AliasKind: "name", Confidence: 1}); err != nil {
		t.Fatalf("AddAlias: %v", err)
	}
	if rows, err := s.ListAliasesByNormalizedAnyKind("aliasa"); err != nil || len(rows) != 1 {
		t.Fatalf("ListAliasesByNormalizedAnyKind: %d err=%v", len(rows), err)
	}
	// FindCodeNodesByAlias resolves the alias back to the code node.
	nodes, err := s.FindCodeNodesByAlias("d", "AliasA", "")
	if err != nil {
		t.Fatalf("FindCodeNodesByAlias: %v", err)
	}
	if len(nodes) != 1 || nodes[0].NodeID != fromID {
		t.Fatalf("FindCodeNodesByAlias resolved wrong: %+v", nodes)
	}
}

func TestGetScanMetrics_Empty(t *testing.T) {
	s := openTestStore(t)
	m, err := s.GetScanMetrics()
	if err != nil {
		t.Fatalf("GetScanMetrics: %v", err)
	}
	if m == nil {
		t.Fatal("metrics nil")
	}
}
