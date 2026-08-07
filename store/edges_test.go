package store

import (
	"testing"
)

func makeEdgeRow(key string, fromID, toID, revID int64) EdgeRow {
	return EdgeRow{
		EdgeKey: key, FromNodeID: fromID, ToNodeID: toID,
		EdgeType: "calls", DerivationKind: "hard", Active: true,
		FirstSeenRevisionID: revID, LastSeenRevisionID: revID,
		Confidence: 1.0, Metadata: "{}",
	}
}

func TestUpsertEdgeInsert(t *testing.T) {
	s := openTestStore(t)
	revID, n1, n2 := seedNodes(t, s)
	id, err := s.UpsertEdge(makeEdgeRow("edge:calls:oc:os", n1, n2, revID))
	if err != nil {
		t.Fatalf("UpsertEdge insert: %v", err)
	}
	if id <= 0 {
		t.Fatalf("expected positive id, got %d", id)
	}
}

func TestUpsertEdgeUpdate(t *testing.T) {
	s := openTestStore(t)
	revID, n1, n2 := seedNodes(t, s)
	e := makeEdgeRow("edge:calls:oc:os", n1, n2, revID)
	id1, err := s.UpsertEdge(e)
	if err != nil {
		t.Fatalf("UpsertEdge first: %v", err)
	}

	e.Confidence = 0.8
	e.DerivationKind = "inferred"
	id2, err := s.UpsertEdge(e)
	if err != nil {
		t.Fatalf("UpsertEdge update: %v", err)
	}
	if id1 != id2 {
		t.Errorf("expected same id on update, got %d vs %d", id1, id2)
	}

	got, err := s.GetEdgeByKey("edge:calls:oc:os")
	if err != nil {
		t.Fatalf("GetEdgeByKey: %v", err)
	}
	if got.Confidence != 0.8 {
		t.Errorf("Confidence = %v, want 0.8", got.Confidence)
	}
	if got.DerivationKind != "inferred" {
		t.Errorf("DerivationKind = %q, want inferred", got.DerivationKind)
	}
}

// TestUpsertEdgeDependencySourceRoundTrip proves dependency_source survives
// write → GetEdgeByKey → ListEdges → ListEdges(EdgeFilter{DependencySourceIn}).
// This is the exact shape of bug the repo already shipped once for
// verification_status: a column added to the DDL and one writer but never
// wired into EdgeRow or any SELECT, so the value was invisible everywhere
// except the raw table.
func TestUpsertEdgeDependencySourceRoundTrip(t *testing.T) {
	s := openTestStore(t)
	revID, n1, n2 := seedNodes(t, s)
	e := makeEdgeRow("edge:calls:oc:os", n1, n2, revID)
	e.DependencySource = "manifest"
	if _, err := s.UpsertEdge(e); err != nil {
		t.Fatalf("UpsertEdge: %v", err)
	}

	got, err := s.GetEdgeByKey("edge:calls:oc:os")
	if err != nil {
		t.Fatalf("GetEdgeByKey: %v", err)
	}
	if got.DependencySource != "manifest" {
		t.Errorf("GetEdgeByKey DependencySource = %q, want manifest", got.DependencySource)
	}

	all, err := s.ListEdges(EdgeFilter{})
	if err != nil {
		t.Fatalf("ListEdges: %v", err)
	}
	found := false
	for _, r := range all {
		if r.EdgeKey == "edge:calls:oc:os" {
			found = true
			if r.DependencySource != "manifest" {
				t.Errorf("ListEdges DependencySource = %q, want manifest", r.DependencySource)
			}
		}
	}
	if !found {
		t.Fatal("edge not found in ListEdges")
	}

	filtered, err := s.ListEdges(EdgeFilter{DependencySourceIn: []string{"manifest"}})
	if err != nil {
		t.Fatalf("ListEdges DependencySourceIn: %v", err)
	}
	if len(filtered) != 1 || filtered[0].EdgeKey != "edge:calls:oc:os" {
		t.Fatalf("ListEdges DependencySourceIn=[manifest] = %+v, want exactly edge:calls:oc:os", filtered)
	}

	filteredOut, err := s.ListEdges(EdgeFilter{DependencySourceIn: []string{"code"}})
	if err != nil {
		t.Fatalf("ListEdges DependencySourceIn code: %v", err)
	}
	for _, r := range filteredOut {
		if r.EdgeKey == "edge:calls:oc:os" {
			t.Fatal("manifest edge leaked into DependencySourceIn=[code] filter")
		}
	}
}

// TestUpsertEdgeDependencySourceDefaultsToCode: callers that build EdgeRow
// directly without setting DependencySource (every existing resolver call
// site in graph/resolve_extractions.go does this) must still get the
// SQ-Contract 2 default, not an empty string outside the closed enum.
func TestUpsertEdgeDependencySourceDefaultsToCode(t *testing.T) {
	s := openTestStore(t)
	revID, n1, n2 := seedNodes(t, s)
	if _, err := s.UpsertEdge(makeEdgeRow("edge:calls:oc:os", n1, n2, revID)); err != nil {
		t.Fatalf("UpsertEdge: %v", err)
	}
	got, err := s.GetEdgeByKey("edge:calls:oc:os")
	if err != nil {
		t.Fatalf("GetEdgeByKey: %v", err)
	}
	if got.DependencySource != "code" {
		t.Errorf("DependencySource = %q, want code (default)", got.DependencySource)
	}
}

func TestListEdgesByFrom(t *testing.T) {
	s := openTestStore(t)
	revID, n1, n2 := seedNodes(t, s)
	s.UpsertEdge(makeEdgeRow("edge:calls:oc:os", n1, n2, revID))
	s.UpsertEdge(makeEdgeRow("edge:calls:os:oc", n2, n1, revID))

	edges, err := s.ListEdges(EdgeFilter{FromNodeID: n1})
	if err != nil {
		t.Fatalf("ListEdges by from: %v", err)
	}
	if len(edges) != 1 {
		t.Errorf("expected 1 edge from n1, got %d", len(edges))
	}
}

func TestListEdgesByTo(t *testing.T) {
	s := openTestStore(t)
	revID, n1, n2 := seedNodes(t, s)
	s.UpsertEdge(makeEdgeRow("edge:calls:oc:os", n1, n2, revID))
	s.UpsertEdge(makeEdgeRow("edge:calls:os:oc", n2, n1, revID))

	edges, err := s.ListEdges(EdgeFilter{ToNodeID: n2})
	if err != nil {
		t.Fatalf("ListEdges by to: %v", err)
	}
	if len(edges) != 1 {
		t.Errorf("expected 1 edge to n2, got %d", len(edges))
	}
}

func TestGetEdgesBetweenNodes(t *testing.T) {
	s := openTestStore(t)
	revID, err := s.CreateRevision("orders", "", "sha1", "manual", "full", "{}")
	if err != nil {
		t.Fatalf("CreateRevision: %v", err)
	}

	// Create 3 nodes: A, B, C
	nA, err := s.UpsertNode(NodeRow{
		NodeKey: "code:controller:orders:a", Layer: "code", NodeType: "controller",
		DomainKey: "orders", Name: "A", Status: "active",
		FirstSeenRevisionID: revID, LastSeenRevisionID: revID, Confidence: 1, Metadata: "{}",
	})
	if err != nil {
		t.Fatalf("UpsertNode A: %v", err)
	}
	nB, err := s.UpsertNode(NodeRow{
		NodeKey: "code:controller:orders:b", Layer: "code", NodeType: "controller",
		DomainKey: "orders", Name: "B", Status: "active",
		FirstSeenRevisionID: revID, LastSeenRevisionID: revID, Confidence: 1, Metadata: "{}",
	})
	if err != nil {
		t.Fatalf("UpsertNode B: %v", err)
	}
	nC, err := s.UpsertNode(NodeRow{
		NodeKey: "code:controller:orders:c", Layer: "code", NodeType: "controller",
		DomainKey: "orders", Name: "C", Status: "active",
		FirstSeenRevisionID: revID, LastSeenRevisionID: revID, Confidence: 1, Metadata: "{}",
	})
	if err != nil {
		t.Fatalf("UpsertNode C: %v", err)
	}

	// Insert edges: A→B, B→C, A→C
	s.UpsertEdge(makeEdgeRow("edge:calls:a:b", nA, nB, revID))
	s.UpsertEdge(makeEdgeRow("edge:calls:b:c", nB, nC, revID))
	s.UpsertEdge(makeEdgeRow("edge:calls:a:c", nA, nC, revID))

	// Query with only [A, B] → expect 1 edge (A→B)
	edges, err := s.GetEdgesBetweenNodes([]int64{nA, nB})
	if err != nil {
		t.Fatalf("GetEdgesBetweenNodes [A,B]: %v", err)
	}
	if len(edges) != 1 {
		t.Errorf("expected 1 edge between [A,B], got %d", len(edges))
	}

	// Query with [A, B, C] → expect 3 edges
	edges, err = s.GetEdgesBetweenNodes([]int64{nA, nB, nC})
	if err != nil {
		t.Fatalf("GetEdgesBetweenNodes [A,B,C]: %v", err)
	}
	if len(edges) != 3 {
		t.Errorf("expected 3 edges between [A,B,C], got %d", len(edges))
	}

	// Query with empty slice → expect 0 edges
	edges, err = s.GetEdgesBetweenNodes([]int64{})
	if err != nil {
		t.Fatalf("GetEdgesBetweenNodes []: %v", err)
	}
	if len(edges) != 0 {
		t.Errorf("expected 0 edges for empty input, got %d", len(edges))
	}
}

func TestDeleteEdge(t *testing.T) {
	s := openTestStore(t)
	revID, n1, n2 := seedNodes(t, s)
	s.UpsertEdge(makeEdgeRow("edge:calls:oc:os", n1, n2, revID))

	if err := s.DeleteEdge("edge:calls:oc:os"); err != nil {
		t.Fatalf("DeleteEdge: %v", err)
	}

	got, err := s.GetEdgeByKey("edge:calls:oc:os")
	if err != nil {
		t.Fatalf("GetEdgeByKey after delete: %v", err)
	}
	if got.Active {
		t.Error("expected Active=false after DeleteEdge")
	}
}
