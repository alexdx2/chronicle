package graph

import (
	"testing"

	"github.com/alexdx2/chronicle-core/store"
	"github.com/alexdx2/chronicle-core/validate"
)

// SQ-Contract 3: the resolver and the importer must agree on ONE canonical
// spelling per key. normalizePascalCase is the resolver's class-name → key
// segment step; it must not shred identifiers that carry no camel boundary.
func TestNormalizePascalCase_UpperSnakeSurvives(t *testing.T) {
	cases := map[string]string{
		"SESSION_COOKIE":       "session_cookie",
		"CONFIG_SERVICE":       "config_service",
		"ArenaService":         "arena.service",
		"BattleResultProducer": "battle.result.producer",
		"HTTPClient":           "http.client",
		"already.lower":        "already.lower",
	}
	for in, want := range cases {
		if got := normalizePascalCase(in); got != want {
			t.Errorf("normalizePascalCase(%q)=%q want %q", in, got, want)
		}
	}
}

// Every key the resolver mints must be canonical already: normalizing it
// again must be identity (SQ-Contract 3). RED today: endpoint keys with
// [paramCase] lowercase differently than validate's kebab normalization.
func TestOracleCanonicalKeys_FixedPoint(t *testing.T) {
	g, s, revID := setupTestGraph(t)
	facts := `[
		{"kind":"endpoint","method":"GET","target":"/invoices/[invoiceId]/lines"},
		{"kind":"injects","to":"SESSION_COOKIE_STORE"},
		{"kind":"import","symbols":["X"],"to":"@okeep/ui/button"}
	]`
	g.SaveFileExtraction(revID, "testapp", "src/api/invoices.ts", "extracted", "controller", facts, "")
	if _, err := g.ResolveExtractions("testapp", revID); err != nil {
		t.Fatal(err)
	}
	nodes, _ := s.ListNodes(store.NodeFilter{})
	if len(nodes) == 0 {
		t.Fatal("resolve produced no nodes — oracle would pass vacuously")
	}
	for _, n := range nodes {
		norm, err := validate.NormalizeNodeKey(n.NodeKey)
		if err != nil {
			t.Errorf("emitted non-validating key %q: %v", n.NodeKey, err)
			continue
		}
		if norm != n.NodeKey {
			t.Errorf("key %q is not a fixed point (normalizes to %q)", n.NodeKey, norm)
		}
	}
}

// The edge half of SQ-Contract 3. An edge key embeds both endpoint node keys,
// so an emitter that normalizes the node but keeps the raw string in the edge
// key produces an edge that points at a key no node is stored under — the
// import_all path would then create a second, phantom node for it.
func TestOracleCanonicalKeys_EdgeKeysAgreeWithNodes(t *testing.T) {
	g, s, revID := setupTestGraph(t)
	facts := `[
		{"kind":"endpoint","method":"GET","target":"/invoices/[invoiceId]/lines"},
		{"kind":"injects","to":"SESSION_COOKIE_STORE"},
		{"kind":"injects","to":"InvoiceService"},
		{"kind":"import","symbols":["X"],"to":"@okeep/ui/button"},
		{"kind":"emits_event","to":"invoice.created"},
		{"kind":"model_field","from":"Invoice","to":"invoiceId"}
	]`
	g.SaveFileExtraction(revID, "testapp", "src/api/invoices.controller.ts", "extracted", "controller", facts, "")
	if _, err := g.ResolveExtractions("testapp", revID); err != nil {
		t.Fatal(err)
	}
	edges, _ := s.ListEdges(store.EdgeFilter{})
	if len(edges) == 0 {
		t.Fatal("resolve produced no edges — oracle would pass vacuously")
	}
	for _, e := range edges {
		norm, err := validate.NormalizeEdgeKey(e.EdgeKey)
		if err != nil {
			t.Errorf("emitted non-validating edge key %q: %v", e.EdgeKey, err)
			continue
		}
		if norm != e.EdgeKey {
			t.Errorf("edge key %q is not a fixed point (normalizes to %q)", e.EdgeKey, norm)
		}
		for _, side := range []struct{ label, key string }{{"from", e.FromNodeKey}, {"to", e.ToNodeKey}} {
			if side.key == "" {
				continue
			}
			if _, err := s.GetNodeByKey(side.key); err != nil {
				t.Errorf("edge %q %s_node_key %q has no node stored under it", e.EdgeKey, side.label, side.key)
			}
		}
	}
}

// The other half of the same field defect: a PascalCase reference arriving
// through import_all must land on the node the resolver minted from the same
// class name. Pre-lowercasing the name before canonicalization ("InvoiceService"
// → "invoiceservice") destroys the word boundary the canonical kebab rule needs
// and mints a second node for one service.
func TestOracleCanonicalKeys_ImportedReferenceHitsResolvedClass(t *testing.T) {
	g, s, revID := setupTestGraph(t)
	facts := `[
		{"kind":"injects","to":"InvoiceService"},
		{"kind":"model","to":"BattleEvent"}
	]`
	g.SaveFileExtraction(revID, "testapp", "src/api/invoices.controller.ts", "extracted", "controller", facts, "")
	if _, err := g.ResolveExtractions("testapp", revID); err != nil {
		t.Fatal(err)
	}
	for _, raw := range []string{
		"code:provider:testapp:InvoiceService",
		"data:model:testapp:BattleEvent",
	} {
		referenced, err := validate.NormalizeNodeKey(raw)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := s.GetNodeByKey(referenced); err != nil {
			nodes, _ := s.ListNodes(store.NodeFilter{})
			var got []string
			for _, n := range nodes {
				got = append(got, n.NodeKey)
			}
			t.Errorf("normalized reference %q (from %q) matches no node; resolver emitted %v", referenced, raw, got)
		}
	}
}

// The field defect this contract exists for: a reference that arrives through
// the import_all path (validate.NormalizeNodeKey) must land on the very node
// the resolver minted for the same route, including its [paramCase] segment.
func TestOracleCanonicalKeys_ImportedReferenceHitsResolvedEndpoint(t *testing.T) {
	g, s, revID := setupTestGraph(t)
	facts := `[{"kind":"endpoint","method":"GET","target":"/invoices/[invoiceId]/lines"}]`
	g.SaveFileExtraction(revID, "testapp", "src/api/invoices.controller.ts", "extracted", "controller", facts, "")
	if _, err := g.ResolveExtractions("testapp", revID); err != nil {
		t.Fatal(err)
	}
	// The shape an honest cross-repo reference carries: the route as written
	// in source, not pre-lowercased.
	referenced, err := validate.NormalizeNodeKey("contract:endpoint:testapp:GET:/invoices/[invoiceId]/lines")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetNodeByKey(referenced); err != nil {
		nodes, _ := s.ListNodes(store.NodeFilter{NodeType: "endpoint"})
		var got []string
		for _, n := range nodes {
			got = append(got, n.NodeKey)
		}
		t.Fatalf("normalized reference %q matches no endpoint node; resolver emitted %v", referenced, got)
	}
}
