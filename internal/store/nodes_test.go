package store

import (
	"errors"
	"testing"
)

func makeNodeRow(key, layer, nodeType, domain, name string, revID int64) NodeRow {
	return NodeRow{
		NodeKey: key, Layer: layer, NodeType: nodeType, DomainKey: domain,
		Name: name, Status: "active",
		FirstSeenRevisionID: revID, LastSeenRevisionID: revID,
		Confidence: 1.0, Metadata: "{}",
	}
}

func TestUpsertNodeInsert(t *testing.T) {
	s := openTestStore(t)
	revID, _ := s.CreateRevision("orders", "", "sha1", "manual", "full", "{}")
	n := makeNodeRow("code:controller:orders:oc", "code", "controller", "orders", "OC", revID)
	id, err := s.UpsertNode(n)
	if err != nil {
		t.Fatalf("UpsertNode insert: %v", err)
	}
	if id <= 0 {
		t.Fatalf("expected positive id, got %d", id)
	}
}

func TestUpsertNodeUpdate(t *testing.T) {
	s := openTestStore(t)
	revID, _ := s.CreateRevision("orders", "", "sha1", "manual", "full", "{}")
	n := makeNodeRow("code:controller:orders:oc", "code", "controller", "orders", "OC", revID)
	id1, err := s.UpsertNode(n)
	if err != nil {
		t.Fatalf("UpsertNode first insert: %v", err)
	}

	// Update name and confidence.
	n.Name = "OrdersController"
	n.Confidence = 0.9
	id2, err := s.UpsertNode(n)
	if err != nil {
		t.Fatalf("UpsertNode update: %v", err)
	}
	if id1 != id2 {
		t.Errorf("expected same id on update, got %d vs %d", id1, id2)
	}

	got, err := s.GetNodeByKey("code:controller:orders:oc")
	if err != nil {
		t.Fatalf("GetNodeByKey: %v", err)
	}
	if got.Name != "OrdersController" {
		t.Errorf("Name = %q, want OrdersController", got.Name)
	}
	if got.Confidence != 0.9 {
		t.Errorf("Confidence = %v, want 0.9", got.Confidence)
	}
}

func TestUpsertNodeConflict(t *testing.T) {
	s := openTestStore(t)
	revID, _ := s.CreateRevision("orders", "", "sha1", "manual", "full", "{}")
	n := makeNodeRow("code:controller:orders:oc", "code", "controller", "orders", "OC", revID)
	_, err := s.UpsertNode(n)
	if err != nil {
		t.Fatalf("UpsertNode first: %v", err)
	}

	// Same key but different layer.
	n.Layer = "service"
	_, err = s.UpsertNode(n)
	if err == nil {
		t.Fatal("expected conflict error, got nil")
	}
}

func TestGetNodeByKeyNotFound(t *testing.T) {
	s := openTestStore(t)
	_, err := s.GetNodeByKey("nonexistent:key")
	if err == nil {
		t.Fatal("expected ErrNotFound, got nil")
	}
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestListNodes(t *testing.T) {
	s := openTestStore(t)
	revID, _ := s.CreateRevision("orders", "", "sha1", "manual", "full", "{}")
	s.UpsertNode(makeNodeRow("code:controller:orders:oc", "code", "controller", "orders", "OC", revID))
	s.UpsertNode(makeNodeRow("service:svc:orders:os", "service", "svc", "orders", "OS", revID))
	s.UpsertNode(makeNodeRow("code:provider:billing:bp", "code", "provider", "billing", "BP", revID))

	all, err := s.ListNodes(NodeFilter{})
	if err != nil {
		t.Fatalf("ListNodes all: %v", err)
	}
	if len(all) != 3 {
		t.Errorf("expected 3 nodes, got %d", len(all))
	}

	byLayer, err := s.ListNodes(NodeFilter{Layer: "code"})
	if err != nil {
		t.Fatalf("ListNodes by layer: %v", err)
	}
	if len(byLayer) != 2 {
		t.Errorf("expected 2 code nodes, got %d", len(byLayer))
	}

	byType, err := s.ListNodes(NodeFilter{NodeType: "controller"})
	if err != nil {
		t.Fatalf("ListNodes by type: %v", err)
	}
	if len(byType) != 1 {
		t.Errorf("expected 1 controller, got %d", len(byType))
	}
}

func TestDeleteNode(t *testing.T) {
	s := openTestStore(t)
	revID, _ := s.CreateRevision("orders", "", "sha1", "manual", "full", "{}")
	s.UpsertNode(makeNodeRow("code:controller:orders:oc", "code", "controller", "orders", "OC", revID))

	if err := s.DeleteNode("code:controller:orders:oc"); err != nil {
		t.Fatalf("DeleteNode: %v", err)
	}

	n, err := s.GetNodeByKey("code:controller:orders:oc")
	if err != nil {
		t.Fatalf("GetNodeByKey after delete: %v", err)
	}
	if n.Status != "deleted" {
		t.Errorf("status = %q, want deleted", n.Status)
	}
}

func TestDeleteNodeNotFound(t *testing.T) {
	s := openTestStore(t)
	err := s.DeleteNode("does:not:exist")
	if err == nil {
		t.Fatal("expected ErrNotFound, got nil")
	}
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}
