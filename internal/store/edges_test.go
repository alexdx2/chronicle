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
