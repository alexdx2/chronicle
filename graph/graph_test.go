package graph

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/alexdx2/chronicle-core/registry"
	"github.com/alexdx2/chronicle-core/store"
	"github.com/alexdx2/chronicle-core/validate"
)

func setupGraph(t *testing.T) *Graph {
	t.Helper()
	dir := t.TempDir()
	s, err := store.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	reg, err := registry.LoadFile("../testdata/registry/valid.yaml")
	if err != nil {
		t.Fatalf("LoadRegistry: %v", err)
	}
	return New(s, reg)
}

func setupGraphDefaults(t *testing.T) *Graph {
	t.Helper()
	dir := t.TempDir()
	s, err := store.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	reg, err := registry.LoadDefaults()
	if err != nil {
		t.Fatalf("LoadDefaults: %v", err)
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

func TestGraphUpsertEdgeDependencySource(t *testing.T) {
	g := setupGraph(t)
	revID := makeRevision(t, g)

	if _, err := g.UpsertNode(validate.NodeInput{
		NodeKey: "code:controller:test-domain:nodea", Layer: "code", NodeType: "controller",
		DomainKey: "test-domain", Name: "NodeA",
	}, revID); err != nil {
		t.Fatalf("UpsertNode A: %v", err)
	}
	if _, err := g.UpsertNode(validate.NodeInput{
		NodeKey: "code:provider:test-domain:nodeb", Layer: "code", NodeType: "provider",
		DomainKey: "test-domain", Name: "NodeB",
	}, revID); err != nil {
		t.Fatalf("UpsertNode B: %v", err)
	}

	if _, err := g.UpsertEdge(validate.EdgeInput{
		FromNodeKey: "code:controller:test-domain:nodea", ToNodeKey: "code:provider:test-domain:nodeb",
		EdgeType: "INJECTS", DerivationKind: "hard", FromLayer: "code", ToLayer: "code",
		DependencySource: "manifest",
	}, revID); err != nil {
		t.Fatalf("UpsertEdge: %v", err)
	}

	got, err := g.store.GetEdgeByKey("code:controller:test-domain:nodea->code:provider:test-domain:nodeb:INJECTS")
	if err != nil {
		t.Fatalf("GetEdgeByKey: %v", err)
	}
	if got.DependencySource != "manifest" {
		t.Errorf("DependencySource = %q, want manifest", got.DependencySource)
	}
}

func TestGraphUpsertEdgeRawKeysMatchNormalizedNodes(t *testing.T) {
	// Regression for the 2026-07-04 debug-journal bug: nodes are stored under
	// normalized keys (tom.weapon.equipped → tom-weapon-equipped), so an edge
	// arriving with the agent's raw keys must still resolve both endpoints.
	g := setupGraph(t)
	revID := makeRevision(t, g)

	_, err := g.UpsertNode(validate.NodeInput{
		NodeKey:   "code:controller:test-domain:tom.weapon.equipped",
		Layer:     "code",
		NodeType:  "controller",
		DomainKey: "test-domain",
		Name:      "TomWeaponEquipped",
	}, revID)
	if err != nil {
		t.Fatalf("UpsertNode from: %v", err)
	}
	_, err = g.UpsertNode(validate.NodeInput{
		NodeKey:   "contract:endpoint:test-domain:get__tom_status",
		Layer:     "contract",
		NodeType:  "endpoint",
		DomainKey: "test-domain",
		Name:      "GET /tom/status",
	}, revID)
	if err != nil {
		t.Fatalf("UpsertNode to: %v", err)
	}

	edgeID, err := g.UpsertEdge(validate.EdgeInput{
		FromNodeKey:    "code:controller:test-domain:tom.weapon.equipped",
		ToNodeKey:      "contract:endpoint:test-domain:get__tom_status",
		EdgeType:       "CALLS_ENDPOINT",
		DerivationKind: "hard",
		FromLayer:      "code",
		ToLayer:        "contract",
	}, revID)
	if err != nil {
		t.Fatalf("UpsertEdge with raw keys must resolve normalized nodes: %v", err)
	}
	if edgeID == 0 {
		t.Fatal("expected non-zero edge ID")
	}
}

func TestGraphUpsertEdgeResolvesResolverLiteralKeys(t *testing.T) {
	// The scan resolver stores topic nodes under literal keys (dots kept:
	// contract:topic:d:tom.weapon.equipped) via store-level upsert. An edge
	// arriving with that exact key must attach to the stored node — never be
	// rewritten to the (kebab) normalized form that matches nothing.
	g := setupGraph(t)
	revID := makeRevision(t, g)

	_, err := g.UpsertNode(validate.NodeInput{
		NodeKey:   "code:provider:test-domain:producer",
		Layer:     "code",
		NodeType:  "provider",
		DomainKey: "test-domain",
		Name:      "Producer",
	}, revID)
	if err != nil {
		t.Fatalf("UpsertNode producer: %v", err)
	}
	// Store-level insert — bypasses validate on purpose (resolver path).
	if _, err := g.Store().UpsertNode(store.NodeRow{
		NodeKey: "contract:topic:test-domain:tom.weapon.equipped",
		Layer:   "contract", NodeType: "topic", DomainKey: "test-domain",
		Name: "tom.weapon.equipped", Status: "active",
		LastSeenRevisionID: revID, Confidence: 1, Freshness: 1, TrustScore: 1,
		Metadata: "{}",
	}); err != nil {
		t.Fatalf("store UpsertNode topic: %v", err)
	}

	edgeID, err := g.UpsertEdge(validate.EdgeInput{
		FromNodeKey:    "code:provider:test-domain:producer",
		ToNodeKey:      "contract:topic:test-domain:tom.weapon.equipped",
		EdgeType:       "PUBLISHES_TOPIC",
		DerivationKind: "hard",
		FromLayer:      "code",
		ToLayer:        "contract",
	}, revID)
	if err != nil {
		t.Fatalf("UpsertEdge to resolver-literal topic key: %v", err)
	}
	if edgeID == 0 {
		t.Fatal("expected non-zero edge ID")
	}

	// The edge must reference the stored dotted key, not a kebab rewrite.
	edges, _ := g.Store().ListEdges(store.EdgeFilter{EdgeType: "PUBLISHES_TOPIC"})
	found := false
	for _, e := range edges {
		if e.ToNodeKey == "contract:topic:test-domain:tom.weapon.equipped" {
			found = true
		}
	}
	if !found {
		t.Errorf("edge does not reference the stored dotted topic key; edges: %v", edges)
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

// seedResolveGraph creates a graph with nodes that have distinct names for resolve tests.
// Nodes: M (module), A (controller), OrdersProvider (provider), PaymentsProvider (provider), D (service).
func seedResolveGraph(t *testing.T) *Graph {
	t.Helper()
	g := setupGraph(t)
	revID, _ := g.Store().CreateRevision("orders", "", "sha1", "manual", "full", "{}")

	g.UpsertNode(validate.NodeInput{NodeKey: "code:module:orders:m", Layer: "code", NodeType: "module", DomainKey: "orders", Name: "M"}, revID)
	g.UpsertNode(validate.NodeInput{NodeKey: "code:controller:orders:a", Layer: "code", NodeType: "controller", DomainKey: "orders", Name: "A"}, revID)
	g.UpsertNode(validate.NodeInput{NodeKey: "code:provider:orders:b", Layer: "code", NodeType: "provider", DomainKey: "orders", Name: "OrdersProvider"}, revID)
	g.UpsertNode(validate.NodeInput{NodeKey: "code:provider:orders:c", Layer: "code", NodeType: "provider", DomainKey: "orders", Name: "PaymentsProvider"}, revID)
	g.UpsertNode(validate.NodeInput{NodeKey: "service:service:orders:d", Layer: "service", NodeType: "service", DomainKey: "orders", Name: "D"}, revID)

	return g
}

func TestResolveNodeKeyExact(t *testing.T) {
	g := seedResolveGraph(t)
	key, err := g.ResolveNodeKey("code:controller:orders:a")
	if err != nil {
		t.Fatalf("ResolveNodeKey exact: %v", err)
	}
	if key != "code:controller:orders:a" {
		t.Errorf("got %q, want code:controller:orders:a", key)
	}
}

func TestResolveNodeKeyByName(t *testing.T) {
	g := seedResolveGraph(t)
	key, err := g.ResolveNodeKey("A")
	if err != nil {
		t.Fatalf("ResolveNodeKey by name: %v", err)
	}
	if key != "code:controller:orders:a" {
		t.Errorf("got %q, want code:controller:orders:a", key)
	}
}

func TestResolveNodeKeyByNameCaseInsensitive(t *testing.T) {
	g := seedResolveGraph(t)
	key, err := g.ResolveNodeKey("a")
	if err != nil {
		t.Fatalf("ResolveNodeKey case-insensitive: %v", err)
	}
	if key != "code:controller:orders:a" {
		t.Errorf("got %q, want code:controller:orders:a", key)
	}
}

func TestResolveNodeKeyAmbiguous(t *testing.T) {
	g := seedResolveGraph(t)
	// "Provider" matches both OrdersProvider and PaymentsProvider.
	_, err := g.ResolveNodeKey("Provider")
	if err == nil {
		t.Fatal("expected ambiguous error, got nil")
	}
	// Error should mention "ambiguous" and list candidates.
	errStr := err.Error()
	if !strings.Contains(errStr, "ambiguous") {
		t.Errorf("error should mention ambiguous: %v", err)
	}
	if !strings.Contains(errStr, "OrdersProvider") || !strings.Contains(errStr, "PaymentsProvider") {
		t.Errorf("error should list candidates: %v", err)
	}
}

func TestResolveNodeKeyNotFound(t *testing.T) {
	g := seedResolveGraph(t)
	_, err := g.ResolveNodeKey("NonExistent")
	if err == nil {
		t.Fatal("expected not-found error, got nil")
	}
	if !strings.Contains(err.Error(), "no node found") {
		t.Errorf("error should mention not found: %v", err)
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
