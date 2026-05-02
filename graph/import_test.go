package graph

import (
	"testing"

	"github.com/alexdx2/chronicle-core/store"
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

func TestImportAllPartialAccept(t *testing.T) {
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

	result, err := g.ImportAll(payload, revID)
	if err != nil {
		t.Fatalf("ImportAll should not return error for partial accept: %v", err)
	}

	// Valid node written, invalid node rejected
	if result.NodesCreated != 1 {
		t.Errorf("NodesCreated = %d, want 1", result.NodesCreated)
	}
	if len(result.Rejected) != 1 {
		t.Fatalf("Rejected = %d, want 1", len(result.Rejected))
	}
	if result.Rejected[0].Kind != "node" {
		t.Errorf("Rejected[0].Kind = %s, want node", result.Rejected[0].Kind)
	}

	// Valid node should be persisted
	nodes, err := g.store.ListNodes(store.NodeFilter{Domain: "test-domain"})
	if err != nil {
		t.Fatalf("ListNodes: %v", err)
	}
	if len(nodes) != 1 {
		t.Errorf("expected 1 node persisted, got %d", len(nodes))
	}
}

func TestImportAllEdgeRejectedWithSuggestion(t *testing.T) {
	g := setupGraphDefaults(t)
	revID := makeRevision(t, g)

	payload := ImportPayload{
		Nodes: []ImportNode{
			{
				NodeKey:   "flow:use_case:test-domain:order",
				Layer:     "flow",
				NodeType:  "use_case",
				DomainKey: "test-domain",
				Name:      "PlaceOrder",
			},
			{
				NodeKey:   "flow:use_case:test-domain:payment",
				Layer:     "flow",
				NodeType:  "use_case",
				DomainKey: "test-domain",
				Name:      "ProcessPayment",
			},
		},
		Edges: []ImportEdge{
			{
				FromNodeKey: "flow:use_case:test-domain:order",
				ToNodeKey:   "flow:use_case:test-domain:payment",
				EdgeType:    "TRIGGERS_FLOW", // wrong: flow→flow should be TRANSITIONS_TO
			},
		},
	}

	result, err := g.ImportAll(payload, revID)
	if err != nil {
		t.Fatalf("ImportAll: %v", err)
	}

	// Nodes accepted, edge rejected
	if result.NodesCreated != 2 {
		t.Errorf("NodesCreated = %d, want 2", result.NodesCreated)
	}
	if result.EdgesCreated != 0 {
		t.Errorf("EdgesCreated = %d, want 0", result.EdgesCreated)
	}
	if len(result.Rejected) != 1 {
		t.Fatalf("Rejected = %d, want 1", len(result.Rejected))
	}

	rej := result.Rejected[0]
	if rej.Kind != "edge" {
		t.Errorf("Rejected[0].Kind = %s, want edge", rej.Kind)
	}
	if rej.Suggestion == nil {
		t.Fatal("expected suggestion for rejected edge")
	}
	if rej.Suggestion.To != "TRANSITIONS_TO" {
		t.Errorf("expected suggestion TRANSITIONS_TO, got %s", rej.Suggestion.To)
	}
}

func TestImportAllDryRun_Valid(t *testing.T) {
	g := setupGraph(t)
	revID := makeRevision(t, g)

	result, err := g.ImportAllDryRun(basePayload(), revID)
	if err != nil {
		t.Fatalf("ImportAllDryRun: %v", err)
	}
	if !result.Valid {
		t.Errorf("expected valid=true, got errors: %v", result.Errors)
	}
	if result.NodesValidated != 2 {
		t.Errorf("NodesValidated = %d, want 2", result.NodesValidated)
	}
	if result.EdgesValidated != 1 {
		t.Errorf("EdgesValidated = %d, want 1", result.EdgesValidated)
	}
	if result.EvidenceValidated != 1 {
		t.Errorf("EvidenceValidated = %d, want 1", result.EvidenceValidated)
	}

	// Nothing should be persisted
	nodes, err := g.store.ListNodes(store.NodeFilter{Domain: "test-domain"})
	if err != nil {
		t.Fatalf("ListNodes: %v", err)
	}
	if len(nodes) != 0 {
		t.Errorf("dry_run should not persist nodes, got %d", len(nodes))
	}
}

func TestImportAllDryRun_Invalid(t *testing.T) {
	g := setupGraphDefaults(t)
	revID := makeRevision(t, g)

	payload := ImportPayload{
		Nodes: []ImportNode{
			{
				NodeKey:   "flow:use_case:test-domain:placeorder",
				Layer:     "flow",
				NodeType:  "use_case",
				DomainKey: "test-domain",
				Name:      "PlaceOrder",
			},
		},
		Edges: []ImportEdge{
			{
				FromNodeKey:    "flow:use_case:test-domain:placeorder",
				ToNodeKey:      "code:provider:test-domain:orderservice",
				EdgeType:       "CALLS_SERVICE",
				DerivationKind: "hard",
				FromLayer:      "flow",
				ToLayer:        "code",
			},
		},
	}

	result, err := g.ImportAllDryRun(payload, revID)
	if err != nil {
		t.Fatalf("ImportAllDryRun: %v", err)
	}
	if result.Valid {
		t.Error("expected valid=false for invalid edge")
	}
	if len(result.Errors) == 0 {
		t.Error("expected errors")
	}
	if len(result.SuggestedFixes) == 0 {
		t.Error("expected suggested fixes")
	}

	// Check the fix suggests INVOKES or REQUIRES
	fix := result.SuggestedFixes[0]
	if fix.From != "CALLS_SERVICE" {
		t.Errorf("expected fix.From=CALLS_SERVICE, got %s", fix.From)
	}
	if fix.To != "INVOKES" && fix.To != "REQUIRES" && fix.To != "DEPENDS_ON" {
		t.Errorf("expected fix.To to be a valid flow edge type, got %s", fix.To)
	}

	// Nothing persisted
	nodes, err := g.store.ListNodes(store.NodeFilter{Domain: "test-domain"})
	if err != nil {
		t.Fatalf("ListNodes: %v", err)
	}
	if len(nodes) != 0 {
		t.Errorf("dry_run should not persist nodes, got %d", len(nodes))
	}
}

func TestImportAllDomainAlias(t *testing.T) {
	g := setupGraphDefaults(t)
	revID := makeRevision(t, g)

	// Claude sends "domain" instead of "domain_key" — both should work
	payload := ImportPayload{
		Nodes: []ImportNode{
			{
				NodeKey:  "data:model:test-domain:user",
				Layer:    "data",
				NodeType: "model",
				Domain:   "test-domain", // alias, not domain_key
				Name:     "User",
			},
		},
	}

	result, err := g.ImportAll(payload, revID)
	if err != nil {
		t.Fatalf("ImportAll with domain alias: %v", err)
	}
	if result.NodesCreated != 1 {
		t.Errorf("NodesCreated = %d, want 1", result.NodesCreated)
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
