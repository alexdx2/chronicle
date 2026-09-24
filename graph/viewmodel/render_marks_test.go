package viewmodel

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func markedSample() *View {
	v := sampleView()
	v.Nodes[0].Marks = []MarkRef{{Key: "contract:topic:shop:order-created", Name: "order-created", Kind: "breaks",
		Note: `consumer "mail" <script>alert(1)</script> | end --> ] <br/>`}}
	v.Groups[1].Marks = []MarkRef{{Key: "code:provider:shop:orders/svc", Name: "orders.service", Kind: "changed", Note: "rename"}}
	v.Groups[0].Marks = []MarkRef{{Key: "infra:broker:kafka:9092", Name: "kafka", Kind: "focus"}}
	v.Edges[1].Marks = []MarkRef{{Key: "a->b:CALLS_SERVICE", Name: "orders → payments", Kind: "breaks", Note: "timeout"}}
	v.Unplaced = []string{"code:provider:shop:elsewhere"}
	return v
}

func TestRenderMermaid_Marks(t *testing.T) {
	m := RenderMermaid(markedSample())
	for _, want := range []string{
		"classDef mark_breaks stroke:#c25757,stroke-width:3px",
		"classDef mark_changed stroke:#d08a2e,stroke-width:3px",
		"class order_created mark_breaks",
		"class orders mark_changed",
		"style kafka stroke:#3b6fd4,stroke-width:3px",
		`orders -.->|"HTTP ×3 · breaks"| payments`,
		"linkStyle 1 stroke:#c25757,stroke-width:3px",
	} {
		if !strings.Contains(m, want) {
			t.Errorf("mermaid missing %q\n%s", want, m)
		}
	}
	if strings.Contains(m, "<script") || strings.Contains(m, "<br/> ") {
		t.Errorf("agent text leaked into mermaid markup:\n%s", m)
	}
	found := false
	for _, line := range strings.Split(m, "\n") {
		if !strings.HasPrefix(line, "  order_created") {
			continue
		}
		found = true
		if strings.Count(line, `"`) != 2 || strings.Contains(line, "|") {
			t.Errorf("label is not a single quoted string: %s", line)
		}
		if !strings.Contains(line, "#quot;mail#quot;") || !strings.Contains(line, "#lt;script#gt;") || !strings.Contains(line, "#124;") {
			t.Errorf("note not escaped: %s", line)
		}
	}
	if !found {
		t.Fatalf("no order_created line:\n%s", m)
	}
	if !utf8.ValidString(m) {
		t.Error("mermaid is not valid UTF-8")
	}
}

func TestRenderMermaid_ClipKeepsUTF8(t *testing.T) {
	v := sampleView()
	v.Nodes[0].Marks = []MarkRef{{Key: v.Nodes[0].Key, Kind: "note", Note: strings.Repeat("жё", 100)}}
	m := RenderMermaid(v)
	if !utf8.ValidString(m) {
		t.Fatal("clipping a Cyrillic note produced invalid UTF-8")
	}
	if !strings.Contains(m, "…") {
		t.Errorf("a long note is clipped with an ellipsis:\n%s", m)
	}
}

func TestRenderText_Marks(t *testing.T) {
	txt := RenderText(markedSample())
	for _, want := range []string{
		`- order-created [topic] [breaks] consumer "mail" <script>alert(1)</script> | end --> ] <br/>`,
		"## orders (service) — 3 endpoints · 4 components · publishes 1 · calls 1 service · marked: orders.service [changed] rename",
		"## kafka (broker, inferred: only declared broker) — 1 topic · [focus]",
		"- orders → payments: HTTP ×3 [breaks] timeout — inferred · confidence 60% · no evidence",
		"## Marked but not in this view\n- code:provider:shop:elsewhere\n",
	} {
		if !strings.Contains(txt, want) {
			t.Errorf("text missing %q\n---\n%s", want, txt)
		}
	}
}

func TestRenderTable_Marks(t *testing.T) {
	key := "flow:use_case:d:get:/orders"
	v := &View{
		Title: "Flows — d",
		Spec:  ViewSpec{Scope: ScopeSpec{Domain: "d"}, Layout: LayoutSpec{Preset: "flows"}},
		Table: &VTable{Columns: []string{"service", "endpoint", "handler", "flow"},
			Rows: [][]string{{"shop-api", "GET /orders", "shop.controller", "list orders"}}, Keys: []string{key}},
		Nodes:    []VNode{{Key: key, Name: "list orders", Type: "use_case", Layer: "flow", Marks: []MarkRef{{Key: key, Name: "list orders", Kind: "breaks", Note: "a|b"}}}},
		Unplaced: []string{"x->y:CALLS_SERVICE"},
	}
	txt := RenderText(v)
	for _, want := range []string{"- GET /orders → shop.controller → list orders [breaks] a|b", "## Marked but not in this view"} {
		if !strings.Contains(txt, want) {
			t.Errorf("text missing %q\n%s", want, txt)
		}
	}
	if md := RenderMarkdown(v); !strings.Contains(md, `| shop-api | GET /orders | shop.controller | list orders [breaks] a\|b |`) {
		t.Errorf("markdown row:\n%s", md)
	}
}

// A frame (subgraph) whose title carries a folded mark note must not let
// that note forge a real mermaid line break: the title goes through
// mermaidEscape, which deliberately preserves a literal "<br/>" so the
// renderer's own separators survive — so an agent note must be escaped to
// text (mermaidText) before it ever reaches that pass.
func TestRenderMermaid_FrameMarkNoteIsText(t *testing.T) {
	v := sampleView()
	v.Groups[0].Marks = []MarkRef{{Key: "contract:topic:shop:x", Name: "x", Kind: "breaks", Note: "a <br/> b"}}
	m := RenderMermaid(v)
	var line string
	for _, l := range strings.Split(m, "\n") {
		if strings.HasPrefix(l, "subgraph kafka[") {
			line = l
			break
		}
	}
	if line == "" {
		t.Fatalf("no subgraph kafka[ line:\n%s", m)
	}
	if strings.Contains(line, "<br/>") {
		t.Errorf("frame mark note leaked a real <br/> into mermaid markup: %s", line)
	}
	if !strings.Contains(line, "#lt;br/#gt;") {
		t.Errorf("frame mark note not escaped as text: %s", line)
	}
}
