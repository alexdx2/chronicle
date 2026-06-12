package graph

import (
	"testing"

	"github.com/alexdx2/chronicle-core/store"
	"github.com/alexdx2/chronicle-core/validate"
)

// seedSearchNodes seeds a small graph with distinct name shapes for ranking tests.
func seedSearchNodes(t *testing.T, g *Graph) int64 {
	t.Helper()
	revID := makeRevision(t, g)
	nodes := []validate.NodeInput{
		{NodeKey: "code:controller:orders:order-controller", Layer: "code", NodeType: "controller", DomainKey: "orders", Name: "OrderController", FilePath: "src/order/order.controller.ts"},
		{NodeKey: "code:provider:orders:order-service", Layer: "code", NodeType: "provider", DomainKey: "orders", Name: "OrderService", FilePath: "src/order/order.service.ts"},
		{NodeKey: "code:provider:orders:order-line-helper", Layer: "code", NodeType: "provider", DomainKey: "orders", Name: "OrderLineHelper", FilePath: "src/order/order-line.helper.ts"},
		{NodeKey: "service:service:billing:billing-api", Layer: "service", NodeType: "service", DomainKey: "billing", Name: "billing-api", FilePath: ""},
		{NodeKey: "data:model:orders:order", Layer: "data", NodeType: "model", DomainKey: "orders", Name: "Order", FilePath: "prisma/schema.prisma"},
		{NodeKey: "code:module:billing:payments-module", Layer: "code", NodeType: "module", DomainKey: "billing", Name: "PaymentsModule", FilePath: "src/payments/reorder-utils.ts"},
	}
	for _, n := range nodes {
		if _, err := g.UpsertNode(n, revID); err != nil {
			t.Fatalf("UpsertNode %s: %v", n.Name, err)
		}
	}
	return revID
}

func TestNodeSearchExactNameWins(t *testing.T) {
	g := setupGraphDefaults(t)
	seedSearchNodes(t, g)

	res, err := g.NodeSearch("OrderController", store.NodeFilter{}, 10)
	if err != nil {
		t.Fatalf("NodeSearch: %v", err)
	}
	if len(res) == 0 {
		t.Fatal("expected results")
	}
	if res[0].NodeKey != "code:controller:orders:order-controller" {
		t.Errorf("expected exact name first, got %q", res[0].NodeKey)
	}
	if res[0].MatchKind != "exact_name" {
		t.Errorf("expected match_kind exact_name, got %q", res[0].MatchKind)
	}
}

func TestNodeSearchAliasHit(t *testing.T) {
	g := setupGraphDefaults(t)
	seedSearchNodes(t, g)

	nodeID, err := g.Store().GetNodeIDByKey("service:service:billing:billing-api")
	if err != nil {
		t.Fatalf("GetNodeIDByKey: %v", err)
	}
	if _, err := g.Store().AddAlias(store.AliasRow{NodeID: nodeID, Alias: "BillingGateway", AliasKind: "class_name", Confidence: 0.9}); err != nil {
		t.Fatalf("AddAlias: %v", err)
	}

	res, err := g.NodeSearch("BillingGateway", store.NodeFilter{}, 10)
	if err != nil {
		t.Fatalf("NodeSearch: %v", err)
	}
	if len(res) == 0 || res[0].NodeKey != "service:service:billing:billing-api" {
		t.Fatalf("expected alias hit on billing-api, got %+v", res)
	}
	if res[0].MatchKind != "exact_alias" {
		t.Errorf("expected match_kind exact_alias, got %q", res[0].MatchKind)
	}
}

func TestNodeSearchGlossaryTerm(t *testing.T) {
	g := setupGraphDefaults(t)
	seedSearchNodes(t, g)

	if _, err := g.Store().UpsertTerm(store.DomainTerm{
		DomainKey: "orders",
		Term:      "Order",
		Aliases:   []string{"purchase"},
	}); err != nil {
		t.Fatalf("UpsertTerm: %v", err)
	}

	// "purchase" matches no node name directly — only via the glossary term "Order".
	res, err := g.NodeSearch("purchase", store.NodeFilter{}, 10)
	if err != nil {
		t.Fatalf("NodeSearch: %v", err)
	}
	if len(res) == 0 || res[0].NodeKey != "data:model:orders:order" {
		t.Fatalf("expected glossary hit on data:model:orders:order, got %+v", res)
	}
	if res[0].MatchKind != "glossary" {
		t.Errorf("expected match_kind glossary, got %q", res[0].MatchKind)
	}
}

func TestNodeSearchPrefixBeatsSubstring(t *testing.T) {
	g := setupGraphDefaults(t)
	seedSearchNodes(t, g)

	// "order" is a prefix of OrderService/OrderController/OrderLineHelper and exact for model Order;
	// it is only a substring inside "reorder-utils.ts" (path of PaymentsModule).
	res, err := g.NodeSearch("order", store.NodeFilter{}, 10)
	if err != nil {
		t.Fatalf("NodeSearch: %v", err)
	}
	if len(res) < 2 {
		t.Fatalf("expected multiple results, got %+v", res)
	}
	if res[0].NodeKey != "data:model:orders:order" {
		t.Errorf("expected exact match (model Order) first, got %q", res[0].NodeKey)
	}
	// PaymentsModule (path-only substring) must rank last among matches.
	last := res[len(res)-1]
	if last.NodeKey != "code:module:billing:payments-module" {
		t.Errorf("expected path-only match last, got %q", last.NodeKey)
	}
}

func TestNodeSearchPathMatch(t *testing.T) {
	g := setupGraphDefaults(t)
	seedSearchNodes(t, g)

	res, err := g.NodeSearch("reorder-utils", store.NodeFilter{}, 10)
	if err != nil {
		t.Fatalf("NodeSearch: %v", err)
	}
	if len(res) != 1 || res[0].NodeKey != "code:module:billing:payments-module" {
		t.Fatalf("expected single path match, got %+v", res)
	}
	if res[0].MatchKind != "path" {
		t.Errorf("expected match_kind path, got %q", res[0].MatchKind)
	}
}

func TestNodeSearchMultiTermAND(t *testing.T) {
	g := setupGraphDefaults(t)
	seedSearchNodes(t, g)

	// "order controller" → only nodes matching BOTH terms.
	res, err := g.NodeSearch("order controller", store.NodeFilter{}, 10)
	if err != nil {
		t.Fatalf("NodeSearch: %v", err)
	}
	if len(res) != 1 || res[0].NodeKey != "code:controller:orders:order-controller" {
		t.Fatalf("expected only order-controller, got %+v", res)
	}
}

func TestNodeSearchDeterministicOrder(t *testing.T) {
	g := setupGraphDefaults(t)
	revID := makeRevision(t, g)
	// Two nodes with identical names → equal score; order must be stable by node_key asc.
	for _, key := range []string{"code:provider:zz:dup-svc", "code:provider:aa:dup-svc"} {
		if _, err := g.UpsertNode(validate.NodeInput{
			NodeKey: key, Layer: "code", NodeType: "provider",
			DomainKey: key[14:16], Name: "DupSvc",
		}, revID); err != nil {
			t.Fatalf("UpsertNode: %v", err)
		}
	}
	for i := 0; i < 3; i++ {
		res, err := g.NodeSearch("DupSvc", store.NodeFilter{}, 10)
		if err != nil {
			t.Fatalf("NodeSearch: %v", err)
		}
		if len(res) != 2 || res[0].NodeKey != "code:provider:aa:dup-svc" {
			t.Fatalf("expected deterministic key-asc order, got %+v", res)
		}
	}
}

func TestNodeSearchLimit(t *testing.T) {
	g := setupGraphDefaults(t)
	seedSearchNodes(t, g)

	res, err := g.NodeSearch("order", store.NodeFilter{}, 2)
	if err != nil {
		t.Fatalf("NodeSearch: %v", err)
	}
	if len(res) != 2 {
		t.Fatalf("expected limit 2, got %d", len(res))
	}
}

func TestNodeSearchFilters(t *testing.T) {
	g := setupGraphDefaults(t)
	seedSearchNodes(t, g)

	res, err := g.NodeSearch("order", store.NodeFilter{Layer: "data"}, 10)
	if err != nil {
		t.Fatalf("NodeSearch: %v", err)
	}
	if len(res) != 1 || res[0].NodeKey != "data:model:orders:order" {
		t.Fatalf("expected only data-layer result, got %+v", res)
	}
}

func TestNodeSearchEmptyQuery(t *testing.T) {
	g := setupGraphDefaults(t)
	if _, err := g.NodeSearch("   ", store.NodeFilter{}, 10); err == nil {
		t.Fatal("expected error for empty query")
	}
}
