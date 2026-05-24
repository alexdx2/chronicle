package store

import (
	"testing"
)

func TestGetScanProgressNoActiveScan(t *testing.T) {
	s := openTestStore(t)

	progress, err := s.GetScanProgress("orders")
	if err != nil {
		t.Fatalf("GetScanProgress: %v", err)
	}
	if progress.Phase != "idle" {
		t.Errorf("expected phase='idle', got %q", progress.Phase)
	}
	if progress.TotalNodes != 0 {
		t.Errorf("expected TotalNodes=0, got %d", progress.TotalNodes)
	}
}

func TestGetScanProgressWithNodes(t *testing.T) {
	s := openTestStore(t)

	// Insert a node directly via SQL to avoid dependency on UpsertNode logic.
	_, err := s.db.Exec(`
		INSERT INTO graph_nodes
			(node_key, layer, node_type, domain_key, name, status,
			 first_seen_revision_id, last_seen_revision_id, confidence, metadata)
		VALUES
			('code:controller:orders:oc', 'code', 'controller', 'orders', 'OC', 'active',
			 0, 0, 1.0, '{}')
	`)
	if err != nil {
		t.Fatalf("insert node: %v", err)
	}

	progress, err := s.GetScanProgress("orders")
	if err != nil {
		t.Fatalf("GetScanProgress: %v", err)
	}

	if progress.TotalNodes != 1 {
		t.Errorf("expected TotalNodes=1, got %d", progress.TotalNodes)
	}
	if progress.NodesByLayer["code"] != 1 {
		t.Errorf("expected NodesByLayer['code']=1, got %d", progress.NodesByLayer["code"])
	}
	if progress.NodesByType["controller"] != 1 {
		t.Errorf("expected NodesByType['controller']=1, got %d", progress.NodesByType["controller"])
	}
}
