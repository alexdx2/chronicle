package viewmodel

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/alexdx2/chronicle-core/store"
)

// Rev3 task 8a: a node of another domain that shares an active,
// non-hierarchy edge with a node of the view's domain is a foreign
// neighbour — boundary material only (never a scope member), dimmed and
// named by domain wherever this store draws a boundary.
//
// synthForeignStore builds one store with THREE domains:
//
//	A — a-svc (service) owning a-comp (provider). a-comp:
//	  - CALLS_SERVICE b-svc (the twin)
//	  - CALLS_ENDPOINT b-ep1, b-ep2 (both twinned to b-svc via the same
//	    caller's CALLS_SERVICE, domain-correlated — the cross-domain twin
//	    rule, since this store never scanned b-svc's own EXPOSES_ENDPOINT)
//	  - CONSUMES_TOPIC b-topic
//	  - CALLS_ENDPOINT c-ep (no twin: no CALLS_SERVICE from a-comp into
//	    domain C at all)
//	B — b-svc (status external), b-ep1/b-ep2 (status active), b-topic
//	  (status active) — deliberately mixed statuses: the rule keeps
//	  external, active AND stale, so this store exercises two of the
//	  three deliberately, not just the federation-typical "external".
//	C — c-ep (status active), otherwise unconnected to B.
func synthForeignStore(t *testing.T) (st *store.Store, aCompKey, aSvcKey, bSvcKey, bEp1Key, bEp2Key, bTopicKey, cEpKey string) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "foreign.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	rev, err := st.CreateRevision("A", "", "foreign-neighbours", "manual", "full", "{}")
	if err != nil {
		t.Fatal(err)
	}

	aSvcKey = "service:service:A:a-svc"
	aCompKey = "code:provider:A:a-comp"
	bSvcKey = "service:service:B:b-svc"
	bEp1Key = "contract:endpoint:B:get:/x"
	bEp2Key = "contract:endpoint:B:get:/y"
	bTopicKey = "contract:topic:B:b-topic"
	cEpKey = "contract:endpoint:C:get:/z"

	mk := func(key, layer, ntype, domain, name, status, file string) int64 {
		id, err := st.UpsertNode(store.NodeRow{NodeKey: key, Layer: layer, NodeType: ntype, DomainKey: domain, Name: name,
			FilePath: file, Status: status, Confidence: 0.9, Freshness: 1, TrustScore: 0.9, Metadata: "{}", LastSeenRevisionID: rev})
		if err != nil {
			t.Fatalf("upsert %s: %v", key, err)
		}
		return id
	}
	edge := func(from, to int64, fk, tk, typ string) {
		if _, err := st.UpsertEdge(store.EdgeRow{EdgeKey: fk + "->" + tk + ":" + typ, FromNodeID: from, ToNodeID: to, EdgeType: typ,
			DerivationKind: "hard", Active: true, Confidence: 0.9, Freshness: 1, TrustScore: 0.9, Metadata: "{}", LastSeenRevisionID: rev}); err != nil {
			t.Fatalf("edge %s->%s %s: %v", fk, tk, typ, err)
		}
	}

	// a-comp's ownership by a-svc comes from the path-prefix rule
	// (ownershipMap), the same signal a real scan produces: a-svc's root
	// dir ("a-svc", from its FilePath) is a prefix of a-comp's FilePath.
	mk(aSvcKey, "service", "service", "A", "a-svc", "active", "a-svc/package.json")
	aCompID := mk(aCompKey, "code", "provider", "A", "a-comp", "active", "a-svc/src/a-comp.ts")

	bSvcID := mk(bSvcKey, "service", "service", "B", "b-svc", "external", "")
	bEp1ID := mk(bEp1Key, "contract", "endpoint", "B", "GET /x", "active", "")
	bEp2ID := mk(bEp2Key, "contract", "endpoint", "B", "GET /y", "active", "")
	bTopicID := mk(bTopicKey, "contract", "topic", "B", "b-topic", "active", "")
	cEpID := mk(cEpKey, "contract", "endpoint", "C", "GET /z", "active", "")

	edge(aCompID, bSvcID, aCompKey, bSvcKey, "CALLS_SERVICE")
	edge(aCompID, bEp1ID, aCompKey, bEp1Key, "CALLS_ENDPOINT")
	edge(aCompID, bEp2ID, aCompKey, bEp2Key, "CALLS_ENDPOINT")
	edge(aCompID, bTopicID, aCompKey, bTopicKey, "CONSUMES_TOPIC")
	edge(aCompID, cEpID, aCompKey, cEpKey, "CALLS_ENDPOINT")

	return st, aCompKey, aSvcKey, bSvcKey, bEp1Key, bEp2Key, bTopicKey, cEpKey
}

func findNode(v *View, key string) *VNode {
	for i := range v.Nodes {
		if v.Nodes[i].Key == key {
			return &v.Nodes[i]
		}
	}
	return nil
}

func findEdge(v *View, from, to, kind string) *VEdge {
	for i := range v.Edges {
		if v.Edges[i].From == from && v.Edges[i].To == to && v.Edges[i].Kind == kind {
			return &v.Edges[i]
		}
	}
	return nil
}

// TestForeignNeighbours_C2_ServiceCardTwinAndTopic covers the brief's main
// scenario: c2 of A draws b-svc as a dimmed boundary card (Domain "B") with
// "HTTP ×2" from a-svc's collapsed card (the twin rule folding 2 endpoint
// calls + the covered CALLS_SERVICE into one arrow), and b-topic as a dimmed
// topic card with a "sub" arrow into a-svc; View.Boundary lists both with
// Domain "B".
func TestForeignNeighbours_C2_ServiceCardTwinAndTopic(t *testing.T) {
	st, _, aSvcKey, bSvcKey, _, _, bTopicKey, _ := synthForeignStore(t)
	v, err := BuildPreset(st, "c2", "A", "")
	if err != nil {
		t.Fatalf("c2: %v", err)
	}

	bSvc := findNode(v, bSvcKey)
	if bSvc == nil {
		t.Fatalf("c2 should draw b-svc as a boundary card; nodes=%+v", v.Nodes)
	}
	if !bSvc.Boundary {
		t.Errorf("b-svc card should be marked Boundary (dimmed): %+v", bSvc)
	}
	if bSvc.Domain != "B" {
		t.Errorf("b-svc card Domain = %q, want B: %+v", bSvc.Domain, bSvc)
	}

	call := findEdge(v, aSvcKey, bSvcKey, "CALLS_SERVICE")
	if call == nil {
		t.Fatalf("expected a-svc -> b-svc CALLS_SERVICE arrow; edges=%+v", v.Edges)
	}
	if call.Weight != 2 {
		t.Errorf("a-svc -> b-svc weight = %d, want 2 (two endpoint calls, twin adds no weight): %+v", call.Weight, call)
	}
	if got := arrowLabel(call); got != "HTTP ×2" {
		t.Errorf("arrow label = %q, want %q", got, "HTTP ×2")
	}

	bTopic := findNode(v, bTopicKey)
	if bTopic == nil {
		t.Fatalf("c2 should draw b-topic as a boundary card; nodes=%+v", v.Nodes)
	}
	if !bTopic.Boundary || bTopic.Domain != "B" {
		t.Errorf("b-topic card should be Boundary+Domain B: %+v", bTopic)
	}
	if bTopic.Group != "" {
		t.Errorf("a foreign topic sits outside any broker frame: group=%q", bTopic.Group)
	}
	sub := findEdge(v, bTopicKey, aSvcKey, "CONSUMES_TOPIC")
	if sub == nil {
		t.Fatalf("expected b-topic -> a-svc CONSUMES_TOPIC (sub) arrow; edges=%+v", v.Edges)
	}
	if got := arrowLabel(sub); got != "sub" {
		t.Errorf("topic arrow label = %q, want sub", got)
	}

	if v.Boundary == nil {
		t.Fatalf("expected a populated Boundary")
	}
	var outDomains, inDomains []string
	for _, o := range v.Boundary.Out {
		if o.ToName == "b-svc" {
			outDomains = append(outDomains, o.Domain)
		}
	}
	for _, in := range v.Boundary.In {
		if in.FromName == "b-topic" {
			inDomains = append(inDomains, in.Domain)
		}
	}
	if len(outDomains) == 0 || outDomains[0] != "B" {
		t.Errorf("Boundary.Out for b-svc should carry Domain B: out=%+v", v.Boundary.Out)
	}
	if len(inDomains) == 0 || inDomains[0] != "B" {
		t.Errorf("Boundary.In for b-topic should carry Domain B: in=%+v", v.Boundary.In)
	}

	// Renderers (brief item 5): text " (domain B)", mermaid "domain B" line.
	txt := RenderText(v)
	if !strings.Contains(txt, "b-svc [service] (domain B)") {
		t.Errorf("RenderText should carry \"b-svc [service] (domain B)\":\n%s", txt)
	}
	mm := RenderMermaid(v)
	if !strings.Contains(mm, "domain B") {
		t.Errorf("RenderMermaid should carry a \"domain B\" line:\n%s", mm)
	}
}

// TestForeignNeighbours_C2_EndpointWithoutTwinStaysEndpointCard: c-ep has no
// sibling CALLS_SERVICE from a-comp into domain C, so it cannot be lifted —
// it stays its own dimmed endpoint card, arrow kind CALLS_ENDPOINT.
func TestForeignNeighbours_C2_EndpointWithoutTwinStaysEndpointCard(t *testing.T) {
	st, _, aSvcKey, _, _, _, _, cEpKey := synthForeignStore(t)
	v, err := BuildPreset(st, "c2", "A", "")
	if err != nil {
		t.Fatalf("c2: %v", err)
	}
	cEp := findNode(v, cEpKey)
	if cEp == nil {
		t.Fatalf("c2 should draw c-ep as its own boundary card (no twin); nodes=%+v", v.Nodes)
	}
	if !cEp.Boundary || cEp.Domain != "C" {
		t.Errorf("c-ep should be Boundary+Domain C: %+v", cEp)
	}
	if cEp.Type != "endpoint" {
		t.Errorf("c-ep should stay an endpoint card, type=%q", cEp.Type)
	}
	edge := findEdge(v, aSvcKey, cEpKey, "CALLS_ENDPOINT")
	if edge == nil {
		t.Fatalf("expected a-svc -> c-ep CALLS_ENDPOINT arrow (not lifted); edges=%+v", v.Edges)
	}
	if edge.Weight != 1 {
		t.Errorf("weight = %d, want 1", edge.Weight)
	}
}

// TestForeignNeighbours_C3_BoundaryCardsFromComponent: c3 of a-svc draws the
// foreign neighbours as boundary cards reached from a-comp itself (component
// altitude, not collapsed to the service).
func TestForeignNeighbours_C3_BoundaryCardsFromComponent(t *testing.T) {
	st, aCompKey, _, bSvcKey, bEp1Key, bEp2Key, bTopicKey, cEpKey := synthForeignStore(t)
	v, err := BuildPreset(st, "c3", "A", "a-svc")
	if err != nil {
		t.Fatalf("c3: %v", err)
	}
	for _, want := range []string{bSvcKey, bTopicKey, cEpKey} {
		n := findNode(v, want)
		if n == nil {
			t.Errorf("c3 should draw %s as a boundary card; nodes=%+v", want, v.Nodes)
			continue
		}
		if !n.Boundary {
			t.Errorf("%s should be marked Boundary: %+v", want, n)
		}
	}
	// The two twinned endpoints must NOT become their own boundary cards —
	// they are lifted into b-svc's arrow, same as the same-domain twin rule.
	for _, unwanted := range []string{bEp1Key, bEp2Key} {
		if n := findNode(v, unwanted); n != nil {
			t.Errorf("%s should be lifted into b-svc, not its own card: %+v", unwanted, n)
		}
	}
	call := findEdge(v, aCompKey, bSvcKey, "CALLS_SERVICE")
	if call == nil {
		t.Fatalf("expected a-comp -> b-svc CALLS_SERVICE arrow at component altitude; edges=%+v", v.Edges)
	}
	if call.Weight != 2 {
		t.Errorf("a-comp -> b-svc weight = %d, want 2", call.Weight)
	}
	if noTwin := findEdge(v, aCompKey, cEpKey, "CALLS_ENDPOINT"); noTwin == nil {
		t.Errorf("expected a-comp -> c-ep CALLS_ENDPOINT arrow (no twin); edges=%+v", v.Edges)
	}
}

// TestForeignNeighbours_Deps_ListsForeignServiceAsOutWithDomain: deps of
// a-comp lists b-svc as an "out" boundary entry carrying Domain "B" — never
// "in" (deps asks only "what does X depend on").
func TestForeignNeighbours_Deps_ListsForeignServiceAsOutWithDomain(t *testing.T) {
	st, aCompKey, _, bSvcKey, _, _, _, _ := synthForeignStore(t)
	v, err := BuildPreset(st, "deps", "A", aCompKey)
	if err != nil {
		t.Fatalf("deps: %v", err)
	}
	if v.Boundary == nil {
		t.Fatalf("expected a boundary")
	}
	if len(v.Boundary.In) != 0 {
		t.Errorf("deps must drop incoming crossings: %+v", v.Boundary.In)
	}
	var found *BoundaryOut
	for i := range v.Boundary.Out {
		if v.Boundary.Out[i].ToName == "b-svc" {
			found = &v.Boundary.Out[i]
		}
	}
	if found == nil {
		t.Fatalf("expected b-svc in deps' out boundary: %+v", v.Boundary.Out)
	}
	if found.Domain != "B" {
		t.Errorf("b-svc boundary entry Domain = %q, want B: %+v", found.Domain, found)
	}
	// The foreign service must never become a scope member/card of its own —
	// boundary material only, collapse=true means it would show as a VGroup
	// if it had (wrongly) joined the scope.
	for _, g := range v.Groups {
		if g.Key == bSvcKey {
			t.Errorf("b-svc must never become a VGroup (scope member): %+v", g)
		}
	}
	if n := findNode(v, bSvcKey); n != nil && !n.Boundary {
		t.Errorf("if b-svc is drawn at all here it must be Boundary, not a scope card: %+v", n)
	}
}

// TestForeignNeighbours_ImpactOfForeignNode_IsolatedFromA: rev3 task 8a
// isolation check — "impact of B's node is not affected in A (only A's
// views change)". Two guarantees:
//  1. Asking for impact of B's own service WHILE scoped to domain A must
//     not silently root a scope on a node this store never owns (it never
//     joins seeds/inView; it is reported Missing, exactly as an unknown key
//     would be).
//  2. Building A's views first (c2, c3, deps — all exercised above) must
//     not leave any state that changes what domain B's OWN impact view of
//     its OWN node reports — BuildView is a pure read of the store.
func TestForeignNeighbours_ImpactOfForeignNode_IsolatedFromA(t *testing.T) {
	st, aCompKey, _, bSvcKey, _, _, _, _ := synthForeignStore(t)

	// (1) impact scoped to A, targeting B's node key.
	v, err := BuildPreset(st, "impact", "A", bSvcKey)
	if err != nil {
		t.Fatalf("impact: %v", err)
	}
	foundMissing := false
	for _, m := range v.Missing {
		if m == bSvcKey {
			foundMissing = true
		}
	}
	if !foundMissing {
		t.Errorf("b-svc requested as an impact subject of domain A should be Missing, not silently scoped: missing=%+v", v.Missing)
	}
	if len(v.Nodes) != 0 {
		t.Errorf("no node should have been drawn (b-svc never joins A's scope): nodes=%+v", v.Nodes)
	}

	// (2) B's own impact view, before and after A's views are built.
	before, err := BuildPreset(st, "impact", "B", bSvcKey)
	if err != nil {
		t.Fatalf("impact B (before): %v", err)
	}
	beforeTxt := RenderText(before)

	// Exercise every A-side path this task touches.
	if _, err := BuildPreset(st, "c2", "A", ""); err != nil {
		t.Fatalf("c2 A: %v", err)
	}
	if _, err := BuildPreset(st, "c3", "A", "a-svc"); err != nil {
		t.Fatalf("c3 A: %v", err)
	}
	if _, err := BuildPreset(st, "deps", "A", aCompKey); err != nil {
		t.Fatalf("deps A: %v", err)
	}

	after, err := BuildPreset(st, "impact", "B", bSvcKey)
	if err != nil {
		t.Fatalf("impact B (after): %v", err)
	}
	afterTxt := RenderText(after)
	if beforeTxt != afterTxt {
		t.Errorf("B's impact view changed after building A's views (should be a pure read):\nbefore:\n%s\nafter:\n%s", beforeTxt, afterTxt)
	}
}
