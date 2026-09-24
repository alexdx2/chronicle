package viewmodel

import (
	"testing"

	"github.com/alexdx2/chronicle-core/store"
)

// Ownership by containment: a service imported without a file path still
// owns its code when the graph says so — service/repository CONTAINS module
// CONTAINS component. Path prefixes are a heuristic; CONTAINS is evidence.
func TestOwnership_ByContainsChain(t *testing.T) {
	st, err := store.Open(t.TempDir() + "/c.db")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer st.Close()
	rev, _ := st.CreateRevision("d", "", "abc", "manual", "full", "{}")
	mk := func(key, layer, ntype, name, file string) int64 {
		id, err := st.UpsertNode(store.NodeRow{NodeKey: key, Layer: layer, NodeType: ntype, DomainKey: "d", Name: name,
			FilePath: file, Status: "active", Confidence: 1, Freshness: 1, TrustScore: 1, Metadata: "{}", LastSeenRevisionID: rev})
		if err != nil {
			t.Fatalf("upsert %s: %v", key, err)
		}
		return id
	}
	edge := func(from, to int64, fk, tk, typ string) {
		if _, err := st.UpsertEdge(store.EdgeRow{EdgeKey: fk + "->" + tk + ":" + typ, FromNodeID: from, ToNodeID: to, EdgeType: typ,
			DerivationKind: "hard", Active: true, Confidence: 1, Freshness: 1, TrustScore: 1, Metadata: "{}", LastSeenRevisionID: rev}); err != nil {
			t.Fatalf("edge %s: %v", typ, err)
		}
	}
	svcA := mk("service:service:d:orders-api", "service", "service", "orders-api", "")
	svcB := mk("service:service:d:pay-api", "service", "service", "pay-api", "")
	repoA := mk("code:repository:d:orders-api", "code", "repository", "orders-api", "")
	modA := mk("code:module:d:ordersmodule", "code", "module", "OrdersModule", "src/orders/orders.module.ts")
	ctlA := mk("code:controller:d:orderscontroller", "code", "controller", "OrdersController", "src/orders/orders.controller.ts")
	// pay-api owns its module through a direct service CONTAINS edge.
	modB := mk("code:module:d:paymodule", "code", "module", "PayModule", "src/pay/pay.module.ts")
	svcB2 := mk("code:provider:d:payservice", "code", "provider", "PayService", "src/pay/pay.service.ts")
	edge(repoA, modA, "code:repository:d:orders-api", "code:module:d:ordersmodule", "CONTAINS")
	edge(modA, ctlA, "code:module:d:ordersmodule", "code:controller:d:orderscontroller", "CONTAINS")
	edge(svcB, modB, "service:service:d:pay-api", "code:module:d:paymodule", "CONTAINS")
	edge(modB, svcB2, "code:module:d:paymodule", "code:provider:d:payservice", "CONTAINS")
	edge(ctlA, svcB2, "code:controller:d:orderscontroller", "code:provider:d:payservice", "INJECTS")
	_ = svcA

	view, err := BuildPreset(st, "c2", "d", "")
	if err != nil {
		t.Fatalf("BuildPreset(c2): %v", err)
	}
	for _, n := range view.Nodes {
		if n.Layer == "code" {
			t.Errorf("code node %s leaked to C2 as a free card (group=%q) — it should fold into its service", n.Name, n.Group)
		}
	}
	found := false
	for _, e := range view.Edges {
		if e.From == "service:service:d:orders-api" && e.To == "service:service:d:pay-api" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected a lifted orders-api → pay-api arrow; edges=%+v", view.Edges)
	}

	c3, err := BuildPreset(st, "c3", "d", "orders-api")
	if err != nil {
		t.Fatalf("BuildPreset(c3): %v", err)
	}
	ctlSeen := false
	for _, n := range c3.Nodes {
		if n.Key == "code:controller:d:orderscontroller" {
			ctlSeen = true
			if n.Group != "code:module:d:ordersmodule" {
				t.Errorf("controller group=%q want its module", n.Group)
			}
		}
	}
	if !ctlSeen {
		t.Errorf("orders-api C3 should hold OrdersController; nodes=%+v", c3.Nodes)
	}
}
