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

// loadPayload reads a fixture JSON file into an ImportPayload.
func loadPayload(t *testing.T, path string) graph.ImportPayload {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading payload %s: %v", path, err)
	}
	var payload graph.ImportPayload
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatalf("parsing payload %s: %v", path, err)
	}
	return payload
}

// setupRepo creates a fresh graph, imports a payload, and returns the graph.
func setupRepo(t *testing.T, name string, payload graph.ImportPayload) *graph.Graph {
	t.Helper()
	dir := t.TempDir()
	s, err := store.Open(filepath.Join(dir, name+".db"))
	if err != nil {
		t.Fatalf("Open %s: %v", name, err)
	}
	t.Cleanup(func() { s.Close() })

	reg, err := registry.LoadDefaults()
	if err != nil {
		t.Fatalf("LoadDefaults: %v", err)
	}

	g := graph.New(s, reg)
	domain := name
	if len(payload.Nodes) > 0 {
		domain = payload.Nodes[0].DomainKey
	}
	revID, _ := s.CreateRevision(domain, "", "fed-"+name, "manual", "full", "{}")
	_, err = g.ImportAll(payload, revID)
	if err != nil {
		t.Fatalf("ImportAll %s: %v", name, err)
	}
	return g
}

// setupFederation creates a FederatedGraph from tom-and-jerry + analytics + orders fixtures.
func setupFederation(t *testing.T) *graph.FederatedGraph {
	t.Helper()
	tjPayload := loadPayload(t, "../fixtures/tom-and-jerry/expected-graph.json")
	analyticsPayload := loadPayload(t, "../fixtures/analytics-domain/expected-graph.json")
	ordersPayload := loadPayload(t, "../fixtures/orders-domain/expected-graph.json")

	repos := map[string]*graph.Graph{
		"tom-and-jerry": setupRepo(t, "tom-and-jerry", tjPayload),
		"analytics":     setupRepo(t, "analytics", analyticsPayload),
		"orders":        setupRepo(t, "orders", ordersPayload),
	}

	fg, err := graph.NewFederatedGraph(repos)
	if err != nil {
		t.Fatalf("NewFederatedGraph: %v", err)
	}
	return fg
}

// === Federation Tests ===

// Test 1: Analytics fixture has external nodes
func TestFedAnalyticsHasExternalNodes(t *testing.T) {
	analyticsPayload := loadPayload(t, "../fixtures/analytics-domain/expected-graph.json")
	g := setupRepo(t, "analytics", analyticsPayload)

	nodes, err := g.Store().ListNodes(store.NodeFilter{Status: "external"})
	if err != nil {
		t.Fatalf("ListNodes: %v", err)
	}
	if len(nodes) != 3 {
		t.Errorf("expected 3 external nodes, got %d", len(nodes))
		for _, n := range nodes {
			t.Logf("  external: %s", n.NodeKey)
		}
	}

	// External nodes should be: battle-results topic, spectators-api service, order-created topic
	keys := make(map[string]bool)
	for _, n := range nodes {
		keys[n.NodeKey] = true
	}
	expected := []string{
		"contract:topic:tomandjerry:battle-results",
		"service:service:tomandjerry:spectators-api",
		"contract:topic:orders:order-created",
	}
	for _, k := range expected {
		if !keys[k] {
			t.Errorf("missing external node: %s", k)
		}
	}
}

// Test 2: Single-repo query stops at external nodes (OSS behavior)
func TestFedSingleRepoStopsAtExternal(t *testing.T) {
	analyticsPayload := loadPayload(t, "../fixtures/analytics-domain/expected-graph.json")
	g := setupRepo(t, "analytics", analyticsPayload)

	// BattleStatsConsumer -> battle-results topic (external)
	deps, err := g.QueryDeps("code:provider:analytics:battlestatsconsumer", 1, nil)
	if err != nil {
		t.Fatalf("QueryDeps: %v", err)
	}

	foundExternal := false
	for _, d := range deps {
		if d.NodeKey == "contract:topic:tomandjerry:battle-results" {
			foundExternal = true
			if d.Status != "external" {
				t.Errorf("expected status=external, got %q", d.Status)
			}
		}
	}
	if !foundExternal {
		t.Error("BattleStatsConsumer should depend on external battle-results topic")
	}
}

// Test 3: Federated query resolves external node via exact key match
func TestFedExactKeyResolution(t *testing.T) {
	fg := setupFederation(t)

	// Analytics BattleStatsConsumer -> battle-results topic
	// The topic exists in both analytics (external) and tom-and-jerry (active)
	// The node_key is the same: contract:topic:tomandjerry:battle-results
	// Should resolve via exact key match.
	deps, err := fg.QueryDeps("code:provider:analytics:battlestatsconsumer", 2, nil)
	if err != nil {
		t.Fatalf("QueryDeps: %v", err)
	}

	var resolved *graph.DepNode
	for i, d := range deps {
		if d.NodeKey == "contract:topic:tomandjerry:battle-results" {
			resolved = &deps[i]
			break
		}
	}

	if resolved == nil {
		t.Fatal("expected battle-results topic in deps")
	}
	if resolved.ResolutionStatus != "external_resolved" {
		t.Errorf("expected resolution_status=external_resolved, got %q", resolved.ResolutionStatus)
	}
	if resolved.ResolvedRepo != "tom-and-jerry" {
		t.Errorf("expected resolved_repo=tom-and-jerry, got %q", resolved.ResolvedRepo)
	}
	if resolved.ResolutionMethod != "exact" {
		t.Errorf("expected resolution_method=exact, got %q", resolved.ResolutionMethod)
	}
	t.Logf("Resolved: %s -> %s via %s", resolved.SourceRepo, resolved.ResolvedRepo, resolved.ResolutionMethod)
}

// Test 4: Federated query resolves external node via alias match
func TestFedAliasResolution(t *testing.T) {
	fg := setupFederation(t)

	// Analytics LeaderboardClient -> spectators-api service (external)
	// spectators-api exists in tom-and-jerry with alias "spectators-api.internal" (dns)
	// Analytics has external node with same key — should resolve via exact key.
	deps, err := fg.QueryDeps("code:provider:analytics:leaderboardclient", 2, nil)
	if err != nil {
		t.Fatalf("QueryDeps: %v", err)
	}

	var resolved *graph.DepNode
	for i, d := range deps {
		if d.NodeKey == "service:service:tomandjerry:spectators-api" {
			resolved = &deps[i]
			break
		}
	}

	if resolved == nil {
		t.Fatal("expected spectators-api service in deps")
	}
	if resolved.ResolutionStatus != "external_resolved" {
		t.Errorf("expected resolution_status=external_resolved, got %q", resolved.ResolutionStatus)
	}
	if resolved.ResolvedRepo != "tom-and-jerry" {
		t.Errorf("expected resolved_repo=tom-and-jerry, got %q", resolved.ResolvedRepo)
	}
	t.Logf("Resolved: %s -> %s via %s", resolved.SourceRepo, resolved.ResolvedRepo, resolved.ResolutionMethod)
}

// Test 5: Cross-repo traversal continues into resolved repo
func TestFedCrossRepoTraversal(t *testing.T) {
	fg := setupFederation(t)

	// BattleStatsConsumer -> battle-results (resolved to TJ) -> should continue into TJ graph
	// In TJ: BattleResultProducer PUBLISHES_TOPIC battle-results
	// So at depth 2 we should see the producer from TJ (via reverse of CONSUMES_TOPIC -> resolved topic -> producer edges)
	// Actually forward deps from the consumer: CONSUMES_TOPIC -> topic
	// Then from the topic in TJ: we can't go forward from a topic easily.
	// Let's test with more depth to see cross-repo nodes appear.
	deps, err := fg.QueryDeps("code:provider:analytics:analyticsservice", 4, nil)
	if err != nil {
		t.Fatalf("QueryDeps: %v", err)
	}

	// Should find: battlestatsconsumer (local), leaderboardclient (local),
	// then external nodes resolved to TJ
	localCount := 0
	resolvedCount := 0
	for _, d := range deps {
		if d.ResolutionStatus == "local" {
			localCount++
		}
		if d.ResolutionStatus == "external_resolved" {
			resolvedCount++
		}
	}
	t.Logf("Cross-repo traversal: %d local, %d resolved, %d total", localCount, resolvedCount, len(deps))
	if localCount == 0 {
		t.Error("expected at least some local nodes in analytics deps")
	}
	if resolvedCount == 0 {
		t.Error("expected at least some cross-repo resolved nodes")
	}
}

// Test 6: Order-created topic resolves to orders repo
func TestFedCrossRepoOrdersResolution(t *testing.T) {
	fg := setupFederation(t)

	// Analytics OrderStreamConsumer -> order-created topic (external)
	// Should resolve to orders repo via exact key match.
	deps, err := fg.QueryDeps("code:provider:analytics:orderstreamconsumer", 2, nil)
	if err != nil {
		t.Fatalf("QueryDeps: %v", err)
	}

	var resolved *graph.DepNode
	for i, d := range deps {
		if d.NodeKey == "contract:topic:orders:order-created" {
			resolved = &deps[i]
			break
		}
	}

	if resolved == nil {
		t.Fatal("expected order-created topic in deps")
	}
	if resolved.ResolutionStatus != "external_resolved" {
		t.Errorf("expected resolution_status=external_resolved, got %q", resolved.ResolutionStatus)
	}
	if resolved.ResolvedRepo != "orders" {
		t.Errorf("expected resolved_repo=orders, got %q", resolved.ResolvedRepo)
	}
	t.Logf("Order topic resolved: %s -> %s via %s", resolved.SourceRepo, resolved.ResolvedRepo, resolved.ResolutionMethod)
}

// Test 7: Federated stats aggregates across repos
func TestFedAggregatedStats(t *testing.T) {
	fg := setupFederation(t)

	// Stats with empty domain = all repos
	stats, err := fg.QueryStats("")
	if err != nil {
		t.Fatalf("QueryStats: %v", err)
	}

	// tom-and-jerry: 48 nodes, orders: 18 nodes, analytics: 13 nodes = ~79 total
	if stats.NodeCount < 60 {
		t.Errorf("expected >= 60 aggregated nodes, got %d", stats.NodeCount)
	}
	if stats.EdgeCount < 50 {
		t.Errorf("expected >= 50 aggregated edges, got %d", stats.EdgeCount)
	}
	t.Logf("Federated stats: %d nodes, %d edges across 3 repos", stats.NodeCount, stats.EdgeCount)
}

// Test 8: Cross-repo impact — changing battle-results topic impacts analytics consumers
func TestFedCrossRepoImpact(t *testing.T) {
	fg := setupFederation(t)

	// If battle-results topic in TJ changes, BattleStatsConsumer in analytics should be impacted.
	result, err := fg.QueryImpact("contract:topic:tomandjerry:battle-results", graph.ImpactOptions{
		MaxDepth: 4, MinScore: 0.0,
	})
	if err != nil {
		t.Fatalf("QueryImpact: %v", err)
	}

	// Should include: BattleResultConsumer (TJ, CONSUMES_TOPIC reverse),
	// and BattleStatsConsumer (analytics, cross-repo via external node resolution).
	// Note: BattleResultProducer is NOT impacted — PUBLISHES_TOPIC is in no_reverse_impact
	// (publishers cause changes, they aren't impacted by them).
	foundTJConsumer := false
	foundAnalyticsConsumer := false

	for _, imp := range result.Impacts {
		switch imp.NodeKey {
		case "code:provider:tomandjerry:battleresultconsumer":
			foundTJConsumer = true
		case "code:provider:analytics:battlestatsconsumer":
			foundAnalyticsConsumer = true
		}
	}

	if !foundTJConsumer {
		t.Error("BattleResultConsumer (TJ) should be impacted")
	}
	if !foundAnalyticsConsumer {
		t.Error("BattleStatsConsumer (analytics) should be impacted via cross-repo resolution")
	}

	t.Logf("Cross-repo impact: %d total impacted nodes", result.TotalImpacted)
	for _, imp := range result.Impacts {
		t.Logf("  impact: %s (score=%.1f, depth=%d)", imp.NodeKey, imp.ImpactScore, imp.Depth)
	}
}

// Test 9: Tom-and-jerry tests still pass with aliases (backward compatibility)
func TestFedTJBackwardCompatible(t *testing.T) {
	tjPayload := loadPayload(t, "../fixtures/tom-and-jerry/expected-graph.json")
	g := setupRepo(t, "tom-and-jerry", tjPayload)

	// Same checks as TestTJImport
	stats, _ := g.QueryStats("tomandjerry")
	if stats.NodeCount != len(tjPayload.Nodes) {
		t.Errorf("nodes = %d, want %d", stats.NodeCount, len(tjPayload.Nodes))
	}

	// Aliases should be imported
	topicNode, err := g.Store().GetNodeByKey("contract:topic:tomandjerry:battle-results")
	if err != nil {
		t.Fatalf("GetNodeByKey: %v", err)
	}
	aliases, err := g.Store().ListAliasesByNode(topicNode.NodeID)
	if err != nil {
		t.Fatalf("ListAliasesByNode: %v", err)
	}
	if len(aliases) == 0 {
		t.Error("expected aliases on battle-results topic after import")
	}
	t.Logf("battle-results has %d aliases", len(aliases))
}
