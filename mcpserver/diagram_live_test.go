package mcpserver

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/alexdx2/chronicle-core/graph/viewmodel"
	"github.com/alexdx2/chronicle-core/live"
)

func TestDiagramBuild_MarksAndLive(t *testing.T) {
	g := newLabTestGraph(t)
	tomKey, _ := seedTomJerry(t, g)
	res, err := diagramBuildHandler(g)(context.Background(), makeRevisionRequest(map[string]any{
		"title": "Containers", "domain": "test", "preset": "c2",
		"marks": `{"` + tomKey + `":{"kind":"changed","note":"new client"},"service:service:test:nope":{"kind":"note"}}`,
	}))
	if err != nil {
		t.Fatal(err)
	}
	out := toolResultJSON(t, res)
	marks, _ := out["marks"].([]any)
	if len(marks) != 1 || marks[0].(map[string]any)["key"] != tomKey || marks[0].(map[string]any)["kind"] != "changed" {
		t.Fatalf("marks = %v", out["marks"])
	}
	if missing, _ := out["missing"].([]any); len(missing) != 1 || missing[0] != "service:service:test:nope" {
		t.Fatalf("missing = %v", out["missing"])
	}
	if unplaced, ok := out["unplaced"].([]any); !ok || len(unplaced) != 0 {
		t.Fatalf("unplaced must be an empty array: %v", out["unplaced"])
	}
	lv, _ := out["live"].(map[string]any)
	if lv["published"] != true || lv["dashboard_open"] != false || lv["event_id"].(float64) <= 0 {
		t.Fatalf("live = %v", lv)
	}
	evs, _ := g.Store().ListLiveEvents(0, live.KindDiagram, 10)
	if len(evs) != 1 {
		t.Fatalf("diagram events = %d", len(evs))
	}
	var d live.Diagram
	json.Unmarshal(evs[0].Payload, &d)
	if d.Title != "Containers" || d.URL != out["url"] || d.Spec.Marks[tomKey].Kind != "changed" || d.Marks != 1 {
		t.Fatalf("event payload = %+v", d)
	}
}

func TestDiagramBuild_MarksMergeParamWins(t *testing.T) {
	g := newLabTestGraph(t)
	tomKey, jerryKey := seedTomJerry(t, g)
	vs := `{"scope":{"domain":"test"},"group":{"by":"service"},"collapse":true,"layout":{"preset":"c2"},
		"marks":{"` + tomKey + `":{"kind":"note","note":"from spec"},"` + jerryKey + `":{"kind":"note"}}}`
	res, _ := diagramBuildHandler(g)(context.Background(), makeRevisionRequest(map[string]any{
		"title": "C2", "view_spec": vs, "marks": `{"` + tomKey + `":{"kind":"breaks"}}`,
	}))
	out := toolResultJSON(t, res)
	kinds := map[string]string{}
	for _, m := range out["marks"].([]any) {
		mm := m.(map[string]any)
		kinds[mm["key"].(string)] = mm["kind"].(string)
	}
	if kinds[tomKey] != "breaks" || kinds[jerryKey] != "note" {
		t.Fatalf("merged kinds = %v", kinds)
	}
}

func TestDiagramBuild_MarksLimitOnMergedMap(t *testing.T) {
	g := newLabTestGraph(t)
	seedTomJerry(t, g)
	spec, param := map[string]viewmodel.Mark{}, map[string]viewmodel.Mark{}
	for i := 0; i < 30; i++ {
		spec[fmt.Sprintf("service:service:test:s%02d", i)] = viewmodel.Mark{Kind: "note"}
		param[fmt.Sprintf("service:service:test:p%02d", i)] = viewmodel.Mark{Kind: "note"}
	}
	vs, _ := json.Marshal(viewmodel.ViewSpec{Scope: viewmodel.ScopeSpec{Domain: "test"}, Group: viewmodel.GroupSpec{By: "service"},
		Collapse: true, Layout: viewmodel.LayoutSpec{Preset: "c2"}, Marks: spec})
	pm, _ := json.Marshal(param)
	res, _ := diagramBuildHandler(g)(context.Background(), makeRevisionRequest(map[string]any{
		"title": "C2", "view_spec": string(vs), "marks": string(pm),
	}))
	if !res.IsError || !strings.Contains(fmt.Sprint(res.Content), "at most 50") {
		t.Fatalf("30 + 30 marks must fail on the merged map: %v", res.Content)
	}
}

func TestDiagramBuild_BadMarkKind(t *testing.T) {
	g := newLabTestGraph(t)
	tomKey, _ := seedTomJerry(t, g)
	res, _ := diagramBuildHandler(g)(context.Background(), makeRevisionRequest(map[string]any{
		"title": "C2", "domain": "test", "preset": "c2", "marks": `{"` + tomKey + `":{"kind":"explode"}}`,
	}))
	if !res.IsError || !strings.Contains(fmt.Sprint(res.Content), "breaks|changed|focus|note") {
		t.Fatalf("bad kind: %v", res.Content)
	}
}

func TestDiagramBuild_DashboardOpenAfterScreen(t *testing.T) {
	g := newLabTestGraph(t)
	seedTomJerry(t, g)
	if _, err := live.ReportScreen(g.Store(), live.Screen{ClientID: "tab", Tab: "graph", Visible: true}); err != nil {
		t.Fatal(err)
	}
	res, _ := diagramBuildHandler(g)(context.Background(), makeRevisionRequest(map[string]any{"title": "C2", "domain": "test", "preset": "c2"}))
	out := toolResultJSON(t, res)
	if lv := out["live"].(map[string]any); lv["dashboard_open"] != true {
		t.Fatalf("live = %v", lv)
	}
}

// Review focus: 50 marks with 200-character notes survive the #v/ link.
func TestDiagramBuild_FiftyLongMarksRoundTrip(t *testing.T) {
	g := newLabTestGraph(t)
	tomKey, jerryKey := seedTomJerry(t, g)
	marks := map[string]viewmodel.Mark{
		tomKey:   {Kind: "breaks", Note: strings.Repeat("ж", 200)},
		jerryKey: {Kind: "changed", Note: strings.Repeat("ё", 200)},
	}
	for i := 0; len(marks) < 50; i++ {
		marks[fmt.Sprintf("service:service:test:ghost%02d", i)] = viewmodel.Mark{Kind: "note", Note: strings.Repeat("я", 200)}
	}
	raw, _ := json.Marshal(marks)
	res, _ := diagramBuildHandler(g)(context.Background(), makeRevisionRequest(map[string]any{
		"title": "C2", "domain": "test", "preset": "c2", "marks": string(raw),
	}))
	out := toolResultJSON(t, res)
	url := out["url"].(string)
	data, err := base64.RawURLEncoding.DecodeString(url[strings.Index(url, "#v/")+3:])
	if err != nil {
		t.Fatalf("decode url: %v", err)
	}
	var spec viewmodel.ViewSpec
	if err := json.Unmarshal(data, &spec); err != nil {
		t.Fatal(err)
	}
	if len(spec.Marks) != 50 || spec.Marks[tomKey].Note != strings.Repeat("ж", 200) {
		t.Fatalf("round-tripped marks = %d", len(spec.Marks))
	}
	view, err := viewmodel.BuildView(g.Store(), spec)
	if err != nil {
		t.Fatal(err)
	}
	if len(view.PlacedMarks()) != 2 || len(view.Missing) != 48 {
		t.Fatalf("placed %d, missing %d", len(view.PlacedMarks()), len(view.Missing))
	}
}
