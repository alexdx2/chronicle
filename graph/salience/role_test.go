package salience

import "testing"

func TestResolveRole_HighestConfidenceWins(t *testing.T) {
	c := []RoleClaim{
		{Role: "helper", Confidence: 0.6},
		{Role: "entity", Confidence: 0.9},
		{Role: "dto", Confidence: 0.7},
	}
	w, ok := ResolveRole(c)
	if !ok || w.Role != "entity" {
		t.Fatalf("want entity, got %+v ok=%v", w, ok)
	}
}

func TestResolveRole_TieBreakLexical(t *testing.T) {
	c := []RoleClaim{{Role: "port", Confidence: 0.8}, {Role: "entity", Confidence: 0.8}}
	w, _ := ResolveRole(c)
	if w.Role != "entity" {
		t.Fatalf("tie should pick lexically smallest 'entity', got %q", w.Role)
	}
}

func TestResolveRole_IgnoresUnknownWhenConcretePresent(t *testing.T) {
	c := []RoleClaim{{Role: "unknown", Confidence: 0.99}, {Role: "entity", Confidence: 0.5}}
	w, _ := ResolveRole(c)
	if w.Role != "entity" {
		t.Fatalf("unknown must not win over concrete, got %q", w.Role)
	}
}

func TestResolveRole_UnknownOnly(t *testing.T) {
	w, ok := ResolveRole([]RoleClaim{{Role: "unknown", Confidence: 0.4}})
	if !ok || w.Role != "unknown" {
		t.Fatalf("unknown-only should return unknown, got %+v ok=%v", w, ok)
	}
}

func TestResolveRole_Empty(t *testing.T) {
	if _, ok := ResolveRole(nil); ok {
		t.Fatal("empty claims should return ok=false")
	}
}
