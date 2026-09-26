package graph

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/alexdx2/chronicle-core/gitutil"
	"github.com/alexdx2/chronicle-core/paths"
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
	_, err = g.SaveFileExtraction(revID, "myapp", "package.json", "extracted", "", string(facts1), "")
	if err != nil {
		t.Fatalf("SaveExtraction 1: %v", err)
	}

	// Agent 2: reads order.service.ts
	facts2, _ := json.Marshal([]Fact{
		{Kind: "import", From: "OrderService", To: "@nestjs/common", Symbols: []string{"Injectable"}},
		{Kind: "import", From: "OrderService", To: "./pricing.engine", Symbols: []string{"PricingEngine"}},
		{Kind: "call", From: "OrderService.create", Object: "pricingEngine", Method: "calculate"},
	})
	_, err = g.SaveFileExtraction(revID, "myapp", "src/order.service.ts", "extracted", "", string(facts2), "")
	if err != nil {
		t.Fatalf("SaveExtraction 2: %v", err)
	}

	// Agent 3: reads pricing.engine.ts
	facts3, _ := json.Marshal([]Fact{
		{Kind: "import", From: "PricingEngine", To: "@nestjs/common", Symbols: []string{"Injectable"}},
		{Kind: "import", From: "PricingEngine", To: "@prisma/client", Symbols: []string{"PrismaClient"}},
	})
	_, err = g.SaveFileExtraction(revID, "myapp", "src/pricing.engine.ts", "extracted", "", string(facts3), "")
	if err != nil {
		t.Fatalf("SaveExtraction 3: %v", err)
	}

	// Agent 4: reads constants.ts — no architecture
	_, err = g.SaveFileExtraction(revID, "myapp", "src/constants.ts", "no_runtime_architecture", "", "", "")
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
		t.Errorf("expected 1 no_runtime_architecture, got %d", noArch)
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
	g.SaveFileExtraction(revID, "myapp", "src/payment.service.ts", "extracted", "", string(facts), "")

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

func TestExtractionWithDelegation(t *testing.T) {
	tmpDir := t.TempDir()
	s, err := store.Open(filepath.Join(tmpDir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	reg, _ := registry.LoadDefaults()
	g := New(s, reg)

	revID, _ := s.CreateRevision("myapp", "scan_1", "", "manual", "full", "{}")

	facts, _ := json.Marshal([]Fact{
		{Kind: "delegates", To: "./handler.factory.ts", Method: "registerHandlers"},
	})
	_, err = g.SaveFileExtraction(revID, "myapp", "src/gateway.ts", "extracted", "", string(facts), "")
	if err != nil {
		t.Fatalf("SaveExtraction: %v", err)
	}

	result, err := g.ResolveExtractions("myapp", revID)
	if err != nil {
		t.Fatalf("ResolveExtractions: %v", err)
	}

	if len(result.Unresolved) == 0 {
		t.Error("expected delegation to be reported as unresolved")
	}

	// Check obligation was created for delegated file
	open, _ := s.ListOpenObligations(revID)
	found := false
	for _, ob := range open {
		if ob.TargetKey == "src/handler.factory.ts" {
			found = true
		}
	}
	if !found {
		t.Error("expected obligation created for delegated file src/handler.factory.ts")
	}
}

// TestResolveExtractions_EvidenceCarriesBlobSHA is an end-to-end check (real
// files on disk, a real ResolveExtractions call — not a hand-built
// store.EvidenceRow) that the evidence resolve creates for a freshly-created
// node AND a freshly-created edge both carry the git blob of the file that
// asserted them. addCreationEvidence and addEdgeCreationEvidence are the two
// functions that build that evidence (graph/resolve_extractions.go); this
// covers both in one fixture since one import fact naturally exercises both.
func TestResolveExtractions_EvidenceCarriesBlobSHA(t *testing.T) {
	root := t.TempDir()
	orderContent := []byte("import { PricingEngine } from './pricing.engine';\nexport class OrderService {}\n")
	pricingContent := []byte("import { Injectable } from '@nestjs/common';\nexport class PricingEngine {}\n")
	if err := os.MkdirAll(filepath.Join(root, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "src/order.service.ts"), orderContent, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "src/pricing.engine.ts"), pricingContent, 0o644); err != nil {
		t.Fatal(err)
	}
	paths.SetProjectRoot(root)
	t.Cleanup(func() { paths.SetProjectRoot("") })

	s, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	reg, _ := registry.LoadDefaults()
	g := New(s, reg)
	revID, _ := s.CreateRevision("myapp", "scan_1", "", "manual", "full", "{}")

	facts1, _ := json.Marshal([]Fact{
		{Kind: "import", From: "OrderService", To: "./pricing.engine", Symbols: []string{"PricingEngine"}},
	})
	if _, err := g.SaveFileExtraction(revID, "myapp", "src/order.service.ts", "extracted", "", string(facts1), ""); err != nil {
		t.Fatalf("SaveExtraction order.service: %v", err)
	}
	facts2, _ := json.Marshal([]Fact{
		{Kind: "import", From: "PricingEngine", To: "@nestjs/common", Symbols: []string{"Injectable"}},
	})
	if _, err := g.SaveFileExtraction(revID, "myapp", "src/pricing.engine.ts", "extracted", "", string(facts2), ""); err != nil {
		t.Fatalf("SaveExtraction pricing.engine: %v", err)
	}

	result, err := g.ResolveExtractions("myapp", revID)
	if err != nil {
		t.Fatalf("ResolveExtractions: %v", err)
	}
	// NodesCreated stays 0 for nodes minted via ensureNodeID's own accounting
	// (a per-fact-kind counter, not a total) — the nodes and their creation
	// evidence exist regardless; asserted directly below via ListNodes.
	if result.EdgesCreated == 0 {
		t.Fatalf("setup: expected an edge created: %+v", result)
	}

	wantOrderBlob, err := gitutil.FileBlobSHA(root, "src/order.service.ts")
	if err != nil {
		t.Fatalf("FileBlobSHA order.service: %v", err)
	}
	wantPricingBlob, err := gitutil.FileBlobSHA(root, "src/pricing.engine.ts")
	if err != nil {
		t.Fatalf("FileBlobSHA pricing.engine: %v", err)
	}
	if wantOrderBlob == "" || wantPricingBlob == "" {
		t.Fatal("test setup: computed want-blobs must not be empty")
	}

	nodes, err := s.ListNodes(store.NodeFilter{Domain: "myapp"})
	if err != nil {
		t.Fatalf("ListNodes: %v", err)
	}
	var orderNodeID, pricingNodeID int64
	for _, n := range nodes {
		switch n.FilePath {
		case "src/order.service.ts":
			orderNodeID = n.NodeID
		case "src/pricing.engine.ts":
			pricingNodeID = n.NodeID
		}
	}
	if orderNodeID == 0 || pricingNodeID == 0 {
		t.Fatalf("setup: expected a node for each file, got: %+v", nodes)
	}

	// --- addCreationEvidence (node path) ---
	checkNodeBlob := func(nodeID int64, file, wantBlob string) {
		t.Helper()
		rows, err := s.ListEvidenceByNode(nodeID)
		if err != nil {
			t.Fatalf("ListEvidenceByNode(%s): %v", file, err)
		}
		found := false
		for _, r := range rows {
			if r.FilePath != file {
				continue
			}
			found = true
			if r.BlobSHA == "" {
				t.Errorf("%s: node creation evidence BlobSHA is empty", file)
			} else if r.BlobSHA != wantBlob {
				t.Errorf("%s: node creation evidence BlobSHA = %q, want %q", file, r.BlobSHA, wantBlob)
			}
		}
		if !found {
			t.Errorf("%s: no evidence row with this file_path: %+v", file, rows)
		}
	}
	checkNodeBlob(orderNodeID, "src/order.service.ts", wantOrderBlob)
	checkNodeBlob(pricingNodeID, "src/pricing.engine.ts", wantPricingBlob)

	// --- addEdgeCreationEvidence (edge path) ---
	edges, err := s.ListEdges(store.EdgeFilter{})
	if err != nil {
		t.Fatalf("ListEdges: %v", err)
	}
	if len(edges) == 0 {
		t.Fatal("setup: expected at least one edge")
	}
	edgeChecked := false
	for _, e := range edges {
		evRows, err := s.ListEvidenceByEdge(e.EdgeID)
		if err != nil {
			t.Fatalf("ListEvidenceByEdge(%s): %v", e.EdgeKey, err)
		}
		for _, r := range evRows {
			if r.FilePath != "src/order.service.ts" {
				continue
			}
			edgeChecked = true
			if r.BlobSHA == "" {
				t.Errorf("edge %s: creation evidence BlobSHA is empty", e.EdgeKey)
			} else if r.BlobSHA != wantOrderBlob {
				t.Errorf("edge %s: creation evidence BlobSHA = %q, want %q", e.EdgeKey, r.BlobSHA, wantOrderBlob)
			}
		}
	}
	if !edgeChecked {
		t.Fatal("no edge evidence asserted by src/order.service.ts found — addEdgeCreationEvidence path not exercised by this fixture")
	}
}
