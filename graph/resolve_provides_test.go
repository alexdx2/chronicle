package graph

import (
	"strings"
	"testing"

	"github.com/alexdx2/chronicle-core/store"
)

// ---------------------------------------------------------------------------
// R8 — Import-map-first provides resolution
//
// When a module file provides a symbol that it also imports (e.g. TomController),
// the resolver must consult the file's import declarations to find the exact path-
// keyed node rather than falling back to class-name / alias resolution.
// This makes CONTAINS edges deterministic and immune to same-class-name collisions
// across services (e.g. four prisma.service.ts files in one domain).
// ---------------------------------------------------------------------------

// TestR8_ProvidesViaImportMap_TwoServicesSameProviderName verifies that when two
// services each have a PrismaService and a module provides PrismaService, the CONTAINS
// edge lands on the correct service's node (the one listed in the module's imports),
// not on the other service's node or a phantom stem node.
//
// Layout:
//   svc-a/src/common/prisma.service.ts  ← svc-a's prisma (imported as ./common/prisma.service)
//   svc-b/src/common/prisma.service.ts  ← svc-b's prisma (same class name, different service)
//   svc-a/src/svc-a.module.ts           ← imports and provides PrismaService from ./common/prisma.service
func TestR8_ProvidesViaImportMap_TwoServicesSameProviderName(t *testing.T) {
	g, s, revID := setupTestGraph(t)

	// svc-a has its own prisma.service.ts at a sibling path
	g.SaveFileExtraction(revID, "testapp", "svc-a/src/common/prisma.service.ts", "extracted", "provider", `[]`, "")
	// svc-b also has prisma.service.ts (same class name, different service)
	g.SaveFileExtraction(revID, "testapp", "svc-b/src/common/prisma.service.ts", "extracted", "provider", `[]`, "")

	// svc-a module imports PrismaService from its own prisma.service (relative path) and provides it.
	// Import path "./common/prisma.service" from "svc-a/src/svc-a.module.ts"
	// resolves to "svc-a/src/common/prisma.service".
	moduleFacts := `[
		{"kind":"import","symbols":["PrismaService"],"to":"./common/prisma.service"},
		{"kind":"provides","to":"PrismaService","to_type":"provider"}
	]`
	g.SaveFileExtraction(revID, "testapp", "svc-a/src/svc-a.module.ts", "extracted", "module", moduleFacts, "")

	_, err := g.ResolveExtractions("testapp", revID)
	if err != nil {
		t.Fatalf("ResolveExtractions: %v", err)
	}

	moduleKey := "code:module:testapp:svc-a/src/svc-a.module"
	// PrismaService from svc-a's own path (import resolved to this exact path)
	svcAKey := "code:provider:testapp:svc-a/src/common/prisma.service"
	// svc-b's node must NOT appear as a CONTAINS target
	svcBKey := "code:provider:testapp:svc-b/src/common/prisma.service"

	edges, _ := s.ListEdges(store.EdgeFilter{EdgeType: "CONTAINS"})
	found := map[string]bool{}
	for _, e := range edges {
		if e.FromNodeKey == moduleKey {
			found[e.ToNodeKey] = true
		}
	}

	if !found[svcAKey] {
		t.Errorf("R8: CONTAINS edge must target svc-a PrismaService %s; got: %v", svcAKey, found)
	}
	if found[svcBKey] {
		t.Errorf("R8: CONTAINS edge must NOT target svc-b PrismaService %s", svcBKey)
	}

	// Must not create a phantom stem-based node
	phantomKey := "code:provider:testapp:prisma.service"
	if _, err := s.GetNodeIDByKey(phantomKey); err == nil {
		t.Errorf("R8: phantom stem node %s must not be created", phantomKey)
	}
}

// TestR8_ProvidesViaImportMap_ControllerResolvesFromImport verifies that a controller
// listed in provides AND in the module's import declarations resolves via import path,
// not by class-name heuristic that could land on the wrong node.
func TestR8_ProvidesViaImportMap_ControllerResolvesFromImport(t *testing.T) {
	g, s, revID := setupTestGraph(t)

	// Controller file exists in svc-a
	g.SaveFileExtraction(revID, "testapp", "svc-a/src/tom/tom.controller.ts", "extracted", "controller", `[
		{"kind":"endpoint","method":"GET","target":"/tom/status"}
	]`, "")
	// Provider in same service
	g.SaveFileExtraction(revID, "testapp", "svc-a/src/tom/tom.service.ts", "extracted", "provider", `[]`, "")

	// Module with both import facts and provides facts
	moduleFacts := `[
		{"kind":"import","symbols":["TomController"],"to":"./tom.controller"},
		{"kind":"import","symbols":["TomService"],"to":"./tom.service"},
		{"kind":"provides","to":"TomController","to_type":"controller"},
		{"kind":"provides","to":"TomService","to_type":"provider"}
	]`
	g.SaveFileExtraction(revID, "testapp", "svc-a/src/tom/tom.module.ts", "extracted", "module", moduleFacts, "")

	_, err := g.ResolveExtractions("testapp", revID)
	if err != nil {
		t.Fatalf("ResolveExtractions: %v", err)
	}

	moduleKey := "code:module:testapp:svc-a/src/tom/tom.module"
	controllerKey := "code:controller:testapp:svc-a/src/tom/tom.controller"
	serviceKey := "code:provider:testapp:svc-a/src/tom/tom.service"

	edges, _ := s.ListEdges(store.EdgeFilter{EdgeType: "CONTAINS"})
	found := map[string]bool{}
	for _, e := range edges {
		if e.FromNodeKey == moduleKey {
			found[e.ToNodeKey] = true
		}
	}

	if !found[controllerKey] {
		t.Errorf("R8: CONTAINS must include controller %s; got: %v", controllerKey, found)
	}
	if !found[serviceKey] {
		t.Errorf("R8: CONTAINS must include service %s; got: %v", serviceKey, found)
	}
}

// TestR8_ProvidesWithoutImport_FallsBackToScanIndex verifies that when a module
// provides a symbol that is NOT listed in its import facts, resolution still works
// via the scan-index fallback (backward compatibility).
func TestR8_ProvidesWithoutImport_FallsBackToScanIndex(t *testing.T) {
	g, s, revID := setupTestGraph(t)

	// Guard file — type_only, no facts
	g.SaveFileExtraction(revID, "testapp", "src/arena/battle.guard.ts", "type_only", "provider", `[]`, "")

	// Module provides BattleGuard but has NO import fact for it (fallback case)
	moduleFacts := `[{"kind":"provides","to":"BattleGuard"}]`
	g.SaveFileExtraction(revID, "testapp", "src/arena/arena.module.ts", "extracted", "module", moduleFacts, "")

	_, err := g.ResolveExtractions("testapp", revID)
	if err != nil {
		t.Fatalf("ResolveExtractions: %v", err)
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
		t.Errorf("R8 fallback: expected module CONTAINS battle.guard via scan-index; got %v", edges)
	}
}

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
