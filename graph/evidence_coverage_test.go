package graph

import (
	"testing"

	"github.com/alexdx2/chronicle-core/store"
)

// countZeroEvidenceNodes returns active current-version nodes with no evidence rows.
func countZeroEvidenceNodes(t *testing.T, s *store.Store) int {
	t.Helper()
	var n int
	err := s.QueryRowScan(`
		SELECT COUNT(*) FROM graph_nodes n
		WHERE n.status = 'active'
		  AND (n.valid_to_revision_id IS NULL OR n.valid_to_revision_id = 0)
		  AND NOT EXISTS (SELECT 1 FROM graph_evidence e WHERE e.node_id = n.node_id)`, &n)
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func TestEnsureNodeCreatesEvidence(t *testing.T) {
	g := setupGraphDefaults(t)
	revID, err := g.store.CreateRevision("dom", "", "abc", "manual", "full", "{}")
	if err != nil {
		t.Fatalf("CreateRevision: %v", err)
	}

	g.ensureNode("dom", revID, "code:provider:dom:src/score.service", "ScoreService", "src/score.service.ts")
	g.ensureNode("dom", revID, "code:provider:dom:phantomservice", "PhantomService", "") // no file → synthetic

	if n := countZeroEvidenceNodes(t, g.store); n != 0 {
		t.Errorf("expected 0 zero-evidence nodes, got %d", n)
	}
}

func TestEnsureFlowNodeCreatesEvidence(t *testing.T) {
	g := setupGraphDefaults(t)
	revID, err := g.store.CreateRevision("dom", "", "abc", "manual", "full", "{}")
	if err != nil {
		t.Fatalf("CreateRevision: %v", err)
	}

	g.ensureFlowNode("dom", revID, "flow:use_case:dom:attack", "attack (POST /arena/attack)")

	if n := countZeroEvidenceNodes(t, g.store); n != 0 {
		t.Errorf("expected 0 zero-evidence nodes, got %d", n)
	}
}
