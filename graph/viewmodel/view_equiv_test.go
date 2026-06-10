package viewmodel

// Regression tests: the view-algebra engine (BuildView/BuildPreset) must be
// SEMANTICALLY equivalent to the legacy per-level builders (BuildC2/BuildC3)
// on the live demo DB. The DB copy helper (openLiveDBCopy) lives in
// live_db_test.go and skips when the DB is absent.
//
// Known, intentional representation differences (normalized here, each
// explained at the assertion site):
//
//  1. Edge kinds. Legacy C2 emits renderer-friendly kinds ("http"/"async" +
//     label); the algebra keeps raw graph edge types (CALLS_SERVICE,
//     PUBLISHES_TOPIC, ...). Tests map raw types to the legacy classes.
//  2. CONSUMES_TOPIC direction. Legacy C2 renders consumption flipped
//     (topic → consumer, label "consumes"); the algebra keeps the graph
//     direction (consumer → topic). Tests flip legacy consumes edges back.
//  3. CALLS_ENDPOINT lifting. Legacy C2 lifts only CALLS_SERVICE; the
//     algebra also lifts CALLS_ENDPOINT (endpoint absorbed into its exposing
//     controller → its service group). In the demo graph every
//     CALLS_ENDPOINT parallels a CALLS_SERVICE between the same service
//     pair, so we assert (a) CALLS_SERVICE-only set == legacy http set
//     exactly, and (b) CALLS_ENDPOINT adds no service pair beyond it.
//  4. C3 ungrouped fallback. group.by=module falls back to the owning
//     service for components without a CONTAINS parent (battle.queue in
//     arena-api). Legacy C3 renders those inside the service boundary but in
//     no module. Tests allow exactly one extra group — the scope service —
//     and require its members to be exactly the legacy module-less
//     components.
//  5. Boundary "Via". Legacy keeps the first-seen via component; equivalence
//     is asserted on (ToName, ToKind, Kind) / (FromName, Kind) sets.
//  6. Cross-service INJECTS. The live demo DB contains two INJECTS edges
//     that cross service boundaries (arena.service and jerry.service both
//     inject tom-api's prisma.service — a canonical-resolution artifact;
//     each service has its own prisma.service). Legacy C2 is structurally
//     blind to INJECTS (it lifts only CALLS_SERVICE/PUBLISHES/CONSUMES), so
//     it can never surface them; the algebra collapses them like any other
//     cross-group sync edge, faithfully to the graph. The C2 test asserts
//     each collapsed INJECTS edge is backed by a raw cross-service INJECTS
//     edge in the store instead of pretending they don't exist.

import (
	"sort"
	"strings"
	"testing"

	"github.com/alexdx2/chronicle-core/store"
)

type pair struct{ from, to string }

func sortedSet(m map[pair]bool) []string {
	out := make([]string, 0, len(m))
	for p := range m {
		out = append(out, p.from+" -> "+p.to)
	}
	sort.Strings(out)
	return out
}

func eqPairSets(t *testing.T, label string, got, want map[pair]bool) {
	t.Helper()
	g, w := sortedSet(got), sortedSet(want)
	if strings.Join(g, "\n") != strings.Join(w, "\n") {
		t.Errorf("%s mismatch:\n got: %v\nwant: %v", label, g, w)
	}
}

func TestViewC2EquivalentToLegacy(t *testing.T) {
	st := openLiveDBCopy(t)

	legacy, err := BuildC2(st, "tom-and-jerry")
	if err != nil {
		t.Fatalf("BuildC2: %v", err)
	}
	view, err := BuildPreset(st, "c2", "tom-and-jerry", "")
	if err != nil {
		t.Fatalf("BuildPreset(c2): %v", err)
	}

	// --- group keys == legacy service keys ---
	gotGroups := map[string]bool{}
	for _, g := range view.Groups {
		gotGroups[g.Key] = true
	}
	wantGroups := map[string]bool{}
	for _, s := range legacy.Services {
		wantGroups[s.Key] = true
	}
	if len(gotGroups) != len(wantGroups) {
		t.Errorf("groups=%v want %v", gotGroups, wantGroups)
	}
	for k := range wantGroups {
		if !gotGroups[k] {
			t.Errorf("missing group %s", k)
		}
	}

	// --- collapsed edges == legacy lifted edges (normalized, see header) ---
	legacySync := map[pair]bool{}  // kind "http"
	legacyAsync := map[pair]bool{} // kind "async"; consumes direction flipped back to graph direction (note 2)
	for _, e := range legacy.Edges {
		switch e.Kind {
		case "http":
			legacySync[pair{e.From, e.To}] = true
		case "async":
			if e.Label == "consumes" {
				legacyAsync[pair{e.To, e.From}] = true // legacy renders topic→consumer
			} else {
				legacyAsync[pair{e.From, e.To}] = true
			}
		default:
			t.Errorf("unexpected legacy C2 edge kind %q", e.Kind)
		}
	}

	// Cross-service INJECTS pairs present in the raw graph (note 6): build
	// the expected service-pair set straight from the store so the assertion
	// is data-backed, not hardcoded.
	wantInjectsPairs := map[pair]bool{}
	{
		rawNodes, err := st.ListNodes(store.NodeFilter{Domain: "tom-and-jerry"})
		if err != nil {
			t.Fatalf("ListNodes: %v", err)
		}
		rawNodes = activeOnly(rawNodes)
		owned := ownershipMap(rawNodes)
		rawEdges, err := st.ListEdges(store.EdgeFilter{})
		if err != nil {
			t.Fatalf("ListEdges: %v", err)
		}
		for _, e := range activeEdges(rawEdges) {
			if e.EdgeType != "INJECTS" {
				continue
			}
			fs, ts := owned[e.FromNodeID], owned[e.ToNodeID]
			if fs != nil && ts != nil && fs.NodeID != ts.NodeID {
				wantInjectsPairs[pair{fs.NodeKey, ts.NodeKey}] = true
			}
		}
	}

	viewCallsService := map[pair]bool{}
	viewCallsEndpoint := map[pair]bool{}
	viewAsync := map[pair]bool{}
	viewInjects := map[pair]bool{}
	for _, e := range view.Edges {
		switch e.Kind {
		case "CALLS_SERVICE":
			viewCallsService[pair{e.From, e.To}] = true
		case "CALLS_ENDPOINT":
			viewCallsEndpoint[pair{e.From, e.To}] = true
		case "PUBLISHES_TOPIC", "CONSUMES_TOPIC":
			viewAsync[pair{e.From, e.To}] = true
		case "INJECTS":
			// Note 6: legitimate discrepancy — legacy C2 cannot represent
			// these; verified against raw cross-service INJECTS below.
			viewInjects[pair{e.From, e.To}] = true
		default:
			t.Errorf("unexpected collapsed edge kind %q (%s -> %s)", e.Kind, e.From, e.To)
		}
		if e.Weight < 1 || (e.Weight >= 1 && len(e.CollapsedFrom) != e.Weight) {
			t.Errorf("edge %s->%s weight=%d collapsed_from=%d", e.From, e.To, e.Weight, len(e.CollapsedFrom))
		}
	}
	eqPairSets(t, "cross-service INJECTS (note 6)", viewInjects, wantInjectsPairs)

	// (a) exact: CALLS_SERVICE collapsed set == legacy http set.
	eqPairSets(t, "sync (CALLS_SERVICE vs legacy http)", viewCallsService, legacySync)
	// (b) CALLS_ENDPOINT lifts to the same service pairs (note 3) — the
	// algebra is allowed to be richer in kind, never in topology.
	for p := range viewCallsEndpoint {
		if !legacySync[p] {
			t.Errorf("CALLS_ENDPOINT collapsed to pair not in legacy http set: %s -> %s", p.from, p.to)
		}
	}
	eqPairSets(t, "async", viewAsync, legacyAsync)

	// --- free-standing topic VNodes == legacy Topics ---
	gotTopics := map[string]bool{}
	for _, n := range view.Nodes {
		if n.Type == "topic" {
			if n.Group != "" {
				t.Errorf("topic %s should be free-standing, got group %q", n.Key, n.Group)
			}
			gotTopics[n.Key] = true
		}
	}
	wantTopics := map[string]bool{}
	for _, tp := range legacy.Topics {
		wantTopics[tp.Key] = true
	}
	if len(gotTopics) != len(wantTopics) {
		t.Errorf("topics=%v want %v", gotTopics, wantTopics)
	}
	for k := range wantTopics {
		if !gotTopics[k] {
			t.Errorf("missing free-standing topic VNode %s", k)
		}
	}
}

func TestViewC3EquivalentToLegacy(t *testing.T) {
	st := openLiveDBCopy(t)

	const domain, svc = "tom-and-jerry", "arena-api"
	legacy, err := BuildC3(st, domain, svc)
	if err != nil {
		t.Fatalf("BuildC3: %v", err)
	}
	view, err := BuildPreset(st, "c3", domain, svc)
	if err != nil {
		t.Fatalf("BuildPreset(c3): %v", err)
	}
	svcKey := legacy.Service["key"]

	// Materialized boundary nodes (Boundary:true) are a view-engine addition
	// on top of the legacy C3 payload — excluded from the equivalence sets.
	boundaryKeys := map[string]bool{}
	for _, n := range view.Nodes {
		if n.Boundary {
			boundaryKeys[n.Key] = true
		}
	}

	// --- VNode key set == legacy component key set ---
	gotNodes := map[string]bool{}
	for _, n := range view.Nodes {
		if n.Boundary {
			continue
		}
		gotNodes[n.Key] = true
	}
	wantNodes := map[string]bool{}
	for _, c := range legacy.Components {
		wantNodes[c.Key] = true
	}
	if len(gotNodes) != len(wantNodes) {
		t.Errorf("nodes: got %d %v want %d %v", len(gotNodes), gotNodes, len(wantNodes), wantNodes)
	}
	for k := range wantNodes {
		if !gotNodes[k] {
			t.Errorf("missing component VNode %s", k)
		}
	}
	for k := range gotNodes {
		if !wantNodes[k] {
			t.Errorf("extra VNode %s not in legacy components", k)
		}
	}

	// --- VGroup keys == legacy module keys (+ the scope-service fallback
	// group allowed, note 4: components with no CONTAINS parent fall back to
	// the owning service; legacy renders them module-less inside the service
	// boundary — same semantics, the scope IS the service). ---
	wantModules := map[string]bool{}
	inAnyModule := map[string]bool{}
	for _, m := range legacy.Modules {
		wantModules[m.Key] = true
		for _, member := range m.Members {
			inAnyModule[member] = true
		}
	}
	for _, g := range view.Groups {
		if wantModules[g.Key] {
			continue
		}
		if g.Key != svcKey {
			t.Errorf("unexpected group %s (not a legacy module, not the scope service)", g.Key)
		}
	}
	for k := range wantModules {
		found := false
		for _, g := range view.Groups {
			if g.Key == k {
				found = true
			}
		}
		if !found {
			t.Errorf("missing module group %s", k)
		}
	}
	// Members of the fallback service group must be exactly the legacy
	// components that belong to no module.
	for _, n := range view.Nodes {
		if n.Group == svcKey && inAnyModule[n.Key] {
			t.Errorf("node %s is in a legacy module but fell back to the service group", n.Key)
		}
		if wantModules[n.Group] && !inAnyModule[n.Key] {
			t.Errorf("node %s grouped into module %s but legacy lists it module-less", n.Key, n.Group)
		}
	}

	// --- (from,to,kind) edge set == legacy internal_edges (kind casing
	// normalized: algebra keeps raw INJECTS, legacy uses "injects") ---
	type triple struct{ from, to, kind string }
	gotEdges := map[triple]bool{}
	for _, e := range view.Edges {
		if boundaryKeys[e.From] || boundaryKeys[e.To] {
			continue // edges to materialized boundary nodes are additive
		}
		gotEdges[triple{e.From, e.To, strings.ToLower(e.Kind)}] = true
	}
	wantEdges := map[triple]bool{}
	for _, e := range legacy.InternalEdges {
		wantEdges[triple{e.From, e.To, e.Kind}] = true
	}
	if len(gotEdges) != len(wantEdges) {
		t.Errorf("edges: got %d want %d\n got: %v\nwant: %v", len(gotEdges), len(wantEdges), gotEdges, wantEdges)
	}
	for e := range wantEdges {
		if !gotEdges[e] {
			t.Errorf("missing internal edge %+v", e)
		}
	}
	for e := range gotEdges {
		if !wantEdges[e] {
			t.Errorf("extra edge %+v not in legacy internal_edges", e)
		}
	}

	// --- boundary sets equal (compared without Via, note 5) ---
	if view.Boundary == nil {
		t.Fatalf("view.Boundary is nil; legacy has %d out / %d in",
			len(legacy.Boundary.Outgoing), len(legacy.Boundary.Incoming))
	}
	type outT struct{ toName, toKind, kind string }
	gotOut := map[outT]bool{}
	for _, o := range view.Boundary.Out {
		gotOut[outT{o.ToName, o.ToKind, o.Kind}] = true
	}
	wantOut := map[outT]bool{}
	for _, o := range legacy.Boundary.Outgoing {
		wantOut[outT{o.ToName, o.ToKind, o.Kind}] = true
	}
	if len(gotOut) != len(wantOut) {
		t.Errorf("boundary out: got %v want %v", gotOut, wantOut)
	}
	for o := range wantOut {
		if !gotOut[o] {
			t.Errorf("missing boundary out %+v", o)
		}
	}
	for o := range gotOut {
		if !wantOut[o] {
			t.Errorf("extra boundary out %+v", o)
		}
	}

	type inT struct{ fromName, kind string }
	gotIn := map[inT]bool{}
	for _, i := range view.Boundary.In {
		gotIn[inT{i.FromName, i.Kind}] = true
	}
	wantIn := map[inT]bool{}
	for _, i := range legacy.Boundary.Incoming {
		wantIn[inT{i.FromName, i.Kind}] = true
	}
	if len(gotIn) != len(wantIn) {
		t.Errorf("boundary in: got %v want %v", gotIn, wantIn)
	}
	for i := range wantIn {
		if !gotIn[i] {
			t.Errorf("missing boundary in %+v", i)
		}
	}
	for i := range gotIn {
		if !wantIn[i] {
			t.Errorf("extra boundary in %+v", i)
		}
	}
}

// TestViewC2AsyncOnly: an async-only C2 is the same preset with a narrower
// edge filter — only PUBLISHES_TOPIC / CONSUMES_TOPIC arrows survive
// (design §5.4: filters run before collapse).
func TestViewC2AsyncOnly(t *testing.T) {
	st := openLiveDBCopy(t)

	spec := PresetSpec("c2", "tom-and-jerry", "")
	spec.Filter = &FilterSpec{EdgeKinds: []string{"async"}}
	view, err := BuildView(st, spec)
	if err != nil {
		t.Fatalf("BuildView: %v", err)
	}

	if len(view.Edges) == 0 {
		t.Fatal("async-only c2 has no edges")
	}
	var pubs, cons int
	for _, e := range view.Edges {
		switch e.Kind {
		case "PUBLISHES_TOPIC":
			pubs++
		case "CONSUMES_TOPIC":
			cons++
		default:
			t.Errorf("non-async edge survived async filter: %s (%s -> %s)", e.Kind, e.From, e.To)
		}
	}
	if pubs == 0 || cons == 0 {
		t.Errorf("expected both publishes and consumes edges, got pubs=%d cons=%d", pubs, cons)
	}
}

// TestViewNameFilter: filter.name="score*" keeps only nodes whose name
// matches the glob (case-insensitive) and only edges between kept nodes.
func TestViewNameFilter(t *testing.T) {
	st := openLiveDBCopy(t)

	view, err := BuildView(st, ViewSpec{
		Scope:  ScopeSpec{Domain: "tom-and-jerry"},
		Filter: &FilterSpec{Name: "score*"},
		Group:  GroupSpec{By: "none"},
	})
	if err != nil {
		t.Fatalf("BuildView: %v", err)
	}

	kept := map[string]bool{}
	for _, n := range view.Nodes {
		kept[n.Key] = true
		if !strings.HasPrefix(strings.ToLower(n.Name), "score") {
			t.Errorf("node %s name %q does not match score*", n.Key, n.Name)
		}
	}
	// Known score* nodes from the demo graph.
	for _, want := range []string{
		"code:controller:tom-and-jerry:fixtures/tom-and-jerry/scoreboard-api/controllers/scorecontroller",
		"code:provider:tom-and-jerry:fixtures/tom-and-jerry/scoreboard-api/services/scoreservice",
		"data:model:tom-and-jerry:score",
	} {
		if !kept[want] {
			t.Errorf("expected node %s to survive name filter; kept=%v", want, kept)
		}
	}
	// BattleResultConsumer must be gone — and with it its INJECTS edge into
	// ScoreService (dropping a node drops its edges).
	if kept["code:provider:tom-and-jerry:fixtures/tom-and-jerry/scoreboard-api/services/battleresultconsumer"] {
		t.Error("battleresultconsumer should be dropped by name filter")
	}

	if len(view.Edges) == 0 {
		t.Fatal("expected surviving edges among score* nodes (e.g. ScoreController INJECTS ScoreService)")
	}
	foundInjects := false
	for _, e := range view.Edges {
		if !kept[e.From] || !kept[e.To] {
			t.Errorf("edge %s -> %s (%s) references a filtered-out node", e.From, e.To, e.Kind)
		}
		if e.Kind == "INJECTS" &&
			strings.HasSuffix(e.From, "scorecontroller") && strings.HasSuffix(e.To, "scoreservice") {
			foundInjects = true
		}
	}
	if !foundInjects {
		t.Errorf("ScoreController->ScoreService INJECTS edge missing; edges=%+v", view.Edges)
	}
}
