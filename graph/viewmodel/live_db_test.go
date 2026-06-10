package viewmodel

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/alexdx2/chronicle-core/store"
)

// liveDBPath is the workspace chronicle DB scanned from the tom-and-jerry
// fixture. The test copies it to a temp dir and never touches the original.
const liveDBPath = "/home/alex/personal/chronicle/.depbot/chronicle.db"

func openLiveDBCopy(t *testing.T) *store.Store {
	t.Helper()

	if _, err := os.Stat(liveDBPath); err != nil {
		t.Skipf("live DB not available: %v", err)
	}

	dir := t.TempDir()
	dst := filepath.Join(dir, "chronicle.db")

	// Copy db + WAL sidecar files if present, to get a consistent snapshot.
	for _, suffix := range []string{"", "-wal", "-shm"} {
		data, err := os.ReadFile(liveDBPath + suffix)
		if err != nil {
			if suffix == "" {
				t.Fatalf("read live db: %v", err)
			}
			continue
		}
		if err := os.WriteFile(dst+suffix, data, 0644); err != nil {
			t.Fatalf("copy db%s: %v", suffix, err)
		}
	}

	st, err := store.Open(dst)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

func TestBuildC1LiveTomAndJerry(t *testing.T) {
	st := openLiveDBCopy(t)

	c1, err := BuildC1(st, "tom-and-jerry")
	if err != nil {
		t.Fatalf("BuildC1: %v", err)
	}

	// Models: 7 — battleresult was reclassified model→enum in the graph
	// oracle (commit aaad956), so the data layer is 7 models + 4 enums.
	want := C1Stats{Services: 5, Endpoints: 19, Models: 7, Topics: 2, Flows: 19}
	if c1.Stats != want {
		t.Errorf("stats=%+v want %+v", c1.Stats, want)
	}
}

// TestViewC1LiveTomAndJerry pins the C1 context view (view algebra) on the
// live demo DB: ONE colored system box carrying service/endpoint stats,
// external systems and shared infra (kafka, redis) as free nodes, and NO
// internal topics leaking outside the box.
func TestViewC1LiveTomAndJerry(t *testing.T) {
	st := openLiveDBCopy(t)

	view, err := BuildPreset(st, "c1", "tom-and-jerry", "")
	if err != nil {
		t.Fatalf("BuildPreset(c1): %v", err)
	}

	// --- exactly one group: the domain system box, with stats ---
	if len(view.Groups) != 1 {
		t.Fatalf("groups=%d want 1 (the system box): %+v", len(view.Groups), view.Groups)
	}
	g := view.Groups[0]
	if g.Key != "tom-and-jerry" || g.Kind != "domain" {
		t.Errorf("system box key=%q kind=%q want tom-and-jerry/domain", g.Key, g.Kind)
	}
	if g.Stats["services"] != 5 {
		t.Errorf("stats.services=%d want 5", g.Stats["services"])
	}
	if g.Stats["endpoints"] != 19 {
		t.Errorf("stats.endpoints=%d want 19", g.Stats["endpoints"])
	}

	// --- free nodes: infra + external systems only; never internal topics ---
	freeLayer := map[string]string{} // name → layer
	freeKey := map[string]string{}   // name → node key
	for _, n := range view.Nodes {
		if n.Type == "topic" {
			t.Errorf("internal topic %q leaked outside the system box", n.Name)
		}
		if n.Layer != "infra" && n.Type != "external_system" {
			t.Errorf("unexpected free node at C1: %s (%s/%s)", n.Name, n.Layer, n.Type)
		}
		freeLayer[n.Name] = n.Layer
		freeKey[n.Name] = n.Key
	}
	if freeLayer["kafka"] != "infra" {
		t.Errorf("kafka missing from C1 or wrong layer: %q", freeLayer["kafka"])
	}
	if freeLayer["redis"] != "infra" {
		t.Errorf("redis missing from C1 or wrong layer: %q", freeLayer["redis"])
	}
	if _, ok := freeLayer["notifications"]; !ok {
		t.Errorf("external system notifications missing from C1; nodes=%v", freeLayer)
	}

	// --- the system box connects to shared infra (mirrors /api/graph's
	// manifest USES_INFRA synthesis): box → kafka ("events"), box → redis
	// ("cache") ---
	infraEdge := map[string]VEdge{} // To key → edge
	for _, e := range view.Edges {
		if e.Kind == "uses_infra" {
			if e.From != "tom-and-jerry" {
				t.Errorf("uses_infra edge from %q, want the system box", e.From)
			}
			infraEdge[e.To] = e
		}
	}
	if e, ok := infraEdge[freeKey["kafka"]]; !ok {
		t.Errorf("missing uses_infra edge system box → kafka; edges=%+v", view.Edges)
	} else if e.Label != "events" {
		t.Errorf("kafka uses_infra label=%q want events", e.Label)
	}
	if e, ok := infraEdge[freeKey["redis"]]; !ok {
		t.Errorf("missing uses_infra edge system box → redis; edges=%+v", view.Edges)
	} else if e.Label != "cache" {
		t.Errorf("redis uses_infra label=%q want cache", e.Label)
	}

	// --- the system box connects to the external system ---
	extEdges := 0
	for _, e := range view.Edges {
		if e.From == "tom-and-jerry" && e.Kind == "CALLS_SERVICE" {
			extEdges++
		}
	}
	if extEdges == 0 {
		t.Errorf("no system-box → external edges at C1; edges=%+v", view.Edges)
	}
}

// TestViewFlowsListOnlyFlows: the Flows preset is a LIST view — flows only
// (no endpoints, no code targets, no TRIGGERS_FLOW/REQUIRES arrows). Each
// flow VNode carries its trigger endpoint name as Detail ("GET /api/score");
// the composition lives in the flow-scoped drill-down (TestViewFlowScoped…).
func TestViewFlowsListOnlyFlows(t *testing.T) {
	st := openLiveDBCopy(t)

	view, err := BuildPreset(st, "flows", "tom-and-jerry", "")
	if err != nil {
		t.Fatalf("BuildPreset(flows): %v", err)
	}

	flows, withDetail := 0, 0
	for _, n := range view.Nodes {
		if n.Layer != "flow" {
			t.Errorf("non-flow node %s (%s/%s) in flows list view", n.Key, n.Layer, n.Type)
			continue
		}
		flows++
		if n.Detail != "" {
			withDetail++
		}
	}
	if flows < 10 {
		t.Errorf("flow nodes=%d want ≥10", flows)
	}
	if withDetail < 10 {
		t.Errorf("flows with trigger Detail=%d want ≥10; nodes=%+v", withDetail, view.Nodes)
	}
	for _, e := range view.Edges {
		if e.Kind == "TRIGGERS_FLOW" || e.Kind == "REQUIRES" {
			t.Errorf("flows list view must not draw %s arrows: %+v", e.Kind, e)
		}
	}
}

// TestViewFlowScopedComposition: dblclick on a flow drills into the
// flow-scoped view — flow card + trigger endpoint + REQUIRES targets with
// TRIGGERS_FLOW/REQUIRES edges (spec scope.flow, group none).
func TestViewFlowScopedComposition(t *testing.T) {
	st := openLiveDBCopy(t)

	const flowKey = "flow:use_case:tom-and-jerry:get:/api/score"
	view, err := BuildView(st, ViewSpec{
		Scope:  ScopeSpec{Domain: "tom-and-jerry", Flow: flowKey},
		Group:  GroupSpec{By: "none"},
		Layout: LayoutSpec{Preset: "flow"},
	})
	if err != nil {
		t.Fatalf("BuildView(flow scope): %v", err)
	}

	var hasFlow, hasTrigger bool
	for _, n := range view.Nodes {
		if n.Key == flowKey {
			hasFlow = true
		}
		if n.Layer == "contract" && n.Type == "endpoint" {
			hasTrigger = true
		}
	}
	if !hasFlow {
		t.Errorf("flow node %s missing from its scoped view; nodes=%+v", flowKey, view.Nodes)
	}
	if !hasTrigger {
		t.Errorf("trigger endpoint missing from flow-scoped view; nodes=%+v", view.Nodes)
	}
	triggers, requires := 0, 0
	for _, e := range view.Edges {
		switch e.Kind {
		case "TRIGGERS_FLOW":
			triggers++
		case "REQUIRES":
			requires++
		}
	}
	if triggers < 1 {
		t.Errorf("TRIGGERS_FLOW edges=%d want ≥1; edges=%+v", triggers, view.Edges)
	}
	if requires < 2 {
		t.Errorf("REQUIRES edges=%d want ≥2; edges=%+v", requires, view.Edges)
	}
	// Breadcrumb title: the flow NAME, not the raw key.
	if view.Title == "" || view.Title == flowKey {
		t.Errorf("flow-scoped title=%q want the flow's display name", view.Title)
	}
}

// TestViewPinServiceSpotlightC1: pinning a service at C1 must not render it
// as an orphan card absorbed away from its own code — the service is carved
// OUT of the domain box as its own VGroup (spotlight): its owned nodes move
// into the carved group (the box stats shrink) and edges lift between the
// carved group and the box.
func TestViewPinServiceSpotlightC1(t *testing.T) {
	st := openLiveDBCopy(t)

	const domain = "tom-and-jerry"
	const arenaKey = "service:service:tom-and-jerry:arena-api"

	base, err := BuildView(st, ViewSpec{
		Scope: ScopeSpec{Domain: domain}, Group: GroupSpec{By: "domain"},
		Collapse: true, Layout: LayoutSpec{Preset: "c1"},
	})
	if err != nil {
		t.Fatalf("BuildView(c1): %v", err)
	}
	view, err := BuildView(st, ViewSpec{
		Scope: ScopeSpec{Domain: domain}, Group: GroupSpec{By: "domain"},
		Collapse: true, Layout: LayoutSpec{Preset: "c1"},
		Pin: []string{arenaKey},
	})
	if err != nil {
		t.Fatalf("BuildView(c1+pin): %v", err)
	}

	var arenaGroup, domainGroup *VGroup
	for i := range view.Groups {
		switch view.Groups[i].Key {
		case arenaKey:
			arenaGroup = &view.Groups[i]
		case domain:
			domainGroup = &view.Groups[i]
		}
	}
	if arenaGroup == nil {
		t.Fatalf("carved arena-api VGroup missing; groups=%+v", view.Groups)
	}
	if arenaGroup.Kind != "service" {
		t.Errorf("carved group kind=%q want service", arenaGroup.Kind)
	}
	if arenaGroup.Stats["nodes"] == 0 {
		t.Errorf("carved group has no member stats: %+v", arenaGroup.Stats)
	}
	if domainGroup == nil {
		t.Fatalf("domain box missing; groups=%+v", view.Groups)
	}
	// The service must NOT also render as a free node card.
	for _, n := range view.Nodes {
		if n.Key == arenaKey {
			t.Errorf("pinned service rendered as a free card despite the carve-out")
		}
	}
	// Domain box stats shrink: arena's nodes + the service itself left the box.
	baseStats := base.Groups[0].Stats
	if got, want := domainGroup.Stats["services"], baseStats["services"]-1; got != want {
		t.Errorf("box services=%d want %d", got, want)
	}
	if got := domainGroup.Stats["nodes"]; got >= baseStats["nodes"] {
		t.Errorf("box nodes=%d did not shrink from %d", got, baseStats["nodes"])
	}
	if got, want := domainGroup.Stats["nodes"]+arenaGroup.Stats["nodes"]+1, baseStats["nodes"]; got != want {
		t.Errorf("box+carved+svc nodes=%d want %d (conservation)", got, want)
	}
	// Edges lift between the carved group and the domain box.
	betweens := 0
	for _, e := range view.Edges {
		if (e.From == arenaKey && e.To == domain) || (e.From == domain && e.To == arenaKey) {
			betweens++
		}
	}
	if betweens == 0 {
		t.Errorf("no lifted edges between carved arena-api group and the domain box; edges=%+v", view.Edges)
	}
}

// TestViewC3AllServices: the c3 preset WITHOUT a target renders components of
// every service, grouped by service (one frame per service), with node-level
// edges crossing the frames directly (no per-service boundary pass).
func TestViewC3AllServices(t *testing.T) {
	st := openLiveDBCopy(t)

	view, err := BuildPreset(st, "c3", "tom-and-jerry", "")
	if err != nil {
		t.Fatalf("BuildPreset(c3, no target): %v", err)
	}

	svcGroups := 0
	for _, g := range view.Groups {
		if g.Kind == "service" {
			svcGroups++
		}
	}
	if svcGroups != 5 {
		t.Errorf("service groups=%d want 5: %+v", svcGroups, view.Groups)
	}
	// Component-level nodes must be visible (collapse=false), with the
	// usual pills riding along for at least some components.
	members, pills := 0, 0
	for _, n := range view.Nodes {
		if n.Group != "" {
			members++
		}
		if len(n.Endpoints) > 0 || len(n.UsesModels) > 0 {
			pills++
		}
	}
	if members < 20 {
		t.Errorf("grouped component nodes=%d want ≥20; nodes=%+v", members, view.Nodes)
	}
	if pills == 0 {
		t.Error("no endpoint/model pills on any component node")
	}
	// No boundary materialization in domain-wide mode.
	for _, n := range view.Nodes {
		if n.Boundary {
			t.Errorf("boundary node %s in C3-all (must render directly)", n.Key)
		}
	}
	if len(view.Edges) == 0 {
		t.Error("C3-all rendered no edges")
	}
}

// TestViewBlankCanvas: scope.nodes PRESENT but empty is a deliberate blank
// canvas — empty view, no whole-domain fallback, no error.
func TestViewBlankCanvas(t *testing.T) {
	st := openLiveDBCopy(t)

	view, err := BuildView(st, ViewSpec{
		Scope:    ScopeSpec{Domain: "tom-and-jerry", Nodes: []string{}},
		Group:    GroupSpec{By: "service"},
		Collapse: false,
		Layout:   LayoutSpec{Preset: "custom"},
	})
	if err != nil {
		t.Fatalf("BuildView(blank canvas): %v", err)
	}
	if len(view.Nodes) != 0 || len(view.Groups) != 0 || len(view.Edges) != 0 {
		t.Errorf("blank canvas not empty: %d nodes, %d groups, %d edges",
			len(view.Nodes), len(view.Groups), len(view.Edges))
	}
}

// TestViewC3BoundaryMaterializedLive: a C3 view must materialize its 1-hop
// boundary targets as Boundary VNodes with connecting edges — otherwise
// components whose only edges cross the service boundary render as orphans
// (notification.service in spectators-api only has CALLS_SERVICE → the
// external notifications system).
func TestViewC3BoundaryMaterializedLive(t *testing.T) {
	st := openLiveDBCopy(t)

	view, err := BuildPreset(st, "c3", "tom-and-jerry", "spectators-api")
	if err != nil {
		t.Fatalf("BuildPreset(c3, spectators-api): %v", err)
	}

	var notif *VNode
	for i := range view.Nodes {
		if view.Nodes[i].Name == "notifications" {
			notif = &view.Nodes[i]
		}
		// In-view components must never carry the Boundary mark.
		if view.Nodes[i].Group != "" && view.Nodes[i].Boundary {
			t.Errorf("in-view node %s marked boundary", view.Nodes[i].Key)
		}
	}
	if notif == nil {
		t.Fatalf("notifications external node not materialized; nodes=%+v", view.Nodes)
	}
	if !notif.Boundary {
		t.Errorf("notifications node not marked boundary:true")
	}
	if notif.Type != "external_system" {
		t.Errorf("notifications type=%q want external_system", notif.Type)
	}

	const nsKey = "code:provider:tom-and-jerry:fixtures/tom-and-jerry/spectators-api/src/spectators/notification.service"
	found := false
	for _, e := range view.Edges {
		if e.From == nsKey && e.To == notif.Key && e.Kind == "CALLS_SERVICE" {
			found = true
		}
	}
	if !found {
		t.Errorf("missing notification.service → notifications CALLS_SERVICE edge; edges=%+v", view.Edges)
	}
}

func TestBuildC2LiveTomAndJerry(t *testing.T) {
	st := openLiveDBCopy(t)

	c2, err := BuildC2(st, "tom-and-jerry")
	if err != nil {
		t.Fatalf("BuildC2: %v", err)
	}

	if len(c2.Services) != 5 {
		names := make([]string, 0, len(c2.Services))
		for _, s := range c2.Services {
			names = append(names, s.Name)
		}
		t.Errorf("services=%d want 5 (%v)", len(c2.Services), names)
	}
}

func TestBuildSelectionLiveProviders(t *testing.T) {
	st := openLiveDBCopy(t)

	// Two providers connected by an INJECTS edge in the tom-and-jerry graph.
	gatewayKey := "code:provider:tom-and-jerry:fixtures/tom-and-jerry/arena-api/src/arena/battle.gateway"
	serviceKey := "code:provider:tom-and-jerry:fixtures/tom-and-jerry/arena-api/src/arena/arena.service"

	sel, err := BuildSelection(st, "tom-and-jerry", []string{gatewayKey, serviceKey}, "Arena pair")
	if err != nil {
		t.Fatalf("BuildSelection: %v", err)
	}

	if sel.Level != "custom" {
		t.Errorf("level=%q want custom", sel.Level)
	}
	if sel.Title != "Arena pair" {
		t.Errorf("title=%q", sel.Title)
	}
	if len(sel.Missing) != 0 {
		t.Errorf("missing=%v want none", sel.Missing)
	}

	if len(sel.Components) != 2 {
		t.Fatalf("components=%d want 2: %+v", len(sel.Components), sel.Components)
	}
	gotKeys := map[string]bool{}
	for _, c := range sel.Components {
		gotKeys[c.Key] = true
		if c.Type != "provider" {
			t.Errorf("component %s type=%q want provider", c.Key, c.Type)
		}
	}
	if !gotKeys[gatewayKey] || !gotKeys[serviceKey] {
		t.Errorf("components missing selected keys: %v", gotKeys)
	}

	foundInjects := false
	for _, e := range sel.InternalEdges {
		if e.From == gatewayKey && e.To == serviceKey && e.Kind == "injects" {
			foundInjects = true
		}
	}
	if !foundInjects {
		t.Errorf("INJECTS internal edge missing; got %+v", sel.InternalEdges)
	}
}
