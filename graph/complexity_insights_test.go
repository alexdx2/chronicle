package graph

import (
	"strings"
	"testing"

	"github.com/alexdx2/chronicle-core/validate"
)

// TestInsightsVerificationWeightedByComplexity proves a higher-complexity source
// outranks a lower-complexity one even when degree and trust are equal and the
// EdgeKey tiebreak would otherwise put it last.
func TestInsightsVerificationWeightedByComplexity(t *testing.T) {
	g := setupGraphDefaults(t)
	revID := makeRevision(t, g)

	nodes := []validate.NodeInput{
		// Low-complexity source: no complexity metadata. Edge key sorts first.
		{NodeKey: "code:provider:orders:aaa-cold", Layer: "code", NodeType: "provider", DomainKey: "orders", Name: "AaaCold"},
		// High-complexity source: transitive_loop_depth 5 -> normComplexity 0.6.
		{NodeKey: "code:provider:orders:zzz-hot", Layer: "code", NodeType: "provider", DomainKey: "orders", Name: "ZzzHot",
			Metadata: `{"complexity":{"transitive_loop_depth":5,"cyclomatic":0}}`},
		{NodeKey: "code:provider:orders:t-a", Layer: "code", NodeType: "provider", DomainKey: "orders", Name: "Ta"},
		{NodeKey: "code:provider:orders:t-z", Layer: "code", NodeType: "provider", DomainKey: "orders", Name: "Tz"},
	}
	for _, n := range nodes {
		if _, err := g.UpsertNode(n, revID); err != nil {
			t.Fatalf("UpsertNode %s: %v", n.Name, err)
		}
	}
	// Two low-trust (inferred) edges, equal source degree (1) and equal trust.
	edges := []validate.EdgeInput{
		{FromNodeKey: "code:provider:orders:aaa-cold", ToNodeKey: "code:provider:orders:t-a", EdgeType: "CALLS_SYMBOL", DerivationKind: "inferred", FromLayer: "code", ToLayer: "code"},
		{FromNodeKey: "code:provider:orders:zzz-hot", ToNodeKey: "code:provider:orders:t-z", EdgeType: "CALLS_SYMBOL", DerivationKind: "inferred", FromLayer: "code", ToLayer: "code"},
	}
	for _, e := range edges {
		if _, err := g.UpsertEdge(e, revID); err != nil {
			t.Fatalf("UpsertEdge %s->%s: %v", e.FromNodeKey, e.ToNodeKey, err)
		}
	}

	ins, err := g.Insights("")
	if err != nil {
		t.Fatalf("Insights: %v", err)
	}
	if len(ins.VerificationTargets) != 2 {
		t.Fatalf("want 2 verification targets, got %d", len(ins.VerificationTargets))
	}
	if ins.VerificationTargets[0].From != "code:provider:orders:zzz-hot" {
		t.Fatalf("high-complexity source should rank first, got %q", ins.VerificationTargets[0].From)
	}
	if !strings.Contains(ins.VerificationTargets[0].Reason, "src cx=") {
		t.Fatalf("reason should expose source complexity, got %q", ins.VerificationTargets[0].Reason)
	}
}

// TestInsightsHotPathTargets proves a complex, highly-connected, TRUSTED function
// (invisible to verification targets because its edges are high-trust) surfaces in
// the new hot-path section with a reason tag.
func TestInsightsHotPathTargets(t *testing.T) {
	g := setupGraphDefaults(t)
	revID := makeRevision(t, g)

	nodes := []validate.NodeInput{
		{NodeKey: "code:provider:orders:hot", Layer: "code", NodeType: "provider", DomainKey: "orders", Name: "Hot",
			Metadata: `{"complexity":{"cyclomatic":10,"transitive_loop_depth":6,"recursive":true}}`},
		{NodeKey: "code:provider:orders:c1", Layer: "code", NodeType: "provider", DomainKey: "orders", Name: "C1"},
		{NodeKey: "code:provider:orders:c2", Layer: "code", NodeType: "provider", DomainKey: "orders", Name: "C2"},
		{NodeKey: "code:provider:orders:c3", Layer: "code", NodeType: "provider", DomainKey: "orders", Name: "C3"},
	}
	for _, n := range nodes {
		if _, err := g.UpsertNode(n, revID); err != nil {
			t.Fatalf("UpsertNode %s: %v", n.Name, err)
		}
	}
	// High-trust (hard) edges: hot is highly connected but NOT a verification target.
	for _, to := range []string{"c1", "c2", "c3"} {
		e := validate.EdgeInput{FromNodeKey: "code:provider:orders:hot", ToNodeKey: "code:provider:orders:" + to,
			EdgeType: "CALLS_SYMBOL", DerivationKind: "hard", FromLayer: "code", ToLayer: "code"}
		if _, err := g.UpsertEdge(e, revID); err != nil {
			t.Fatalf("UpsertEdge ->%s: %v", to, err)
		}
	}

	ins, err := g.Insights("")
	if err != nil {
		t.Fatalf("Insights: %v", err)
	}
	if len(ins.HotPathTargets) == 0 {
		t.Fatalf("hot node should surface in hot-path targets")
	}
	if ins.HotPathTargets[0].NodeKey != "code:provider:orders:hot" {
		t.Fatalf("top hot-path target = %q, want hot", ins.HotPathTargets[0].NodeKey)
	}
	if !strings.Contains(ins.HotPathTargets[0].Reason, "complex") {
		t.Fatalf("reason should tag the dominant factor, got %q", ins.HotPathTargets[0].Reason)
	}
	// Hot-path is complexity-driven: a node with no complexity metadata is absent.
	for _, n := range ins.HotPathTargets {
		if n.NodeKey == "code:provider:orders:c1" {
			t.Fatalf("non-complex node c1 must not appear in hot-path targets")
		}
	}
}
