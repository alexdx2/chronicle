package graph

import (
	"strings"
	"testing"
)

// resolve_extractions on a large graph can outlive the MCP client's tool
// timeout; agents then retry while the first resolve is still running
// (observed on the otopoint scan: two 180s client timeouts, server still
// working). A concurrent resolve for the same revision must be rejected with
// a clear in-progress error, not raced.
func TestResolveExtractions_ConcurrentGuard(t *testing.T) {
	g, _, revID := setupTestGraph(t)

	release, err := g.tryBeginResolve("testapp", revID)
	if err != nil {
		t.Fatalf("first tryBeginResolve: %v", err)
	}

	if _, err := g.tryBeginResolve("testapp", revID); err == nil {
		t.Fatal("second tryBeginResolve succeeded; want in-progress rejection")
	} else if !strings.Contains(err.Error(), "in progress") {
		t.Errorf("error should say a resolve is in progress, got: %v", err)
	}

	// A different revision is unaffected.
	release2, err := g.tryBeginResolve("testapp", revID+1)
	if err != nil {
		t.Fatalf("different revision blocked: %v", err)
	}
	release2()

	release()
	release3, err := g.tryBeginResolve("testapp", revID)
	if err != nil {
		t.Fatalf("re-acquire after release: %v", err)
	}
	release3()
}

func TestResolveExtractions_RejectsWhileInProgress(t *testing.T) {
	g, _, revID := setupTestGraph(t)

	release, err := g.tryBeginResolve("testapp", revID)
	if err != nil {
		t.Fatalf("tryBeginResolve: %v", err)
	}
	defer release()

	if _, err := g.ResolveExtractions("testapp", revID); err == nil {
		t.Fatal("ResolveExtractions ran despite in-progress resolve; want rejection")
	}
}

func TestIsFlowTriggerFile(t *testing.T) {
	cases := []struct {
		path string
		want bool
	}{
		{"api/src/controllers/auth.controller.ts", true},
		{"scoreboard-api/Controllers/ScoreController.cs", true},
		{"api/src/services/pubsub/order.pubsub.ts", true},
		{"cmd/server/main.go", true},
		// Config/infra files are never flow triggers, even when junk endpoint
		// facts were extracted from them (fly.toml got a trace_flow obligation
		// on the otopoint scan).
		{"fly.customer-web.toml", false},
		{"docker-compose.yml", false},
		{"app.json", false},
		{"README.md", false},
		{"Dockerfile", false},
	}
	for _, c := range cases {
		if got := isFlowTriggerFile(c.path); got != c.want {
			t.Errorf("isFlowTriggerFile(%q) = %v; want %v", c.path, got, c.want)
		}
	}
}
