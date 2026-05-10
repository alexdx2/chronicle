package graph

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alexdx2/chronicle-core/registry"
	"github.com/alexdx2/chronicle-core/store"
)

// L2 — Mocked enrichment pipeline tests.
// These tests feed realistic LLM-like responses (good and bad) through
// normalization → validation → resolve and verify the resulting graph.
// No actual LLM is called.

func setupTestGraph(t *testing.T) (*Graph, *store.Store, int64) {
	t.Helper()
	tmpDir := t.TempDir()
	s, err := store.Open(filepath.Join(tmpDir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })

	reg, _ := registry.LoadDefaults()
	g := New(s, reg)
	revID, _ := s.CreateRevision("testapp", "scan_1", "", "manual", "full", "{}")
	return g, s, revID
}

// --- L2.1: Good enrichment responses ---

func TestEnrichment_GoodControllerResponse(t *testing.T) {
	g, _, revID := setupTestGraph(t)

	// Simulates combined AST + LLM facts for a NestJS controller
	facts := `[
		{"kind":"endpoint","method":"POST","target":"/orders"},
		{"kind":"injects","to":"OrderService"},
		{"kind":"calls_service","to":"OrderService","method":"create"}
	]`
	g.SaveFileExtraction(revID, "testapp", "src/order.controller.ts", "extracted", "controller", facts, "")

	result, err := g.ResolveExtractions("testapp", revID)
	if err != nil {
		t.Fatal(err)
	}

	assertEdgeExists(t, g, "testapp", "EXPOSES_ENDPOINT")
	assertEdgeExists(t, g, "testapp", "INJECTS")
	assertEdgeExists(t, g, "testapp", "CALLS_SERVICE")

	if result.FilesProcessed == 0 {
		t.Error("expected files to be processed")
	}
}

func TestEnrichment_GoodServiceResponse(t *testing.T) {
	g, _, revID := setupTestGraph(t)

	facts := `[
		{"kind":"injects","to":"PrismaService"},
		{"kind":"uses_model","to":"Order"},
		{"kind":"produces","to":"order.created","method":"emit"},
		{"kind":"calls_service","to":"PaymentService","method":"charge"}
	]`
	g.SaveFileExtraction(revID, "testapp", "src/order.service.ts", "extracted", "", facts, "")

	_, err := g.ResolveExtractions("testapp", revID)
	if err != nil {
		t.Fatal(err)
	}

	assertEdgeExists(t, g, "testapp", "INJECTS")
	assertEdgeExists(t, g, "testapp", "USES_MODEL")
	assertEdgeExists(t, g, "testapp", "PUBLISHES_TOPIC")
	assertEdgeExists(t, g, "testapp", "CALLS_SERVICE")
}

func TestEnrichment_PrismaSchemaResponse(t *testing.T) {
	g, _, revID := setupTestGraph(t)

	// Simulates correct Prisma extraction
	facts := `[
		{"kind":"model","to":"User"},
		{"kind":"model","to":"Order"},
		{"kind":"model","to":"Payment"},
		{"kind":"enum","to":"OrderStatus"},
		{"kind":"model_relation","from":"Order","to":"User"},
		{"kind":"model_relation","from":"Payment","to":"Order"}
	]`
	g.SaveFileExtraction(revID, "testapp", "prisma/schema.prisma", "extracted", "", facts, "")

	result, err := g.ResolveExtractions("testapp", revID)
	if err != nil {
		t.Fatal(err)
	}

	// Must have 3 models and 1 enum
	nodes, _ := g.store.ListNodes(store.NodeFilter{Domain: "testapp"})
	modelCount := 0
	enumCount := 0
	for _, n := range nodes {
		if n.Layer == "data" && n.NodeType == "model" {
			modelCount++
		}
		if n.Layer == "data" && n.NodeType == "enum" {
			enumCount++
		}
	}
	if modelCount != 3 {
		t.Errorf("expected 3 models, got %d", modelCount)
	}
	if enumCount != 1 {
		t.Errorf("expected 1 enum, got %d", enumCount)
	}

	// Must have REFERENCES_MODEL edges
	edges, _ := g.store.ListEdges(store.EdgeFilter{EdgeType: "REFERENCES_MODEL"})
	if len(edges) < 2 {
		t.Errorf("expected at least 2 REFERENCES_MODEL edges, got %d", len(edges))
	}

	if result.FilesProcessed != 1 {
		t.Errorf("expected 1 file processed, got %d", result.FilesProcessed)
	}
}

// --- L2.1b: Bad enrichment responses (normalizer should handle) ---

func TestEnrichment_InventedKinds(t *testing.T) {
	g, _, revID := setupTestGraph(t)

	// LLM invents kinds like "dependency", "depends_on", "class", "component"
	facts := `[
		{"kind":"dependency","to":"@nestjs/common"},
		{"kind":"depends_on","from":"OrderService","to":"PrismaService"},
		{"kind":"class","name":"OrderController"},
		{"kind":"component","name":"CartView"},
		{"kind":"method_call","to":"orderService","method":"create"},
		{"kind":"uses_modal","to":"Order"}
	]`
	g.SaveFileExtraction(revID, "testapp", "src/order.controller.ts", "extracted", "", facts, "")

	result, err := g.ResolveExtractions("testapp", revID)
	if err != nil {
		t.Fatal(err)
	}

	// "dependency" should be normalized to import
	// "depends_on" should be normalized to injects
	// "class", "component" should be dropped
	// "method_call" should become calls_service
	// "uses_modal" should become uses_model

	// Should NOT have errors for normalized kinds
	for _, u := range result.Unresolved {
		if u.Kind == "parse_error" {
			t.Errorf("unexpected parse error: %s", u.Reason)
		}
	}
}

func TestEnrichment_CandidateWrapperFormat(t *testing.T) {
	g, _, revID := setupTestGraph(t)

	// LLM returns candidate wrapper as top-level fact (common mistake)
	facts := `[
		{"candidate_id":"c1","decision":"accept","fact":{"kind":"calls_service","to":"OrderService","method":"create"}},
		{"candidate_id":"c2","decision":"reject","reason":"logging"},
		{"candidate_id":"c3","decision":"accept","fact":{"kind":"uses_model","to":"User"}}
	]`
	g.SaveFileExtraction(revID, "testapp", "src/order.service.ts", "extracted", "", facts, "")

	result, err := g.ResolveExtractions("testapp", revID)
	if err != nil {
		t.Fatal(err)
	}

	// c1 and c3 accepted, c2 rejected
	// Should produce calls_service and uses_model edges
	assertEdgeExists(t, g, "testapp", "CALLS_SERVICE")
	assertEdgeExists(t, g, "testapp", "USES_MODEL")

	if result.FilesProcessed != 1 {
		t.Errorf("expected 1 file, got %d", result.FilesProcessed)
	}
}

func TestEnrichment_StructuredOutput(t *testing.T) {
	g, _, revID := setupTestGraph(t)

	// New structured format: {candidate_decisions, additional_facts}
	facts := `{
		"candidate_decisions": [
			{"candidate_id":"c1","decision":"accept","fact":{"kind":"calls_service","to":"PaymentService","method":"charge"}},
			{"candidate_id":"c2","decision":"reject","reason":"generic_utility"}
		],
		"additional_facts": [
			{"kind":"http_call","method":"POST","target":"https://api.stripe.com/v1/charges"},
			{"kind":"produces","to":"payment.completed","method":"emit"}
		]
	}`
	g.SaveFileExtraction(revID, "testapp", "src/payment.service.ts", "extracted", "", facts, "")

	result, err := g.ResolveExtractions("testapp", revID)
	if err != nil {
		t.Fatal(err)
	}

	// Should produce calls_service, http_call (no edge but stored), produces
	assertEdgeExists(t, g, "testapp", "CALLS_SERVICE")
	assertEdgeExists(t, g, "testapp", "PUBLISHES_TOPIC")

	if result.FilesProcessed != 1 {
		t.Errorf("expected 1 file, got %d", result.FilesProcessed)
	}
}

func TestEnrichment_EmptyExtractedFile(t *testing.T) {
	g, _, revID := setupTestGraph(t)

	// LLM returns empty facts for a service file (bad behavior we want to detect)
	g.SaveFileExtraction(revID, "testapp", "src/order.service.ts", "extracted", "", "[]", "")

	result, err := g.ResolveExtractions("testapp", revID)
	if err != nil {
		t.Fatal(err)
	}

	// No nodes/edges created from empty facts
	nodes, _ := g.store.ListNodes(store.NodeFilter{Domain: "testapp"})
	if len(nodes) != 0 {
		t.Errorf("expected 0 nodes from empty facts, got %d", len(nodes))
	}
	_ = result
}

func TestEnrichment_MixedFieldAliases(t *testing.T) {
	g, _, revID := setupTestGraph(t)

	// LLM uses various field name aliases that normalizer should handle
	facts := `[
		{"kind":"produces","event_type":"order.created","handler_method":"emit"},
		{"kind":"http_call","url":"https://api.example.com/webhook","method":"POST"},
		{"kind":"dependency_injection","injected_type":"PrismaService"}
	]`
	g.SaveFileExtraction(revID, "testapp", "src/order.service.ts", "extracted", "", facts, "")

	result, err := g.ResolveExtractions("testapp", revID)
	if err != nil {
		t.Fatal(err)
	}

	// "event_type" -> "to", "handler_method" -> "method" => PUBLISHES_TOPIC
	assertEdgeExists(t, g, "testapp", "PUBLISHES_TOPIC")
	// "dependency_injection" -> "injects", "injected_type" -> "to" => INJECTS
	assertEdgeExists(t, g, "testapp", "INJECTS")

	// No parse errors
	for _, u := range result.Unresolved {
		if u.Kind == "parse_error" {
			t.Errorf("unexpected parse error: %s", u.Reason)
		}
	}
}

// --- L2.2: Must-have / Must-not-have assertion pattern ---

type GraphAssertion struct {
	MustHaveEdges    []string // "EDGE_TYPE" that must exist
	MustNotHaveEdges []string // "EDGE_TYPE" that must NOT exist
	MinNodes         int
	MinEdges         int
}

func TestEnrichment_MustHaveMustNotHave(t *testing.T) {
	g, _, revID := setupTestGraph(t)

	// Controller + Service + Prisma model
	controllerFacts := `[
		{"kind":"endpoint","method":"POST","target":"/orders"},
		{"kind":"injects","to":"OrderService"},
		{"kind":"calls_service","to":"OrderService","method":"create"}
	]`
	g.SaveFileExtraction(revID, "testapp", "src/order.controller.ts", "extracted", "controller", controllerFacts, "")

	serviceFacts := `[
		{"kind":"injects","to":"PrismaService"},
		{"kind":"uses_model","to":"Order"},
		{"kind":"produces","to":"order.created","method":"emit"}
	]`
	g.SaveFileExtraction(revID, "testapp", "src/order.service.ts", "extracted", "", serviceFacts, "")

	modelFacts := `[
		{"kind":"model","to":"Order"},
		{"kind":"model","to":"User"},
		{"kind":"enum","to":"OrderStatus"},
		{"kind":"model_relation","from":"Order","to":"User"}
	]`
	g.SaveFileExtraction(revID, "testapp", "prisma/schema.prisma", "extracted", "", modelFacts, "")

	_, err := g.ResolveExtractions("testapp", revID)
	if err != nil {
		t.Fatal(err)
	}

	assertion := GraphAssertion{
		MustHaveEdges: []string{
			"EXPOSES_ENDPOINT",
			"INJECTS",
			"CALLS_SERVICE",
			"USES_MODEL",
			"PUBLISHES_TOPIC",
			"REFERENCES_MODEL",
		},
		MustNotHaveEdges: []string{
			"DEPENDS_ON", // should not exist — all edges should be specific types
		},
		MinNodes: 5, // controller, service, prisma, order model, user model, enum, endpoint, topic
		MinEdges: 5,
	}

	assertGraph(t, g, "testapp", assertion)
}

// --- L3: Full scan idempotency ---

func TestFullScan_Idempotent(t *testing.T) {
	g, s, revID1 := setupTestGraph(t)

	facts := `[
		{"kind":"endpoint","method":"POST","target":"/orders"},
		{"kind":"injects","to":"OrderService"},
		{"kind":"calls_service","to":"OrderService","method":"create"}
	]`
	g.SaveFileExtraction(revID1, "testapp", "src/order.controller.ts", "extracted", "controller", facts, "")

	result1, err := g.ResolveExtractions("testapp", revID1)
	if err != nil {
		t.Fatal(err)
	}

	nodes1, _ := s.ListNodes(store.NodeFilter{Domain: "testapp"})
	edges1, _ := s.ListEdges(store.EdgeFilter{})

	// Second scan with same facts
	revID2, _ := s.CreateRevision("testapp", "scan_2", "", "manual", "full", "{}")
	g.SaveFileExtraction(revID2, "testapp", "src/order.controller.ts", "extracted", "controller", facts, "")

	result2, err := g.ResolveExtractions("testapp", revID2)
	if err != nil {
		t.Fatal(err)
	}

	nodes2, _ := s.ListNodes(store.NodeFilter{Domain: "testapp"})
	edges2, _ := s.ListEdges(store.EdgeFilter{})

	// Same graph — no duplicates
	if len(nodes2) != len(nodes1) {
		t.Errorf("idempotent scan changed node count: %d -> %d", len(nodes1), len(nodes2))
	}
	if len(edges2) != len(edges1) {
		t.Errorf("idempotent scan changed edge count: %d -> %d", len(edges1), len(edges2))
	}

	_ = result1
	_ = result2
}

// --- Helpers ---

func assertEdgeExists(t *testing.T, g *Graph, domain, edgeType string) {
	t.Helper()
	edges, _ := g.store.ListEdges(store.EdgeFilter{EdgeType: edgeType})
	if len(edges) == 0 {
		t.Errorf("expected at least one %s edge, got 0", edgeType)
	}
}

func assertGraph(t *testing.T, g *Graph, domain string, a GraphAssertion) {
	t.Helper()

	nodes, _ := g.store.ListNodes(store.NodeFilter{Domain: domain})
	allEdges, _ := g.store.ListEdges(store.EdgeFilter{})

	if len(nodes) < a.MinNodes {
		t.Errorf("expected at least %d nodes, got %d", a.MinNodes, len(nodes))
	}
	if len(allEdges) < a.MinEdges {
		t.Errorf("expected at least %d edges, got %d", a.MinEdges, len(allEdges))
	}

	edgeTypes := map[string]int{}
	for _, e := range allEdges {
		edgeTypes[e.EdgeType]++
	}

	for _, must := range a.MustHaveEdges {
		if edgeTypes[must] == 0 {
			t.Errorf("MUST HAVE edge type %s — not found. Have: %v", must, edgeTypeList(edgeTypes))
		}
	}

	for _, mustNot := range a.MustNotHaveEdges {
		if edgeTypes[mustNot] > 0 {
			t.Errorf("MUST NOT HAVE edge type %s — found %d", mustNot, edgeTypes[mustNot])
		}
	}
}

func edgeTypeList(m map[string]int) string {
	var parts []string
	for k, v := range m {
		parts = append(parts, k+"("+json.Number(strings.Repeat("1", v)).String()+")")
	}
	return strings.Join(parts, ", ")
}
