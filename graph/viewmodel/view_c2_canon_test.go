package viewmodel

import (
	"strings"
	"testing"
)

// C2 canon: a topic lives inside its broker. With one declared broker and no
// BELONGS_TO evidence, topics are framed by that broker (inferred, and the
// group says so). The broker is a frame, not a card: topics stay VNodes and
// edges keep pointing at the topics. battle-queue is arena-api's own private
// queue (every publisher and consumer is arena-api's battle.queue provider)
// — c2 hides it with reason "private" (rev3 task 3), but it stays framed by
// the broker (Group is still set); battle-results has three distinct
// services on it and stays a drawn box.
func TestViewC2_TopicsFramedByBroker(t *testing.T) {
	st := openLiveDBCopy(t)
	view, err := BuildPreset(st, "c2", "tom-and-jerry", "")
	if err != nil {
		t.Fatalf("BuildPreset(c2): %v", err)
	}
	const kafka = "infra:broker:kafka:9092"
	var broker *VGroup
	for i := range view.Groups {
		if view.Groups[i].Key == kafka {
			broker = &view.Groups[i]
		}
	}
	if broker == nil {
		t.Fatalf("no broker group %q; groups=%+v", kafka, view.Groups)
	}
	if broker.Kind != "broker" || !broker.Inferred || broker.Note == "" {
		t.Errorf("broker group should be kind=broker, inferred, with a note: %+v", *broker)
	}
	topics := 0
	for _, n := range view.Nodes {
		if n.Key == kafka {
			t.Errorf("kafka must be a frame (group), not a node")
		}
		if n.Type == "topic" {
			topics++
			if n.Group != kafka {
				t.Errorf("topic %s group=%q want %q", n.Name, n.Group, kafka)
			}
			switch n.Name {
			case "battle-queue":
				if n.RenderMode != "hidden" || n.SalienceReason != "private" {
					t.Errorf("private topic %s render_mode=%q reason=%q want hidden/private", n.Name, n.RenderMode, n.SalienceReason)
				}
			default:
				if n.RenderMode != "box" {
					t.Errorf("topic %s render_mode=%q want box", n.Name, n.RenderMode)
				}
			}
		}
	}
	if topics != 2 {
		t.Errorf("topics=%d want 2", topics)
	}
	topicEdges := 0
	for _, e := range view.Edges {
		if e.To == kafka || e.From == kafka {
			t.Errorf("edge lifted to the broker frame: %+v (topics must stay the endpoints)", e)
		}
		if e.Kind == "PUBLISHES_TOPIC" || e.Kind == "CONSUMES_TOPIC" {
			topicEdges++
		}
	}
	if topicEdges == 0 {
		t.Errorf("no topic edges at C2; edges=%+v", view.Edges)
	}
}

// C2 canon: infrastructure nobody touches in this view is not an answer to
// "how do the containers talk" — it is hidden, and the view says why.
func TestViewC2_IsolatedInfraHiddenWithReason(t *testing.T) {
	st := openLiveDBCopy(t)
	view, err := BuildPreset(st, "c2", "tom-and-jerry", "")
	if err != nil {
		t.Fatalf("BuildPreset(c2): %v", err)
	}
	const redis = "infra:cache:redis:6379"
	found := false
	for _, n := range view.Nodes {
		if n.Key == redis {
			found = true
			if n.RenderMode != "hidden" {
				t.Errorf("redis render_mode=%q want hidden (no edges at C2)", n.RenderMode)
			}
		}
	}
	if !found {
		t.Fatalf("redis must stay in view.Nodes (hidden, revealable); nodes=%d", len(view.Nodes))
	}
	var entry *HiddenEntry
	for i := range view.Hidden {
		if view.Hidden[i].Type == "cache" {
			entry = &view.Hidden[i]
		}
	}
	if entry == nil || entry.Count != 1 || entry.Reason != "isolated" {
		t.Fatalf("hidden summary should list 1 cache as isolated: %+v", view.Hidden)
	}
}

// C1 keeps declared infra visible: the context question is "what do we
// depend on", and the manifest declares redis even without code evidence.
func TestViewC1_DeclaredInfraVisible(t *testing.T) {
	st := openLiveDBCopy(t)
	view, err := BuildPreset(st, "c1", "tom-and-jerry", "")
	if err != nil {
		t.Fatalf("BuildPreset(c1): %v", err)
	}
	for _, n := range view.Nodes {
		if n.Layer == "infra" && n.RenderMode != "box" {
			t.Errorf("%s render_mode=%q want box at C1", n.Name, n.RenderMode)
		}
	}
	if len(view.Hidden) != 0 {
		t.Errorf("C1 should hide nothing here: %+v", view.Hidden)
	}
}

// Hidden summary: every hidden node is accounted for by (layer, type, reason).
func TestViewHiddenSummary_CountsMatchNodes(t *testing.T) {
	st := openLiveDBCopy(t)
	view, err := BuildPreset(st, "c3", "tom-and-jerry", "")
	if err != nil {
		t.Fatalf("BuildPreset(c3): %v", err)
	}
	hidden := 0
	for _, n := range view.Nodes {
		if n.RenderMode == "hidden" {
			hidden++
		}
	}
	sum := 0
	for _, h := range view.Hidden {
		if h.Reason == "" || h.Type == "" {
			t.Errorf("hidden entry without type/reason: %+v", h)
		}
		sum += h.Count
	}
	if sum != hidden {
		t.Errorf("hidden summary sums to %d, nodes hidden=%d (%+v)", sum, hidden, view.Hidden)
	}
}

// C2 canon: one arrow per conversation. A provider calling an endpoint of
// another service and a direct CALLS_SERVICE are the same HTTP conversation
// at container altitude — one CALLS_SERVICE arrow whose weight counts both.
func TestViewC2_EndpointCallsMergeIntoServiceCalls(t *testing.T) {
	st := openLiveDBCopy(t)
	view, err := BuildPreset(st, "c2", "tom-and-jerry", "")
	if err != nil {
		t.Fatalf("BuildPreset(c2): %v", err)
	}
	const arena, jerry = "service:service:tom-and-jerry:arena-api", "service:service:tom-and-jerry:jerry-api"
	var calls *VEdge
	for i := range view.Edges {
		e := &view.Edges[i]
		if e.Kind == "CALLS_ENDPOINT" {
			t.Errorf("CALLS_ENDPOINT must be lifted into CALLS_SERVICE when collapsed: %+v", *e)
		}
		if e.From == arena && e.To == jerry && e.Kind == "CALLS_SERVICE" {
			calls = e
		}
	}
	if calls == nil {
		t.Fatalf("no arena→jerry CALLS_SERVICE edge; edges=%+v", view.Edges)
	}
	// jerry.client's direct CALLS_SERVICE to jerry-api twins its own two
	// resolved CALLS_ENDPOINT calls (fix round 1, rev3 task 2): it still
	// merges into this one arrow (collapsed_from keeps all 3 edges as
	// evidence), but weight counts the two real calls, not a third.
	if calls.Weight != 2 || len(calls.CollapsedFrom) != 3 {
		t.Errorf("arena→jerry should count 2 resolved endpoint calls (collapsed_from still 3), got weight=%d from=%v", calls.Weight, calls.CollapsedFrom)
	}
}

// Message flow reads publisher → topic → consumer. The graph stores
// CONSUMES_TOPIC as consumer → topic (a dependency); the view draws it in
// flow direction so an arrow means "messages go this way".
func TestViewC2_ConsumesDrawnInFlowDirection(t *testing.T) {
	st := openLiveDBCopy(t)
	view, err := BuildPreset(st, "c2", "tom-and-jerry", "")
	if err != nil {
		t.Fatalf("BuildPreset(c2): %v", err)
	}
	const results = "contract:topic:tom-and-jerry:battle-results"
	const scoreboard = "service:service:tom-and-jerry:scoreboardapi"
	found := false
	for _, e := range view.Edges {
		if e.Kind != "CONSUMES_TOPIC" {
			continue
		}
		if !strings.HasPrefix(e.From, "contract:topic:") {
			t.Errorf("CONSUMES_TOPIC must start at the topic: %+v", e)
		}
		if e.From == results && e.To == scoreboard {
			found = true
		}
	}
	if !found {
		t.Errorf("expected battle-results → scoreboardapi CONSUMES_TOPIC; edges=%+v", view.Edges)
	}
}
