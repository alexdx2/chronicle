package graph

import (
	"path/filepath"
	"testing"

	"github.com/anthropics/depbot/internal/registry"
	"github.com/anthropics/depbot/internal/store"
	"github.com/anthropics/depbot/internal/validate"
)

func setupGraph(t *testing.T) *Graph {
	t.Helper()
	dir := t.TempDir()
	s, err := store.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	reg, err := registry.LoadFile("../../testdata/registry/valid.yaml")
	if err != nil {
		t.Fatalf("LoadRegistry: %v", err)
	}
	return New(s, reg)
}

func makeRevision(t *testing.T, g *Graph) int64 {
	t.Helper()
	id, err := g.store.CreateRevision("test-domain", "", "abc123", "full_scan", "full", "{}")
	if err != nil {
		t.Fatalf("CreateRevision: %v", err)
	}
	return id
}

func TestGraphUpsertNodeValid(t *testing.T) {
	g := setupGraph(t)
	revID := makeRevision(t, g)

	id, err := g.UpsertNode(validate.NodeInput{
		NodeKey:   "code:controller:test-domain:mycontroller",
		Layer:     "code",
		NodeType:  "controller",
		DomainKey: "test-domain",
		Name:      "MyController",
	}, revID)
	if err != nil {
		t.Fatalf("UpsertNode: %v", err)
	}
	if id == 0 {
		t.Fatal("expected non-zero node ID")
	}
}

func TestGraphUpsertNodeInvalidType(t *testing.T) {
	g := setupGraph(t)
	revID := makeRevision(t, g)

	_, err := g.UpsertNode(validate.NodeInput{
		NodeKey:   "code:badtype:test-domain:mynode",
		Layer:     "code",
		NodeType:  "badtype",
		DomainKey: "test-domain",
		Name:      "MyNode",
	}, revID)
	if err == nil {
		t.Fatal("expected validation error for invalid node type")
	}
}

func TestGraphUpsertEdgeValid(t *testing.T) {
	g := setupGraph(t)
	revID := makeRevision(t, g)

	// Create two nodes first.
	_, err := g.UpsertNode(validate.NodeInput{
		NodeKey:   "code:controller:test-domain:nodea",
		Layer:     "code",
		NodeType:  "controller",
		DomainKey: "test-domain",
		Name:      "NodeA",
	}, revID)
	if err != nil {
		t.Fatalf("UpsertNode A: %v", err)
	}

	_, err = g.UpsertNode(validate.NodeInput{
		NodeKey:   "code:provider:test-domain:nodeb",
		Layer:     "code",
		NodeType:  "provider",
		DomainKey: "test-domain",
		Name:      "NodeB",
	}, revID)
	if err != nil {
		t.Fatalf("UpsertNode B: %v", err)
	}

	edgeID, err := g.UpsertEdge(validate.EdgeInput{
		FromNodeKey:    "code:controller:test-domain:nodea",
		ToNodeKey:      "code:provider:test-domain:nodeb",
		EdgeType:       "INJECTS",
		DerivationKind: "hard",
		FromLayer:      "code",
		ToLayer:        "code",
	}, revID)
	if err != nil {
		t.Fatalf("UpsertEdge: %v", err)
	}
	if edgeID == 0 {
		t.Fatal("expected non-zero edge ID")
	}
}

func TestGraphUpsertEdgeInvalidLayers(t *testing.T) {
	g := setupGraph(t)
	revID := makeRevision(t, g)

	// Create two nodes.
	_, err := g.UpsertNode(validate.NodeInput{
		NodeKey:   "code:controller:test-domain:nodea",
		Layer:     "code",
		NodeType:  "controller",
		DomainKey: "test-domain",
		Name:      "NodeA",
	}, revID)
	if err != nil {
		t.Fatalf("UpsertNode A: %v", err)
	}

	_, err = g.UpsertNode(validate.NodeInput{
		NodeKey:   "service:service:test-domain:svcb",
		Layer:     "service",
		NodeType:  "service",
		DomainKey: "test-domain",
		Name:      "SvcB",
	}, revID)
	if err != nil {
		t.Fatalf("UpsertNode B: %v", err)
	}

	// INJECTS is code->code only; code->service should fail.
	_, err = g.UpsertEdge(validate.EdgeInput{
		FromNodeKey:    "code:controller:test-domain:nodea",
		ToNodeKey:      "service:service:test-domain:svcb",
		EdgeType:       "INJECTS",
		DerivationKind: "hard",
		FromLayer:      "code",
		ToLayer:        "service",
	}, revID)
	if err == nil {
		t.Fatal("expected validation error for invalid edge layers")
	}
}

func TestGraphAddEvidence(t *testing.T) {
	g := setupGraph(t)
	revID := makeRevision(t, g)

	_, err := g.UpsertNode(validate.NodeInput{
		NodeKey:   "code:controller:test-domain:mynode",
		Layer:     "code",
		NodeType:  "controller",
		DomainKey: "test-domain",
		Name:      "MyNode",
	}, revID)
	if err != nil {
		t.Fatalf("UpsertNode: %v", err)
	}

	evidID, err := g.AddNodeEvidence("code:controller:test-domain:mynode", validate.EvidenceInput{
		TargetKind:       "node",
		SourceKind:       "file",
		ExtractorID:      "test-extractor",
		ExtractorVersion: "1.0.0",
	})
	if err != nil {
		t.Fatalf("AddNodeEvidence: %v", err)
	}
	if evidID == 0 {
		t.Fatal("expected non-zero evidence ID")
	}
}
