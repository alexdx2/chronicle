package graph

import (
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/alexdx2/chronicle-core/registry"
	"github.com/alexdx2/chronicle-core/store"
)

// TestExtractionFlow tests the complete extraction → resolution flow:
// Multiple agents extract facts from files independently,
// then MCP resolves all facts into a unified graph.
func TestExtractionFlow(t *testing.T) {
	tmpDir := t.TempDir()
	s, err := store.Open(filepath.Join(tmpDir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	reg, _ := registry.LoadDefaults()
	g := New(s, reg)

	revID, _ := s.CreateRevision("myapp", "scan_1", "", "manual", "full", "{}")

	// === Simulate 3 agents extracting from 3 files in parallel ===

	// Agent 1: reads package.json
	facts1, _ := json.Marshal([]Fact{
		{Kind: "dependency", To: "@nestjs/common"},
		{Kind: "dependency", To: "@prisma/client"},
		{Kind: "dependency", To: "kafkajs"},
	})
	_, err = g.SaveFileExtraction(revID, "myapp", "package.json", "extracted", string(facts1), "")
	if err != nil {
		t.Fatalf("SaveExtraction 1: %v", err)
	}

	// Agent 2: reads order.service.ts
	facts2, _ := json.Marshal([]Fact{
		{Kind: "import", From: "OrderService", To: "@nestjs/common", Symbols: []string{"Injectable"}},
		{Kind: "import", From: "OrderService", To: "./pricing.engine", Symbols: []string{"PricingEngine"}},
		{Kind: "call", From: "OrderService.create", Object: "pricingEngine", Method: "calculate"},
	})
	_, err = g.SaveFileExtraction(revID, "myapp", "src/order.service.ts", "extracted", string(facts2), "")
	if err != nil {
		t.Fatalf("SaveExtraction 2: %v", err)
	}

	// Agent 3: reads pricing.engine.ts
	facts3, _ := json.Marshal([]Fact{
		{Kind: "import", From: "PricingEngine", To: "@nestjs/common", Symbols: []string{"Injectable"}},
		{Kind: "import", From: "PricingEngine", To: "@prisma/client", Symbols: []string{"PrismaClient"}},
	})
	_, err = g.SaveFileExtraction(revID, "myapp", "src/pricing.engine.ts", "extracted", string(facts3), "")
	if err != nil {
		t.Fatalf("SaveExtraction 3: %v", err)
	}

	// Agent 4: reads constants.ts — no architecture
	_, err = g.SaveFileExtraction(revID, "myapp", "src/constants.ts", "no_architecture", "", "")
	if err != nil {
		t.Fatalf("SaveExtraction 4: %v", err)
	}

	// === Verify extractions stored ===
	extractions, _ := s.ListExtractions(revID, "myapp")
	if len(extractions) != 4 {
		t.Fatalf("expected 4 extractions, got %d", len(extractions))
	}

	unresolved, _ := s.ListUnresolvedExtractions(revID, "myapp")
	if len(unresolved) != 3 {
		t.Fatalf("expected 3 unresolved (extracted status), got %d", len(unresolved))
	}

	// === Resolve all extractions into graph ===
	result, err := g.ResolveExtractions("myapp", revID)
	if err != nil {
		t.Fatalf("ResolveExtractions: %v", err)
	}

	t.Logf("ResolveExtractions: files=%d nodes=%d edges=%d evidence=%d unresolved=%d",
		result.FilesProcessed, result.NodesCreated, result.EdgesCreated,
		result.EvidenceCreated, len(result.Unresolved))

	// Should have created nodes and edges
	if result.FilesProcessed != 3 {
		t.Errorf("expected 3 files processed, got %d", result.FilesProcessed)
	}
	if result.EdgesCreated == 0 {
		t.Error("expected edges to be created")
	}
	if result.EvidenceCreated == 0 {
		t.Error("expected evidence to be created")
	}

	// === Verify graph state ===
	nodes, _ := s.ListNodes(store.NodeFilter{Domain: "myapp"})
	t.Logf("Nodes created: %d", len(nodes))
	for _, n := range nodes {
		t.Logf("  %s (%s/%s)", n.NodeKey, n.Layer, n.NodeType)
	}

	edges, _ := s.ListEdges(store.EdgeFilter{})
	t.Logf("Edges created: %d", len(edges))
	for _, e := range edges {
		t.Logf("  %s", e.EdgeKey)
	}

	// After filtering: @nestjs/common, @prisma/client, kafkajs are all filtered
	// (architectural or infrastructure). Only ./pricing.engine remains as tracked dep.
	if len(nodes) < 2 {
		t.Errorf("expected at least 2 nodes (source + pricing.engine), got %d", len(nodes))
	}
	if len(edges) < 1 {
		t.Errorf("expected at least 1 edge (order.service -> pricing.engine), got %d", len(edges))
	}

	// === Verify coverage ===
	total, extracted, noArch, skipped, errored, err := s.GetScanCoverage(revID, "myapp")
	if err != nil {
		t.Fatalf("GetScanCoverage: %v", err)
	}
	t.Logf("Coverage: total=%d extracted=%d noArch=%d skipped=%d errored=%d",
		total, extracted, noArch, skipped, errored)

	if total != 4 {
		t.Errorf("expected 4 total files tracked, got %d", total)
	}
	if extracted != 3 {
		t.Errorf("expected 3 extracted, got %d", extracted)
	}
	if noArch != 1 {
		t.Errorf("expected 1 no_architecture, got %d", noArch)
	}

	// === Verify no unresolved extractions remain ===
	stillUnresolved, _ := s.ListUnresolvedExtractions(revID, "myapp")
	if len(stillUnresolved) != 0 {
		t.Errorf("expected 0 unresolved after resolution, got %d", len(stillUnresolved))
	}
}

// TestExtractionWithHTTPCall tests that external HTTP calls are reported as unresolved.
func TestExtractionWithHTTPCall(t *testing.T) {
	tmpDir := t.TempDir()
	s, err := store.Open(filepath.Join(tmpDir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	reg, _ := registry.LoadDefaults()
	g := New(s, reg)

	revID, _ := s.CreateRevision("myapp", "scan_http", "", "manual", "full", "{}")

	facts, _ := json.Marshal([]Fact{
		{Kind: "import", From: "PaymentService", To: "@nestjs/common", Symbols: []string{"Injectable", "HttpService"}},
		{Kind: "http_call", From: "PaymentService.charge", Target: "http://stripe-api:3000/charge", Method: "POST"},
	})
	g.SaveFileExtraction(revID, "myapp", "src/payment.service.ts", "extracted", string(facts), "")

	result, err := g.ResolveExtractions("myapp", revID)
	if err != nil {
		t.Fatalf("ResolveExtractions: %v", err)
	}

	t.Logf("Edges: %d, Evidence: %d, Unresolved: %d", result.EdgesCreated, result.EvidenceCreated, len(result.Unresolved))

	// HTTP calls now create external_system nodes + CALLS_SERVICE edges with evidence
	if result.EdgesCreated == 0 {
		t.Error("expected edge created for HTTP call to stripe-api")
	}
	if result.EvidenceCreated == 0 {
		t.Error("expected evidence with assertion for HTTP call")
	}

	// Verify the external system node was created
	nodes, _ := s.ListNodes(store.NodeFilter{Domain: "myapp"})
	foundExternal := false
	for _, n := range nodes {
		if n.Name == "stripe-api" {
			foundExternal = true
		}
	}
	if !foundExternal {
		t.Error("expected external_system node 'stripe-api' to be created")
	}
}
