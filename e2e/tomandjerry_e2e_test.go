package e2e

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/alexdx2/chronicle-core/internal/graph"
	"github.com/alexdx2/chronicle-core/internal/registry"
	"github.com/alexdx2/chronicle-core/internal/store"
)

func loadTomAndJerryPayload(t *testing.T) graph.ImportPayload {
	t.Helper()
	data, err := os.ReadFile("../fixtures/tom-and-jerry/expected-graph.json")
	if err != nil {
		t.Fatalf("reading payload: %v", err)
	}
	var payload graph.ImportPayload
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatalf("parsing payload: %v", err)
	}
	return payload
}

func setupTomAndJerry(t *testing.T) (*graph.Graph, graph.ImportPayload) {
	t.Helper()
	dir := t.TempDir()
	s, err := store.Open(filepath.Join(dir, "tj.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	reg, err := registry.LoadDefaults()
	if err != nil {
		t.Fatalf("LoadDefaults: %v", err)
	}
	g := graph.New(s, reg)
	payload := loadTomAndJerryPayload(t)
	revID, _ := s.CreateRevision("tomandjerry", "", "abc123", "manual", "full", "{}")
	_, err = g.ImportAll(payload, revID)
	if err != nil {
		t.Fatalf("ImportAll: %v", err)
	}
	return g, payload
}

// Test 1: Golden payload validates — unique keys, referential integrity, counts
func TestTJPayloadValid(t *testing.T) {
	payload := loadTomAndJerryPayload(t)
	nodeKeys := make(map[string]bool)
	for _, n := range payload.Nodes {
		if nodeKeys[n.NodeKey] {
			t.Errorf("duplicate node_key: %s", n.NodeKey)
		}
		nodeKeys[n.NodeKey] = true
	}
	for i, e := range payload.Edges {
		if !nodeKeys[e.FromNodeKey] {
			t.Errorf("edge[%d] from %q not in nodes", i, e.FromNodeKey)
		}
		if !nodeKeys[e.ToNodeKey] {
			t.Errorf("edge[%d] to %q not in nodes", i, e.ToNodeKey)
		}
	}
	if len(payload.Nodes) < 40 {
		t.Errorf("nodes = %d, want >= 40", len(payload.Nodes))
	}
	if len(payload.Edges) < 50 {
		t.Errorf("edges = %d, want >= 50", len(payload.Edges))
	}
}

// Test 2: Import succeeds with all 4 layers present
func TestTJImport(t *testing.T) {
	g, payload := setupTomAndJerry(t)
	stats, _ := g.QueryStats("tomandjerry")
	if stats.NodeCount != len(payload.Nodes) {
		t.Errorf("nodes = %d, want %d", stats.NodeCount, len(payload.Nodes))
	}
	// Must have data layer nodes (models + enums)
	if stats.NodesByLayer["data"] < 5 {
		t.Errorf("data nodes = %d, want >= 5 (models + enums)", stats.NodesByLayer["data"])
	}
	// Must have contract layer (12 endpoints + 1 topic)
	if stats.NodesByLayer["contract"] < 12 {
		t.Errorf("contract nodes = %d, want >= 12 (endpoints + topic)", stats.NodesByLayer["contract"])
	}
	// Must have service layer
	if stats.NodesByLayer["service"] != 4 {
		t.Errorf("service nodes = %d, want 4", stats.NodesByLayer["service"])
	}
	// Must have code layer
	if stats.NodesByLayer["code"] < 20 {
		t.Errorf("code nodes = %d, want >= 20", stats.NodesByLayer["code"])
	}
}

// Test 3: "Tom attacks Jerry" — path from ArenaController through ArenaService → TomClient → tom-api service
func TestTJTomAttacksJerry(t *testing.T) {
	g, _ := setupTomAndJerry(t)
	result, err := g.QueryPath(
		"code:controller:tomandjerry:arenacontroller",
		"service:service:tomandjerry:tom-api",
		graph.PathOptions{MaxDepth: 6, TopK: 3, Mode: "directed"},
	)
	if err != nil {
		t.Fatalf("QueryPath: %v", err)
	}
	if len(result.Paths) == 0 {
		t.Fatal("expected path from ArenaController to tom-api service (the attack chain)")
	}
	// Path should go: ArenaController -> ArenaService -> TomClient -> tom-api service
	t.Logf("Path found: %d hops, score %.2f", result.Paths[0].Depth, result.Paths[0].PathScore)
}

// Test 4: Kafka flow — BattleResultProducer → battle-results topic → BattleResultConsumer (connected mode)
func TestTJKafkaFlow(t *testing.T) {
	g, _ := setupTomAndJerry(t)
	result, err := g.QueryPath(
		"code:provider:tomandjerry:battleresultproducer",
		"code:provider:tomandjerry:battleresultconsumer",
		graph.PathOptions{MaxDepth: 4, TopK: 3, Mode: "connected"},
	)
	if err != nil {
		t.Fatalf("QueryPath: %v", err)
	}
	if len(result.Paths) == 0 {
		t.Fatal("expected connected path via battle-results topic")
	}
	t.Logf("Kafka path: %v", result.Paths[0].Nodes)
}

// Test 5: Impact — if Cat model changes, TomService is affected (USES_MODEL reverse)
func TestTJCharacterModelImpact(t *testing.T) {
	g, _ := setupTomAndJerry(t)
	result, err := g.QueryImpact("data:model:tomandjerry:cat", graph.ImpactOptions{
		MaxDepth: 4, MinScore: 0.0,
	})
	if err != nil {
		t.Fatalf("QueryImpact: %v", err)
	}
	found := false
	for _, imp := range result.Impacts {
		if imp.NodeKey == "code:provider:tomandjerry:tomservice" {
			found = true
			break
		}
	}
	if !found {
		t.Error("TomService should be impacted when Cat model changes (USES_MODEL)")
	}
	t.Logf("Cat model change impacts %d nodes", result.TotalImpacted)
}

// Test 6: Cross-service dependency — TomClient depends on tom-api (CALLS_SERVICE) + GET /tom/status (CALLS_ENDPOINT)
func TestTJCrossServiceDeps(t *testing.T) {
	g, _ := setupTomAndJerry(t)
	deps, err := g.QueryDeps("code:provider:tomandjerry:tomclient", 1, nil)
	if err != nil {
		t.Fatalf("QueryDeps: %v", err)
	}
	foundService := false
	foundEndpoint := false
	for _, d := range deps {
		if d.NodeKey == "service:service:tomandjerry:tom-api" {
			foundService = true
		}
		if d.NodeKey == "contract:endpoint:tomandjerry:get:/tom/status" {
			foundEndpoint = true
		}
	}
	if !foundService {
		t.Error("TomClient should depend on tom-api service (CALLS_SERVICE via env URL)")
	}
	if !foundEndpoint {
		t.Error("TomClient should depend on GET /tom/status endpoint (CALLS_ENDPOINT)")
	}
}

// Test 7: Data model relations — Cat → CatWeapon (REFERENCES_MODEL)
func TestTJDataModelRelations(t *testing.T) {
	g, _ := setupTomAndJerry(t)
	deps, err := g.QueryDeps("data:model:tomandjerry:cat", 1, nil)
	if err != nil {
		t.Fatalf("QueryDeps: %v", err)
	}
	foundWeapon := false
	for _, d := range deps {
		if d.NodeKey == "data:model:tomandjerry:catweapon" {
			foundWeapon = true
		}
	}
	if !foundWeapon {
		t.Error("Cat model should reference CatWeapon model (REFERENCES_MODEL via @relation)")
	}
}

// Test 8: 4 services exist
func TestTJ4Services(t *testing.T) {
	g, _ := setupTomAndJerry(t)
	nodes, _ := g.Store().ListNodes(store.NodeFilter{Layer: "service", Domain: "tomandjerry"})
	if len(nodes) != 4 {
		t.Errorf("services = %d, want 4 (tom-api, jerry-api, arena-api, spectators-api)", len(nodes))
	}
}

// Test 9: 12 endpoints + 1 topic
func TestTJEndpoints(t *testing.T) {
	g, _ := setupTomAndJerry(t)
	nodes, _ := g.Store().ListNodes(store.NodeFilter{Layer: "contract", Domain: "tomandjerry"})
	endpoints := 0
	topics := 0
	for _, n := range nodes {
		if n.NodeType == "endpoint" {
			endpoints++
		}
		if n.NodeType == "topic" {
			topics++
		}
	}
	if endpoints != 12 {
		t.Errorf("endpoints = %d, want 12", endpoints)
	}
	if topics != 1 {
		t.Errorf("topics = %d, want 1 (battle-results)", topics)
	}
}

// Test 10: Evidence count >= 15
func TestTJEvidence(t *testing.T) {
	_, payload := setupTomAndJerry(t)
	if len(payload.Evidence) < 15 {
		t.Errorf("evidence entries = %d, want >= 15", len(payload.Evidence))
	}
}

// Test 11: ArenaService → jerry-api (cross-service via JerryClient)
func TestTJArenaToJerry(t *testing.T) {
	g, _ := setupTomAndJerry(t)
	result, err := g.QueryPath(
		"code:provider:tomandjerry:arenaservice",
		"service:service:tomandjerry:jerry-api",
		graph.PathOptions{MaxDepth: 4, TopK: 3, Mode: "directed"},
	)
	if err != nil {
		t.Fatalf("QueryPath: %v", err)
	}
	if len(result.Paths) == 0 {
		t.Fatal("expected path from ArenaService to jerry-api (via JerryClient)")
	}
	t.Logf("Arena→Jerry path: %d hops", result.Paths[0].Depth)
}

// Test 12: No directed path from tom-api service to jerry-api service (they only connect through arena)
func TestTJNoDirectTomJerryPath(t *testing.T) {
	g, _ := setupTomAndJerry(t)
	result, err := g.QueryPath(
		"service:service:tomandjerry:tom-api",
		"service:service:tomandjerry:jerry-api",
		graph.PathOptions{MaxDepth: 6, TopK: 3, Mode: "directed"},
	)
	if err != nil {
		t.Fatalf("QueryPath: %v", err)
	}
	if len(result.Paths) > 0 {
		t.Errorf("expected NO directed path from tom-api to jerry-api, but found %d paths", len(result.Paths))
	}
}
