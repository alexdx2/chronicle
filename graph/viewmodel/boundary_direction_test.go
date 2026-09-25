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

// Rev3 task 6b: topic consumption used to be classified by message flow
// everywhere (CONSUMES_TOPIC drawn topic -> consumer, an "in" entry) even
// though deps/impact's Boundary now means "depends on" (rev3 task 5), not
// "message arrives". In a dependency question, CONSUMES_TOPIC must follow
// dependency direction instead: an in-view consumer of an outside topic
// depends on it (an "out" entry), and an outside consumer of an in-view
// topic depends on IT (an "in" entry, impact's own question) — the
// materialized edge follows the same raw consumer -> topic direction
// either way, never the message-flow reversal.

// synthConsumerOnlyStore extends synthStore with two independent,
// publish-free topic/consumer pairs:
//   - queue.worker (shop-api) consumes queue-in; nothing depends on
//     queue.worker and it depends on nothing else — the exact
//     "impact of battle.queue" shape (a lone node whose only crossing is
//     its own CONSUMES_TOPIC).
//   - queue.listener (mail-api) consumes queue-out — used from queue-out's
//     own side, as the "outside consumer of an in-view topic" case.
func synthConsumerOnlyStore(t *testing.T) (st *store.Store, workerKey, queueInKey, listenerKey, queueOutKey string) {
	t.Helper()
	st = synthStore(t)
	rev, err := st.CreateRevision("d", "", "consumer-only", "manual", "full", "{}")
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
	workerKey = "code:provider:d:shop-api/src/queue.worker"
	queueInKey = "contract:topic:d:queue-in"
	listenerKey = "code:provider:d:mail-api/src/queue.listener"
	queueOutKey = "contract:topic:d:queue-out"

	workerID := mk(workerKey, "code", "provider", "queue.worker", "shop-api/src/queue.worker.ts")
	queueInID := mk(queueInKey, "contract", "topic", "queue-in", "")
	listenerID := mk(listenerKey, "code", "provider", "queue.listener", "mail-api/src/queue.listener.ts")
	queueOutID := mk(queueOutKey, "contract", "topic", "queue-out", "")

	edge(workerID, queueInID, workerKey, queueInKey, "CONSUMES_TOPIC")
	edge(listenerID, queueOutID, listenerKey, queueOutKey, "CONSUMES_TOPIC")
	return st, workerKey, queueInKey, listenerKey, queueOutKey
}

func TestBuildViewImpact_ConsumerOnlyTopicNoBoundary(t *testing.T) {
	st, workerKey, _, _, _ := synthConsumerOnlyStore(t)
	v, err := BuildView(st, ViewSpec{
		Scope:    ScopeSpec{Domain: "d", Nodes: []string{workerKey}},
		Group:    GroupSpec{By: "service"},
		Collapse: true,
		Layout:   LayoutSpec{Preset: "impact"},
	})
	if err != nil {
		t.Fatalf("impact: %v", err)
	}
	if v.Boundary != nil {
		t.Errorf("impact of a consumer-only node should have no boundary at all (its own CONSUMES_TOPIC is now an out-direction fact, and impact drops out): %+v", v.Boundary)
	}
}

func TestBuildViewDeps_ConsumerOnlyTopicKeptAsOut(t *testing.T) {
	st, workerKey, _, _, _ := synthConsumerOnlyStore(t)
	v, err := BuildView(st, ViewSpec{
		Scope:    ScopeSpec{Domain: "d", Nodes: []string{workerKey}},
		Group:    GroupSpec{By: "service"},
		Collapse: true,
		Layout:   LayoutSpec{Preset: "deps"},
	})
	if err != nil {
		t.Fatalf("deps: %v", err)
	}
	if v.Boundary == nil || len(v.Boundary.Out) != 1 {
		t.Fatalf("deps should keep the consumed topic as an out crossing: %+v", v.Boundary)
	}
	if len(v.Boundary.In) != 0 {
		t.Errorf("deps must not carry an in crossing (that is impact's question): %+v", v.Boundary.In)
	}
	out := v.Boundary.Out[0]
	if out.ToName != "queue-in" || out.ToKind != "topic" || out.Kind != "async" || out.Via != "queue.worker" {
		t.Errorf("out crossing = %+v, want queue-in/topic/async via queue.worker", out)
	}
}

func TestBuildViewImpact_OutsideConsumerOfInViewTopicIsIn(t *testing.T) {
	st, _, _, listenerKey, queueOutKey := synthConsumerOnlyStore(t)
	v, err := BuildView(st, ViewSpec{
		Scope:    ScopeSpec{Domain: "d", Nodes: []string{queueOutKey}},
		Group:    GroupSpec{By: "service"},
		Collapse: true,
		Layout:   LayoutSpec{Preset: "impact"},
	})
	if err != nil {
		t.Fatalf("impact: %v", err)
	}
	if v.Boundary == nil || len(v.Boundary.In) != 1 {
		t.Fatalf("impact of the topic should show its outside consumer as an in crossing (it depends on the topic): %+v", v.Boundary)
	}
	if v.Boundary.In[0].FromName != "queue.listener" || v.Boundary.In[0].Kind != "async" {
		t.Errorf("in crossing = %+v, want queue.listener/async", v.Boundary.In[0])
	}
	if len(v.Boundary.Out) != 0 {
		t.Errorf("impact must not carry an out crossing (that is deps' question): %+v", v.Boundary.Out)
	}
	_ = listenerKey
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
