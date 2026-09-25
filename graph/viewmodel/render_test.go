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
			{Key: "infra:broker:kafka:9092", Name: "kafka", Kind: "broker", Stats: map[string]int{"topics": 1}, Inferred: true, Note: "only declared broker"},
			{Key: "service:service:shop:orders", Name: "orders", Kind: "service", Stats: map[string]int{"endpoints": 3, "components": 4, "publishes": 1, "calls": 1}},
			{Key: "service:service:shop:payments", Name: "payments", Kind: "service", Stats: map[string]int{"endpoints": 1, "components": 2}},
		},
		Nodes: []VNode{
			{Key: "contract:topic:shop:order-created", Name: "order-created", Type: "topic", Layer: "contract", Group: "infra:broker:kafka:9092", Tier: "primary", RenderMode: "box", Confidence: 0.9, Trust: 0.9, EvidenceCount: 2},
			{Key: "data:dto:shop:orderdto", Name: "OrderDto", Type: "dto", Layer: "data", Tier: "detail", RenderMode: "hidden", SalienceReason: "type:data.dto"},
			{Key: "service:external_system:shop:stripe", Name: "stripe", Type: "external_system", Layer: "service", Tier: "primary", RenderMode: "box", Confidence: 1, Trust: 1, EvidenceCount: 1},
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
		`orders["orders<br/>3 endpoints · 4 components · publishes 1 · calls 1 service"]`,
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
		"- order-created [topic]\n", // healthy: no evidence line
		"## orders (service) — 3 endpoints · 4 components · publishes 1 · calls 1 service",
		"## Arrows",
		"- orders → order-created: pub (kafka)\n", // hard, evidence-backed: silent
		"- orders → payments: HTTP ×3 — inferred · confidence 60% · no evidence",
		"## Hidden",
		"- 1 dto — type:data.dto",
	} {
		if !strings.Contains(txt, want) {
			t.Errorf("text missing %q\n---\n%s", want, txt)
		}
	}
}

// TestRenderText_HeaderHiddenCountSumsViewHidden (rev3 fix F4): the header's
// "N hidden" must equal the sum of View.Hidden's counts — the same total
// the "## Hidden" section lists below it — not just how many VNodes carry
// render_mode=hidden. A bulk filter reason (filteredOut) or a removed
// empty-service group (rev3 fix F3's emptyServiceHidden) contributes a
// HiddenEntry with no VNode behind it at all, so counting VNodes alone
// undercounts.
func TestRenderText_HeaderHiddenCountSumsViewHidden(t *testing.T) {
	v := sampleView()
	// A second Hidden entry with no corresponding VNode — the shape a bulk
	// filter or a removed empty-service group produces.
	v.Hidden = append(v.Hidden, HiddenEntry{Layer: "code", Type: "field", Reason: "filter:layers", Count: 33})
	txt := RenderText(v)
	if !strings.Contains(txt, "level: c2 · domain: shop · 3 groups · 2 nodes · 3 arrows · 34 hidden") {
		t.Errorf("header hidden count should be the SUM of View.Hidden (1 + 33 = 34), not just render_mode=hidden VNodes:\n%s", txt)
	}
}

// TestRenderText_Notes (rev3 task 2): each note prints as "note: <text>"
// right after the header line, in order, before any section.
func TestRenderText_Notes(t *testing.T) {
	v := sampleView()
	v.Notes = []string{"first note", "second note"}
	txt := RenderText(v)
	lines := strings.Split(txt, "\n")
	if len(lines) < 3 || lines[0] != "# C2 — shop" || lines[1] != "note: first note" || lines[2] != "note: second note" {
		t.Fatalf("notes should follow the header line, one per line:\n%s", txt)
	}
	if strings.Index(txt, "note: first note") > strings.Index(txt, "## kafka") {
		t.Errorf("notes must render before sections:\n%s", txt)
	}
}

// TestRenderText_NoNotes: a view with nothing to explain prints no note
// lines at all (no empty "note: " noise).
func TestRenderText_NoNotes(t *testing.T) {
	txt := RenderText(sampleView())
	if strings.Contains(txt, "note:") {
		t.Errorf("sampleView has no notes, but text carries one:\n%s", txt)
	}
}

// TestRenderMermaid_Notes (rev3 task 2): notes render as "%% note: …"
// comments right after the title comment, and a literal newline inside a
// note (an unlikely but possible node-derived name) collapses to a space
// so the comment stays on one line and the mermaid syntax does not break.
func TestRenderMermaid_Notes(t *testing.T) {
	v := sampleView()
	v.Notes = []string{"first note", "line one\nline two"}
	m := RenderMermaid(v)
	if !strings.Contains(m, "%% C2 — shop\n%% note: first note\n%% note: line one line two\n") {
		t.Fatalf("notes should follow the title comment, one comment line each, newlines sanitized:\n%s", m)
	}
	if strings.Contains(m, "line one\nline two") {
		t.Errorf("a raw newline must not leak into a mermaid comment:\n%s", m)
	}
}

// TestRenderMarkdown_NotesBeforeTable (rev3 task 2): a list (Table) view
// prints its notes before the table, not interleaved with rows.
func TestRenderMarkdown_NotesBeforeTable(t *testing.T) {
	v := &View{
		Title: "Flows — d",
		Table: &VTable{Columns: []string{"service", "endpoint"}, Rows: [][]string{{"shop-api", "GET /orders"}}, Keys: []string{"flow:use_case:d:x"}},
		Notes: []string{"an explanatory note"},
	}
	md := RenderMarkdown(v)
	noteIdx := strings.Index(md, "note: an explanatory note")
	tableIdx := strings.Index(md, "| service | endpoint |")
	if noteIdx < 0 || tableIdx < 0 || noteIdx > tableIdx {
		t.Fatalf("note should render before the table:\n%s", md)
	}
}
