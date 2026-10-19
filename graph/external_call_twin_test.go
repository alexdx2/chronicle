package graph

import (
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/alexdx2/chronicle-core/registry"
	"github.com/alexdx2/chronicle-core/store"
	"github.com/alexdx2/chronicle-core/validate"
)

const badgeClientFile = "analytics-api/src/analytics/badge.client.ts"

// badgeClientFacts is the fact shape a model emits for one outbound call:
// the http_call, the service it names and the endpoint it hits.
func badgeClientFacts() []Fact {
	return []Fact{
		{Kind: "http_call", From: "BadgeClient.get", FromType: "provider", Target: "http://badges-api:3009/badges/:userId", Method: "GET"},
		{Kind: "calls_service", From: "BadgeClient", FromType: "provider", To: "badges-api"},
		{Kind: "calls_endpoint", From: "BadgeClient", FromType: "provider", Target: "/badges/:userId", Method: "GET"},
	}
}

func newTwinGraph(t *testing.T) (*Graph, *store.Store) {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	reg, _ := registry.LoadDefaults()
	return New(s, reg), s
}

func scanFile(t *testing.T, g *Graph, s *store.Store, scan string, facts []Fact) {
	t.Helper()
	revID, _ := s.CreateRevision("demo", scan, "", "manual", "full", "{}")
	b, _ := json.Marshal(facts)
	if _, err := g.SaveFileExtraction(revID, "demo", badgeClientFile, "extracted", "", string(b), ""); err != nil {
		t.Fatalf("SaveFileExtraction: %v", err)
	}
	if _, err := g.ResolveExtractions("demo", revID); err != nil {
		t.Fatalf("ResolveExtractions: %v", err)
	}
}

func activeKeys(t *testing.T, s *store.Store, typ string) map[string]string {
	t.Helper()
	nodes, _ := s.ListNodes(store.NodeFilter{Domain: "demo"})
	out := map[string]string{}
	for _, n := range nodes {
		if n.NodeType == typ && n.Status != "deleted" {
			out[n.NodeKey] = n.Status
		}
	}
	return out
}

func TestExternalCall_NoProviderTwinAndEndpointExternal(t *testing.T) {
	g, s := newTwinGraph(t)
	scanFile(t, g, s, "s1", badgeClientFacts())

	if ext := activeKeys(t, s, "external_system"); len(ext) != 1 || ext["service:external_system:demo:badges-api"] == "" {
		t.Errorf("want exactly one external_system badges-api, got %v", ext)
	}
	if prov := activeKeys(t, s, "provider"); prov["code:provider:demo:badges-api"] != "" {
		t.Errorf("code:provider twin of the external service exists: %v", prov)
	}
	eps := activeKeys(t, s, "endpoint")
	if st := eps["contract:endpoint:demo:get:/badges/:userid"]; st != "external" {
		t.Errorf("called-only endpoint status = %q, want external (%v)", st, eps)
	}
	// Exactly one CALLS_SERVICE edge from the client, to the external_system.
	edges, _ := s.ListEdges(store.EdgeFilter{EdgeType: "CALLS_SERVICE"})
	n := 0
	for _, e := range edges {
		if e.Active {
			n++
			if e.ToNodeKey != "service:external_system:demo:badges-api" {
				t.Errorf("CALLS_SERVICE to %s", e.ToNodeKey)
			}
		}
	}
	if n != 1 {
		t.Errorf("want 1 active CALLS_SERVICE, got %d", n)
	}
}

func TestExternalCall_ExposedEndpointStaysActive(t *testing.T) {
	g, s := newTwinGraph(t)
	revID, _ := s.CreateRevision("demo", "s0", "", "manual", "full", "{}")
	b, _ := json.Marshal([]Fact{{Kind: "endpoint", Method: "GET", Target: "/badges/:userId", From: "BadgesController", FromType: "controller"}})
	if _, err := g.SaveFileExtraction(revID, "demo", "badges-api/src/badges.controller.ts", "extracted", "", string(b), ""); err != nil {
		t.Fatal(err)
	}
	if _, err := g.ResolveExtractions("demo", revID); err != nil {
		t.Fatal(err)
	}
	scanFile(t, g, s, "s1", badgeClientFacts())
	if st := activeKeys(t, s, "endpoint")["contract:endpoint:demo:get:/badges/:userid"]; st != "active" {
		t.Errorf("locally exposed endpoint status = %q, want active", st)
	}
}

// A twin minted by an older build retires when the calling file is rescanned.
func TestExternalCall_LegacyTwinRetiresOnRescan(t *testing.T) {
	g, s := newTwinGraph(t)
	revID, _ := s.CreateRevision("demo", "old", "", "manual", "full", "{}")
	// Reproduce the old graph: provider twin + hard edge from the client file node.
	from := typedNodeKeyFromFile("demo", badgeClientFile, "provider")
	fromID := g.ensureNodeID("demo", revID, from, "badge.client", badgeClientFile)
	twinKey := "code:provider:demo:badges-api"
	twinID := g.ensureNodeID("demo", revID, twinKey, "badges-api", "")
	if _, err := s.UpsertEdge(store.EdgeRow{
		EdgeKey: from + "->" + twinKey + ":CALLS_SERVICE", FromNodeID: fromID, ToNodeID: twinID,
		FromNodeKey: from, ToNodeKey: twinKey, EdgeType: "CALLS_SERVICE", DerivationKind: "hard",
		Active: true, LastSeenRevisionID: revID, Confidence: 0.85, Freshness: 1, TrustScore: 0.85, Metadata: "{}",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := g.resolverEdgeEvidence(from+"->"+twinKey+":CALLS_SERVICE", validate.EvidenceInput{
		TargetKind: "edge", SourceKind: "file", FilePath: badgeClientFile,
		ExtractorID: "chronicle-scan", ExtractorVersion: "1.0", Confidence: 0.85, RevisionID: revID,
		AssertionKind: "text_contains", Assertion: `{"substring":"badges-api"}`,
	}); err != nil {
		t.Fatal(err)
	}
	// The rescan: the calling file's old evidence goes stale, trust is
	// recomputed for its owners (what a snapshot rebuild does), then the file
	// is re-extracted and resolved.
	_, edgeIDs, nodeIDs, err := s.MarkEvidenceStaleByFiles([]string{badgeClientFile})
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range edgeIDs {
		g.noteEvidenceErr(g.RecalculateEdgeTrust(id))
	}
	for _, id := range nodeIDs {
		g.noteEvidenceErr(g.RecalculateNodeTrust(id))
	}
	scanFile(t, g, s, "s2", badgeClientFacts())

	if prov := activeKeys(t, s, "provider"); prov[twinKey] != "" {
		t.Errorf("provider twin still alive after rescan: %v", prov)
	}
	edges, _ := s.ListEdges(store.EdgeFilter{EdgeType: "CALLS_SERVICE"})
	for _, e := range edges {
		if e.Active && e.ToNodeKey == twinKey {
			t.Errorf("edge to twin still active: %s", e.EdgeKey)
		}
	}
}

func resolveFile(t *testing.T, g *Graph, s *store.Store, scan, file string, facts []Fact) {
	t.Helper()
	revID, _ := s.CreateRevision("demo", scan, "", "manual", "full", "{}")
	b, _ := json.Marshal(facts)
	if _, err := g.SaveFileExtraction(revID, "demo", file, "extracted", "", string(b), ""); err != nil {
		t.Fatal(err)
	}
	if _, err := g.ResolveExtractions("demo", revID); err != nil {
		t.Fatal(err)
	}
}

func orderFacts() (code, openapi []Fact) {
	code = []Fact{
		{Kind: "endpoint", Method: "GET", Target: "/orders/:id", From: "OrdersController", FromType: "controller"},
		{Kind: "endpoint", Method: "POST", Target: "/orders/:id/capture", From: "OrdersController", FromType: "controller"},
	}
	openapi = []Fact{
		{Kind: "endpoint", Method: "GET", Target: "/orders/{id}", From: "openapi", FromType: "controller"},
		{Kind: "endpoint", Method: "POST", Target: "/orders/{id}/capture", From: "openapi", FromType: "controller"},
	}
	return
}

func TestEndpointParamSyntax_OneNodePerRoute(t *testing.T) {
	for _, codeFirst := range []bool{true, false} {
		g, s := newTwinGraph(t)
		code, oa := orderFacts()
		a, aFile, b, bFile := code, "api/orders.controller.ts", oa, "api/openapi.yaml"
		if !codeFirst {
			a, aFile, b, bFile = oa, "api/openapi.yaml", code, "api/orders.controller.ts"
		}
		resolveFile(t, g, s, "s1", aFile, a)
		resolveFile(t, g, s, "s2", bFile, b)
		eps := activeKeys(t, s, "endpoint")
		if len(eps) != 2 || eps["contract:endpoint:demo:get:/orders/:id"] == "" || eps["contract:endpoint:demo:post:/orders/:id/capture"] == "" {
			t.Errorf("codeFirst=%v: want the two colon endpoints only, got %v", codeFirst, eps)
		}
	}
}

func TestEndpointParamSyntax_CamelCaseParamsConverge(t *testing.T) {
	k1, _ := normalizeEndpointKey("demo", "get", "/orders/{orderId}")
	k2, _ := normalizeEndpointKey("demo", "GET", "/orders/:orderId")
	if k1 != k2 {
		t.Errorf("%s != %s", k1, k2)
	}
	if _, name := normalizeEndpointKey("demo", "get", "/orders/${userId}"); name != "GET /orders/:userId" {
		t.Errorf("template display name = %q", name)
	}
}

// A graph minted before the rule has the brace twin; rescanning the OpenAPI
// file folds it into the colon node and moves its edges.
func TestEndpointParamSyntax_ExistingBraceTwinRetiresOnRescan(t *testing.T) {
	g, s := newTwinGraph(t)
	code, oa := orderFacts()
	resolveFile(t, g, s, "s1", "api/orders.controller.ts", code)

	revID, _ := s.CreateRevision("demo", "old", "", "manual", "full", "{}")
	oldKey := "contract:endpoint:demo:get:/orders/{id}"
	oldID, err := s.UpsertNode(store.NodeRow{NodeKey: oldKey, Layer: "contract", NodeType: "endpoint", DomainKey: "demo",
		Name: "GET /orders/{id}", Status: "active", Confidence: 0.9, Freshness: 1, TrustScore: 0.9, LastSeenRevisionID: revID, Metadata: "{}"})
	if err != nil {
		t.Fatal(err)
	}
	from := typedNodeKeyFromFile("demo", "api/openapi.yaml", "controller")
	fromID := g.ensureNodeID("demo", revID, from, "openapi", "api/openapi.yaml")
	if _, err := s.UpsertEdge(store.EdgeRow{EdgeKey: from + "->" + oldKey + ":EXPOSES_ENDPOINT", FromNodeID: fromID, ToNodeID: oldID,
		FromNodeKey: from, ToNodeKey: oldKey, EdgeType: "EXPOSES_ENDPOINT", DerivationKind: "hard", Active: true,
		LastSeenRevisionID: revID, Confidence: 0.9, Freshness: 1, TrustScore: 0.9, Metadata: "{}"}); err != nil {
		t.Fatal(err)
	}

	resolveFile(t, g, s, "s2", "api/openapi.yaml", oa)

	eps := activeKeys(t, s, "endpoint")
	if eps[oldKey] != "" {
		t.Errorf("brace twin still alive: %v", eps)
	}
	edges, _ := s.ListEdges(store.EdgeFilter{EdgeType: "EXPOSES_ENDPOINT"})
	var toColon bool
	for _, e := range edges {
		if e.Active && e.ToNodeKey == oldKey {
			t.Errorf("active edge still on the twin: %s", e.EdgeKey)
		}
		if e.Active && e.FromNodeKey == from && e.ToNodeKey == "contract:endpoint:demo:get:/orders/:id" {
			toColon = true
		}
	}
	if !toColon {
		t.Error("openapi file does not expose the canonical endpoint")
	}
}
