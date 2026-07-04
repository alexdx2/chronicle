package store

import "testing"

func TestUpdateNodeMetadata(t *testing.T) {
	s := openTestStore(t)
	revID, _ := s.CreateRevision("orders", "", "sha", "manual", "full", "{}")
	id, err := s.UpsertNode(makeNodeRow("code:provider:orders:p", "code", "provider", "orders", "P", revID))
	if err != nil {
		t.Fatalf("UpsertNode: %v", err)
	}
	want := `{"complexity":{"cyclomatic":7}}`
	if err := s.UpdateNodeMetadata(id, want); err != nil {
		t.Fatalf("UpdateNodeMetadata: %v", err)
	}
	n, err := s.GetNodeByKey("code:provider:orders:p")
	if err != nil {
		t.Fatalf("GetNodeByKey: %v", err)
	}
	if n.Metadata != want {
		t.Fatalf("metadata = %q, want %q", n.Metadata, want)
	}
}
