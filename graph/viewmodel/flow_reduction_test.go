package viewmodel

import (
	"strings"
	"testing"

	"github.com/alexdx2/chronicle-core/store"
)

// Rev3 task 6: a flow view includes the handler of its trigger endpoint
// (EXPOSES_ENDPOINT reversed to "handled by", reusing the path view's own
// mechanism, commit b0dc442 — not a duplicate of it) and applies transitive
// reduction to its own REQUIRES arrows: a requires arrow to X is not drawn
// when X is reachable from another of the use case's own REQUIRES targets
// by a drawn INJECTS hop — the dependency already reads through that chain.

// synthFlowReductionStore: a "checkout" flow required by A, B and C. A
// injects B (making the flow's own "requires B" redundant — B is also
// reachable through A) but nothing reaches C, so its own requires arrow is
// NOT redundant. The endpoint's handler injects A and C (nodes already in
// the view through REQUIRES) — the same "handler's injects to nodes
// already in view" shape as arena.controller -> arena.service / battle.guard
// in the real fixture (rev3 task 6 fixture expectation).
func synthFlowReductionStore(t *testing.T) (st *store.Store, flowKey, handlerKey, epKey, aKey, bKey, cKey string) {
	t.Helper()
	st, err := store.Open(t.TempDir() + "/flow.db")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	rev, err := st.CreateRevision("d", "", "flow-reduction", "manual", "full", "{}")
	if err != nil {
		t.Fatal(err)
	}
	mk := func(key, layer, ntype, name, file string) int64 {
		id, err := st.UpsertNode(store.NodeRow{NodeKey: key, Layer: layer, NodeType: ntype, DomainKey: "d", Name: name,
			FilePath: file, Status: "active", Confidence: 0.8, Freshness: 1, TrustScore: 0.8, Metadata: "{}", LastSeenRevisionID: rev})
		if err != nil {
			t.Fatalf("upsert %s: %v", key, err)
		}
		return id
	}
	edge := func(from, to int64, fk, tk, typ string) {
		if _, err := st.UpsertEdge(store.EdgeRow{EdgeKey: fk + "->" + tk + ":" + typ, FromNodeID: from, ToNodeID: to, EdgeType: typ,
			DerivationKind: "hard", Active: true, Confidence: 0.8, Freshness: 1, TrustScore: 0.8, Metadata: "{}", LastSeenRevisionID: rev}); err != nil {
			t.Fatalf("edge %s: %v", typ, err)
		}
	}
	flowKey = "flow:use_case:d:post:/checkout"
	epKey = "contract:endpoint:d:post:/checkout"
	handlerKey = "code:controller:d:checkout-svc/src/checkout.controller"
	aKey = "code:provider:d:checkout-svc/src/checkout.service"
	bKey = "code:provider:d:checkout-svc/src/payment.client"
	cKey = "code:provider:d:checkout-svc/src/audit.guard"

	flowID := mk(flowKey, "flow", "use_case", "checkout (POST /checkout)", "")
	epID := mk(epKey, "contract", "endpoint", "POST /checkout", "")
	handlerID := mk(handlerKey, "code", "controller", "checkout.controller", "checkout-svc/src/checkout.controller.ts")
	aID := mk(aKey, "code", "provider", "checkout.service", "checkout-svc/src/checkout.service.ts")
	bID := mk(bKey, "code", "provider", "payment.client", "checkout-svc/src/payment.client.ts")
	cID := mk(cKey, "code", "provider", "audit.guard", "checkout-svc/src/audit.guard.ts")

	edge(handlerID, epID, handlerKey, epKey, "EXPOSES_ENDPOINT")
	edge(epID, flowID, epKey, flowKey, "TRIGGERS_FLOW")
	edge(flowID, aID, flowKey, aKey, "REQUIRES")
	edge(flowID, bID, flowKey, bKey, "REQUIRES")
	edge(flowID, cID, flowKey, cKey, "REQUIRES")
	edge(handlerID, aID, handlerKey, aKey, "INJECTS")
	edge(handlerID, cID, handlerKey, cKey, "INJECTS")
	edge(aID, bID, aKey, bKey, "INJECTS")
	return st, flowKey, handlerKey, epKey, aKey, bKey, cKey
}

func TestBuildViewFlow_HandlerAndTransitiveReduction(t *testing.T) {
	st, flowKey, handlerKey, epKey, aKey, bKey, cKey := synthFlowReductionStore(t)
	v, err := BuildView(st, ViewSpec{
		Scope:  ScopeSpec{Domain: "d", Flow: flowKey},
		Group:  GroupSpec{By: "none"},
		Layout: LayoutSpec{Preset: "flow"},
	})
	if err != nil {
		t.Fatalf("flow: %v", err)
	}

	nodeKeys := map[string]bool{}
	for _, n := range v.Nodes {
		nodeKeys[n.Key] = true
	}
	for _, want := range []string{flowKey, epKey, handlerKey, aKey, bKey, cKey} {
		if !nodeKeys[want] {
			t.Errorf("expected node %s in view; nodes=%+v", want, v.Nodes)
		}
	}

	var handled *VEdge
	requiresTo := map[string]bool{}
	injectsPairs := map[[2]string]bool{}
	for i := range v.Edges {
		e := &v.Edges[i]
		switch e.Kind {
		case "EXPOSES_ENDPOINT":
			handled = e
		case "REQUIRES":
			requiresTo[e.To] = true
		case "INJECTS":
			injectsPairs[[2]string{e.From, e.To}] = true
		}
	}

	if handled == nil {
		t.Fatalf("no EXPOSES_ENDPOINT arrow; edges=%+v", v.Edges)
	}
	if handled.From != epKey || handled.To != handlerKey {
		t.Errorf("EXPOSES_ENDPOINT should draw endpoint -> handler, got %s -> %s", handled.From, handled.To)
	}
	if handled.Label != "handled by" {
		t.Errorf("EXPOSES_ENDPOINT label = %q, want %q", handled.Label, "handled by")
	}

	// Transitive reduction: A and C stay (A is the one that injects B; C is
	// not reachable from anything); B is dropped (reachable from A).
	if !requiresTo[aKey] {
		t.Errorf("requires -> A should stay drawn (nothing reaches A): %+v", v.Edges)
	}
	if !requiresTo[cKey] {
		t.Errorf("requires -> C should stay drawn (nothing reaches C): %+v", v.Edges)
	}
	if requiresTo[bKey] {
		t.Errorf("requires -> B should be reduced (reachable via A's drawn injects): %+v", v.Edges)
	}

	// The handler's own injects to nodes already in the view still draw,
	// and so does the reduction's own basis (A -> B).
	if !injectsPairs[[2]string{handlerKey, aKey}] {
		t.Errorf("handler -> A injects should draw: %+v", v.Edges)
	}
	if !injectsPairs[[2]string{handlerKey, cKey}] {
		t.Errorf("handler -> C injects should draw: %+v", v.Edges)
	}
	if !injectsPairs[[2]string{aKey, bKey}] {
		t.Errorf("A -> B injects (the reduction basis) should still draw: %+v", v.Edges)
	}

	// rev3 fix F1: the skipped requires arrow is named once, as a
	// View.Notes entry — Notes already reach every surface (text,
	// markdown, mermaid, the MCP answer, the dashboard), so nothing
	// disappears on any of them the way a RenderText-only field did.
	wantNote := "also requires (reached via injects): payment.client"
	foundNote := false
	for _, n := range v.Notes {
		if n == wantNote {
			foundNote = true
		}
	}
	if !foundNote {
		t.Errorf("Notes = %v, want to contain %q", v.Notes, wantNote)
	}
	txt := RenderText(v)
	if !strings.Contains(txt, wantNote) {
		t.Errorf("RenderText should print the note:\n%s", txt)
	}
	if !strings.Contains(RenderMermaid(v), wantNote) {
		t.Errorf("RenderMermaid should carry the note too (Notes reach every surface):\n%s", RenderMermaid(v))
	}
}

// TestBuildViewPathAndFlow_HandlerPillsSuppressed: rev3 fix F8. A handler
// card on a path or flow view lists its EXPOSES_ENDPOINT pills same as
// anywhere else, but the view already draws the one endpoint that matters
// as its own node (the trigger/path endpoint) — the pill list distracts
// (and can lead with an unrelated endpoint on a busier handler). Path and
// flow must not fill VNode.Endpoints; every other preset is unaffected.
func TestBuildViewPathAndFlow_HandlerPillsSuppressed(t *testing.T) {
	st, flowKey, handlerKey, epKey, _, _, _ := synthFlowReductionStore(t)

	// flow: the handler (checkout.controller) exposes POST /checkout.
	flowView, err := BuildView(st, ViewSpec{
		Scope:  ScopeSpec{Domain: "d", Flow: flowKey},
		Group:  GroupSpec{By: "none"},
		Layout: LayoutSpec{Preset: "flow"},
	})
	if err != nil {
		t.Fatalf("flow: %v", err)
	}
	handler := findNode(flowView, handlerKey)
	if handler == nil {
		t.Fatalf("handler missing from flow view; nodes=%+v", flowView.Nodes)
	}
	if len(handler.Endpoints) != 0 {
		t.Errorf("flow: handler's endpoint pills should be suppressed, got %+v", handler.Endpoints)
	}
	if strings.Contains(RenderText(flowView), "exposes") {
		t.Errorf("flow: text must not print an \"exposes\" pill list:\n%s", RenderText(flowView))
	}

	// path: epKey -> handlerKey (endpoint<->handler) -> aKey (injects) is
	// the only route (TRIGGERS_FLOW/REQUIRES are excluded from path
	// search), so the handler is drawn on the path too.
	pathView, err := BuildView(st, ViewSpec{
		Scope:  ScopeSpec{Domain: "d", Nodes: []string{epKey, "code:provider:d:checkout-svc/src/checkout.service"}},
		Expand: &ExpandSpec{Mode: "path"},
		Group:  GroupSpec{By: "none"},
		Layout: LayoutSpec{Preset: "path"},
	})
	if err != nil {
		t.Fatalf("path: %v", err)
	}
	pathHandler := findNode(pathView, handlerKey)
	if pathHandler == nil {
		t.Fatalf("handler missing from path view; nodes=%+v", pathView.Nodes)
	}
	if len(pathHandler.Endpoints) != 0 {
		t.Errorf("path: handler's endpoint pills should be suppressed, got %+v", pathHandler.Endpoints)
	}

	// Negative control: the SAME handler, same store, a preset that is
	// neither path nor flow — the pill must still be there.
	otherView, err := BuildView(st, ViewSpec{
		Scope: ScopeSpec{Domain: "d", Flow: flowKey},
		Group: GroupSpec{By: "none"},
	})
	if err != nil {
		t.Fatalf("no-preset: %v", err)
	}
	otherHandler := findNode(otherView, handlerKey)
	if otherHandler == nil || len(otherHandler.Endpoints) != 1 || otherHandler.Endpoints[0].Key != epKey {
		t.Errorf("other presets must be unaffected: handler=%+v", otherHandler)
	}
}
