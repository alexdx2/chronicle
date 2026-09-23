package store

import (
	"path/filepath"
	"testing"
)

// EvidenceSummaries answers, in one query, how many evidence rows back each
// node and edge and where the best one lives (file:line) — the diagram's
// per-element provenance. Invalidated/superseded/negative rows don't count.
func TestEvidenceSummaries(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	revID, _ := s.CreateRevision("dom", "", "abc", "manual", "full", "{}")
	a, _ := s.UpsertNode(NodeRow{NodeKey: "code:provider:dom:a", Layer: "code", NodeType: "provider", DomainKey: "dom", Name: "a", Status: "active", LastSeenRevisionID: revID, Metadata: "{}"})
	b, _ := s.UpsertNode(NodeRow{NodeKey: "code:provider:dom:b", Layer: "code", NodeType: "provider", DomainKey: "dom", Name: "b", Status: "active", LastSeenRevisionID: revID, Metadata: "{}"})
	e, _ := s.UpsertEdge(EdgeRow{EdgeKey: "code:provider:dom:a->code:provider:dom:b:INJECTS", FromNodeID: a, ToNodeID: b, EdgeType: "INJECTS", DerivationKind: "hard", Active: true, LastSeenRevisionID: revID, Metadata: "{}"})

	add := func(kind string, nid, eid int64, file string, line int, status, polarity string) {
		if _, err := s.AddEvidence(EvidenceRow{TargetKind: kind, NodeID: nid, EdgeID: eid, SourceKind: "file", FilePath: file, LineStart: line,
			ExtractorID: "x", ExtractorVersion: "1", Confidence: 0.9, EvidenceStatus: status, EvidencePolarity: polarity, Metadata: "{}"}); err != nil {
			t.Fatal(err)
		}
	}
	add("node", a, 0, "a.ts", 12, "valid", "positive")
	add("node", a, 0, "a.ts", 30, "invalidated", "positive") // does not count
	add("node", a, 0, "a.ts", 40, "valid", "negative")       // does not count
	add("node", b, 0, "b.ts", 0, "stale", "positive")         // counts, ref without line
	add("edge", 0, e, "a.ts", 15, "valid", "positive")
	add("edge", 0, e, "a.ts", 16, "revalidated", "positive")

	nodes, edges, err := s.EvidenceSummaries()
	if err != nil {
		t.Fatal(err)
	}
	if got := nodes[a]; got.Count != 1 || got.Ref != "a.ts:12" {
		t.Errorf("node a: %+v want count 1 ref a.ts:12", got)
	}
	if got := nodes[b]; got.Count != 1 || got.Ref != "b.ts" {
		t.Errorf("node b: %+v want count 1 ref b.ts", got)
	}
	if got := edges[e]; got.Count != 2 || got.Ref != "a.ts:15" {
		t.Errorf("edge: %+v want count 2 ref a.ts:15", got)
	}
}
