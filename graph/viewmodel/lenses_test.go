package viewmodel

import (
	"strings"
	"testing"
)

// API lens: who provides an endpoint and who calls it. Endpoints framed by
// the service that exposes them; the callers are the client components,
// framed by THEIR service, with the real CALLS_ENDPOINT arrows. Code that
// does not touch the contract layer stays out.
func TestAPILens_ProvidersAndCallers(t *testing.T) {
	st := openLiveDBCopy(t)
	v, err := BuildPreset(st, "api", "tom-and-jerry", "")
	if err != nil {
		t.Fatalf("api: %v", err)
	}
	byKey := map[string]VNode{}
	for _, n := range v.Nodes {
		byKey[n.Key] = n
	}
	const jerryStatus = "contract:endpoint:tom-and-jerry:get:/jerry/status"
	const jerryClient = "code:provider:tom-and-jerry:fixtures/tom-and-jerry/arena-api/src/arena/jerry.client"
	if n, ok := byKey[jerryStatus]; !ok || n.Group != "service:service:tom-and-jerry:jerry-api" {
		t.Errorf("endpoint should be framed by jerry-api: %+v", n)
	}
	if n, ok := byKey[jerryClient]; !ok || n.Group != "service:service:tom-and-jerry:arena-api" {
		t.Errorf("the caller jerry.client should be in the arena-api frame: %+v (ok=%v)", n, ok)
	}
	if _, ok := byKey["code:provider:tom-and-jerry:fixtures/tom-and-jerry/arena-api/src/arena/battle.guard"]; ok {
		t.Errorf("battle.guard touches no contract — it must not be in the API lens")
	}
	if _, ok := byKey["code:controller:tom-and-jerry:fixtures/tom-and-jerry/arena-api/src/arena/arena.controller"]; ok {
		t.Errorf("controllers only EXPOSE endpoints (hierarchy) — they must not appear as callers")
	}
	found := false
	for _, e := range v.Edges {
		if e.From == jerryClient && e.To == jerryStatus && e.Kind == "CALLS_ENDPOINT" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected jerry.client → GET /jerry/status; edges=%d", len(v.Edges))
	}
}

// Data lens: models framed by the service that owns them; models nobody
// owns land in an explicit "owner unknown" frame rather than floating.
func TestDataLens_ModelsByOwner(t *testing.T) {
	st := openLiveDBCopy(t)
	v, err := BuildPreset(st, "data", "tom-and-jerry", "")
	if err != nil {
		t.Fatalf("data: %v", err)
	}
	groups := map[string]VGroup{}
	for _, g := range v.Groups {
		groups[g.Key] = g
	}
	byName := map[string]VNode{}
	for _, n := range v.Nodes {
		byName[n.Name] = n
	}
	if n := byName["BattleEvent"]; n.Group != "service:service:tom-and-jerry:arena-api" {
		t.Errorf("BattleEvent lives in arena-api's prisma schema; group=%q", n.Group)
	}
	for _, n := range v.Nodes {
		if n.RenderMode == "hidden" {
			continue
		}
		if n.Group == "" {
			t.Errorf("%s floats without a frame — data nodes get an owner or the 'owner unknown' frame", n.Name)
		}
	}
	if g, ok := groups["unowned"]; ok && g.Name != "owner unknown" {
		t.Errorf("unowned frame should be named 'owner unknown': %+v", g)
	}
}

// Flows are a table, not a graph: service → method/path → handler → flow.
func TestFlowsLens_IsATable(t *testing.T) {
	st := openLiveDBCopy(t)
	v, err := BuildPreset(st, "flows", "tom-and-jerry", "")
	if err != nil {
		t.Fatalf("flows: %v", err)
	}
	if v.Table == nil || len(v.Table.Rows) != 19 {
		t.Fatalf("flows should be a 19-row table: %+v", v.Table)
	}
	if strings.Join(v.Table.Columns, "|") != "service|endpoint|handler|flow" {
		t.Errorf("columns=%v", v.Table.Columns)
	}
	// Sorted by service, then endpoint; the arena row names its handler.
	first := v.Table.Rows[0]
	if first[0] != "arena-api" || !strings.HasPrefix(first[1], "GET /arena/") || first[2] != "arena.controller" {
		t.Errorf("first row=%v", first)
	}
	if len(v.Edges) != 0 {
		t.Errorf("a table view draws no arrows")
	}
	if m := RenderMermaid(v); m != "" {
		t.Errorf("a list view has no picture, got mermaid:\n%s", m)
	}
	if md := RenderMarkdown(v); !strings.Contains(md, "| service | endpoint | handler | flow |") {
		t.Errorf("a table view renders as a markdown table, got:\n%s", md)
	}
	txt := RenderText(v)
	if !strings.Contains(txt, "## arena-api") || !strings.Contains(txt, "GET /arena/score") {
		t.Errorf("text should list flows by service:\n%s", txt)
	}
}

// Evidence is a line only on an absolute signal: low confidence, no
// evidence, synthesized, non-active status. A healthy node says nothing.
func TestProvenanceText_OnlyOnSignal(t *testing.T) {
	if s := nodeProvenanceText(&VNode{Confidence: 0.9, Trust: 0.9, EvidenceCount: 2}); s != "" {
		t.Errorf("healthy node should carry no evidence line, got %q", s)
	}
	cases := map[string]VNode{
		"low":     {Confidence: 0.4, Trust: 0.4, EvidenceCount: 2},
		"none":    {Confidence: 0.9, Trust: 0.9, EvidenceCount: 0},
		"stale":   {Confidence: 0.9, Trust: 0.9, EvidenceCount: 1, Status: "stale"},
		"unknown": {Confidence: 0.9, Trust: 0.9, EvidenceCount: 1, Status: "unresolved"},
	}
	for name, n := range cases {
		if s := nodeProvenanceText(&n); s == "" {
			t.Errorf("%s: expected a signal line", name)
		}
	}
	if s := edgeProvenanceText(&VEdge{Derivation: "hard", Confidence: 0.85, Trust: 0.85, EvidenceCount: 3}); s != "" {
		t.Errorf("healthy arrow should carry no evidence line, got %q", s)
	}
	for name, e := range map[string]VEdge{
		"inferred":    {Derivation: "inferred", Confidence: 0.9, Trust: 0.9, EvidenceCount: 1},
		"synthesized": {Derivation: "synthesized"},
		"zero":        {Derivation: "linked", Confidence: 0, Trust: 0, EvidenceCount: 1},
		"noevidence":  {Derivation: "hard", Confidence: 0.9, Trust: 0.9, EvidenceCount: 0},
	} {
		if s := edgeProvenanceText(&e); s == "" {
			t.Errorf("%s: expected a signal line", name)
		}
	}
}

// Container stats speak the container's language: a service says what it
// exposes, publishes and calls; a broker frame says how many topics,
// publishers and subscribers it has; declared infra with no evidence says so.
func TestC2_StatsAreViewSpecific(t *testing.T) {
	st := openLiveDBCopy(t)
	v, err := BuildPreset(st, "c2", "tom-and-jerry", "")
	if err != nil {
		t.Fatalf("c2: %v", err)
	}
	for _, g := range v.Groups {
		switch g.Key {
		case "service:service:tom-and-jerry:arena-api":
			if g.Stats["publishes"] != 2 || g.Stats["calls"] != 2 || g.Stats["endpoints"] != 5 {
				t.Errorf("arena-api stats=%v want endpoints 5, publishes 2, calls 2", g.Stats)
			}
			if _, ok := g.Stats["nodes"]; ok {
				t.Errorf("'nodes' is engine jargon, not a container stat: %v", g.Stats)
			}
		case "infra:broker:kafka:9092":
			if g.Stats["topics"] != 2 || g.Stats["publishers"] != 1 || g.Stats["subscribers"] != 3 {
				t.Errorf("kafka stats=%v want topics 2, publishers 1, subscribers 3", g.Stats)
			}
		}
	}
}

func TestC1_DeclaredInfraWithoutEvidenceSaysSo(t *testing.T) {
	st := openLiveDBCopy(t)
	v, err := BuildPreset(st, "c1", "tom-and-jerry", "")
	if err != nil {
		t.Fatalf("c1: %v", err)
	}
	labels := map[string]string{}
	for _, e := range v.Edges {
		if e.Kind == "uses_infra" {
			labels[e.To] = e.Label
		}
	}
	if labels["infra:cache:redis:6379"] != "declared, no evidence" {
		t.Errorf("redis has no graph evidence of use: label=%q", labels["infra:cache:redis:6379"])
	}
	if labels["infra:broker:kafka:9092"] != "events" {
		t.Errorf("kafka carries topics: label=%q", labels["infra:broker:kafka:9092"])
	}
}

// C3: a component the scan attached to no module sits in a frame that says so.
func TestC3_ModulelessFrameIsExplained(t *testing.T) {
	st := openLiveDBCopy(t)
	v, err := BuildPreset(st, "c3", "tom-and-jerry", "arena-api")
	if err != nil {
		t.Fatalf("c3: %v", err)
	}
	for _, g := range v.Groups {
		if g.Key == "service:service:tom-and-jerry:arena-api" && g.Note != "not in any module" {
			t.Errorf("fallback frame should say 'not in any module': %+v", g)
		}
	}
}
