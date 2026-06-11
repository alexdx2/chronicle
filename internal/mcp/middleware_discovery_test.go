package mcp

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/alexdx2/chronicle-core/graph"
	"github.com/alexdx2/chronicle-core/registry"
	"github.com/alexdx2/chronicle-core/store"
)

func newDiscoveryTestGraph(t *testing.T) (*graph.Graph, *store.Store) {
	t.Helper()
	dir := t.TempDir()
	s, err := store.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	reg, err := registry.LoadDefaults()
	if err != nil {
		t.Fatalf("registry.LoadDefaults: %v", err)
	}
	return graph.New(s, reg), s
}

// TestAutoDiscoverFlagsLowTrustEdges: the post-import discovery scan flags
// edges by derived trust_score (capped confidence × freshness), not raw
// confidence:
//   - an edge whose only evidence is LLM-asserted and unverified
//     (chronicle-scan) is capped at 0.65 → trust < 0.7 → flagged
//   - an edge with chronicle-ast (structural) evidence reaches 0.85 → not flagged
//   - an edge with HIGH raw confidence but degraded trust (stale) is still
//     flagged — proving the predicate reads trust_score, not confidence
//   - a flow edge (TRIGGERS_FLOW, backed by chronicle:derive_flows) with low
//     trust is NOT flagged — flow edges are derived by construction and never
//     mechanically verifiable, so flagging them is noise
func TestAutoDiscoverFlagsLowTrustEdges(t *testing.T) {
	g, s := newDiscoveryTestGraph(t)

	revID, err := s.CreateRevision("orders", "", "abc123", "manual", "full", "{}")
	if err != nil {
		t.Fatalf("CreateRevision: %v", err)
	}

	payload := graph.ImportPayload{
		Nodes: []graph.ImportNode{
			{NodeKey: "code:provider:orders:a", Layer: "code", NodeType: "provider", DomainKey: "orders", Name: "A", FilePath: "src/a.ts"},
			{NodeKey: "code:provider:orders:b", Layer: "code", NodeType: "provider", DomainKey: "orders", Name: "B", FilePath: "src/b.ts"},
			{NodeKey: "code:provider:orders:c", Layer: "code", NodeType: "provider", DomainKey: "orders", Name: "C", FilePath: "src/c.ts"},
			{NodeKey: "code:provider:orders:d", Layer: "code", NodeType: "provider", DomainKey: "orders", Name: "D", FilePath: "src/d.ts"},
			{NodeKey: "contract:endpoint:orders:getorders", Layer: "contract", NodeType: "endpoint", DomainKey: "orders", Name: "GET /orders"},
			{NodeKey: "flow:use_case:orders:getorders", Layer: "flow", NodeType: "use_case", DomainKey: "orders", Name: "Get Orders"},
		},
		Edges: []graph.ImportEdge{
			{FromNodeKey: "code:provider:orders:a", ToNodeKey: "code:provider:orders:b", EdgeType: "INJECTS", DerivationKind: "hard", FromLayer: "code", ToLayer: "code"},
			{FromNodeKey: "code:provider:orders:b", ToNodeKey: "code:provider:orders:c", EdgeType: "INJECTS", DerivationKind: "hard", FromLayer: "code", ToLayer: "code"},
			{FromNodeKey: "code:provider:orders:c", ToNodeKey: "code:provider:orders:d", EdgeType: "INJECTS", DerivationKind: "hard", FromLayer: "code", ToLayer: "code"},
			{FromNodeKey: "contract:endpoint:orders:getorders", ToNodeKey: "flow:use_case:orders:getorders", EdgeType: "TRIGGERS_FLOW", DerivationKind: "inferred", FromLayer: "contract", ToLayer: "flow"},
		},
		Evidence: []graph.ImportEvidence{
			// LLM-only, unverified → confidence cap 0.65 → trust < 0.7
			{TargetKind: "edge", EdgeKey: "code:provider:orders:a->code:provider:orders:b:INJECTS",
				SourceKind: "file", FilePath: "src/a.ts", LineStart: 5,
				ExtractorID: "chronicle-scan", ExtractorVersion: "1.0"},
			// Structural AST evidence → cap 0.85 → trust >= 0.7
			{TargetKind: "edge", EdgeKey: "code:provider:orders:b->code:provider:orders:c:INJECTS",
				SourceKind: "file", FilePath: "src/b.ts", LineStart: 7,
				ExtractorID: "chronicle-ast", ExtractorVersion: "1.0"},
			{TargetKind: "edge", EdgeKey: "code:provider:orders:c->code:provider:orders:d:INJECTS",
				SourceKind: "file", FilePath: "src/c.ts", LineStart: 9,
				ExtractorID: "chronicle-ast", ExtractorVersion: "1.0"},
			// Derived flow evidence → cap 0.60 → trust < 0.7, but flow edges
			// must be excluded from the low-trust scan.
			{TargetKind: "edge", EdgeKey: "contract:endpoint:orders:getorders->flow:use_case:orders:getorders:TRIGGERS_FLOW",
				SourceKind: "synthetic",
				ExtractorID: "chronicle:derive_flows", ExtractorVersion: "1.0"},
		},
	}
	if _, err := g.ImportAll(payload, revID); err != nil {
		t.Fatalf("ImportAll: %v", err)
	}

	// Sanity: derived trust matches the ConfidenceCap v2 tiers.
	llmEdge, err := s.GetEdgeByKey("code:provider:orders:a->code:provider:orders:b:INJECTS")
	if err != nil {
		t.Fatalf("GetEdgeByKey llm: %v", err)
	}
	if llmEdge.TrustScore <= 0 || llmEdge.TrustScore >= 0.7 {
		t.Fatalf("LLM-only edge trust = %v, want in (0, 0.7)", llmEdge.TrustScore)
	}
	astEdge, err := s.GetEdgeByKey("code:provider:orders:b->code:provider:orders:c:INJECTS")
	if err != nil {
		t.Fatalf("GetEdgeByKey ast: %v", err)
	}
	if astEdge.TrustScore < 0.7 {
		t.Fatalf("AST edge trust = %v, want >= 0.7", astEdge.TrustScore)
	}

	// Degrade the third edge's trust while keeping raw confidence high:
	// if the discovery scan read raw confidence (0.9), it would miss this one.
	staleEdge, err := s.GetEdgeByKey("code:provider:orders:c->code:provider:orders:d:INJECTS")
	if err != nil {
		t.Fatalf("GetEdgeByKey stale: %v", err)
	}
	if err := s.UpdateEdgeTrust(staleEdge.EdgeID, 0.9, 0.65, 0.6, "stale"); err != nil {
		t.Fatalf("UpdateEdgeTrust: %v", err)
	}

	// Sanity: the flow edge really has low trust — it is skipped by edge
	// type, not because its trust happens to be high.
	flowEdge, err := s.GetEdgeByKey("contract:endpoint:orders:getorders->flow:use_case:orders:getorders:TRIGGERS_FLOW")
	if err != nil {
		t.Fatalf("GetEdgeByKey flow: %v", err)
	}
	if flowEdge.TrustScore <= 0 || flowEdge.TrustScore >= 0.7 {
		t.Fatalf("flow edge trust = %v, want in (0, 0.7) so the exclusion is actually exercised", flowEdge.TrustScore)
	}

	autoDiscover(s, "{}")

	discoveries, err := s.ListDiscoveries("orders", "")
	if err != nil {
		t.Fatalf("ListDiscoveries: %v", err)
	}
	var lowTrust *store.Discovery
	for i := range discoveries {
		if strings.Contains(discoveries[i].Title, "low-trust edges detected") {
			lowTrust = &discoveries[i]
			break
		}
	}
	if lowTrust == nil {
		t.Fatalf("no low-trust discovery created; got %d discoveries", len(discoveries))
	}
	// Exactly 2 flagged: the LLM-only edge and the stale edge — NOT the AST
	// edge and NOT the low-trust TRIGGERS_FLOW edge (flow edges are excluded).
	if !strings.HasPrefix(lowTrust.Title, "2 ") {
		t.Errorf("low-trust discovery title = %q, want 2 edges flagged (LLM-only + stale; not AST, not flow)", lowTrust.Title)
	}
}

// TestAutoDiscoverNoLowTrustDiscoveryWhenAllVerified: a graph whose edges all
// carry structural (chronicle-ast) evidence produces no low-trust discovery.
func TestAutoDiscoverNoLowTrustDiscoveryWhenAllVerified(t *testing.T) {
	g, s := newDiscoveryTestGraph(t)

	revID, err := s.CreateRevision("orders", "", "abc123", "manual", "full", "{}")
	if err != nil {
		t.Fatalf("CreateRevision: %v", err)
	}

	payload := graph.ImportPayload{
		Nodes: []graph.ImportNode{
			{NodeKey: "code:provider:orders:a", Layer: "code", NodeType: "provider", DomainKey: "orders", Name: "A", FilePath: "src/a.ts"},
			{NodeKey: "code:provider:orders:b", Layer: "code", NodeType: "provider", DomainKey: "orders", Name: "B", FilePath: "src/b.ts"},
		},
		Edges: []graph.ImportEdge{
			{FromNodeKey: "code:provider:orders:a", ToNodeKey: "code:provider:orders:b", EdgeType: "INJECTS", DerivationKind: "hard", FromLayer: "code", ToLayer: "code"},
		},
		Evidence: []graph.ImportEvidence{
			{TargetKind: "edge", EdgeKey: "code:provider:orders:a->code:provider:orders:b:INJECTS",
				SourceKind: "file", FilePath: "src/a.ts", LineStart: 5,
				ExtractorID: "chronicle-ast", ExtractorVersion: "1.0"},
		},
	}
	if _, err := g.ImportAll(payload, revID); err != nil {
		t.Fatalf("ImportAll: %v", err)
	}

	autoDiscover(s, "{}")

	discoveries, err := s.ListDiscoveries("orders", "")
	if err != nil {
		t.Fatalf("ListDiscoveries: %v", err)
	}
	for _, d := range discoveries {
		if strings.Contains(d.Title, "low-trust edges detected") {
			t.Errorf("unexpected low-trust discovery: %q", d.Title)
		}
	}
}
