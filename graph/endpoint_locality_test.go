package graph

import (
	"encoding/json"
	"testing"

	"github.com/alexdx2/chronicle-core/store"
)

const (
	locCtrlFile   = "api/orders.controller.ts"
	locCallerFile = "web/orders.client.ts"
)

func locCallerFacts() []Fact {
	return []Fact{{Kind: "calls_endpoint", From: "OrdersClient", FromType: "provider", Target: "/orders/:id", Method: "GET"}}
}

func locCtrlFacts() []Fact {
	return []Fact{{Kind: "endpoint", Method: "GET", Target: "/orders/:id", From: "OrdersController", FromType: "controller"}}
}

const locEpKey = "contract:endpoint:demo:get:/orders/:id"

func mustEndpointStatus(t *testing.T, s *store.Store, key, want string) {
	t.Helper()
	if got := activeKeys(t, s, "endpoint")[key]; got != want {
		t.Errorf("endpoint %s status = %q, want %q (%v)", key, got, want, activeKeys(t, s, "endpoint"))
	}
}

// An endpoint the domain declares itself (route evidence) but that has no
// EXPOSES_ENDPOINT edge must not be demoted by an in-repo caller.
func TestEndpointLocality_OwnedWithoutExposesStaysActive(t *testing.T) {
	g, s := newTwinGraph(t)
	revID, _ := s.CreateRevision("demo", "s0", "", "manual", "full", "{}")
	id, err := s.UpsertNode(store.NodeRow{NodeKey: locEpKey, Layer: "contract", NodeType: "endpoint", DomainKey: "demo",
		Name: "GET /orders/:id", Status: "active", Confidence: 0.9, Freshness: 1, TrustScore: 0.9, LastSeenRevisionID: revID, Metadata: "{}"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddEvidence(store.EvidenceRow{TargetKind: "node", NodeID: id, SourceKind: "file", FilePath: "api/openapi.yaml",
		ExtractorID: "openapi", ExtractorVersion: "1.0", Confidence: 0.9, AssertionKind: "route_declared",
		Assertion: `{"route":"/orders/:id"}`, Metadata: "{}", ValidFromRevisionID: revID}); err != nil {
		t.Fatal(err)
	}
	resolveFile(t, g, s, "s1", locCallerFile, locCallerFacts())
	mustEndpointStatus(t, s, locEpKey, "active")
}

// The caller is scanned first (endpoint external), the controller later.
func TestEndpointLocality_CallerThenControllerLaterScan(t *testing.T) {
	g, s := newTwinGraph(t)
	resolveFile(t, g, s, "s1", locCallerFile, locCallerFacts())
	mustEndpointStatus(t, s, locEpKey, "external")
	resolveFile(t, g, s, "s2", locCtrlFile, locCtrlFacts())
	mustEndpointStatus(t, s, locEpKey, "active")
}

// Caller and controller extracted before one resolve.
func TestEndpointLocality_CallerAndControllerInOneBatch(t *testing.T) {
	g, s := newTwinGraph(t)
	revID, _ := s.CreateRevision("demo", "s1", "", "manual", "full", "{}")
	for file, facts := range map[string][]Fact{locCallerFile: locCallerFacts(), locCtrlFile: locCtrlFacts()} {
		saveFacts(t, g, revID, file, facts)
	}
	if _, err := g.ResolveExtractions("demo", revID); err != nil {
		t.Fatal(err)
	}
	mustEndpointStatus(t, s, locEpKey, "active")
}

func saveFacts(t *testing.T, g *Graph, revID int64, file string, facts []Fact) {
	t.Helper()
	b, _ := json.Marshal(facts)
	if _, err := g.SaveFileExtraction(revID, "demo", file, "extracted", "", string(b), ""); err != nil {
		t.Fatal(err)
	}
}

// mintBraceTwin builds the graph an older build left: a brace-keyed endpoint
// exposed by an openapi file node, plus an outgoing edge and an inactive edge.
func mintBraceTwin(t *testing.T, g *Graph, s *store.Store) (braceKey string, braceID int64, from string) {
	t.Helper()
	revID, _ := s.CreateRevision("demo", "old", "", "manual", "full", "{}")
	braceKey = "contract:endpoint:demo:get:/orders/{id}"
	var err error
	braceID, err = s.UpsertNode(store.NodeRow{NodeKey: braceKey, Layer: "contract", NodeType: "endpoint", DomainKey: "demo",
		Name: "GET /orders/{id}", Status: "active", Confidence: 0.9, Freshness: 1, TrustScore: 0.9, LastSeenRevisionID: revID, Metadata: "{}"})
	if err != nil {
		t.Fatal(err)
	}
	from = typedNodeKeyFromFile("demo", "api/openapi.yaml", "controller")
	fromID := g.ensureNodeID("demo", revID, from, "openapi", "api/openapi.yaml")
	add := func(ek, fk string, fid int64, tk string, tid int64, typ string) {
		if _, err := s.UpsertEdge(store.EdgeRow{EdgeKey: fk + "->" + tk + ":" + typ, FromNodeID: fid, ToNodeID: tid,
			FromNodeKey: fk, ToNodeKey: tk, EdgeType: typ, DerivationKind: "hard", Active: true,
			LastSeenRevisionID: revID, Confidence: 0.9, Freshness: 1, TrustScore: 0.9, Metadata: "{}"}); err != nil {
			t.Fatal(err)
		}
	}
	add("", from, fromID, braceKey, braceID, "EXPOSES_ENDPOINT")
	modelKey := "data:model:demo:order"
	modelID := g.ensureNodeID("demo", revID, modelKey, "Order", "")
	add("", braceKey, braceID, modelKey, modelID, "USES_MODEL")
	return
}

// A brace-only graph converges on the next resolve, without re-extracting the
// openapi file; the outgoing edge follows; a later call finds the colon key.
func TestBraceFold_BraceOnlyGraphConverges(t *testing.T) {
	g, s := newTwinGraph(t)
	braceKey, _, from := mintBraceTwin(t, g, s)

	resolveFile(t, g, s, "s1", locCallerFile, locCallerFacts())

	eps := activeKeys(t, s, "endpoint")
	if eps[braceKey] != "" {
		t.Errorf("brace key still alive: %v", eps)
	}
	if eps[locEpKey] == "" {
		t.Fatalf("colon endpoint missing: %v", eps)
	}
	exp, _ := s.ListEdges(store.EdgeFilter{EdgeType: "EXPOSES_ENDPOINT"})
	okIn := false
	for _, e := range exp {
		if e.Active && e.FromNodeKey == from && e.ToNodeKey == locEpKey {
			okIn = true
		}
	}
	if !okIn {
		t.Error("openapi exposes edge not on the colon endpoint")
	}
	use, _ := s.ListEdges(store.EdgeFilter{EdgeType: "USES_MODEL"})
	okOut := false
	for _, e := range use {
		if e.Active && e.FromNodeKey == locEpKey && e.ToNodeKey == "data:model:demo:order" {
			okOut = true
		}
		if e.FromNodeKey == braceKey {
			t.Errorf("outgoing edge left on the twin: %s", e.EdgeKey)
		}
	}
	if !okOut {
		t.Error("outgoing edge not moved to the colon endpoint")
	}
	// The caller's lookup of the colon key hits the same node: no new key.
	calls, _ := s.ListEdges(store.EdgeFilter{EdgeType: "CALLS_ENDPOINT"})
	found := false
	for _, e := range calls {
		if e.Active && e.ToNodeKey == locEpKey {
			found = true
		}
	}
	if !found {
		t.Error("calls_endpoint did not land on the colon endpoint")
	}
	if n := len(activeKeys(t, s, "endpoint")); n != 1 {
		t.Errorf("want exactly one endpoint, got %v", activeKeys(t, s, "endpoint"))
	}
}

// With a canonical node already present the twin folds into it.
func TestBraceFold_FoldsIntoExistingColonNode(t *testing.T) {
	g, s := newTwinGraph(t)
	braceKey, _, _ := mintBraceTwin(t, g, s)
	resolveFile(t, g, s, "s1", locCtrlFile, locCtrlFacts())
	eps := activeKeys(t, s, "endpoint")
	if eps[braceKey] != "" || eps[locEpKey] == "" || len(eps) != 1 {
		t.Errorf("want only the colon endpoint, got %v", eps)
	}
	use, _ := s.ListEdges(store.EdgeFilter{EdgeType: "USES_MODEL"})
	moved := false
	for _, e := range use {
		if e.Active && e.FromNodeKey == locEpKey {
			moved = true
		}
	}
	if !moved {
		t.Error("outgoing edge not moved onto the existing colon node")
	}
}
