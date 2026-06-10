package viewmodel

import (
	"strings"
	"testing"
)

const (
	pinEndpointKey = "contract:endpoint:tom-and-jerry:get:/api/score"
	scoreboardSvc  = "service:service:tom-and-jerry:scoreboardapi"
	arenaSvc       = "service:service:tom-and-jerry:arena-api"
	scoreSvcKey    = "code:provider:tom-and-jerry:fixtures/tom-and-jerry/scoreboard-api/services/scoreservice"
)

// TestViewPinEndpointC2: pinning an endpoint key into the C2 spec must BYPASS
// the EXPOSES_ENDPOINT absorption: the endpoint renders as a real VNode plus
// an EXPOSES_ENDPOINT edge to its owning service group (the exposer is
// collapsed into the group at C2).
func TestViewPinEndpointC2(t *testing.T) {
	st := openLiveDBCopy(t)

	spec := PresetSpec("c2", "tom-and-jerry", "")
	spec.Pin = []string{pinEndpointKey}

	view, err := BuildView(st, spec)
	if err != nil {
		t.Fatalf("BuildView: %v", err)
	}
	if len(view.Missing) != 0 {
		t.Fatalf("missing=%v want none", view.Missing)
	}

	var ep *VNode
	for i := range view.Nodes {
		if view.Nodes[i].Key == pinEndpointKey {
			ep = &view.Nodes[i]
		}
	}
	if ep == nil {
		t.Fatalf("pinned endpoint not rendered as VNode; nodes=%+v", view.Nodes)
	}
	if ep.Boundary {
		t.Errorf("pinned endpoint marked boundary — pins are first-class, not dimmed")
	}

	found := false
	for _, e := range view.Edges {
		if e.From == scoreboardSvc && e.To == pinEndpointKey && e.Kind == "EXPOSES_ENDPOINT" {
			found = true
		}
	}
	if !found {
		t.Errorf("missing EXPOSES_ENDPOINT edge %s -> %s; edges=%+v", scoreboardSvc, pinEndpointKey, view.Edges)
	}
}

// TestViewPinProviderBringsNeighborsC2: a pinned provider brings its 1-hop
// out-of-view neighbors as dimmed Boundary nodes with connecting edges
// (ScoreService -> USES_MODEL -> Score/BattleRecord; data layer is outside
// the C2 layer filter).
func TestViewPinProviderBringsNeighborsC2(t *testing.T) {
	st := openLiveDBCopy(t)

	spec := PresetSpec("c2", "tom-and-jerry", "")
	spec.Pin = []string{scoreSvcKey}

	view, err := BuildView(st, spec)
	if err != nil {
		t.Fatalf("BuildView: %v", err)
	}

	var pin *VNode
	boundaryByKey := map[string]VNode{}
	for _, n := range view.Nodes {
		if n.Key == scoreSvcKey {
			c := n
			pin = &c
		}
		if n.Boundary {
			boundaryByKey[n.Key] = n
		}
	}
	if pin == nil {
		t.Fatalf("pinned provider not rendered as VNode; nodes=%+v", view.Nodes)
	}

	wantNeighbors := []string{
		"data:model:tom-and-jerry:score",
		"data:model:tom-and-jerry:battlerecord",
	}
	for _, k := range wantNeighbors {
		bn, ok := boundaryByKey[k]
		if !ok {
			t.Errorf("1-hop neighbor %s not materialized as Boundary node; boundary=%v", k, boundaryByKey)
			continue
		}
		if !bn.Boundary {
			t.Errorf("neighbor %s not marked boundary:true", k)
		}
		edgeFound := false
		for _, e := range view.Edges {
			if e.From == scoreSvcKey && e.To == k && e.Kind == "USES_MODEL" {
				edgeFound = true
			}
		}
		if !edgeFound {
			t.Errorf("missing connecting USES_MODEL edge %s -> %s; edges=%+v", scoreSvcKey, k, view.Edges)
		}
	}
}

// findViaEdge returns the first synthesized "via …" edge between the two
// rendered keys, or nil.
func findViaEdge(v *View, from, to string) *VEdge {
	for i := range v.Edges {
		e := &v.Edges[i]
		if e.From == from && e.To == to && strings.HasPrefix(e.Label, "via") {
			return e
		}
	}
	return nil
}

// TestViewBypassConnectivity: filter.layers [code, service] hides the
// contract layer (topics). With bypass_layers:["contract"] the arena->
// scoreboard link that only exists through the battle-results topic must
// survive as a synthesized "via battle-results" edge between the two service
// groups; without the bypass selector it must be severed (plain hide).
func TestViewBypassConnectivity(t *testing.T) {
	st := openLiveDBCopy(t)

	mkSpec := func(bypass bool) ViewSpec {
		f := &FilterSpec{Layers: []string{"code", "service"}}
		if bypass {
			f.BypassLayers = []string{"contract"}
		}
		return ViewSpec{
			Scope:    ScopeSpec{Domain: "tom-and-jerry"},
			Filter:   f,
			Group:    GroupSpec{By: "service"},
			Collapse: true,
			Layout:   LayoutSpec{Preset: "c2"},
		}
	}

	on, err := BuildView(st, mkSpec(true))
	if err != nil {
		t.Fatalf("BuildView(bypass_layers): %v", err)
	}
	via := findViaEdge(on, arenaSvc, scoreboardSvc)
	if via == nil {
		t.Fatalf("bypass_layers:[contract] — no synthesized via-edge %s -> %s; edges=%+v", arenaSvc, scoreboardSvc, on.Edges)
	}
	if via.Label != "via battle-results" {
		t.Errorf("label=%q want %q", via.Label, "via battle-results")
	}
	if via.Kind != "PUBLISHES_TOPIC" {
		t.Errorf("kind=%q want PUBLISHES_TOPIC (first removed-crossing edge)", via.Kind)
	}
	if via.Weight < 1 {
		t.Errorf("weight=%d want >=1", via.Weight)
	}
	if len(via.CollapsedFrom) < 2 {
		t.Errorf("collapsed_from=%v want the underlying publish+consume edge keys", via.CollapsedFrom)
	}
	// Bypassed nodes must not render.
	for _, n := range on.Nodes {
		if n.Layer == "contract" {
			t.Errorf("bypass_layers:[contract] — contract node %s still rendered", n.Key)
		}
	}

	off, err := BuildView(st, mkSpec(false))
	if err != nil {
		t.Fatalf("BuildView(no bypass): %v", err)
	}
	if e := findViaEdge(off, arenaSvc, scoreboardSvc); e != nil {
		t.Errorf("no bypass — unexpected via-edge present: %+v", e)
	}
	// Without bypass there must be NO direct arena->scoreboard edge at all
	// (their only link is through the hidden topic).
	for _, e := range off.Edges {
		if e.From == arenaSvc && e.To == scoreboardSvc {
			t.Errorf("no bypass — unexpected edge %+v (only link is the hidden topic)", e)
		}
	}
}

// TestViewBypassTypeTopic: the per-TYPE bypass selector (the "via" tristate
// of a single node-type row). At the C2 preset spec, bypass_types:["topic"]
// removes the topic nodes but synthesizes via-edges between the services
// they connect; exclude_types:["topic"] (plain hide) severs those links.
func TestViewBypassTypeTopic(t *testing.T) {
	st := openLiveDBCopy(t)

	// via: topics removed-with-bypass
	spec := PresetSpec("c2", "tom-and-jerry", "")
	spec.Filter.BypassTypes = []string{"topic"}
	on, err := BuildView(st, spec)
	if err != nil {
		t.Fatalf("BuildView(bypass_types): %v", err)
	}
	for _, n := range on.Nodes {
		if n.Type == "topic" {
			t.Errorf("bypass_types:[topic] — topic node %s still rendered", n.Key)
		}
	}
	via := findViaEdge(on, arenaSvc, scoreboardSvc)
	if via == nil {
		t.Fatalf("bypass_types:[topic] — no via-edge %s -> %s; edges=%+v", arenaSvc, scoreboardSvc, on.Edges)
	}
	if via.Label != "via battle-results" {
		t.Errorf("label=%q want %q", via.Label, "via battle-results")
	}

	// hidden: topics plain-hidden — links severed
	spec = PresetSpec("c2", "tom-and-jerry", "")
	spec.Filter.ExcludeTypes = []string{"topic"}
	off, err := BuildView(st, spec)
	if err != nil {
		t.Fatalf("BuildView(exclude_types): %v", err)
	}
	for _, n := range off.Nodes {
		if n.Type == "topic" {
			t.Errorf("exclude_types:[topic] — topic node %s still rendered", n.Key)
		}
	}
	for _, e := range off.Edges {
		if e.From == arenaSvc && e.To == scoreboardSvc {
			t.Errorf("exclude_types:[topic] — unexpected edge %+v (only link is the hidden topic)", e)
		}
	}
}

// TestViewPinSurvivesNameFilter: a pinned node bypasses node-level filters —
// a name glob that excludes everything still renders the pinned endpoint.
func TestViewPinSurvivesNameFilter(t *testing.T) {
	st := openLiveDBCopy(t)

	spec := PresetSpec("c2", "tom-and-jerry", "")
	spec.Filter.Name = "zzz-no-match*"
	spec.Pin = []string{pinEndpointKey}

	view, err := BuildView(st, spec)
	if err != nil {
		t.Fatalf("BuildView: %v", err)
	}
	found := false
	for _, n := range view.Nodes {
		if n.Key == pinEndpointKey && !n.Boundary {
			found = true
		}
	}
	if !found {
		t.Errorf("pinned endpoint did not survive the name filter; nodes=%+v", view.Nodes)
	}
}
