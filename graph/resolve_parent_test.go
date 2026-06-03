package graph

import (
	"testing"

	"github.com/alexdx2/chronicle-core/store"
)

// Tests for "parent" fact kind → CONTAINS edge creation.

// TestParentFact_CreatesContainsEdge verifies that a parent fact creates a
// CONTAINS edge from the named parent module node to the child file's node.
func TestParentFact_CreatesContainsEdge(t *testing.T) {
	g, _, revID := setupTestGraph(t)

	// First extraction: establish the module node.
	// from_type="module" creates a code:module node for arena.module.
	moduleFacts := `[
		{"kind":"injects","from_type":"module","to":"ArenaController"},
		{"kind":"injects","from_type":"module","to":"ArenaService"}
	]`
	g.SaveFileExtraction(revID, "testapp", "src/arena/arena.module.ts", "extracted", "module", moduleFacts, "")

	// Second extraction: controller declares parent module.
	controllerFacts := `[
		{"kind":"endpoint","method":"POST","target":"/arena/attack"},
		{"kind":"parent","to":"arena.module","reason":"registered in ArenaModule"}
	]`
	g.SaveFileExtraction(revID, "testapp", "src/arena/arena.controller.ts", "extracted", "controller", controllerFacts, "")

	result, err := g.ResolveExtractions("testapp", revID)
	if err != nil {
		t.Fatalf("ResolveExtractions: %v", err)
	}

	if result.FilesProcessed == 0 {
		t.Error("expected files to be processed")
	}

	// Verify CONTAINS edge was created
	edges, _ := g.store.ListEdges(store.EdgeFilter{EdgeType: "CONTAINS"})
	if len(edges) == 0 {
		t.Error("expected at least one CONTAINS edge, got 0")
	}

	// Verify edge direction: module → controller
	moduleNodeKey := "code:module:testapp:src/arena/arena.module"
	controllerNodeKey := "code:controller:testapp:src/arena/arena.controller"
	found := false
	for _, e := range edges {
		if e.FromNodeKey == moduleNodeKey && e.ToNodeKey == controllerNodeKey {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected CONTAINS edge from %q to %q; got edges: %v", moduleNodeKey, controllerNodeKey, edges)
	}
}

// TestParentFact_MissingTarget verifies that a parent fact referencing a
// nonexistent node produces no CONTAINS edge and records a diagnostic discovery.
func TestParentFact_MissingTarget(t *testing.T) {
	g, s, revID := setupTestGraph(t)

	facts := `[
		{"kind":"parent","to":"nonexistent.module","reason":"test"}
	]`
	g.SaveFileExtraction(revID, "testapp", "src/some/orphan.controller.ts", "extracted", "controller", facts, "")

	result, err := g.ResolveExtractions("testapp", revID)
	if err != nil {
		t.Fatalf("ResolveExtractions: %v", err)
	}

	// No CONTAINS edge should be created
	edges, _ := g.store.ListEdges(store.EdgeFilter{EdgeType: "CONTAINS"})
	if len(edges) != 0 {
		t.Errorf("expected 0 CONTAINS edges for missing parent, got %d", len(edges))
	}

	// The unresolved list should contain a "parent" entry
	foundUnresolved := false
	for _, u := range result.Unresolved {
		if u.Kind == "parent" && u.Target == "nonexistent.module" {
			foundUnresolved = true
			break
		}
	}
	if !foundUnresolved {
		t.Errorf("expected unresolved ref for missing parent; got: %v", result.Unresolved)
	}

	// A discovery warning should have been stored
	discoveries, err := s.ListDiscoveries("testapp", "missing_edge")
	if err != nil {
		t.Fatalf("ListDiscoveries: %v", err)
	}
	if len(discoveries) == 0 {
		t.Error("expected a missing_edge discovery for unknown parent, got 0")
	}
}

// TestParentFact_SelfReference verifies that a parent fact pointing to the same
// node as the file itself is silently skipped (no CONTAINS self-loop).
func TestParentFact_SelfReference(t *testing.T) {
	g, s, revID := setupTestGraph(t)

	// The file is arena.module.ts with from_type="module".
	// Its node key will be code:module:testapp:arena.module.
	// The parent fact points to "arena.module" — same node.
	facts := `[
		{"kind":"parent","from_type":"module","to":"arena.module","reason":"self ref test"}
	]`
	g.SaveFileExtraction(revID, "testapp", "src/arena/arena.module.ts", "extracted", "module", facts, "")

	_, err := g.ResolveExtractions("testapp", revID)
	if err != nil {
		t.Fatalf("ResolveExtractions: %v", err)
	}

	// No CONTAINS edge should be created (self-loop is skipped)
	edges, _ := g.store.ListEdges(store.EdgeFilter{EdgeType: "CONTAINS"})
	for _, e := range edges {
		if e.FromNodeKey == e.ToNodeKey {
			t.Errorf("found self-referencing CONTAINS edge: %s", e.EdgeKey)
		}
	}

	// A correction discovery should have been stored
	discoveries, err := s.ListDiscoveries("testapp", "correction")
	if err != nil {
		t.Fatalf("ListDiscoveries: %v", err)
	}
	if len(discoveries) == 0 {
		t.Error("expected a correction discovery for self-referencing parent, got 0")
	}
}


// TestParentFact_DedupWithProvides verifies that when both injects (from module)
// and parent facts create CONTAINS edges to the same parent, they are deduplicated.
func TestParentFact_DedupWithProvides(t *testing.T) {
	g, _, revID := setupTestGraph(t)

	// Module declares injects (which inferImportEdgeType turns into CONTAINS)
	moduleFacts := `[
		{"kind":"injects","to":"ArenaController"},
		{"kind":"injects","to":"ArenaService"}
	]`
	g.SaveFileExtraction(revID, "testapp", "src/arena/arena.module.ts", "extracted", "module", moduleFacts, "")

	// Controller also declares parent (creates second CONTAINS to same module)
	controllerFacts := `[
		{"kind":"endpoint","method":"POST","target":"/arena/attack"},
		{"kind":"parent","to":"arena.module","reason":"declared in @Module.controllers"}
	]`
	g.SaveFileExtraction(revID, "testapp", "src/arena/arena.controller.ts", "extracted", "controller", controllerFacts, "")

	_, err := g.ResolveExtractions("testapp", revID)
	if err != nil {
		t.Fatalf("ResolveExtractions: %v", err)
	}

	// Module→controller CONTAINS should be active (from either injects or parent)
	edges, _ := g.store.ListEdges(store.EdgeFilter{EdgeType: "CONTAINS"})
	activeContains := 0
	for _, e := range edges {
		if e.Active {
			activeContains++
		}
	}

	if activeContains < 1 {
		t.Errorf("expected at least 1 active CONTAINS edge, got %d", activeContains)
		for _, e := range edges {
			t.Logf("  CONTAINS: %s active=%v", e.EdgeKey, e.Active)
		}
	}

	// No conflict discoveries — same parent should dedup, not conflict
	discoveries, _ := g.store.ListDiscoveries("testapp", "contains_conflict")
	if len(discoveries) > 0 {
		t.Errorf("expected 0 contains_conflict (same parent = dedup), got %d", len(discoveries))
	}
}
