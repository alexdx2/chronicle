package graph

import (
	"strings"
	"testing"

	"github.com/alexdx2/chronicle-core/store"
)

// Traced flows and derived flows used different key formats for the same
// entry point (post__tom_arm vs post:/tom/arm), so every traced flow created
// a duplicate node next to the derived one (observed on otopoint-claude:
// use cases grew 534→600 instead of enriching in place). Traced flows must
// land on the derived node's key, and the traced name/requires win.

func saveFlowExtraction(t *testing.T, g *Graph, revID int64, domain, filePath, factsJSON string) {
	t.Helper()
	if _, _, err := g.Store().SaveExtractionWithOutcome(revID, domain, filePath, "extracted", "", factsJSON, "", "flow", "", 0); err != nil {
		t.Fatalf("save flow extraction: %v", err)
	}
}

func TestFlowUnification_TracedClaimsDerivedNode(t *testing.T) {
	g, revID := setupFlowTestGraph(t)
	domain := "uni"

	epKey := "contract:endpoint:" + domain + ":post:/tom/arm"
	upsertTestNode(t, g, revID, epKey, "contract", "endpoint", domain, "POST /tom/arm")
	ctrlKey := "code:controller:" + domain + ":tomcontroller"
	upsertTestNode(t, g, revID, ctrlKey, "code", "controller", domain, "TomController")
	svcKey := "code:provider:" + domain + ":tomservice"
	upsertTestNode(t, g, revID, svcKey, "code", "provider", domain, "TomService")
	upsertTestEdge(t, g, revID, ctrlKey, epKey, "EXPOSES_ENDPOINT", "code", "contract")
	upsertTestEdge(t, g, revID, ctrlKey, svcKey, "INJECTS", "code", "code")

	// Phase-1 state: derived flow exists.
	if err := g.DeriveFlows(domain, revID); err != nil {
		t.Fatalf("DeriveFlows: %v", err)
	}
	if flows := listFlowNodes(t, g, domain); len(flows) != 1 {
		t.Fatalf("precondition: want 1 derived flow, got %d", len(flows))
	}

	// Phase-2: traced flow for the same trigger.
	saveFlowExtraction(t, g, revID, domain, "tom-api/src/tom/tom.controller.ts",
		`[{"kind":"flow","flow_name":"Arm Tom with weapon","trigger":"POST /tom/arm","method":"arm","requires":["TomService"],"steps":["Validate weapon","Arm the cat"]}]`)
	if _, err := g.ResolveExtractions(domain, revID); err != nil {
		t.Fatalf("ResolveExtractions: %v", err)
	}

	flows := listFlowNodes(t, g, domain)
	if len(flows) != 1 {
		keys := []string{}
		for _, f := range flows {
			keys = append(keys, f.NodeKey)
		}
		t.Fatalf("want 1 unified flow node, got %d: %v", len(flows), keys)
	}
	if flows[0].Name != "Arm Tom with weapon" {
		t.Errorf("flow name = %q; traced name must win over derived", flows[0].Name)
	}
	if flows[0].NodeKey != "flow:use_case:"+domain+":post:/tom/arm" {
		t.Errorf("flow key = %q; want the derived-format key", flows[0].NodeKey)
	}

	// Traced REQUIRES present on the unified node.
	active := true
	edges, _ := g.Store().ListEdges(store.EdgeFilter{EdgeType: "REQUIRES", Active: &active})
	found := false
	for _, e := range edges {
		if e.FromNodeKey == flows[0].NodeKey && e.ToNodeKey == svcKey {
			found = true
		}
	}
	if !found {
		t.Error("traced REQUIRES edge missing on unified flow node")
	}
}

func TestFlowUnification_DeriveDoesNotClobberTraced(t *testing.T) {
	g, revID := setupFlowTestGraph(t)
	domain := "uni2"

	epKey := "contract:endpoint:" + domain + ":get:/status"
	upsertTestNode(t, g, revID, epKey, "contract", "endpoint", domain, "GET /status")
	ctrlKey := "code:controller:" + domain + ":statuscontroller"
	upsertTestNode(t, g, revID, ctrlKey, "code", "controller", domain, "StatusController")
	upsertTestEdge(t, g, revID, ctrlKey, epKey, "EXPOSES_ENDPOINT", "code", "contract")

	// Traced flow first.
	saveFlowExtraction(t, g, revID, domain, "src/status.controller.ts",
		`[{"kind":"flow","flow_name":"Check Tom status","trigger":"GET /status","method":"getStatus"}]`)
	if _, err := g.ResolveExtractions(domain, revID); err != nil {
		t.Fatalf("ResolveExtractions: %v", err)
	}

	// DeriveFlows again (runs after every resolve) must not rename or duplicate.
	if err := g.DeriveFlows(domain, revID); err != nil {
		t.Fatalf("DeriveFlows: %v", err)
	}

	flows := listFlowNodes(t, g, domain)
	if len(flows) != 1 {
		t.Fatalf("want 1 flow node after re-derive, got %d", len(flows))
	}
	if flows[0].Name != "Check Tom status" {
		t.Errorf("derive clobbered traced name: %q", flows[0].Name)
	}
	if !strings.HasPrefix(flows[0].NodeKey, "flow:use_case:"+domain+":get:/status") {
		t.Errorf("unexpected flow key: %q", flows[0].NodeKey)
	}
}
