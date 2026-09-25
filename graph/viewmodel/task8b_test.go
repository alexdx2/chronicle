package viewmodel

import (
	"strings"
	"testing"

	"github.com/alexdx2/chronicle-core/store"
)

// Rev3 task 8b: C1 ("who is outside and how do we touch them") draws the
// foreign neighbours task 8a already computes — one collapsed card PER
// FOREIGN DOMAIN (not per node, unlike c2/services' one-card-per-neighbour),
// keyed and labelled by that domain, note "other domain". Arrows fold per
// domain: calls -> "HTTP ×N" (twin-aware, same weight accounting as
// c2/services), topics -> "events: <topic>" (exactly one) or "events ×N"
// (several DISTINCT topics), direction following the relationship
// (calls/publications system -> foreign, subscriptions foreign -> system,
// message-flow direction as c2 already draws).

func TestBuildViewC1_ForeignDomainCards(t *testing.T) {
	st, _, _, _, _, _, _, _ := synthForeignStore(t)

	// An unrelated domain with a real node but NO edge at all to A: must
	// never get a c1 card. foreignByID only ever admits the OTHER END of
	// an actual crossing edge, so this is a structural guarantee, not an
	// omission this fixture happens not to exercise — proved directly
	// rather than just inferred from what IS drawn.
	rev, err := st.CreateRevision("Z", "", "unrelated-domain", "manual", "full", "{}")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.UpsertNode(store.NodeRow{NodeKey: "service:service:Z:lonely-svc", Layer: "service", NodeType: "service",
		DomainKey: "Z", Name: "lonely-svc", Status: "active", Confidence: 0.9, Freshness: 1, TrustScore: 0.9, Metadata: "{}", LastSeenRevisionID: rev}); err != nil {
		t.Fatal(err)
	}

	v, err := BuildPreset(st, "c1", "A", "")
	if err != nil {
		t.Fatalf("c1: %v", err)
	}

	var domB, domC, domZ *VGroup
	for i := range v.Groups {
		switch v.Groups[i].Key {
		case "B":
			domB = &v.Groups[i]
		case "C":
			domC = &v.Groups[i]
		case "Z":
			domZ = &v.Groups[i]
		}
	}
	if domZ != nil {
		t.Errorf("domain Z has no neighbour of A at all and must not get a card: %+v", domZ)
	}
	// Exactly the system box (A) + the two domains actually touched (B via
	// the twin + topic, C via the un-twinned endpoint) — nothing spurious.
	if len(v.Groups) != 3 {
		t.Fatalf("expected 3 groups (system box A + domain cards B, C), got %d: %+v", len(v.Groups), v.Groups)
	}

	if domB == nil {
		t.Fatalf("expected a card for domain B: groups=%+v", v.Groups)
	}
	if domB.Kind != "domain" || domB.Note != "other domain" {
		t.Errorf("domain B card should be Kind domain, Note \"other domain\": %+v", domB)
	}
	if domB.Stats["services"] != 1 {
		t.Errorf("domain B services stat = %d, want 1 (spectators-api; both twin-lifted endpoints count under it, not separately): %+v", domB.Stats["services"], domB.Stats)
	}
	if domB.Stats["topics"] != 1 {
		t.Errorf("domain B topics stat = %d, want 1: %+v", domB.Stats["topics"], domB.Stats)
	}
	if _, ok := domB.Stats["endpoints"]; ok {
		t.Errorf("domain B should carry no separate endpoints stat (both endpoints twin-lifted to b-svc): %+v", domB.Stats)
	}

	// Calls fold system(A) -> foreign domain card, twin-aware: two
	// resolved endpoint calls + the covered CALLS_SERVICE twin = ×2 (the
	// exact case the task names).
	call := findEdge(v, "A", "B", "CALLS_SERVICE")
	if call == nil {
		t.Fatalf("expected A -> B CALLS_SERVICE arrow; edges=%+v", v.Edges)
	}
	if call.Weight != 2 {
		t.Errorf("A -> B weight = %d, want 2 (two endpoint calls, the covered service twin adds no weight): %+v", call.Weight, call)
	}
	if got := arrowLabel(call); got != "HTTP ×2" {
		t.Errorf("label = %q, want %q", got, "HTTP ×2")
	}

	// Subscriptions draw message-flow direction: foreign domain -> system,
	// naming the one topic.
	sub := findEdge(v, "B", "A", "CONSUMES_TOPIC")
	if sub == nil {
		t.Fatalf("expected B -> A CONSUMES_TOPIC arrow (message-flow direction); edges=%+v", v.Edges)
	}
	if sub.Label != "events: b-topic" {
		t.Errorf("label = %q, want %q", sub.Label, "events: b-topic")
	}
	if sub.Weight != 1 {
		t.Errorf("weight = %d, want 1 (one distinct topic — Weight here counts topics, not raw edges)", sub.Weight)
	}

	// Domain C: the un-twinned endpoint call still makes it a neighbour
	// (its own card, its own "endpoints" stat) and its call still folds
	// into the domain's one HTTP arrow (C1 answers "do we talk to C over
	// HTTP", not which unresolved endpoint).
	if domC == nil {
		t.Fatalf("expected a card for domain C: groups=%+v", v.Groups)
	}
	if domC.Stats["endpoints"] != 1 {
		t.Errorf("domain C endpoints stat = %d, want 1 (c-ep has no twin): %+v", domC.Stats["endpoints"], domC.Stats)
	}
	if _, ok := domC.Stats["services"]; ok {
		t.Errorf("domain C should carry no services stat: %+v", domC.Stats)
	}
	callC := findEdge(v, "A", "C", "CALLS_SERVICE")
	if callC == nil {
		t.Fatalf("expected A -> C CALLS_SERVICE arrow; edges=%+v", v.Edges)
	}
	if callC.Weight != 1 {
		t.Errorf("A -> C weight = %d, want 1", callC.Weight)
	}
	if got := arrowLabel(callC); got != "HTTP" {
		t.Errorf("label = %q, want %q (no ×N at weight 1)", got, "HTTP")
	}

	txt := RenderText(v)
	// rev3 fix F6: Kind "domain" and Note "other domain" used to print
	// both ("## B (domain, other domain)"), repeating the word "domain" —
	// the same shape task 7 already fixed once, for a different label.
	// The note alone says everything the kind would.
	if !strings.Contains(txt, "## B (other domain)") {
		t.Errorf("text should frame domain B's card without repeating \"domain\":\n%s", txt)
	}
	if strings.Contains(txt, "## B (domain, other domain)") {
		t.Errorf("text must not repeat the word \"domain\":\n%s", txt)
	}
	if !strings.Contains(txt, "A → B: HTTP ×2") {
		t.Errorf("text should draw the calls arrow:\n%s", txt)
	}
	if !strings.Contains(txt, "B → A: events: b-topic") {
		t.Errorf("text should draw the events arrow:\n%s", txt)
	}
	mm := RenderMermaid(v)
	if !strings.Contains(mm, "other domain") {
		t.Errorf("mermaid should carry the domain card's note:\n%s", mm)
	}
	if !strings.Contains(mm, "events: b-topic") {
		t.Errorf("mermaid should carry the events label:\n%s", mm)
	}
}

// TestBuildViewC1_SeveralTopicsFoldIntoEventsCount: "events ×N" means N
// DISTINCT topics, not raw edge count — a second consumed topic from the
// same foreign domain must read "events ×2", not restate the first name.
func TestBuildViewC1_SeveralTopicsFoldIntoEventsCount(t *testing.T) {
	st, aCompKey, _, _, _, _, bTopicKey, _ := synthForeignStore(t)
	rev, err := st.CreateRevision("B", "", "second-topic", "manual", "full", "{}")
	if err != nil {
		t.Fatal(err)
	}
	second := "contract:topic:B:b-topic-2"
	secondID, err := st.UpsertNode(store.NodeRow{NodeKey: second, Layer: "contract", NodeType: "topic", DomainKey: "B",
		Name: "b-topic-2", Status: "active", Confidence: 0.9, Freshness: 1, TrustScore: 0.9, Metadata: "{}", LastSeenRevisionID: rev})
	if err != nil {
		t.Fatal(err)
	}
	aComp, err := st.GetNodeByKey(aCompKey)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.UpsertEdge(store.EdgeRow{EdgeKey: aCompKey + "->" + second + ":CONSUMES_TOPIC", FromNodeID: aComp.NodeID, ToNodeID: secondID,
		EdgeType: "CONSUMES_TOPIC", DerivationKind: "hard", Active: true, Confidence: 0.9, Freshness: 1, TrustScore: 0.9, Metadata: "{}", LastSeenRevisionID: rev}); err != nil {
		t.Fatal(err)
	}
	_ = bTopicKey

	v, err := BuildPreset(st, "c1", "A", "")
	if err != nil {
		t.Fatalf("c1: %v", err)
	}
	sub := findEdge(v, "B", "A", "CONSUMES_TOPIC")
	if sub == nil {
		t.Fatalf("expected B -> A CONSUMES_TOPIC arrow; edges=%+v", v.Edges)
	}
	if sub.Label != "events" {
		t.Errorf("label = %q, want plain %q (arrowLabel appends the ×N suffix)", sub.Label, "events")
	}
	if sub.Weight != 2 {
		t.Errorf("weight = %d, want 2 (two DISTINCT topics)", sub.Weight)
	}
	if got := arrowLabel(sub); got != "events ×2" {
		t.Errorf("rendered label = %q, want %q", got, "events ×2")
	}
}

// TestBuildViewC1_NoForeignNeighbours_NoDomainCards: a single-domain store
// (no cross-domain edges at all — the tom-and-jerry shape) draws no
// foreign-domain cards; c1 looks exactly as it did before this task.
func TestBuildViewC1_NoForeignNeighbours_NoDomainCards(t *testing.T) {
	st := synthStore(t)
	v, err := BuildPreset(st, "c1", "d", "")
	if err != nil {
		t.Fatalf("c1: %v", err)
	}
	for _, g := range v.Groups {
		if g.Kind == "domain" && g.Key != "d" {
			t.Errorf("no foreign-domain card should appear in a single-domain store: %+v", g)
		}
	}
}
