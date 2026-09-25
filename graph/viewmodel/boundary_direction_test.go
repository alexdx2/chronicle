package viewmodel

import (
	"testing"

	"github.com/alexdx2/chronicle-core/store"
)

// Rev3 task 5: the boundary answers the view's own question. deps asks
// "what does X depend on" (out crossings only — an incoming caller of
// something X happens to depend on is impact's question, not deps'); impact
// asks "what depends on X" (in crossings only).

// synthBoundaryStore extends synthStore with one more crossing: mail-api's
// mailer calls INTO an endpoint of shop.controller — from a view rooted at
// shop.controller with expand depth 1, this is an "in" crossing (someone
// outside calls something in scope), alongside shop.service's own "out"
// crossings to the topic and to mail-api (both one hop beyond depth 1, so
// still outside the view).
func synthBoundaryStore(t *testing.T) *store.Store {
	t.Helper()
	st := synthStore(t)
	rev, err := st.CreateRevision("d", "", "boundary-cross", "manual", "full", "{}")
	if err != nil {
		t.Fatal(err)
	}
	ctl, err := st.GetNodeByKey(mkShopCtl)
	if err != nil {
		t.Fatal(err)
	}
	mailer, err := st.GetNodeByKey("code:provider:d:mail-api/src/mailer")
	if err != nil {
		t.Fatal(err)
	}
	epID, err := st.UpsertNode(store.NodeRow{NodeKey: mkEndpoint, Layer: "contract", NodeType: "endpoint", DomainKey: "d",
		Name: "GET /orders", Status: "active", Confidence: 0.9, Freshness: 1, TrustScore: 0.9, Metadata: "{}", LastSeenRevisionID: rev})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.UpsertEdge(store.EdgeRow{EdgeKey: mkShopCtl + "->" + mkEndpoint + ":EXPOSES_ENDPOINT", FromNodeID: ctl.NodeID,
		ToNodeID: epID, EdgeType: "EXPOSES_ENDPOINT", DerivationKind: "hard", Active: true, Confidence: 0.9, Freshness: 1,
		TrustScore: 0.9, Metadata: "{}", LastSeenRevisionID: rev}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.UpsertEdge(store.EdgeRow{EdgeKey: "code:provider:d:mail-api/src/mailer->" + mkEndpoint + ":CALLS_ENDPOINT",
		FromNodeID: mailer.NodeID, ToNodeID: epID, EdgeType: "CALLS_ENDPOINT", DerivationKind: "hard", Active: true,
		Confidence: 0.9, Freshness: 1, TrustScore: 0.9, Metadata: "{}", LastSeenRevisionID: rev}); err != nil {
		t.Fatal(err)
	}
	return st
}

func TestBuildViewDeps_BoundaryOutOnlyNotIn(t *testing.T) {
	st := synthBoundaryStore(t)
	v, err := BuildView(st, ViewSpec{
		Scope:    ScopeSpec{Domain: "d", Nodes: []string{mkShopCtl}},
		Expand:   &ExpandSpec{Direction: "out", Depth: 1},
		Group:    GroupSpec{By: "service"},
		Collapse: true,
		Layout:   LayoutSpec{Preset: "deps"},
	})
	if err != nil {
		t.Fatalf("deps: %v", err)
	}
	if v.Boundary == nil {
		t.Fatalf("expected a boundary (shop.service's own out-of-depth crossings to the topic and to mail-api)")
	}
	if len(v.Boundary.In) != 0 {
		t.Errorf("deps must drop incoming crossings (that is impact's question): %+v", v.Boundary.In)
	}
	if len(v.Boundary.Out) == 0 {
		t.Errorf("deps must keep outgoing crossings: %+v", v.Boundary)
	}
}

func TestBuildViewImpact_BoundaryInOnlyNotOut(t *testing.T) {
	st := synthBoundaryStore(t)
	v, err := BuildView(st, ViewSpec{
		Scope:    ScopeSpec{Domain: "d", Nodes: []string{mkShopCtl}},
		Expand:   &ExpandSpec{Direction: "out", Depth: 1},
		Group:    GroupSpec{By: "service"},
		Collapse: true,
		Layout:   LayoutSpec{Preset: "impact"},
	})
	if err != nil {
		t.Fatalf("impact: %v", err)
	}
	if v.Boundary == nil {
		t.Fatalf("expected a boundary (mail-api's mailer calling into shop.controller's endpoint)")
	}
	if len(v.Boundary.Out) != 0 {
		t.Errorf("impact must drop outgoing crossings (that is deps' question): %+v", v.Boundary.Out)
	}
	if len(v.Boundary.In) == 0 {
		t.Errorf("impact must keep incoming crossings: %+v", v.Boundary)
	}
}

func TestBuildViewDeps_BoundaryNilWhenOutAlsoEmpty(t *testing.T) {
	st := synthBoundaryStore(t)
	// No Expand: the view holds only the seed (shop.controller), so
	// shop.service's own out crossings never enter the picture — the only
	// crossing at all is mailer's incoming call. deps drops "in", and "out"
	// was already empty, so Boundary must be nil, not an empty shell.
	v, err := BuildView(st, ViewSpec{
		Scope:    ScopeSpec{Domain: "d", Nodes: []string{mkShopCtl}},
		Group:    GroupSpec{By: "service"},
		Collapse: true,
		Layout:   LayoutSpec{Preset: "deps"},
	})
	if err != nil {
		t.Fatalf("deps: %v", err)
	}
	if v.Boundary != nil {
		t.Errorf("with only an incoming crossing and deps dropping it, boundary must be nil, got %+v", v.Boundary)
	}
}
