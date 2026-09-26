package review

import (
	"fmt"
	"path/filepath"
	"testing"

	"github.com/alexdx2/chronicle-core/graph"
	"github.com/alexdx2/chronicle-core/graph/viewmodel"
	"github.com/alexdx2/chronicle-core/registry"
	"github.com/alexdx2/chronicle-core/snapshot"
	"github.com/alexdx2/chronicle-core/store"
)

// --- shared fixture: the real battle-results pipeline ----------------------
//
// Real keys from fixtures/tom-and-jerry/.depbot/chronicle.db (confirmed by
// direct sqlite3 query against the tracked fixture, and already exercised by
// impact_test.go's TestCoreImpactQuerier_TopicSubscribers):
//
//	code:provider:tom-and-jerry:arena-api/src/arena/battle-result.producer
//	  --PUBLISHES_TOPIC--> contract:topic:tom-and-jerry:battle-results
//	code:provider:tom-and-jerry:scoreboard-api/services/battleresultconsumer
//	  --CONSUMES_TOPIC--> contract:topic:tom-and-jerry:battle-results
//	code:provider:tom-and-jerry:spectators-api/src/spectators/battle-result.consumer
//	  --CONSUMES_TOPIC--> contract:topic:tom-and-jerry:battle-results
//
// arena-api/scoreboard-api/spectators-api are real service:service nodes in
// domain "tom-and-jerry". This is "the same synthetic [style] as
// finding_test.go" the brief asks for: a hand-built ReviewFinding tree (not
// derived from a real Diff/ClassifyDelta/Analyze pipeline), but built from
// real node keys so viewmodel.BuildView (given the real head.Graph) resolves
// them for real, exactly like the mockup's "battle-results" example.
const (
	arenaProducerKey  = "code:provider:tom-and-jerry:arena-api/src/arena/battle-result.producer"
	battleTopicKey    = "contract:topic:tom-and-jerry:battle-results"
	arenaServiceKey   = "service:service:tom-and-jerry:arena-api"
	scoreboardKey     = "service:service:tom-and-jerry:scoreboard-api"
	spectatorsKey     = "service:service:tom-and-jerry:spectators-api"
	tomAndJerryDomain = "tom-and-jerry"
)

var (
	arenaRef = SystemRef{Kind: kindService, Key: arenaServiceKey, Name: "arena-api", Domain: tomAndJerryDomain}
	scoreRef = SystemRef{Kind: kindService, Key: scoreboardKey, Name: "scoreboard-api", Domain: tomAndJerryDomain}
	specRef  = SystemRef{Kind: kindService, Key: spectatorsKey, Name: "spectators-api", Domain: tomAndJerryDomain}
)

// battleResultsReviewResult hand-builds the ReviewResult for one Changed
// finding (arena-api gained a PUBLISHES_TOPIC integration onto battle-results)
// with two real Affected downstream findings (its two real subscribers),
// matching TestCoreImpactQuerier_TopicSubscribers' own data exactly.
func battleResultsReviewResult() *ReviewResult {
	edge := snapshot.Edge{
		ID: arenaProducerKey + "->" + battleTopicKey + ":PUBLISHES_TOPIC",
		Type: "PUBLISHES_TOPIC", From: arenaProducerKey, To: battleTopicKey,
	}
	changed := ReviewFinding{
		Severity:    SeverityChanged,
		Kind:        IntegrationAdded,
		System:      arenaRef,
		Counterpart: battleTopicKey,
		Facts:       []Fact{edgeFact(edge.ID, edge)},
		Downstream: []ReviewFinding{
			{Severity: SeverityAffected, Kind: IntegrationAdded, System: scoreRef,
				Path: []PathStep{{Node: "battle-results", Edge: "CONSUMES_TOPIC"}}},
			{Severity: SeverityAffected, Kind: IntegrationAdded, System: specRef,
				Path: []PathStep{{Node: "battle-results", Edge: "CONSUMES_TOPIC"}}},
		},
	}
	return &ReviewResult{Findings: []ReviewFinding{changed}}
}

func stringSet(ss []string) map[string]bool {
	out := make(map[string]bool, len(ss))
	for _, s := range ss {
		out[s] = true
	}
	return out
}

// rebuildView calls viewmodel.BuildView directly against head's own store,
// using a Spec a DiagramResult already produced — so a test can assert on
// structured View fields (Nodes, Missing) instead of parsing mermaid text.
func rebuildView(t *testing.T, head *VersionGraph, spec viewmodel.ViewSpec) *viewmodel.View {
	t.Helper()
	v, err := viewmodel.BuildView(head.Graph.Store(), spec)
	if err != nil {
		t.Fatalf("rebuild View: %v", err)
	}
	return v
}

func findViewNode(v *viewmodel.View, key string) *viewmodel.VNode {
	for i := range v.Nodes {
		if v.Nodes[i].Key == key {
			return &v.Nodes[i]
		}
	}
	return nil
}

// --- Step 1 tests, as named by the brief -----------------------------------

// TestCompactDiagram_Fixture: mermaid contains nodes for the changed service
// (arena-api), the intermediate topic (battle-results), and both affected
// services (scoreboard-api, spectators-api) — no node beyond those four.
func TestCompactDiagram_Fixture(t *testing.T) {
	head := &VersionGraph{Graph: openTomAndJerryFixture(t)}
	r := battleResultsReviewResult()

	got, err := CompactDiagram(r, head, DiagramOptions{})
	if err != nil {
		t.Fatalf("CompactDiagram: %v", err)
	}

	if got.Spec.Scope.Domain != tomAndJerryDomain {
		t.Errorf("Scope.Domain = %q, want %q", got.Spec.Scope.Domain, tomAndJerryDomain)
	}
	if got.Spec.Group.By != "service" {
		t.Errorf("Group.By = %q, want service", got.Spec.Group.By)
	}
	if !got.Spec.Collapse {
		t.Error("Collapse = false, want true")
	}
	if got.Spec.Layout.Preset != "custom" {
		t.Errorf("Layout.Preset = %q, want custom", got.Spec.Layout.Preset)
	}
	if got.Truncated != 0 {
		t.Errorf("Truncated = %d, want 0", got.Truncated)
	}

	wantNodes := map[string]bool{arenaServiceKey: true, scoreboardKey: true, spectatorsKey: true}
	if gotSet := stringSet(got.Spec.Scope.Nodes); len(gotSet) != len(wantNodes) {
		t.Errorf("Scope.Nodes = %v, want exactly %v", got.Spec.Scope.Nodes, wantNodes)
	} else {
		for k := range wantNodes {
			if !gotSet[k] {
				t.Errorf("Scope.Nodes = %v, missing %s", got.Spec.Scope.Nodes, k)
			}
		}
	}

	if len(got.Spec.Pin) != 1 || got.Spec.Pin[0] != battleTopicKey {
		t.Errorf("Pin = %v, want [%s]", got.Spec.Pin, battleTopicKey)
	}

	if got.Mermaid == "" {
		t.Fatal("Mermaid is empty")
	}

	// Rebuild the View from the exact Spec CompactDiagram produced, to
	// assert on structured keys instead of parsing mermaid text: "ни одного
	// узла сверх ожидаемых" (not a single node beyond expected).
	//
	// Group:{By:"service"}+Collapse:true renders each of the three pinned
	// services as a VGroup (a collapsed card), not a VNode — confirmed
	// empirically (see task-12-report.md): view.Groups carries arena-api/
	// scoreboard-api/spectators-api, each with its own severity Mark.
	// view.Nodes carries the pinned topic PLUS three dimmed Boundary nodes:
	// the real code-level producer/consumer this fixture's PUBLISHES_TOPIC/
	// CONSUMES_TOPIC edges actually connect to the topic (Pin's own doc
	// comment: a pinned node "brings its connections... plus 1-hop
	// out-of-view neighbors materialized as dimmed Boundary nodes" — since
	// those code nodes are only present as collapsed, invisible members of
	// their now-collapsed service group, viewmodel materializes them so the
	// topic's real edges have somewhere concrete to land instead of
	// silently vanishing). This is the existing diagram engine's own,
	// correct, unmodified behavior — not something CompactDiagram adds.
	view := rebuildView(t, head, got.Spec)

	wantGroups := map[string]string{ // key -> want Mark kind
		arenaServiceKey: "changed",
		scoreboardKey:   "breaks",
		spectatorsKey:   "breaks",
	}
	if len(view.Groups) != len(wantGroups) {
		t.Errorf("View.Groups = %d entries, want %d", len(view.Groups), len(wantGroups))
	}
	for _, g := range view.Groups {
		wantKind, ok := wantGroups[g.Key]
		if !ok {
			t.Errorf("View.Groups contains unexpected key %s", g.Key)
			continue
		}
		if len(g.Marks) != 1 || g.Marks[0].Kind != wantKind {
			t.Errorf("View.Groups[%s].Marks = %+v, want a single mark of kind %q", g.Key, g.Marks, wantKind)
		}
	}

	wantBoundaryNodes := map[string]bool{
		arenaProducerKey:                                                              true,
		"code:provider:tom-and-jerry:scoreboard-api/services/battleresultconsumer":    true,
		"code:provider:tom-and-jerry:spectators-api/src/spectators/battle-result.consumer": true,
	}
	seenTopic := false
	for _, n := range view.Nodes {
		switch {
		case n.Key == battleTopicKey:
			seenTopic = true
			if n.Boundary {
				t.Errorf("topic node %s has Boundary=true, want false (it is the pinned node itself)", n.Key)
			}
		case wantBoundaryNodes[n.Key]:
			if !n.Boundary {
				t.Errorf("materialized code node %s has Boundary=false, want true", n.Key)
			}
		default:
			t.Errorf("View.Nodes contains unexpected key %s", n.Key)
		}
	}
	if !seenTopic {
		t.Error("View.Nodes is missing the pinned topic " + battleTopicKey)
	}
	if len(view.Nodes) != 1+len(wantBoundaryNodes) {
		t.Errorf("View.Nodes has %d entries, want %d (topic + %d materialized boundary code nodes)",
			len(view.Nodes), 1+len(wantBoundaryNodes), len(wantBoundaryNodes))
	}

	if len(view.Missing) != 0 {
		t.Errorf("View.Missing = %v, want none (all pinned keys are real fixture nodes)", view.Missing)
	}
}

// TestCompactDiagram_MarksMatchSeverity: every pinned key in Spec.Marks
// carries the kind the brief's severity->kind table says it should
// (Changed -> "changed", Affected -> "breaks").
func TestCompactDiagram_MarksMatchSeverity(t *testing.T) {
	head := &VersionGraph{Graph: openTomAndJerryFixture(t)}
	r := battleResultsReviewResult()

	got, err := CompactDiagram(r, head, DiagramOptions{})
	if err != nil {
		t.Fatalf("CompactDiagram: %v", err)
	}

	table := map[string]string{
		arenaServiceKey: "changed",
		scoreboardKey:   "breaks",
		spectatorsKey:   "breaks",
	}
	if len(got.Spec.Marks) != len(table) {
		t.Fatalf("Marks = %+v, want exactly %d entries matching %v", got.Spec.Marks, len(table), table)
	}
	for key, wantKind := range table {
		m, ok := got.Spec.Marks[key]
		if !ok {
			t.Errorf("Marks missing entry for %s", key)
			continue
		}
		if m.Kind != wantKind {
			t.Errorf("Marks[%s].Kind = %q, want %q", key, m.Kind, wantKind)
		}
	}
	// The contract pin (the topic) is never itself a Mark target — only
	// SystemRef-kind pins carry severity marks (see systemMarks).
	if _, ok := got.Spec.Marks[battleTopicKey]; ok {
		t.Errorf("Marks unexpectedly contains the raw contract pin %s", battleTopicKey)
	}
}

// TestCompactDiagram_TruncatesOverMax: 20 affected systems (plus the one
// changed system = 21 distinct SystemRef-kind pins), MaxNodes:5 ->
// Truncated>0 and at most 5 service/domain nodes survive into Spec.
// Additionally verifies the priority rule itself (changed always kept, then
// ascending Path length among affected).
func TestCompactDiagram_TruncatesOverMax(t *testing.T) {
	head := &VersionGraph{Graph: openTomAndJerryFixture(t)}

	changed := ReviewFinding{Severity: SeverityChanged, Kind: ContractRemoved, System: arenaRef}
	downstream := make([]ReviewFinding, 0, 20)
	// Path lengths 1..20 ascending — with "changed" taking one of the 5
	// MaxNodes slots, only the 4 shortest-path affected systems
	// (fake-affected-00..03, path length 1..4) should survive.
	for i := 0; i < 20; i++ {
		key := fmt.Sprintf("service:service:tom-and-jerry:fake-affected-%02d", i)
		path := make([]PathStep, i+1)
		for j := range path {
			path[j] = PathStep{Node: "hop", Edge: "CALLS_SERVICE"}
		}
		downstream = append(downstream, ReviewFinding{
			Severity: SeverityAffected, Kind: ContractRemoved,
			System: SystemRef{Kind: kindService, Key: key, Name: key, Domain: tomAndJerryDomain},
			Path:   path,
		})
	}
	changed.Downstream = downstream
	r := &ReviewResult{Findings: []ReviewFinding{changed}}

	got, err := CompactDiagram(r, head, DiagramOptions{MaxNodes: 5})
	if err != nil {
		t.Fatalf("CompactDiagram: %v", err)
	}
	if got.Truncated <= 0 {
		t.Errorf("Truncated = %d, want > 0 (21 distinct systems, MaxNodes 5)", got.Truncated)
	}
	if len(got.Spec.Scope.Nodes) > 5 {
		t.Errorf("Scope.Nodes has %d entries, want <= 5: %v", len(got.Spec.Scope.Nodes), got.Spec.Scope.Nodes)
	}

	kept := stringSet(got.Spec.Scope.Nodes)
	if !kept[arenaServiceKey] {
		t.Errorf("Scope.Nodes = %v, missing the changed system %s (changed must always outrank affected)", got.Spec.Scope.Nodes, arenaServiceKey)
	}
	for i := 0; i < 4; i++ {
		key := fmt.Sprintf("service:service:tom-and-jerry:fake-affected-%02d", i)
		if !kept[key] {
			t.Errorf("Scope.Nodes = %v, missing %s (one of the 4 shortest-path affected systems)", got.Spec.Scope.Nodes, key)
		}
	}
	for i := 4; i < 20; i++ {
		key := fmt.Sprintf("service:service:tom-and-jerry:fake-affected-%02d", i)
		if kept[key] {
			t.Errorf("Scope.Nodes = %v, unexpectedly kept %s (a longer-path affected system that should have been cut)", got.Spec.Scope.Nodes, key)
		}
	}
}

// TestFullDiagram_NeverTruncates: many more affected systems than the
// default MaxNodes would allow — FullDiagram's own Truncated is always 0.
func TestFullDiagram_NeverTruncates(t *testing.T) {
	head := &VersionGraph{Graph: openTomAndJerryFixture(t)}

	changed := ReviewFinding{Severity: SeverityChanged, Kind: ContractRemoved, System: arenaRef}
	for i := 0; i < 30; i++ {
		key := fmt.Sprintf("service:service:tom-and-jerry:fake-full-%02d", i)
		changed.Downstream = append(changed.Downstream, ReviewFinding{
			Severity: SeverityAffected, Kind: ContractRemoved,
			System: SystemRef{Kind: kindService, Key: key, Name: key, Domain: tomAndJerryDomain},
		})
	}
	r := &ReviewResult{Findings: []ReviewFinding{changed}}

	got, err := FullDiagram(r, head)
	if err != nil {
		t.Fatalf("FullDiagram: %v", err)
	}
	if got.Truncated != 0 {
		t.Errorf("Truncated = %d, want 0 (FullDiagram never truncates)", got.Truncated)
	}
	if len(got.Spec.Scope.Nodes) != 31 { // 1 changed + 30 affected, all distinct
		t.Errorf("Scope.Nodes has %d entries, want 31 (FullDiagram keeps every distinct system)", len(got.Spec.Scope.Nodes))
	}
}

// --- pinnedKeys unit coverage ------------------------------------------------

func TestPinnedKeys_BattleResultsFixture(t *testing.T) {
	r := battleResultsReviewResult()
	got := stringSet(pinnedKeys(r))
	want := []string{arenaServiceKey, scoreboardKey, spectatorsKey, battleTopicKey}
	if len(got) != len(want) {
		t.Fatalf("pinnedKeys = %v, want exactly %v", got, want)
	}
	for _, k := range want {
		if !got[k] {
			t.Errorf("pinnedKeys missing %s", k)
		}
	}
}

// TestIsTopicCounterpart_CallEndpointExcluded: a ConsumerChanged finding
// whose underlying edge is CALLS_ENDPOINT (a call, not a topic) must NOT be
// treated as "on a topic" — the brief's own "не эндпоинте вызова" carve-out
// — while the same Kind over CONSUMES_TOPIC must.
func TestIsTopicCounterpart_CallEndpointExcluded(t *testing.T) {
	callEdge := snapshot.Edge{ID: "a->b:CALLS_ENDPOINT", Type: "CALLS_ENDPOINT", From: "a", To: "b"}
	f := ReviewFinding{Kind: ConsumerChanged, Counterpart: "b", Facts: []Fact{edgeFact(callEdge.ID, callEdge)}}
	if isTopicCounterpart(f) {
		t.Error("isTopicCounterpart(ConsumerChanged over CALLS_ENDPOINT) = true, want false")
	}

	topicEdge := snapshot.Edge{ID: "a->t:CONSUMES_TOPIC", Type: "CONSUMES_TOPIC", From: "a", To: "t"}
	f2 := ReviewFinding{Kind: ConsumerChanged, Counterpart: "t", Facts: []Fact{edgeFact(topicEdge.ID, topicEdge)}}
	if !isTopicCounterpart(f2) {
		t.Error("isTopicCounterpart(ConsumerChanged over CONSUMES_TOPIC) = false, want true")
	}
}

// TestIsTopicCounterpart_IntegrationConsumesTopicIsNotTopic: for
// IntegrationAdded/Removed specifically, CONSUMES_TOPIC behaves like a plain
// sync dependency (finding.go's analyzeIntegration), so it must NOT count as
// "on a topic" here either — the one place this rule is deliberately NOT
// symmetric with ConsumerChanged.
func TestIsTopicCounterpart_IntegrationConsumesTopicIsNotTopic(t *testing.T) {
	edge := snapshot.Edge{ID: "a->t:CONSUMES_TOPIC", Type: "CONSUMES_TOPIC", From: "a", To: "t"}
	f := ReviewFinding{Kind: IntegrationAdded, Counterpart: "t", Facts: []Fact{edgeFact(edge.ID, edge)}}
	if isTopicCounterpart(f) {
		t.Error("isTopicCounterpart(IntegrationAdded over CONSUMES_TOPIC) = true, want false")
	}
}

// --- genuine structural investigation (context note 3) ---------------------
//
// synthCrossDomainStore builds a fresh, minimal, two-domain store (the real
// tom-and-jerry fixture is entirely single-domain — confirmed by direct
// query, `select distinct domain_key from graph_nodes` returns exactly one
// row — so it cannot reproduce this): domain-a's a-comp CALLS_SERVICE
// domain-b's svc-b, the same "foreign neighbour" crossing-edge shape
// graph/viewmodel/foreign_neighbours_test.go's synthForeignStore already
// uses to exercise this exact mechanism from the viewmodel side.
func synthCrossDomainStore(t *testing.T) *graph.Graph {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "cross-domain.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	rev, err := st.CreateRevision("domain-a", "", "task-12-cross-domain", "manual", "full", "{}")
	if err != nil {
		t.Fatalf("CreateRevision: %v", err)
	}
	mk := func(key, layer, ntype, domain, name, status, file string) int64 {
		id, err := st.UpsertNode(store.NodeRow{NodeKey: key, Layer: layer, NodeType: ntype, DomainKey: domain, Name: name,
			FilePath: file, Status: status, Confidence: 0.9, Freshness: 1, TrustScore: 0.9, Metadata: "{}", LastSeenRevisionID: rev})
		if err != nil {
			t.Fatalf("upsert %s: %v", key, err)
		}
		return id
	}
	mk("service:service:domain-a:svc-a", "service", "service", "domain-a", "svc-a", "active", "svc-a/package.json")
	aCompID := mk("code:provider:domain-a:svc-a/comp", "code", "provider", "domain-a", "comp", "active", "svc-a/src/comp.ts")
	bSvcID := mk("service:service:domain-b:svc-b", "service", "service", "domain-b", "svc-b", "active", "")
	if _, err := st.UpsertEdge(store.EdgeRow{
		EdgeKey:        "code:provider:domain-a:svc-a/comp->service:service:domain-b:svc-b:CALLS_SERVICE",
		FromNodeID:     aCompID,
		ToNodeID:       bSvcID,
		EdgeType:       "CALLS_SERVICE",
		DerivationKind: "hard", Active: true, Confidence: 0.9, Freshness: 1, TrustScore: 0.9,
		Metadata: "{}", LastSeenRevisionID: rev,
	}); err != nil {
		t.Fatalf("upsert edge: %v", err)
	}
	reg, err := registry.LoadDefaults()
	if err != nil {
		t.Fatalf("LoadDefaults: %v", err)
	}
	return graph.New(st, reg)
}

// TestCompactDiagram_CrossDomainAffectedSystemDroppedToMissing empirically
// confirms the structural limitation flagged in task-12-report.md: a
// WhoMayBeAffected() system in a domain OTHER than Scope.Domain is silently
// dropped by viewmodel.BuildView (ViewSpec is single-domain-scoped, and
// Layout.Preset "custom" has no c1/c2/c3 boundary-neighbour fallback), and
// confirms CompactDiagram's own remediation (folding it into Truncated)
// actually surfaces the loss instead of staying silent.
func TestCompactDiagram_CrossDomainAffectedSystemDroppedToMissing(t *testing.T) {
	g := synthCrossDomainStore(t)
	head := &VersionGraph{Graph: g}

	changed := ReviewFinding{
		Severity: SeverityChanged, Kind: ConsumerChanged,
		System: SystemRef{Kind: kindService, Key: "service:service:domain-a:svc-a", Name: "svc-a", Domain: "domain-a"},
		Downstream: []ReviewFinding{
			{Severity: SeverityAffected, Kind: ConsumerChanged,
				System: SystemRef{Kind: kindService, Key: "service:service:domain-b:svc-b", Name: "svc-b", Domain: "domain-b"},
				Path:   []PathStep{{Node: "svc-b", Edge: "CALLS_SERVICE"}}},
		},
	}
	r := &ReviewResult{Findings: []ReviewFinding{changed}}

	got, err := CompactDiagram(r, head, DiagramOptions{})
	if err != nil {
		t.Fatalf("CompactDiagram: %v", err)
	}

	// Both systems were asked for (Scope.Domain fixed to domain-a from the
	// changed finding; svc-b pinned as an Affected system in domain-b).
	if len(got.Spec.Scope.Nodes) != 2 {
		t.Fatalf("Scope.Nodes = %v, want both svc-a and svc-b requested", got.Spec.Scope.Nodes)
	}

	view := rebuildView(t, head, got.Spec)
	if findViewNode(view, "service:service:domain-b:svc-b") != nil {
		t.Error("svc-b (domain-b) rendered as a node despite Scope.Domain=domain-a — expected it to be dropped, not drawn")
	}
	foundMissing := false
	for _, m := range view.Missing {
		if m == "service:service:domain-b:svc-b" {
			foundMissing = true
		}
	}
	if !foundMissing {
		t.Errorf("View.Missing = %v, want it to contain svc-b (dropped cross-domain pin)", view.Missing)
	}

	// The real assertion this test exists for: CompactDiagram's own
	// Truncated reflects the drop instead of reporting 0 and looking
	// complete.
	if got.Truncated != 1 {
		t.Errorf("Truncated = %d, want 1 (one cross-domain Affected system silently dropped by BuildView)", got.Truncated)
	}
}
