package graph

import (
	"testing"

	"github.com/alexdx2/chronicle-core/internal/store"
)

func basePayload() ImportPayload {
	return ImportPayload{
		Nodes: []ImportNode{
			{
				NodeKey:   "code:controller:test-domain:nodea",
				Layer:     "code",
				NodeType:  "controller",
				DomainKey: "test-domain",
				Name:      "NodeA",
			},
			{
				NodeKey:   "code:provider:test-domain:nodeb",
				Layer:     "code",
				NodeType:  "provider",
				DomainKey: "test-domain",
				Name:      "NodeB",
			},
		},
		Edges: []ImportEdge{
			{
				FromNodeKey:    "code:controller:test-domain:nodea",
				ToNodeKey:      "code:provider:test-domain:nodeb",
				EdgeType:       "INJECTS",
				DerivationKind: "hard",
				FromLayer:      "code",
				ToLayer:        "code",
			},
		},
		Evidence: []ImportEvidence{
			{
				TargetKind:       "node",
				NodeKey:          "code:controller:test-domain:nodea",
				SourceKind:       "file",
				ExtractorID:      "test-extractor",
				ExtractorVersion: "1.0.0",
			},
		},
	}
}

func TestImportAll(t *testing.T) {
	g := setupGraph(t)
	revID := makeRevision(t, g)

	result, err := g.ImportAll(basePayload(), revID)
	if err != nil {
		t.Fatalf("ImportAll: %v", err)
	}
	if result.NodesCreated != 2 {
		t.Errorf("NodesCreated = %d, want 2", result.NodesCreated)
	}
	if result.EdgesCreated != 1 {
		t.Errorf("EdgesCreated = %d, want 1", result.EdgesCreated)
	}
	if result.EvidenceCreated != 1 {
		t.Errorf("EvidenceCreated = %d, want 1", result.EvidenceCreated)
	}

	// Verify nodes persisted.
	nodes, err := g.store.ListNodes(store.NodeFilter{Domain: "test-domain"})
	if err != nil {
		t.Fatalf("ListNodes: %v", err)
	}
	if len(nodes) != 2 {
		t.Errorf("expected 2 nodes, got %d", len(nodes))
	}
}

func TestImportAllValidationFailure(t *testing.T) {
	g := setupGraph(t)
	revID := makeRevision(t, g)

	payload := ImportPayload{
		Nodes: []ImportNode{
			{
				NodeKey:   "code:controller:test-domain:valid",
				Layer:     "code",
				NodeType:  "controller",
				DomainKey: "test-domain",
				Name:      "Valid",
			},
			{
				NodeKey:   "code:badtype:test-domain:invalid",
				Layer:     "code",
				NodeType:  "badtype", // invalid
				DomainKey: "test-domain",
				Name:      "Invalid",
			},
		},
	}

	_, err := g.ImportAll(payload, revID)
	if err == nil {
		t.Fatal("expected error for invalid node type")
	}

	// Verify rollback: no nodes should exist.
	nodes, err := g.store.ListNodes(store.NodeFilter{Domain: "test-domain"})
	if err != nil {
		t.Fatalf("ListNodes: %v", err)
	}
	if len(nodes) != 0 {
		t.Errorf("expected 0 nodes after rollback, got %d", len(nodes))
	}
}

func TestImportAllIdempotent(t *testing.T) {
	g := setupGraph(t)
	revID := makeRevision(t, g)

	payload := basePayload()

	// First import.
	r1, err := g.ImportAll(payload, revID)
	if err != nil {
		t.Fatalf("ImportAll first: %v", err)
	}

	// Second import (same data).
	r2, err := g.ImportAll(payload, revID)
	if err != nil {
		t.Fatalf("ImportAll second: %v", err)
	}

	// Counts returned should be the same (upsert is idempotent).
	if r1.NodesCreated != r2.NodesCreated {
		t.Errorf("NodesCreated differs: %d vs %d", r1.NodesCreated, r2.NodesCreated)
	}

	// Actual rows in DB should not be duplicated.
	nodes, err := g.store.ListNodes(store.NodeFilter{Domain: "test-domain"})
	if err != nil {
		t.Fatalf("ListNodes: %v", err)
	}
	if len(nodes) != 2 {
		t.Errorf("expected 2 nodes after double import, got %d", len(nodes))
	}

	edges, err := g.store.ListEdges(store.EdgeFilter{})
	if err != nil {
		t.Fatalf("ListEdges: %v", err)
	}
	if len(edges) != 1 {
		t.Errorf("expected 1 edge after double import, got %d", len(edges))
	}
}
