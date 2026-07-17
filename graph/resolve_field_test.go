package graph

import (
	"testing"

	"github.com/alexdx2/chronicle-core/store"
)

// ---------------------------------------------------------------------------
// Field-level graph: model_field facts (deterministic, from Prisma AST) become
// data:field nodes + HAS_FIELD edges; field_usage facts (LLM, optional) become
// READS_FIELD / WRITES_FIELD edges from the using code node.
// ---------------------------------------------------------------------------

func TestResolveModelFieldFacts(t *testing.T) {
	g, s, revID := setupTestGraph(t)

	facts := `[
		{"kind":"model","to":"Battle"},
		{"kind":"model_field","from":"Battle","to":"winnerId","to_type":"String"},
		{"kind":"model_field","from":"Battle","to":"score","to_type":"Int"}
	]`
	g.SaveFileExtraction(revID, "testapp", "arena-api/prisma/schema.prisma", "extracted", "schema", facts, "")

	if _, err := g.ResolveExtractions("testapp", revID); err != nil {
		t.Fatalf("ResolveExtractions: %v", err)
	}

	modelKey := "data:model:testapp:battle"
	fieldKey := "data:field:testapp:battle/winner-id"

	fieldNode, err := s.GetNodeByKey(fieldKey)
	if err != nil {
		t.Fatalf("field node %s not created: %v", fieldKey, err)
	}
	if fieldNode.Name != "Battle.winnerId" {
		t.Errorf("field node name: got %q want Battle.winnerId", fieldNode.Name)
	}

	edges, _ := s.ListEdges(store.EdgeFilter{EdgeType: "HAS_FIELD"})
	found := false
	for _, e := range edges {
		if e.FromNodeKey == modelKey && e.ToNodeKey == fieldKey {
			found = true
			// Evidence must exist on the edge.
			evs, err := s.ListEvidenceByEdge(e.EdgeID)
			if err != nil || len(evs) == 0 {
				t.Errorf("HAS_FIELD edge has no evidence: err=%v n=%d", err, len(evs))
			}
		}
	}
	if !found {
		t.Errorf("HAS_FIELD %s -> %s missing; edges: %v", modelKey, fieldKey, edges)
	}
}

func TestResolveFieldUsageFacts(t *testing.T) {
	g, s, revID := setupTestGraph(t)

	// Schema file defines the model + field.
	schemaFacts := `[
		{"kind":"model","to":"Battle"},
		{"kind":"model_field","from":"Battle","to":"winnerId","to_type":"String"}
	]`
	g.SaveFileExtraction(revID, "testapp", "arena-api/prisma/schema.prisma", "extracted", "schema", schemaFacts, "")

	// Provider writes the field explicitly; also one usage of an unknown model
	// that must be ignored (no phantom fields from hallucinated models).
	providerFacts := `[
		{"kind":"field_usage","target":"Battle","to":"winnerId","method":"write"},
		{"kind":"field_usage","target":"Ghost","to":"nope","method":"read"}
	]`
	g.SaveFileExtraction(revID, "testapp", "arena-api/src/battle.service.ts", "extracted", "provider", providerFacts, "")

	if _, err := g.ResolveExtractions("testapp", revID); err != nil {
		t.Fatalf("ResolveExtractions: %v", err)
	}

	fieldKey := "data:field:testapp:battle/winner-id"
	writes, _ := s.ListEdges(store.EdgeFilter{EdgeType: "WRITES_FIELD"})
	found := false
	for _, e := range writes {
		if e.ToNodeKey == fieldKey {
			found = true
		}
	}
	if !found {
		t.Errorf("WRITES_FIELD edge to %s missing; got %v", fieldKey, writes)
	}

	// Ghost model must not have produced a field node.
	if _, err := s.GetNodeByKey("data:field:testapp:ghost/nope"); err == nil {
		t.Error("phantom field node created for unknown model Ghost")
	}
}
