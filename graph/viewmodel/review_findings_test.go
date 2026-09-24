package viewmodel

import (
	"strings"
	"testing"
)

// Review finding 1: the text rendering listed arrows into hidden nodes
// ("BattleEvent → BattleEvent.id: has field" × 33 in the data lens) that
// the mermaid rendering — and the dashboard — never draw. Text and picture
// must describe the same diagram.
func TestRenderText_SkipsArrowsToHiddenNodes(t *testing.T) {
	st := openLiveDBCopy(t)
	view, err := BuildPreset(st, "data", "tom-and-jerry", "")
	if err != nil {
		t.Fatalf("BuildPreset(data): %v", err)
	}
	txt := RenderText(view)
	if strings.Contains(txt, "has field") {
		t.Errorf("text lists arrows to hidden field nodes:\n%s", txt)
	}
	if !strings.Contains(txt, "· 4 arrows ·") {
		t.Errorf("header should count drawn arrows only (4 refs), got:\n%s", strings.SplitN(txt, "\n", 3)[1])
	}
}

// Review finding 2: "how does arena.controller reach ScoreboardApi" found no
// path because a service node has no edges of its own — its components do.
// A service named as a path endpoint means "any node that service owns".
func TestPath_ServiceEndpointResolvesToItsComponents(t *testing.T) {
	st := openLiveDBCopy(t)
	view, err := BuildPreset(st, "path", "tom-and-jerry",
		"code:controller:tom-and-jerry:fixtures/tom-and-jerry/arena-api/src/arena/arena.controller,service:service:tom-and-jerry:scoreboardapi")
	if err != nil {
		t.Fatalf("BuildPreset(path): %v", err)
	}
	if len(view.Trace) < 3 || len(view.Edges) == 0 {
		t.Fatalf("expected a path controller → … → a ScoreboardApi component; trace=%v edges=%d", view.Trace, len(view.Edges))
	}
	last := view.Trace[len(view.Trace)-1]
	if !strings.Contains(last, "scoreboard") {
		t.Errorf("path should end inside ScoreboardApi, ends at %s", last)
	}
	hasBoundaryGroup := false
	for _, g := range view.Groups {
		if g.Key == "service:service:tom-and-jerry:scoreboardapi" {
			hasBoundaryGroup = true
		}
	}
	if !hasBoundaryGroup {
		t.Errorf("the target service should frame its component so the reader sees where the path lands; groups=%+v", view.Groups)
	}
}

// Review finding 3: the API lens drew 19 endpoints as free cards with empty
// service frames. An endpoint belongs to the service whose controller
// exposes it.
func TestAPILens_EndpointsGroupedByService(t *testing.T) {
	st := openLiveDBCopy(t)
	view, err := BuildPreset(st, "api", "tom-and-jerry", "")
	if err != nil {
		t.Fatalf("BuildPreset(api): %v", err)
	}
	free := 0
	for _, n := range view.Nodes {
		if n.Type == "endpoint" {
			if n.Group == "" {
				free++
			}
			if n.Key == "contract:endpoint:tom-and-jerry:get:/arena/score" && n.Group != "service:service:tom-and-jerry:arena-api" {
				t.Errorf("GET /arena/score group=%q want arena-api", n.Group)
			}
		}
	}
	if free != 0 {
		t.Errorf("%d endpoints have no service frame", free)
	}
}

// Review finding 4: "dependencies of arena.service" folded its own service
// into a card, producing the arrow "arena.service → arena-api: injects ×4"
// (a component depending on its own container). The subject's own service
// is a frame in expand views: its components stay visible, other services
// collapse to cards.
func TestDeps_SubjectServiceIsAFrame(t *testing.T) {
	st := openLiveDBCopy(t)
	const subject = "code:provider:tom-and-jerry:fixtures/tom-and-jerry/arena-api/src/arena/arena.service"
	const arena = "service:service:tom-and-jerry:arena-api"
	view, err := BuildPreset(st, "deps", "tom-and-jerry", subject)
	if err != nil {
		t.Fatalf("BuildPreset(deps): %v", err)
	}
	for _, e := range view.Edges {
		if e.From == subject && e.To == arena {
			t.Errorf("subject must not depend on its own service card: %+v", e)
		}
	}
	seen := map[string]string{}
	for _, n := range view.Nodes {
		seen[n.Name] = n.Group
	}
	if seen["jerry.client"] != arena || seen["tom.client"] != arena {
		t.Errorf("arena.service's own-service dependencies should be visible inside the arena-api frame; nodes=%v", seen)
	}
	otherCards := 0
	for _, g := range view.Groups {
		if g.Kind == "service" && g.Key != arena {
			otherCards++
		}
	}
	if otherCards == 0 {
		t.Errorf("other services should still collapse to cards; groups=%+v", view.Groups)
	}
}
