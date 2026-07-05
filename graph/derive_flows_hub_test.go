package graph

import (
	"fmt"
	"strings"
	"testing"

	"github.com/alexdx2/chronicle-core/store"
)

// Field finding (otopoint, 2026-07-05): on a monorepo with god-hubs
// (prisma.service in-degree 447, logger 305), DeriveFlows' depth-6 closure
// emitted ~45 REQUIRES per endpoint — 17,820 inferred edges, 87% of the whole
// graph, drowning impact and path queries. Derived REQUIRES must exclude hub
// providers and cap per-flow fan-out; real traced flows are unaffected.

func requiresTargets(t *testing.T, g *Graph, flowKeyPrefix string) map[string][]string {
	t.Helper()
	active := true
	edges, err := g.Store().ListEdges(store.EdgeFilter{EdgeType: "REQUIRES", Active: &active})
	if err != nil {
		t.Fatalf("ListEdges: %v", err)
	}
	out := map[string][]string{}
	for _, e := range edges {
		if strings.HasPrefix(e.FromNodeKey, flowKeyPrefix) {
			out[e.FromNodeKey] = append(out[e.FromNodeKey], e.ToNodeKey)
		}
	}
	return out
}

func TestDeriveFlows_ExcludesHubProviders(t *testing.T) {
	g, revID := setupFlowTestGraph(t)
	domain := "hubtest"

	epKey := "contract:endpoint:" + domain + ":post:/orders"
	upsertTestNode(t, g, revID, epKey, "contract", "endpoint", domain, "POST /orders")
	ctrlKey := "code:controller:" + domain + ":orderscontroller"
	upsertTestNode(t, g, revID, ctrlKey, "code", "controller", domain, "OrdersController")
	upsertTestEdge(t, g, revID, ctrlKey, epKey, "EXPOSES_ENDPOINT", "code", "contract")

	// Normal provider — injected by the controller only.
	svcKey := "code:provider:" + domain + ":ordersservice"
	upsertTestNode(t, g, revID, svcKey, "code", "provider", domain, "OrdersService")
	upsertTestEdge(t, g, revID, ctrlKey, svcKey, "INJECTS", "code", "code")

	// Hub provider — injected by the controller AND >= hubInDegreeThreshold others.
	hubKey := "code:provider:" + domain + ":prismaservice"
	upsertTestNode(t, g, revID, hubKey, "code", "provider", domain, "PrismaService")
	upsertTestEdge(t, g, revID, ctrlKey, hubKey, "INJECTS", "code", "code")
	for i := 0; i < hubInDegreeThreshold; i++ {
		k := fmt.Sprintf("code:provider:%s:consumer%d", domain, i)
		upsertTestNode(t, g, revID, k, "code", "provider", domain, fmt.Sprintf("Consumer%d", i))
		upsertTestEdge(t, g, revID, k, hubKey, "INJECTS", "code", "code")
	}

	if err := g.DeriveFlows(domain, revID); err != nil {
		t.Fatalf("DeriveFlows: %v", err)
	}

	targets := requiresTargets(t, g, "flow:use_case:"+domain+":")
	if len(targets) != 1 {
		t.Fatalf("want REQUIRES from 1 flow, got %d", len(targets))
	}
	for _, tos := range targets {
		joined := strings.Join(tos, ",")
		if !strings.Contains(joined, svcKey) {
			t.Errorf("normal provider missing from REQUIRES: %v", tos)
		}
		if strings.Contains(joined, hubKey) {
			t.Errorf("hub provider (in-degree %d) must be excluded from derived REQUIRES: %v", hubInDegreeThreshold+1, tos)
		}
	}
}

func TestDeriveFlows_CapsRequiresPerFlow(t *testing.T) {
	g, revID := setupFlowTestGraph(t)
	domain := "captest"

	epKey := "contract:endpoint:" + domain + ":get:/big"
	upsertTestNode(t, g, revID, epKey, "contract", "endpoint", domain, "GET /big")
	ctrlKey := "code:controller:" + domain + ":bigcontroller"
	upsertTestNode(t, g, revID, ctrlKey, "code", "controller", domain, "BigController")
	upsertTestEdge(t, g, revID, ctrlKey, epKey, "EXPOSES_ENDPOINT", "code", "contract")

	for i := 0; i < maxFlowRequires+10; i++ {
		k := fmt.Sprintf("code:provider:%s:svc%02d", domain, i)
		upsertTestNode(t, g, revID, k, "code", "provider", domain, fmt.Sprintf("Svc%02d", i))
		upsertTestEdge(t, g, revID, ctrlKey, k, "INJECTS", "code", "code")
	}

	if err := g.DeriveFlows(domain, revID); err != nil {
		t.Fatalf("DeriveFlows: %v", err)
	}

	targets := requiresTargets(t, g, "flow:use_case:"+domain+":")
	for flow, tos := range targets {
		if len(tos) > maxFlowRequires {
			t.Errorf("%s has %d derived REQUIRES; cap is %d", flow, len(tos), maxFlowRequires)
		}
	}
}

// "GET /:19000" — port numbers regex-extracted as endpoints must not spawn
// derived flows (each one drags a full closure behind it).
func TestDeriveFlows_SkipsNonAlphaEndpointPaths(t *testing.T) {
	g, revID := setupFlowTestGraph(t)
	domain := "junktest"

	epKey := "contract:endpoint:" + domain + ":get:/:19000"
	upsertTestNode(t, g, revID, epKey, "contract", "endpoint", domain, "GET /:19000")
	ctrlKey := "code:provider:" + domain + ":flyconfig"
	upsertTestNode(t, g, revID, ctrlKey, "code", "provider", domain, "flyconfig")
	upsertTestEdge(t, g, revID, ctrlKey, epKey, "EXPOSES_ENDPOINT", "code", "contract")

	if err := g.DeriveFlows(domain, revID); err != nil {
		t.Fatalf("DeriveFlows: %v", err)
	}

	if flows := listFlowNodes(t, g, domain); len(flows) != 0 {
		t.Errorf("junk endpoint spawned %d derived flows: %v", len(flows), flows[0].NodeKey)
	}
}
