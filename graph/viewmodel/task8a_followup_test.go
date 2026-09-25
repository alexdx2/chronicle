package viewmodel

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/alexdx2/chronicle-core/store"
)

// Rev3 task 8a follow-up: federation data exposed three gaps that predate
// task 8a itself but only became visible once a real cross-repo store
// (orders/analytics) exercised them.

// ---- fix 1: a topic with a publisher and no known consumer is an
// outbound event, not a self-conversation ----

func synthPublisherOnlyTopicStore(t *testing.T) (st *store.Store, svcKey, topicKey string) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "pubonly.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	rev, err := st.CreateRevision("d", "", "publisher-only-topic", "manual", "full", "{}")
	if err != nil {
		t.Fatal(err)
	}
	mk := func(key, layer, ntype, name, file string) int64 {
		id, err := st.UpsertNode(store.NodeRow{NodeKey: key, Layer: layer, NodeType: ntype, DomainKey: "d", Name: name,
			FilePath: file, Status: "active", Confidence: 0.9, Freshness: 1, TrustScore: 0.9, Metadata: "{}", LastSeenRevisionID: rev})
		if err != nil {
			t.Fatalf("upsert %s: %v", key, err)
		}
		return id
	}
	edge := func(from, to int64, fk, tk, typ string) {
		if _, err := st.UpsertEdge(store.EdgeRow{EdgeKey: fk + "->" + tk + ":" + typ, FromNodeID: from, ToNodeID: to, EdgeType: typ,
			DerivationKind: "hard", Active: true, Confidence: 0.9, Freshness: 1, TrustScore: 0.9, Metadata: "{}", LastSeenRevisionID: rev}); err != nil {
			t.Fatalf("edge %s: %v", typ, err)
		}
	}
	svcKey = "service:service:d:pub-svc"
	topicKey = "contract:topic:d:outbound-event"
	mk(svcKey, "service", "service", "pub-svc", "pub-svc/package.json")
	prov := mk("code:provider:d:pub-svc/src/producer", "code", "provider", "producer", "pub-svc/src/producer.ts")
	topicID := mk(topicKey, "contract", "topic", "outbound-event", "")
	mk("infra:broker:d:bus", "infra", "broker", "bus", "") // the only declared broker: frames the topic (same as any other visible topic)
	edge(prov, topicID, "code:provider:d:pub-svc/src/producer", topicKey, "PUBLISHES_TOPIC")
	return st, svcKey, topicKey
}

// TestBuildViewC2_PublisherOnlyTopicStaysVisible: orders' order-created has
// a publisher (orders-api) and no consumer in that store at all (its
// consumer lives in the analytics repo) — the old "every publisher AND
// consumer belong to one service" rule folded "the graph hasn't shown a
// consumer" into "must be private" the moment exactly one service touched
// the topic. It must stay visible, framed, with its pub arrow.
func TestBuildViewC2_PublisherOnlyTopicStaysVisible(t *testing.T) {
	st, svcKey, topicKey := synthPublisherOnlyTopicStore(t)
	v, err := BuildPreset(st, "c2", "d", "")
	if err != nil {
		t.Fatalf("c2: %v", err)
	}
	topic := findNode(v, topicKey)
	if topic == nil {
		t.Fatalf("outbound-event should be drawn; nodes=%+v", v.Nodes)
	}
	if topic.RenderMode == "hidden" {
		t.Errorf("a publisher-only topic must stay visible, not hidden as private: %+v", topic)
	}
	if topic.Group == "" {
		t.Errorf("it should still sit framed by the broker/service the same as before rev3: %+v", topic)
	}
	if e := findEdge(v, svcKey, topicKey, "PUBLISHES_TOPIC"); e == nil {
		t.Errorf("pub-svc -> outbound-event PUBLISHES_TOPIC arrow should be drawn: edges=%+v", v.Edges)
	}
	for _, h := range v.Hidden {
		if h.Reason == "private" {
			t.Errorf("no topic should be reported private here: %+v", h)
		}
	}
	txt := RenderText(v)
	if !strings.Contains(txt, "outbound-event") {
		t.Errorf("text should mention the topic:\n%s", txt)
	}
	if !strings.Contains(txt, "pub-svc → outbound-event: pub") {
		t.Errorf("text should draw the pub arrow:\n%s", txt)
	}
}

// TestBuildViewC2_PrivateTopicStillHidden_Regression pins the ORIGINAL
// rev3 task 3 case (synthPrivateTopicStore, c2_noise_test.go) stays exactly
// as before: a topic with BOTH a publisher and a consumer, both the same
// service, is still private and hidden. (The full existing test suite
// already re-runs TestBuildViewC2_PrivateTopicHiddenSharedTopicFramed
// unmodified; this is an explicit second pin at the tightened code path
// so the fix's own test file carries the negative case next to the
// positive one above.)
func TestBuildViewC2_PrivateTopicStillHidden_Regression(t *testing.T) {
	st, _, privateTopicKey, sharedTopicKey := synthPrivateTopicStore(t)
	v, err := BuildPreset(st, "c2", "d", "")
	if err != nil {
		t.Fatalf("c2: %v", err)
	}
	priv := findNode(v, privateTopicKey)
	if priv == nil || priv.RenderMode != "hidden" || priv.SalienceReason != "private" {
		t.Fatalf("a topic BOTH published and consumed by the same service must stay private: %+v", priv)
	}
	shared := findNode(v, sharedTopicKey)
	if shared == nil || shared.RenderMode == "hidden" {
		t.Fatalf("a topic published and consumed by two DIFFERENT services stays visible: %+v", shared)
	}
}

// ---- fix 2: an endpoint nobody owns has nothing to badge onto at
// container altitude ----

func synthUnownedEndpointStore(t *testing.T) (st *store.Store, svcKey, callerKey, orphanKey string) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "unowned.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	rev, err := st.CreateRevision("d", "", "unowned-endpoint", "manual", "full", "{}")
	if err != nil {
		t.Fatal(err)
	}
	mk := func(key, layer, ntype, name, file string) int64 {
		id, err := st.UpsertNode(store.NodeRow{NodeKey: key, Layer: layer, NodeType: ntype, DomainKey: "d", Name: name,
			FilePath: file, Status: "active", Confidence: 0.9, Freshness: 1, TrustScore: 0.9, Metadata: "{}", LastSeenRevisionID: rev})
		if err != nil {
			t.Fatalf("upsert %s: %v", key, err)
		}
		return id
	}
	edge := func(from, to int64, fk, tk, typ string) {
		if _, err := st.UpsertEdge(store.EdgeRow{EdgeKey: fk + "->" + tk + ":" + typ, FromNodeID: from, ToNodeID: to, EdgeType: typ,
			DerivationKind: "hard", Active: true, Confidence: 0.9, Freshness: 1, TrustScore: 0.9, Metadata: "{}", LastSeenRevisionID: rev}); err != nil {
			t.Fatalf("edge %s: %v", typ, err)
		}
	}
	svcKey = "service:service:d:svc-x"
	callerKey = "code:provider:d:svc-x/src/client"
	orphanKey = "contract:endpoint:d:get:/orphan"
	mk(svcKey, "service", "service", "svc-x", "svc-x/package.json")
	callerID := mk(callerKey, "code", "provider", "client", "svc-x/src/client.ts")
	orphanID := mk(orphanKey, "contract", "endpoint", "GET /orphan", "")
	// The endpoint IS touched (a real caller), it is simply never exposed
	// by anything — the exact shape of orders-api's GET /orders: no
	// EXPOSES_ENDPOINT edge anywhere in the graph.
	edge(callerID, orphanID, callerKey, orphanKey, "CALLS_ENDPOINT")
	return st, svcKey, callerKey, orphanKey
}

// TestBuildViewC2_UnownedEndpointHidden: on c2, an endpoint with no
// resolvable owner reaches VNode status (it is touched — a real caller —
// so it is not simply absent) and is then hidden with SalienceReason
// "unowned", accounted for in View.Hidden, its arrow dropped from
// View.Edges the same way a private topic's arrow is.
func TestBuildViewC2_UnownedEndpointHidden(t *testing.T) {
	st, _, callerKey, orphanKey := synthUnownedEndpointStore(t)
	v, err := BuildPreset(st, "c2", "d", "")
	if err != nil {
		t.Fatalf("c2: %v", err)
	}
	ep := findNode(v, orphanKey)
	if ep == nil {
		t.Fatalf("the endpoint should still be a VNode (revealable), just hidden: nodes=%+v", v.Nodes)
	}
	if ep.RenderMode != "hidden" || ep.SalienceReason != "unowned" {
		t.Errorf("unowned endpoint should be hidden with reason unowned: %+v", ep)
	}
	found := false
	for _, h := range v.Hidden {
		if h.Type == "endpoint" && h.Reason == "unowned" && h.Count == 1 {
			found = true
		}
	}
	if !found {
		t.Errorf("hidden summary missing the unowned endpoint: %+v", v.Hidden)
	}
	if e := findEdge(v, callerKey, orphanKey, "CALLS_ENDPOINT"); e != nil {
		t.Errorf("the arrow into a hidden endpoint should not ride in View.Edges either: %+v", e)
	}
	txt := RenderText(v)
	if strings.Contains(txt, "- GET /orphan [") {
		t.Errorf("text should not draw the hidden endpoint as a free node:\n%s", txt)
	}
	// rev3 fix F5: the Hidden summary now names it — that is the fix, not
	// a regression of the "not drawn" guarantee above.
	if !strings.Contains(txt, "unowned: GET /orphan") {
		t.Errorf("hidden summary should name GET /orphan:\n%s", txt)
	}
}

// TestBuildViewServices_UnownedEndpointNeverSurfaces: the "services" preset
// filters to layers=[service, code] — EVERY endpoint, owned or not, is
// already excluded before this task's new rule would ever run (it never
// becomes a VNode there at all, unlike c2's pill/free-node path). This
// pins that the new c2/services gate is harmless on "services": the
// endpoint still never leaks into the view, just via the pre-existing
// layer filter (Hidden reason "filter:layers"), not "unowned".
func TestBuildViewServices_UnownedEndpointNeverSurfaces(t *testing.T) {
	st, _, _, orphanKey := synthUnownedEndpointStore(t)
	v, err := BuildPreset(st, "services", "d", "")
	if err != nil {
		t.Fatalf("services: %v", err)
	}
	if ep := findNode(v, orphanKey); ep != nil {
		t.Errorf("services never draws any endpoint (its own layer filter excludes contract layer entirely): %+v", ep)
	}
	txt := RenderText(v)
	if strings.Contains(txt, "- GET /orphan [") {
		t.Errorf("text should not draw the endpoint as a free node:\n%s", txt)
	}
	// rev3 fix F5: the Hidden summary now names it — that is the fix, not
	// a regression of the "not drawn" guarantee above.
	if !strings.Contains(txt, "filter:layers: GET /orphan") {
		t.Errorf("hidden summary should name GET /orphan:\n%s", txt)
	}
}

// TestBuildViewAPI_UnownedEndpointStillShown: the api lens exists
// specifically to say what nobody owns — the new c2/services-only rule
// must not touch it.
func TestBuildViewAPI_UnownedEndpointStillShown(t *testing.T) {
	st, _, _, orphanKey := synthUnownedEndpointStore(t)
	v, err := BuildPreset(st, "api", "d", "")
	if err != nil {
		t.Fatalf("api: %v", err)
	}
	ep := findNode(v, orphanKey)
	if ep == nil {
		t.Fatalf("api lens should still draw the unowned endpoint: nodes=%+v", v.Nodes)
	}
	if ep.RenderMode == "hidden" {
		t.Errorf("api lens must not hide it: %+v", ep)
	}
	txt := RenderText(v)
	if !strings.Contains(txt, "GET /orphan") {
		t.Errorf("api lens text should mention it:\n%s", txt)
	}
}

// ---- fix 3: a foreign card names its domain once, not three times ----

func synthForeignStatusStore(t *testing.T) (st *store.Store, extSvcKey, staleSvcKey string) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "status.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	rev, err := st.CreateRevision("X", "", "foreign-status", "manual", "full", "{}")
	if err != nil {
		t.Fatal(err)
	}
	mk := func(key, layer, ntype, domain, name, status, file string) int64 {
		id, err := st.UpsertNode(store.NodeRow{NodeKey: key, Layer: layer, NodeType: ntype, DomainKey: domain, Name: name,
			FilePath: file, Status: status, Confidence: 0.9, Freshness: 1, TrustScore: 0.9, Metadata: "{}", LastSeenRevisionID: rev})
		if err != nil {
			t.Fatalf("upsert %s: %v", key, err)
		}
		return id
	}
	edge := func(from, to int64, fk, tk, typ string) {
		if _, err := st.UpsertEdge(store.EdgeRow{EdgeKey: fk + "->" + tk + ":" + typ, FromNodeID: from, ToNodeID: to, EdgeType: typ,
			DerivationKind: "hard", Active: true, Confidence: 0.9, Freshness: 1, TrustScore: 0.9, Metadata: "{}", LastSeenRevisionID: rev}); err != nil {
			t.Fatalf("edge %s: %v", typ, err)
		}
	}
	mk("service:service:X:svc-a", "service", "service", "X", "svc-a", "active", "svc-a/package.json")
	compID := mk("code:provider:X:svc-a/src/comp", "code", "provider", "X", "comp", "active", "svc-a/src/comp.ts")
	extSvcKey = "service:service:Y:ext-svc"
	staleSvcKey = "service:service:Z:stale-svc"
	extID := mk(extSvcKey, "service", "service", "Y", "ext-svc", "external", "")
	staleID := mk(staleSvcKey, "service", "service", "Z", "stale-svc", "stale", "")
	edge(compID, extID, "code:provider:X:svc-a/src/comp", extSvcKey, "CALLS_SERVICE")
	edge(compID, staleID, "code:provider:X:svc-a/src/comp", staleSvcKey, "CALLS_SERVICE")
	return st, extSvcKey, staleSvcKey
}

func lineContaining(text, substr string) string {
	for _, line := range strings.Split(text, "\n") {
		if strings.Contains(line, substr) {
			return line
		}
	}
	return ""
}

func TestRenderText_ForeignCardDomainOnce(t *testing.T) {
	st, _, _ := synthForeignStatusStore(t)
	v, err := BuildPreset(st, "c2", "X", "")
	if err != nil {
		t.Fatalf("c2: %v", err)
	}
	txt := RenderText(v)

	extLine := lineContaining(txt, "ext-svc")
	if extLine == "" {
		t.Fatalf("expected an ext-svc line:\n%s", txt)
	}
	if !strings.Contains(extLine, "(domain Y)") {
		t.Errorf("ext-svc line should carry its domain: %q", extLine)
	}
	if strings.Contains(extLine, "outside scope") {
		t.Errorf("a foreign card's domain tag already says it sits outside the scope, must not also print \"(outside scope)\": %q", extLine)
	}
	if strings.Contains(extLine, "external") {
		t.Errorf("a foreign card's \"external\" status is redundant with its domain tag and must not print: %q", extLine)
	}

	staleLine := lineContaining(txt, "stale-svc")
	if staleLine == "" {
		t.Fatalf("expected a stale-svc line:\n%s", txt)
	}
	if !strings.Contains(staleLine, "(domain Z)") {
		t.Errorf("stale-svc line should carry its domain: %q", staleLine)
	}
	if !strings.Contains(staleLine, "stale") {
		t.Errorf("a non-\"external\" status (stale) is information the domain tag does not carry and must still print: %q", staleLine)
	}
}

func TestRenderMermaid_ForeignCardNoOutsideOrExternalLine(t *testing.T) {
	st, _, _ := synthForeignStatusStore(t)
	v, err := BuildPreset(st, "c2", "X", "")
	if err != nil {
		t.Fatalf("c2: %v", err)
	}
	mm := RenderMermaid(v)
	if !strings.Contains(mm, "domain Y") {
		t.Errorf("mermaid should carry the foreign card's domain line:\n%s", mm)
	}
	if strings.Contains(mm, "outside") {
		t.Errorf("mermaid must not carry an \"outside\" line for a foreign card (its domain line already says so):\n%s", mm)
	}
	if strings.Contains(mm, "external") {
		t.Errorf("mermaid must not carry an \"external\" line for a foreign card:\n%s", mm)
	}
	if !strings.Contains(mm, "domain Z") || !strings.Contains(mm, "stale") {
		t.Errorf("stale should still show for the other foreign card:\n%s", mm)
	}
}

// Same-domain boundary wording (n.Domain == "") is a deliberate
// non-change: render_live_test.go's existing "(outside scope)" assertion
// (a tom-and-jerry C3 boundary card, no foreign domain involved) is left
// completely unmodified by this follow-up and still passes — see the
// report for the cross-reference instead of duplicating that fixture here.
