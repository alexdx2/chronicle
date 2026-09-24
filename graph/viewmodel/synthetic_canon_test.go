package viewmodel

import (
	"strings"
	"testing"

	"github.com/alexdx2/chronicle-core/store"
)

// A synthetic store the CI can run (the live-DB tests skip there): one
// service with a controller, a provider, a declared broker with a topic, an
// isolated cache, and evidence on some rows. Exercises the C2 canon and the
// evidence propagation deterministically.
func synthStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(t.TempDir() + "/c.db")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	rev, _ := st.CreateRevision("d", "", "abc", "manual", "full", "{}")
	mk := func(key, layer, ntype, name, file string, conf float64) int64 {
		id, err := st.UpsertNode(store.NodeRow{NodeKey: key, Layer: layer, NodeType: ntype, DomainKey: "d", Name: name,
			FilePath: file, Status: "active", Confidence: conf, Freshness: 1, TrustScore: conf, Metadata: "{}", LastSeenRevisionID: rev})
		if err != nil {
			t.Fatalf("upsert %s: %v", key, err)
		}
		return id
	}
	edge := func(from, to int64, fk, tk, typ, deriv string, conf float64) int64 {
		id, err := st.UpsertEdge(store.EdgeRow{EdgeKey: fk + "->" + tk + ":" + typ, FromNodeID: from, ToNodeID: to, EdgeType: typ,
			DerivationKind: deriv, Active: true, Confidence: conf, Freshness: 1, TrustScore: conf, Metadata: "{}", LastSeenRevisionID: rev})
		if err != nil {
			t.Fatalf("edge %s: %v", typ, err)
		}
		return id
	}
	svcA := mk("service:service:d:shop-api", "service", "service", "shop-api", "shop-api/package.json", 0.9)
	svcB := mk("service:service:d:mail-api", "service", "service", "mail-api", "mail-api/package.json", 0.9)
	ctl := mk("code:controller:d:shop-api/src/shop.controller", "code", "controller", "shop.controller", "shop-api/src/shop.controller.ts", 0.8)
	prov := mk("code:provider:d:shop-api/src/shop.service", "code", "provider", "shop.service", "shop-api/src/shop.service.ts", 0.8)
	mailer := mk("code:provider:d:mail-api/src/mailer", "code", "provider", "mailer", "mail-api/src/mailer.ts", 0.8)
	broker := mk("infra:broker:d:kafka", "infra", "broker", "kafka", "", 1)
	_ = mk("infra:cache:d:redis", "infra", "cache", "redis", "", 1)
	_ = broker
	topic := mk("contract:topic:d:order-created", "contract", "topic", "order-created", "", 0.7)
	e1 := edge(ctl, prov, "code:controller:d:shop-api/src/shop.controller", "code:provider:d:shop-api/src/shop.service", "INJECTS", "hard", 0.9)
	e2 := edge(prov, topic, "code:provider:d:shop-api/src/shop.service", "contract:topic:d:order-created", "PUBLISHES_TOPIC", "hard", 0.6)
	edge(mailer, topic, "code:provider:d:mail-api/src/mailer", "contract:topic:d:order-created", "CONSUMES_TOPIC", "inferred", 0.5)
	edge(prov, svcB, "code:provider:d:shop-api/src/shop.service", "service:service:d:mail-api", "CALLS_SERVICE", "linked", 0)
	_ = svcA
	add := func(kind string, nid, eid int64, file string, line int) {
		if _, err := st.AddEvidence(store.EvidenceRow{TargetKind: kind, NodeID: nid, EdgeID: eid, SourceKind: "file", FilePath: file, LineStart: line,
			ExtractorID: "ts", ExtractorVersion: "1", Confidence: 0.9, EvidenceStatus: "valid", EvidencePolarity: "positive", Metadata: "{}"}); err != nil {
			t.Fatal(err)
		}
	}
	add("node", ctl, 0, "shop-api/src/shop.controller.ts", 3)
	add("node", ctl, 0, "shop-api/src/shop.controller.ts", 9)
	add("edge", 0, e1, "shop-api/src/shop.controller.ts", 5)
	add("edge", 0, e2, "shop-api/src/shop.service.ts", 12)
	return st
}

func TestSynthetic_C2_BrokerFrameAndIsolatedCache(t *testing.T) {
	st := synthStore(t)
	v, err := BuildPreset(st, "c2", "d", "")
	if err != nil {
		t.Fatalf("c2: %v", err)
	}
	var kafka *VGroup
	for i := range v.Groups {
		if v.Groups[i].Key == "infra:broker:d:kafka" {
			kafka = &v.Groups[i]
		}
	}
	if kafka == nil || !kafka.Inferred || kafka.Note != "only declared broker" {
		t.Fatalf("broker frame should be inferred from the only declared broker: %+v", v.Groups)
	}
	for _, n := range v.Nodes {
		switch n.Key {
		case "contract:topic:d:order-created":
			if n.Group != "infra:broker:d:kafka" {
				t.Errorf("topic should sit in the broker frame, group=%q", n.Group)
			}
		case "infra:cache:d:redis":
			if n.RenderMode != "hidden" || n.SalienceReason != "isolated" {
				t.Errorf("isolated cache should be hidden with reason isolated: %+v", n)
			}
		}
	}
	found := false
	for _, h := range v.Hidden {
		if h.Type == "cache" && h.Reason == "isolated" && h.Count == 1 {
			found = true
		}
	}
	if !found {
		t.Errorf("hidden summary missing the isolated cache: %+v", v.Hidden)
	}
	// Layer-filtered nodes are accounted for too (C2 drops the data layer;
	// this fixture has none, but the summary must not invent entries).
	for _, h := range v.Hidden {
		if strings.HasPrefix(h.Reason, "filter:") {
			t.Errorf("unexpected filter entry on a fixture with nothing to filter: %+v", h)
		}
	}
}

func TestSynthetic_EvidencePropagation(t *testing.T) {
	st := synthStore(t)
	v, err := BuildPreset(st, "c3", "d", "shop-api")
	if err != nil {
		t.Fatalf("c3: %v", err)
	}
	nodes := map[string]VNode{}
	for _, n := range v.Nodes {
		nodes[n.Name] = n
	}
	ctl := nodes["shop.controller"]
	if ctl.EvidenceCount != 2 || ctl.EvidenceRef != "shop-api/src/shop.controller.ts:3" || ctl.Confidence != 0.8 || ctl.Trust != 0.8 {
		t.Errorf("controller provenance: %+v", ctl)
	}
	if p := nodes["shop.service"]; p.EvidenceCount != 0 || p.EvidenceRef != "shop-api/src/shop.service.ts" {
		t.Errorf("provider without evidence rows should fall back to its file path: %+v", p)
	}
	var inj *VEdge
	for i := range v.Edges {
		if v.Edges[i].Kind == "INJECTS" {
			inj = &v.Edges[i]
		}
	}
	if inj == nil || inj.EvidenceCount != 1 || inj.Confidence != 0.9 || inj.Derivation != "hard" || len(inj.CollapsedFrom) != 1 {
		t.Errorf("INJECTS arrow provenance: %+v", inj)
	}

	// Collapsed C2 arrow: weakest component wins, evidence sums, and a
	// zero-confidence linked edge is reported as 0, never as "unknown".
	c2, err := BuildPreset(st, "c2", "d", "")
	if err != nil {
		t.Fatalf("c2: %v", err)
	}
	for _, e := range c2.Edges {
		if e.Kind == "CALLS_SERVICE" {
			if e.Confidence != 0 || e.Derivation != "linked" {
				t.Errorf("zero-confidence linked call must stay 0/linked: %+v", e)
			}
			txt := RenderText(c2)
			if !strings.Contains(txt, "shop-api → mail-api: HTTP — linked · 0% · no evidence") {
				t.Errorf("text should say 0%%, not hide it:\n%s", txt)
			}
			if !strings.Contains(RenderMermaid(c2), `shop_api -.->|"HTTP"| mail_api`) {
				t.Errorf("a 0%% arrow is dashed in mermaid")
			}
		}
		if e.Kind == "PUBLISHES_TOPIC" && e.EvidenceCount != 1 {
			t.Errorf("publish arrow should carry its evidence: %+v", e)
		}
	}
}

func TestSynthetic_TwoBrokers_UndeclaredTransportIsNotAMatch(t *testing.T) {
	st := synthStore(t)
	rev, _ := st.CreateRevision("d", "", "def", "manual", "full", "{}")
	if _, err := st.UpsertNode(store.NodeRow{NodeKey: "infra:broker:d:rabbit", Layer: "infra", NodeType: "broker", DomainKey: "d", Name: "rabbitmq",
		Status: "active", Confidence: 1, Freshness: 1, TrustScore: 1, Metadata: "{}", LastSeenRevisionID: rev}); err != nil {
		t.Fatal(err)
	}
	v, err := BuildPreset(st, "c2", "d", "")
	if err != nil {
		t.Fatalf("c2: %v", err)
	}
	for _, n := range v.Nodes {
		if n.Type == "topic" && n.Group != "" {
			t.Errorf("with two brokers and no declared transport the topic must stay free, got group %q", n.Group)
		}
	}
}

func TestRenderMermaid_ReservedNamesAndMarkup(t *testing.T) {
	v := &View{
		Groups: []VGroup{{Key: "service:service:d:end", Name: "end", Kind: "service", Stats: map[string]int{}}},
		Nodes: []VNode{
			{Key: "flow:outcome:d:end", Name: "end", Type: "outcome", Layer: "flow", Group: "service:service:d:end", RenderMode: "box"},
			{Key: "code:provider:d:x", Name: "<img src=x onerror=alert(1)>", Type: "provider", Layer: "code", RenderMode: "box"},
		},
	}
	m := RenderMermaid(v)
	if strings.Contains(m, "\nend[") || strings.Contains(m, "subgraph end[") || strings.Contains(m, "  end[") {
		t.Errorf("reserved word used as an id:\n%s", m)
	}
	if strings.Contains(m, "<img") {
		t.Errorf("markup must be escaped in labels:\n%s", m)
	}
}

func TestHiddenSummary_ListsFilteredLayers(t *testing.T) {
	st := synthStore(t)
	v, err := BuildView(st, ViewSpec{Scope: ScopeSpec{Domain: "d"}, Filter: &FilterSpec{Layers: []string{"service"}},
		Group: GroupSpec{By: "service"}, Collapse: true, Layout: LayoutSpec{Preset: "c2"}})
	if err != nil {
		t.Fatalf("view: %v", err)
	}
	seen := map[string]int{}
	for _, h := range v.Hidden {
		seen[h.Layer+"/"+h.Reason] += h.Count
	}
	if seen["code/filter:layers"] != 3 || seen["contract/filter:layers"] != 1 || seen["infra/filter:layers"] != 2 {
		t.Errorf("filtered layers should be accounted for: %v", seen)
	}
}
