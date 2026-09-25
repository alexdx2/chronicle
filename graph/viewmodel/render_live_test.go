package viewmodel

import (
	"strings"
	"testing"
)

// The C2 canon survives the trip to mermaid on the real fixture: the broker
// frames its topics, one HTTP arrow per service pair, flow direction for
// consumption, hidden infra accounted for. battle-queue is arena-api's own
// private queue (rev3 task 3) — hidden along with its pub/sub arrows, same
// as isolated infra; battle-results has three distinct services on it and
// stays a drawn box in the kafka frame.
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
		// jerry.client's direct CALLS_SERVICE to jerry-api twins its own
		// two resolved CALLS_ENDPOINT calls (fix round 1, rev3 task 2):
		// weight counts the two real calls, not a third.
		`|"HTTP ×2"| jerry_api`,
		`battle_results -`, // consumption drawn topic → consumer
		// The full hidden comment is one line (all reasons, biggest count
		// first); check each reason's presence rather than the line's
		// start, since which reason sorts first depends on their counts.
		// rev3 fix F5: a count-1 entry names what it hides.
		`1 cache (isolated: redis)`,
		`1 topic (private: battle-queue)`,
	} {
		if !strings.Contains(m, want) {
			t.Errorf("mermaid missing %q", want)
		}
	}
	// redis is named in the hidden comment (F5) but must not be drawn as
	// its own node anywhere else.
	for _, line := range strings.Split(m, "\n") {
		if strings.HasPrefix(line, "%% hidden:") {
			continue
		}
		if strings.Contains(line, "redis") {
			t.Errorf("isolated redis must not be drawn at C2: %q", line)
		}
	}
	if strings.Contains(m, "battle_queue") {
		t.Errorf("arena-api's private battle-queue must not be drawn at C2")
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
