package viewmodel

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/alexdx2/chronicle-core/store"
)

// Rev3 task 3: C2/services hide a service that talks to nobody (empty), and
// C2 hides a topic that is really one service talking to itself (private).
// Extra: the same commit's twin-call fix (addBoundaryEdge/the main edge
// loop, rev3 task 2 fix round 1) is applied to addPinEdge too.

// synthEmptyServiceStore extends synthStore with "empty-svc" (no owned
// members, no edges at all — the pure isolated case) and "silent-target"
// (no owned members either, but shop.service calls it — the negative case:
// an arrow is enough to keep a service drawn even with nothing of its own).
func synthEmptyServiceStore(t *testing.T) *store.Store {
	t.Helper()
	st := synthStore(t)
	rev, err := st.CreateRevision("d", "", "empty-services", "manual", "full", "{}")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.UpsertNode(store.NodeRow{NodeKey: "service:service:d:empty-svc", Layer: "service", NodeType: "service",
		DomainKey: "d", Name: "empty-svc", Status: "active", Confidence: 1, Freshness: 1, TrustScore: 1, Metadata: "{}", LastSeenRevisionID: rev}); err != nil {
		t.Fatal(err)
	}
	silentID, err := st.UpsertNode(store.NodeRow{NodeKey: "service:service:d:silent-target", Layer: "service", NodeType: "service",
		DomainKey: "d", Name: "silent-target", Status: "active", Confidence: 1, Freshness: 1, TrustScore: 1, Metadata: "{}", LastSeenRevisionID: rev})
	if err != nil {
		t.Fatal(err)
	}
	prov, err := st.GetNodeByKey(mkShopProv)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.UpsertEdge(store.EdgeRow{EdgeKey: mkShopProv + "->service:service:d:silent-target:CALLS_SERVICE",
		FromNodeID: prov.NodeID, ToNodeID: silentID, EdgeType: "CALLS_SERVICE", DerivationKind: "hard", Active: true,
		Confidence: 0.9, Freshness: 1, TrustScore: 0.9, Metadata: "{}", LastSeenRevisionID: rev}); err != nil {
		t.Fatal(err)
	}
	return st
}

func TestBuildViewC2AndServices_EmptyIsolatedServiceHidden(t *testing.T) {
	for _, preset := range []string{"c2", "services"} {
		st := synthEmptyServiceStore(t)
		v, err := BuildPreset(st, preset, "d", "")
		if err != nil {
			t.Fatalf("%s: %v", preset, err)
		}
		groupKeys := map[string]bool{}
		for _, g := range v.Groups {
			groupKeys[g.Key] = true
		}
		if groupKeys["service:service:d:empty-svc"] {
			t.Errorf("%s: empty-svc owns nothing and has no arrow, must not be drawn as a group: %+v", preset, v.Groups)
		}
		// Negative cases: a service that owns something (shop-api, mail-api)
		// and a service with no members but a real arrow (silent-target)
		// both stay drawn.
		for _, want := range []string{"service:service:d:shop-api", "service:service:d:mail-api", "service:service:d:silent-target"} {
			if !groupKeys[want] {
				t.Errorf("%s: %s should stay drawn: groups=%+v", preset, want, v.Groups)
			}
		}
		found := false
		for _, h := range v.Hidden {
			if h.Type == "service" && h.Reason == "empty" && h.Count == 1 {
				found = true
			}
		}
		if !found {
			t.Errorf("%s: hidden summary missing the empty service: %+v", preset, v.Hidden)
		}
		for _, h := range v.Hidden {
			if h.Reason == "empty" && h.Count != 1 {
				t.Errorf("%s: silent-target has an arrow, must not be counted as empty: %+v", preset, h)
			}
		}
		txt := RenderText(v)
		if strings.Contains(txt, "empty-svc") {
			t.Errorf("%s: text should not print the empty service:\n%s", preset, txt)
		}
	}
}

// synthPrivateTopicStore: a broker with two topics. private-topic is
// published AND consumed only by svc-a's own provider (the battle-queue
// shape: one component, one service, both roles); shared-topic is published
// by svc-a and consumed by svc-b (the battle-results shape: two services) —
// stays framed and visible.
func synthPrivateTopicStore(t *testing.T) (st *store.Store, svcAKey, privateTopicKey, sharedTopicKey string) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "pt.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	rev, err := st.CreateRevision("d", "", "private-topic", "manual", "full", "{}")
	if err != nil {
		t.Fatal(err)
	}
	mk := func(key, layer, ntype, name, file string) int64 {
		id, err := st.UpsertNode(store.NodeRow{NodeKey: key, Layer: layer, NodeType: ntype, DomainKey: "d", Name: name,
			FilePath: file, Status: "active", Confidence: 0.9, Freshness: 1, TrustScore: 0.9, Metadata: "{}", LastSeenRevisionID: rev})
		if err != nil {
			t.Fatalf("upsert %s: %v", key, err)
		}
		return id
	}
	edge := func(from, to int64, fk, tk, typ string) {
		if _, err := st.UpsertEdge(store.EdgeRow{EdgeKey: fk + "->" + tk + ":" + typ, FromNodeID: from, ToNodeID: to, EdgeType: typ,
			DerivationKind: "hard", Active: true, Confidence: 0.9, Freshness: 1, TrustScore: 0.9, Metadata: "{}", LastSeenRevisionID: rev}); err != nil {
			t.Fatalf("edge %s: %v", typ, err)
		}
	}
	svcAKey = "service:service:d:svc-a"
	svcBKey := "service:service:d:svc-b"
	svcAProvKey := "code:provider:d:svc-a/src/queue"
	svcBProvKey := "code:provider:d:svc-b/src/consumer"
	privateTopicKey = "contract:topic:d:private-topic"
	sharedTopicKey = "contract:topic:d:shared-topic"
	brokerKey := "infra:broker:d:bus"

	mk(svcAKey, "service", "service", "svc-a", "svc-a/package.json")
	mk(svcBKey, "service", "service", "svc-b", "svc-b/package.json")
	provAID := mk(svcAProvKey, "code", "provider", "queue", "svc-a/src/queue.ts")
	provBID := mk(svcBProvKey, "code", "provider", "consumer", "svc-b/src/consumer.ts")
	mk(brokerKey, "infra", "broker", "bus", "")
	privID := mk(privateTopicKey, "contract", "topic", "private-topic", "")
	sharedID := mk(sharedTopicKey, "contract", "topic", "shared-topic", "")

	edge(provAID, privID, svcAProvKey, privateTopicKey, "PUBLISHES_TOPIC")
	edge(provAID, privID, svcAProvKey, privateTopicKey, "CONSUMES_TOPIC")
	edge(provAID, sharedID, svcAProvKey, sharedTopicKey, "PUBLISHES_TOPIC")
	edge(provBID, sharedID, svcBProvKey, sharedTopicKey, "CONSUMES_TOPIC")
	return st, svcAKey, privateTopicKey, sharedTopicKey
}

func TestBuildViewC2_PrivateTopicHiddenSharedTopicFramed(t *testing.T) {
	st, _, privateTopicKey, sharedTopicKey := synthPrivateTopicStore(t)
	v, err := BuildPreset(st, "c2", "d", "")
	if err != nil {
		t.Fatalf("c2: %v", err)
	}
	var priv, shared *VNode
	for i := range v.Nodes {
		switch v.Nodes[i].Key {
		case privateTopicKey:
			priv = &v.Nodes[i]
		case sharedTopicKey:
			shared = &v.Nodes[i]
		}
	}
	if priv == nil || priv.RenderMode != "hidden" || priv.SalienceReason != "private" {
		t.Fatalf("private topic should be hidden with reason private: %+v", priv)
	}
	if shared == nil || shared.RenderMode == "hidden" {
		t.Fatalf("shared topic (two services) must stay visible: %+v", shared)
	}
	if shared.Group == "" {
		t.Errorf("shared topic should stay framed by the broker: %+v", shared)
	}
	found := false
	for _, h := range v.Hidden {
		if h.Type == "topic" && h.Reason == "private" && h.Count == 1 {
			found = true
		}
	}
	if !found {
		t.Errorf("hidden summary missing the private topic: %+v", v.Hidden)
	}
	// Its pub/sub arrows go with it; the shared topic's arrow stays.
	sawSharedArrow := false
	for _, e := range v.Edges {
		if e.From == privateTopicKey || e.To == privateTopicKey {
			t.Errorf("private topic's arrow should not be drawn: %+v", e)
		}
		if e.From == sharedTopicKey || e.To == sharedTopicKey {
			sawSharedArrow = true
		}
	}
	if !sawSharedArrow {
		t.Errorf("shared topic's arrow should still be drawn: %+v", v.Edges)
	}
	txt := RenderText(v)
	if strings.Contains(txt, "private-topic") {
		t.Errorf("private topic must not appear in rendered text:\n%s", txt)
	}
	if !strings.Contains(txt, "shared-topic") {
		t.Errorf("shared topic should still appear in rendered text:\n%s", txt)
	}
}

func TestBuildViewC3_PrivateTopicStaysVisible(t *testing.T) {
	st, svcAKey, privateTopicKey, _ := synthPrivateTopicStore(t)
	v, err := BuildPreset(st, "c3", "d", svcAKey)
	if err != nil {
		t.Fatalf("c3: %v", err)
	}
	var priv *VNode
	for i := range v.Nodes {
		if v.Nodes[i].Key == privateTopicKey {
			priv = &v.Nodes[i]
		}
	}
	if priv == nil {
		t.Fatalf("private topic should still be reachable from its own service's c3: nodes=%+v", v.Nodes)
	}
	if priv.RenderMode == "hidden" {
		t.Errorf("private topic must stay visible on its own service's c3 (the private-topic rule is c2-only): %+v", priv)
	}
}

// TestAddPinEdge_CoveredCallsServiceNoWeight (rev3 task 3, extra): the
// coordinator's twin rule (a CALLS_SERVICE edge covered by the SAME caller's
// resolved CALLS_ENDPOINT to that service adds provenance/collapsed_from but
// no weight — rev3 task 2 fix round 1) was applied to the main edge-folding
// loop and addBoundaryEdge, but not to addPinEdge, the third place a caller's
// neighborhood gets materialized (a pinned/hand-selected node's out-of-view
// connections). Uses the caller's out-of-view neighborhood so both the
// covered CALLS_SERVICE and its two resolved CALLS_ENDPOINT twins go through
// addPinEdge, not the C2 collapse fold or the C3 boundary path.
func TestAddPinEdge_CoveredCallsServiceNoWeight(t *testing.T) {
	st, _, calleeSvcKey, callerCompKey := newTwinCallStore(t)
	v, err := BuildView(st, ViewSpec{
		Scope:  ScopeSpec{Domain: "d", Nodes: []string{callerCompKey}},
		Group:  GroupSpec{By: "none"},
		Layout: LayoutSpec{},
	})
	if err != nil {
		t.Fatalf("view: %v", err)
	}
	var covered *VEdge
	epWeights := 0
	for i := range v.Edges {
		e := &v.Edges[i]
		if e.Kind == "CALLS_SERVICE" && e.From == callerCompKey && e.To == calleeSvcKey {
			covered = e
		}
		if e.Kind == "CALLS_ENDPOINT" && e.From == callerCompKey {
			epWeights++
			if e.Weight != 1 {
				t.Errorf("CALLS_ENDPOINT pin edge weight = %d, want 1 (uncovered, unaffected by the fix): %+v", e.Weight, e)
			}
		}
	}
	if epWeights != 2 {
		t.Fatalf("expected 2 pin-materialized CALLS_ENDPOINT edges (ep1, ep2), got %d: %+v", epWeights, v.Edges)
	}
	if covered == nil {
		t.Fatalf("expected a pin-materialized caller -> callee-api CALLS_SERVICE arrow: %+v", v.Edges)
	}
	if covered.Weight != 0 {
		t.Errorf("weight = %d, want 0 (the covered CALLS_SERVICE pin edge must not add weight, only provenance/collapsed_from)", covered.Weight)
	}
	if len(covered.CollapsedFrom) != 1 {
		t.Errorf("collapsed_from = %v, want the one underlying edge (still evidence)", covered.CollapsedFrom)
	}
	if covered.Derivation == "" {
		t.Errorf("provenance should still be set on a covered edge: %+v", covered)
	}
}
