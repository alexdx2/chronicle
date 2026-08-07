package graph

import (
	"sort"
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
		// Digit→upper is a boundary on this side; validate.NormalizeName was
		// taught the same boundary so both sides key "S3Client" as
		// "s3-client" (see TestNormalizeName).
		"S3Client":      "s3.client",
		"V2Service":     "v2.service",
		"Oauth2Service": "oauth2.service",
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
	// Every fact family that mints a key of its own shape must be here, or the
	// oracle certifies only the families it happens to cover: http_call to a
	// dotted host mints service:external_system (Task 5: a real third-party
	// FQDN never materializes a domain-internal contract:endpoint node — see
	// oracle_external_urls_test.go); http_call to a bare/undeclared
	// internal-shaped host still mints service:external_system AND its
	// post-pass contract:endpoint (cross-repo federation candidate);
	// declares_service mints service:service from a dotted/Pascal declared
	// name (.csproj, package.json).
	facts := `[
		{"kind":"endpoint","method":"GET","target":"/invoices/[invoiceId]/lines"},
		{"kind":"injects","to":"SESSION_COOKIE_STORE"},
		{"kind":"injects","to":"S3Client"},
		{"kind":"import","symbols":["X"],"to":"@okeep/ui/button"},
		{"kind":"http_call","method":"POST","target":"https://hooks.example.com/battles"},
		{"kind":"http_call","method":"POST","target":"http://battle-svc/battles"},
		{"kind":"declares_service","to":"Spectators.Api"},
		{"kind":"declares_service","to":"ScoreboardApi"}
	]`
	g.SaveFileExtraction(revID, "testapp", "src/api/invoices.ts", "extracted", "controller", facts, "")
	if _, err := g.ResolveExtractions("testapp", revID); err != nil {
		t.Fatal(err)
	}
	nodes, _ := s.ListNodes(store.NodeFilter{})
	if len(nodes) == 0 {
		t.Fatal("resolve produced no nodes — oracle would pass vacuously")
	}
	emitted := map[string]bool{}
	for _, n := range nodes {
		emitted[n.NodeKey] = true
		norm, err := validate.NormalizeNodeKey(n.NodeKey)
		if err != nil {
			t.Errorf("emitted non-validating key %q: %v", n.NodeKey, err)
			continue
		}
		if norm != n.NodeKey {
			t.Errorf("key %q is not a fixed point (normalizes to %q)", n.NodeKey, norm)
		}
	}
	// Each family above must actually be present, or a handler that silently
	// stopped emitting would leave its shape certified by an empty set.
	for _, want := range []string{
		"contract:endpoint:testapp:get:/invoices/[invoiceid]/lines",
		"code:provider:testapp:session-cookie-store",
		"code:provider:testapp:s3-client",
		"code:provider:testapp:@okeep/ui",
		"service:external_system:testapp:hooks-example-com",
		// Bare/undeclared internal-shaped host (no dot) — the http_call
		// post-pass still materializes its endpoint (Task 5: isExternalHost
		// only routes dotted, unmatched hosts to the external-only path).
		"service:external_system:testapp:battle-svc",
		"contract:endpoint:testapp:post:/battles",
		"service:service:testapp:spectators-api",
		"service:service:testapp:scoreboard-api",
	} {
		if !emitted[want] {
			var got []string
			for k := range emitted {
				got = append(got, k)
			}
			sort.Strings(got)
			t.Errorf("fact family produced no node under %q; emitted %v", want, got)
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
		{"kind":"injects","to":"S3Client"},
		{"kind":"injects","to":"V2Service"},
		{"kind":"injects","to":"Oauth2Service"},
		{"kind":"model","to":"BattleEvent"}
	]`
	g.SaveFileExtraction(revID, "testapp", "src/api/invoices.controller.ts", "extracted", "controller", facts, "")
	if _, err := g.ResolveExtractions("testapp", revID); err != nil {
		t.Fatal(err)
	}
	for _, raw := range []string{
		"code:provider:testapp:InvoiceService",
		"data:model:testapp:BattleEvent",
		// Digit→upper: the resolver splits "S3Client" into "s3.client" →
		// "s3-client". If NormalizeName does not split on the same boundary
		// the reference normalizes to "s3client" and misses.
		"code:provider:testapp:S3Client",
		"code:provider:testapp:V2Service",
		"code:provider:testapp:Oauth2Service",
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

// declares_service is the .csproj/package.json path: the extractor emits the
// declared name as written ("ScoreboardApi", "Spectators.Api"). Pre-lowering it
// before canonicalization gives "scoreboardapi" while the canonical spelling of
// the same name is "scoreboard-api" — a key reference to the service then
// misses, and a manifest-declared or cross-repo twin is minted instead.
func TestOracleCanonicalKeys_ImportedReferenceHitsDeclaredService(t *testing.T) {
	g, s, revID := setupTestGraph(t)
	facts := `[
		{"kind":"declares_service","to":"ScoreboardApi"},
		{"kind":"declares_service","to":"Spectators.Api"}
	]`
	g.SaveFileExtraction(revID, "testapp", "src/Scoreboard.Api/Scoreboard.Api.csproj", "extracted", "", facts, "")
	if _, err := g.ResolveExtractions("testapp", revID); err != nil {
		t.Fatal(err)
	}
	for _, raw := range []string{
		"service:service:testapp:ScoreboardApi",
		"service:service:testapp:Spectators.Api",
		"service:service:testapp:scoreboard-api",
	} {
		referenced, err := validate.NormalizeNodeKey(raw)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := s.GetNodeByKey(referenced); err != nil {
			nodes, _ := s.ListNodes(store.NodeFilter{Layer: "service"})
			var got []string
			for _, n := range nodes {
				got = append(got, n.NodeKey)
			}
			t.Errorf("normalized reference %q (from %q) matches no service node; resolver emitted %v", referenced, raw, got)
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
