package store

import (
	"path/filepath"
	"testing"
)

func TestEvidenceCoverageReport(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	revID, _ := s.CreateRevision("dom", "", "abc", "manual", "full", "{}")
	nodeID, _ := s.UpsertNode(NodeRow{
		NodeKey: "code:module:dom:a", Layer: "code", NodeType: "module", DomainKey: "dom",
		Name: "a", Status: "active", LastSeenRevisionID: revID, Metadata: "{}",
	})
	rep, err := s.EvidenceCoverageReport("dom")
	if err != nil {
		t.Fatal(err)
	}
	if rep.NodesWithoutEvidence != 1 {
		t.Fatalf("NodesWithoutEvidence = %d, want 1", rep.NodesWithoutEvidence)
	}

	s.AddEvidence(EvidenceRow{
		TargetKind: "node", NodeID: nodeID, SourceKind: "file",
		FilePath: "a.ts", ExtractorID: "", // missing extractor_id
		ExtractorVersion: "1.0", Confidence: 0.8, Metadata: "{}",
	})
	rep, _ = s.EvidenceCoverageReport("dom")
	if rep.NodesWithoutEvidence != 0 {
		t.Fatalf("NodesWithoutEvidence = %d, want 0", rep.NodesWithoutEvidence)
	}
	if rep.IncompleteEvidence != 1 {
		t.Fatalf("IncompleteEvidence = %d, want 1", rep.IncompleteEvidence)
	}
}
