package viewmodel

import (
	"strings"
	"testing"
)

// The C2 canon survives the trip to mermaid on the real fixture: the broker
// frames its topics, one HTTP arrow per service pair, flow direction for
// consumption, hidden infra accounted for.
func TestRenderMermaid_LiveC2(t *testing.T) {
	st := openLiveDBCopy(t)
	view, err := BuildPreset(st, "c2", "tom-and-jerry", "")
	if err != nil {
		t.Fatalf("BuildPreset(c2): %v", err)
	}
	m := RenderMermaid(view)
	t.Logf("\n%s", m)
	for _, want := range []string{
		`subgraph kafka["kafka · broker (inferred: only declared broker)"]`,
		`battle_results(["battle-results<br/>confidence 60%"])`, // below the bar: the card says so
		`arena_api -`, // arrows leave the arena-api card
		`|"HTTP ×3"| jerry_api`,
		`battle_results -`, // consumption drawn topic → consumer
		`%% hidden: 1 cache (isolated)`,
	} {
		if !strings.Contains(m, want) {
			t.Errorf("mermaid missing %q", want)
		}
	}
	if strings.Contains(m, "redis") {
		t.Errorf("isolated redis must not be drawn at C2")
	}
}

func TestRenderText_LiveC3(t *testing.T) {
	st := openLiveDBCopy(t)
	view, err := BuildPreset(st, "c3", "tom-and-jerry", "arena-api")
	if err != nil {
		t.Fatalf("BuildPreset(c3): %v", err)
	}
	txt := RenderText(view)
	t.Logf("\n%s", txt)
	for _, want := range []string{
		"# C3 — arena-api",
		"## arena.module (module)",
		"- arena.controller [controller] exposes ",
		"(outside scope)",
		"## Arrows",
		"## Boundary",
	} {
		if !strings.Contains(txt, want) {
			t.Errorf("text missing %q", want)
		}
	}
}
