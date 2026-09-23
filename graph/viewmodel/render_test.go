package viewmodel

import (
	"strings"
	"testing"
)

// A View renders to text and mermaid deterministically: the same content
// the dashboard draws, in a form an agent can paste into an answer, a PR
// or a wiki page. Ids are stable slugs, order is sorted, provenance rides
// along (dashed arrows for inferred/evidence-less edges).
func sampleView() *View {
	return &View{
		Spec:  ViewSpec{Scope: ScopeSpec{Domain: "shop"}, Group: GroupSpec{By: "service"}, Collapse: true, Layout: LayoutSpec{Preset: "c2"}},
		Title: "C2 — shop",
		Groups: []VGroup{
			{Key: "infra:broker:kafka:9092", Name: "kafka", Kind: "broker", Stats: map[string]int{"nodes": 1}, Inferred: true, Note: "only declared broker"},
			{Key: "service:service:shop:orders", Name: "orders", Kind: "service", Stats: map[string]int{"endpoints": 3, "nodes": 4}},
			{Key: "service:service:shop:payments", Name: "payments", Kind: "service", Stats: map[string]int{"endpoints": 1, "nodes": 2}},
		},
		Nodes: []VNode{
			{Key: "contract:topic:shop:order-created", Name: "order-created", Type: "topic", Layer: "contract", Group: "infra:broker:kafka:9092", Tier: "primary", RenderMode: "box", Confidence: 0.9, Trust: 0.9, EvidenceCount: 2},
			{Key: "data:dto:shop:orderdto", Name: "OrderDto", Type: "dto", Layer: "data", Tier: "detail", RenderMode: "hidden", SalienceReason: "type:data.dto"},
			{Key: "service:external_system:shop:stripe", Name: "stripe", Type: "external_system", Layer: "service", Tier: "primary", RenderMode: "box", Confidence: 1, Trust: 1},
		},
		Edges: []VEdge{
			{From: "service:service:shop:orders", To: "contract:topic:shop:order-created", Kind: "PUBLISHES_TOPIC", Transport: "kafka", Weight: 1, Confidence: 0.9, Trust: 0.9, Derivation: "hard", EvidenceCount: 1},
			{From: "service:service:shop:orders", To: "service:service:shop:payments", Kind: "CALLS_SERVICE", Transport: "http", Weight: 3, Confidence: 0.6, Trust: 0.6, Derivation: "inferred", EvidenceCount: 0},
			{From: "service:service:shop:payments", To: "service:external_system:shop:stripe", Kind: "CALLS_SERVICE", Transport: "http", Weight: 1, Confidence: 1, Trust: 1, Derivation: "hard", EvidenceCount: 4},
		},
		Hidden: []HiddenEntry{{Layer: "data", Type: "dto", Reason: "type:data.dto", Count: 1}},
	}
}

func TestRenderMermaid_Structure(t *testing.T) {
	m := RenderMermaid(sampleView())
	for _, want := range []string{
		"flowchart TB",
		`subgraph kafka["kafka · broker (inferred: only declared broker)"]`,
		`order_created(["order-created"])`,
		`orders["orders<br/>3 endpoints · 4 nodes"]`,
		`stripe[["stripe"]]`,
		`orders -->|"pub"| order_created`,
		`orders -.->|"HTTP ×3"| payments`, // inferred, no evidence → dashed
		`payments -->|"HTTP"| stripe`,
	} {
		if !strings.Contains(m, want) {
			t.Errorf("mermaid missing %q\n---\n%s", want, m)
		}
	}
	if strings.Contains(m, "OrderDto") {
		t.Errorf("hidden nodes must not be drawn:\n%s", m)
	}
	if !strings.Contains(m, "%% hidden: 1 dto (type:data.dto)") {
		t.Errorf("hidden summary should be a mermaid comment:\n%s", m)
	}
}

func TestRenderMermaid_DirectionFollowsQuestion(t *testing.T) {
	v := sampleView()
	v.Spec.Layout.Preset = "path"
	if !strings.HasPrefix(RenderMermaid(v), "flowchart LR") {
		t.Errorf("path views read left→right")
	}
	v.Spec.Layout.Preset = "impact"
	if !strings.HasPrefix(RenderMermaid(v), "flowchart BT") {
		t.Errorf("impact views put the subject on top")
	}
}

func TestRenderMermaid_Deterministic(t *testing.T) {
	a, b := RenderMermaid(sampleView()), RenderMermaid(sampleView())
	if a != b {
		t.Fatalf("two renders differ:\n%s\n---\n%s", a, b)
	}
}

func TestRenderMermaid_IdCollisionsStaySeparate(t *testing.T) {
	v := &View{Nodes: []VNode{
		{Key: "code:provider:a:x/prisma.service", Name: "prisma.service", Type: "provider", Layer: "code", RenderMode: "box"},
		{Key: "code:provider:a:y/prisma.service", Name: "prisma.service", Type: "provider", Layer: "code", RenderMode: "box"},
	}}
	m := RenderMermaid(v)
	if !strings.Contains(m, "prisma_service[") || !strings.Contains(m, "prisma_service_2[") {
		t.Errorf("same-name nodes need distinct ids:\n%s", m)
	}
}

func TestRenderText_Structure(t *testing.T) {
	txt := RenderText(sampleView())
	for _, want := range []string{
		"# C2 — shop",
		"level: c2 · domain: shop · 3 groups · 2 nodes · 3 arrows · 1 hidden",
		"## kafka (broker, inferred: only declared broker)",
		"- order-created [topic] confidence 90% · 2 evidence",
		"## orders (service) — 3 endpoints · 4 nodes",
		"## Arrows",
		"- orders → order-created: pub (kafka) — hard · 90% · 1 evidence",
		"- orders → payments: HTTP ×3 — inferred · 60% · no evidence",
		"## Hidden",
		"- 1 dto — type:data.dto",
	} {
		if !strings.Contains(txt, want) {
			t.Errorf("text missing %q\n---\n%s", want, txt)
		}
	}
}
