package graph

import (
	"strings"
	"testing"

	"github.com/alexdx2/chronicle-core/store"
)

func TestProvidesFact_CreatesContainsEdges(t *testing.T) {
	g, s, revID := setupTestGraph(t)

	// Target files — controller/service with facts (creates path-keyed nodes).
	g.SaveFileExtraction(revID, "testapp", "src/arena/arena.controller.ts", "extracted", "controller", `[
		{"kind":"endpoint","method":"POST","target":"/arena/attack"}
	]`, "")
	g.SaveFileExtraction(revID, "testapp", "src/arena/arena.service.ts", "extracted", "provider", `[
		{"kind":"uses_model","to":"BattleEvent"}
	]`, "")

	// Module with provides facts (the fact-schema contract).
	moduleFacts := `[
		{"kind":"provides","to":"ArenaController"},
		{"kind":"provides","to":"ArenaService"}
	]`
	g.SaveFileExtraction(revID, "testapp", "src/arena/arena.module.ts", "extracted", "module", moduleFacts, "")

	result, err := g.ResolveExtractions("testapp", revID)
	if err != nil {
		t.Fatalf("ResolveExtractions: %v", err)
	}
	if result.EdgesCreated == 0 {
		t.Fatal("expected edges from provides facts")
	}

	moduleKey := "code:module:testapp:src/arena/arena.module"
	controllerKey := "code:controller:testapp:src/arena/arena.controller"
	serviceKey := "code:provider:testapp:src/arena/arena.service"

	edges, _ := s.ListEdges(store.EdgeFilter{EdgeType: "CONTAINS"})
	found := map[string]bool{}
	for _, e := range edges {
		if e.FromNodeKey == moduleKey {
			found[e.ToNodeKey] = true
		}
	}
	if !found[controllerKey] {
		t.Errorf("expected CONTAINS %s -> %s; edges: %v", moduleKey, controllerKey, edges)
	}
	if !found[serviceKey] {
		t.Errorf("expected CONTAINS %s -> %s; edges: %v", moduleKey, serviceKey, edges)
	}
}

func TestProvides_ZeroFactFile_ResolvesFromScanIndex(t *testing.T) {
	g, s, revID := setupTestGraph(t)

	// Guard file scanned but no runtime facts (type_only / empty).
	g.SaveFileExtraction(revID, "testapp", "src/arena/battle.guard.ts", "type_only", "provider", `[]`, "")

	moduleFacts := `[{"kind":"provides","to":"BattleGuard"}]`
	g.SaveFileExtraction(revID, "testapp", "src/arena/arena.module.ts", "extracted", "module", moduleFacts, "")

	_, err := g.ResolveExtractions("testapp", revID)
	if err != nil {
		t.Fatalf("ResolveExtractions: %v", err)
	}

	guardKey := "code:provider:testapp:src/arena/battle.guard"
	if _, err := s.GetNodeIDByKey(guardKey); err != nil {
		t.Fatalf("expected node for zero-fact guard file, key %s", guardKey)
	}

	edges, _ := s.ListEdges(store.EdgeFilter{EdgeType: "CONTAINS"})
	found := false
	for _, e := range edges {
		if strings.Contains(e.ToNodeKey, "battle.guard") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected module CONTAINS battle.guard, got %v", edges)
	}
}

func TestInjects_NoDuplicatePhantomWhenPathNodeExists(t *testing.T) {
	g, s, revID := setupTestGraph(t)

	g.SaveFileExtraction(revID, "testapp", "src/arena/arena.service.ts", "extracted", "provider", `[]`, "")
	g.SaveFileExtraction(revID, "testapp", "src/arena/arena.controller.ts", "extracted", "controller", `[
		{"kind":"endpoint","method":"GET","target":"/arena/status"},
		{"kind":"injects","to":"ArenaService"}
	]`, "")

	_, err := g.ResolveExtractions("testapp", revID)
	if err != nil {
		t.Fatalf("ResolveExtractions: %v", err)
	}

	serviceKey := "code:provider:testapp:src/arena/arena.service"
	phantomKey := "code:provider:testapp:arena.service"
	if _, err := s.GetNodeIDByKey(phantomKey); err == nil {
		t.Errorf("unexpected phantom provider node %s", phantomKey)
	}
	if _, err := s.GetNodeIDByKey(serviceKey); err != nil {
		t.Fatalf("missing canonical service node %s", serviceKey)
	}

	edges, _ := s.ListEdges(store.EdgeFilter{EdgeType: "INJECTS"})
	for _, e := range edges {
		if e.ToNodeKey == phantomKey {
			t.Errorf("INJECTS should not target phantom %s", phantomKey)
		}
		if e.ToNodeKey != serviceKey {
			continue
		}
		return
	}
	t.Errorf("expected INJECTS edge to %s", serviceKey)
}
