package review

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/alexdx2/chronicle-core/graph"
	"github.com/alexdx2/chronicle-core/registry"
	"github.com/alexdx2/chronicle-core/store"
)

// openTomAndJerryFixture opens a throwaway COPY of the real, committed
// fixtures/tom-and-jerry/.depbot/chronicle.db — the actual scanned demo
// database (richer than e2e's golden expected-graph.json payload: it has
// service:external_system nodes, an infra:broker, and the real
// producer/consumer wiring these tests need), copied to a temp file first so
// merely opening it (store.Open can create -wal/-shm siblings) can never
// touch the tracked fixture.
func openTomAndJerryFixture(t *testing.T) *graph.Graph {
	t.Helper()
	src := filepath.Join("..", "fixtures", "tom-and-jerry", ".depbot", "chronicle.db")
	data, err := os.ReadFile(src)
	if err != nil {
		t.Fatalf("read fixture db: %v", err)
	}
	dst := filepath.Join(t.TempDir(), "chronicle.db")
	if err := os.WriteFile(dst, data, 0o644); err != nil {
		t.Fatalf("copy fixture db: %v", err)
	}
	st, err := store.Open(dst)
	if err != nil {
		t.Fatalf("open copied fixture db: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	reg, err := registry.LoadDefaults()
	if err != nil {
		t.Fatalf("LoadDefaults: %v", err)
	}
	return graph.New(st, reg)
}

// TestCoreImpactQuerier_CollapsesToService seeds from the battle-results
// topic node.
//
// NOTE on the seed: the brief's own example names a different seed,
// code:provider:...:battle.queue (arena-api's PUBLISHES_TOPIC/CONSUMES_TOPIC
// wrapper around the battle-queue topic). Verified directly against this
// fixture (see task-11-report.md): that node has ZERO incoming edges — it is
// a producer/consumer wrapper, so every edge it has points OUTWARD (to
// arena.service via INJECTS, to its topic via PUBLISHES_TOPIC/
// CONSUMES_TOPIC) — and graph.Graph.QueryImpact is a REVERSE BFS (who
// depends on the seed). Seeding from it can therefore never reach
// scoreboard-api, or anything else, at any depth: this is a structural
// impossibility (reverse BFS from a pure producer node the read-side has
// zero dependents on), not a typo in the exact key. Seeding from the
// battle-results topic instead is the smallest change that preserves the
// brief's intent: same real messaging pipeline, and it exercises exactly
// the mechanism under test (collapsing multiple raw impacted code nodes,
// across two different owning services, with no duplicates).
func TestCoreImpactQuerier_CollapsesToService(t *testing.T) {
	g := openTomAndJerryFixture(t)
	q := CoreImpactQuerier{Graph: g}

	impacts, err := q.QueryImpact("contract:topic:tom-and-jerry:battle-results", 4)
	if err != nil {
		t.Fatalf("QueryImpact: %v", err)
	}
	if len(impacts) == 0 {
		t.Fatal("QueryImpact returned no impacts, want at least scoreboard-api")
	}

	seen := map[string]int{}
	var scoreboard *SystemImpact
	for i := range impacts {
		seen[impacts[i].System.Key]++
		if impacts[i].System.Key == "service:service:tom-and-jerry:scoreboard-api" {
			scoreboard = &impacts[i]
		}
	}
	for key, n := range seen {
		if n > 1 {
			t.Errorf("System.Key %q appears %d times in %+v, want at most 1 (dedup)", key, n, impacts)
		}
	}
	if scoreboard == nil {
		t.Fatalf("impacts = %+v, want an entry for scoreboard-api", impacts)
	}
	if scoreboard.System.Kind != kindService {
		t.Errorf("scoreboard-api Kind = %q, want %q", scoreboard.System.Kind, kindService)
	}
	if len(scoreboard.Path) == 0 {
		t.Error("scoreboard-api Path is empty, want at least one PathStep")
	}
}

// TestCoreImpactQuerier_FlowNodesCollapseToTriggeringEndpointOwner is Bug 3's
// reproduction (task-18-report.md), against the real tom-and-jerry fixture's
// own real wiring rather than hand-built data: arena-api's JerryClient
// (code:provider:...:arena-api/src/arena/jerry.client) is injected by
// arena.service, and 5 of arena-api's own flow nodes (get:/arena/history,
// get:/arena/score, post:/arena/attack, post:/arena/trap, ws:/watch-battle)
// REQUIRE it — a real REQUIRES closure DeriveFlows wrote from each flow to
// every provider its triggering endpoint's controller injects, verified
// directly against the fixture db before writing this test. QueryImpact(jerry
// client) is exactly the reverse-BFS a removed CALLS_ENDPOINT edge FROM
// jerry.client seeds (ClassifyDelta's IntegrationRemoved, SourceID=
// jerry.client) — so before the fix, this call alone produced arena.service
// (correctly attributed to arena-api) PLUS 5 separately-mislabeled "domain"
// systems, one per flow node, none of them owned by anything a human would
// recognize as a system. All 6 must now collapse to the ONE real owner: the
// service whose endpoint triggers each flow (arena-api, the same service
// that owns jerry.client itself and arena.service) — QueryImpact's own
// per-System.Key dedup then folds all 6 raw entries into that single result.
func TestCoreImpactQuerier_FlowNodesCollapseToTriggeringEndpointOwner(t *testing.T) {
	g := openTomAndJerryFixture(t)
	q := CoreImpactQuerier{Graph: g}

	const jerryClient = "code:provider:tom-and-jerry:arena-api/src/arena/jerry.client"
	const arenaAPI = "service:service:tom-and-jerry:arena-api"

	impacts, err := q.QueryImpact(jerryClient, 4)
	if err != nil {
		t.Fatalf("QueryImpact: %v", err)
	}
	if len(impacts) == 0 {
		t.Fatal("QueryImpact returned no impacts, want at least arena-api (via arena.service's INJECTS)")
	}

	for _, imp := range impacts {
		if imp.System.Kind == kindDomain {
			t.Errorf("impact %+v collapsed to a bare domain label, want every entry attributed to a real service (no unowned flow-node fallout)", imp)
		}
	}

	seen := map[string]int{}
	for _, imp := range impacts {
		seen[imp.System.Key]++
	}
	if n := seen[arenaAPI]; n != 1 {
		t.Errorf("System.Key %q appears %d times in %+v, want exactly 1 (QueryImpact's own dedup must fold arena.service and all 5 flow nodes into one arena-api entry)", arenaAPI, n, impacts)
	}
	if len(impacts) != 1 {
		t.Errorf("impacts = %+v, want exactly [arena-api] (every raw entry reachable from jerry.client in this fixture collapses to arena-api)", impacts)
	}
}

// TestCoreImpactQuerier_TopicSubscribers: battle-results has two real
// CONSUMES_TOPIC subscribers in this fixture (scoreboard-api's
// BattleResultConsumer and spectators-api's battle-result.consumer) — both
// must collapse to their owning services, each with the one-step Path the
// brief specifies: {Node: the topic's own name, Edge: CONSUMES_TOPIC}.
func TestCoreImpactQuerier_TopicSubscribers(t *testing.T) {
	g := openTomAndJerryFixture(t)
	q := CoreImpactQuerier{Graph: g}

	subs, err := q.TopicSubscribers("contract:topic:tom-and-jerry:battle-results")
	if err != nil {
		t.Fatalf("TopicSubscribers: %v", err)
	}

	want := map[string]bool{
		"service:service:tom-and-jerry:scoreboard-api": false,
		"service:service:tom-and-jerry:spectators-api": false,
	}
	for _, s := range subs {
		if _, ok := want[s.System.Key]; ok {
			want[s.System.Key] = true
		}
		if len(s.Path) != 1 {
			t.Errorf("subscriber %s Path = %+v, want exactly one PathStep", s.System.Key, s.Path)
			continue
		}
		if s.Path[0].Edge != "CONSUMES_TOPIC" {
			t.Errorf("subscriber %s Path[0].Edge = %q, want CONSUMES_TOPIC", s.System.Key, s.Path[0].Edge)
		}
		if s.Path[0].Node != "battle-results" {
			t.Errorf("subscriber %s Path[0].Node = %q, want the topic's own name %q", s.System.Key, s.Path[0].Node, "battle-results")
		}
	}
	for key, found := range want {
		if !found {
			t.Errorf("TopicSubscribers = %+v, missing expected subscriber %s", subs, key)
		}
	}
}

// TestCoreImpactQuerier_Collapse covers both halves the brief names (owned
// node -> its service; unowned top-level node -> Kind domain) plus two
// extra cases worth pinning given SystemRef.Kind's third enum value and the
// self-collapse branch this task's own design needs (see impact.go's
// kindForUnowned): a bare service key collapsing to itself, and a real
// service:external_system node.
func TestCoreImpactQuerier_Collapse(t *testing.T) {
	g := openTomAndJerryFixture(t)
	q := CoreImpactQuerier{Graph: g}

	t.Run("owned node collapses to its service", func(t *testing.T) {
		ref, err := q.Collapse("code:provider:tom-and-jerry:scoreboard-api/services/battleresultconsumer")
		if err != nil {
			t.Fatalf("Collapse: %v", err)
		}
		if ref.Kind != kindService || ref.Key != "service:service:tom-and-jerry:scoreboard-api" {
			t.Errorf("Collapse = %+v, want Kind=service Key=service:service:tom-and-jerry:scoreboard-api", ref)
		}
		if ref.Name != "scoreboard-api" {
			t.Errorf("Collapse.Name = %q, want scoreboard-api", ref.Name)
		}
	})

	t.Run("unowned top-level node collapses to itself as domain", func(t *testing.T) {
		// infra:broker:battle-result (name "battle-bus"): no CONTAINS chain,
		// no file_path, not itself a service — ownershipMap never assigns it
		// an owner. Confirmed directly against the fixture db.
		ref, err := q.Collapse("infra:broker:battle-result")
		if err != nil {
			t.Fatalf("Collapse: %v", err)
		}
		if ref.Kind != kindDomain {
			t.Errorf("Collapse.Kind = %q, want domain", ref.Kind)
		}
		if ref.Key != "infra:broker:battle-result" {
			t.Errorf("Collapse.Key = %q, want the node's own key (self-collapse)", ref.Key)
		}
	})

	t.Run("external_system node collapses to itself", func(t *testing.T) {
		ref, err := q.Collapse("service:external_system:tom-and-jerry:jerry-api")
		if err != nil {
			t.Fatalf("Collapse: %v", err)
		}
		if ref.Kind != kindExternalSystem {
			t.Errorf("Collapse.Kind = %q, want external_system", ref.Kind)
		}
		if ref.Key != "service:external_system:tom-and-jerry:jerry-api" {
			t.Errorf("Collapse.Key = %q, want self", ref.Key)
		}
	})

	t.Run("a bare service key collapses to itself", func(t *testing.T) {
		ref, err := q.Collapse("service:service:tom-and-jerry:scoreboard-api")
		if err != nil {
			t.Fatalf("Collapse: %v", err)
		}
		if ref.Kind != kindService || ref.Key != "service:service:tom-and-jerry:scoreboard-api" {
			t.Errorf("Collapse = %+v, want self Kind=service", ref)
		}
	})

	t.Run("unknown key errors", func(t *testing.T) {
		if _, err := q.Collapse("code:provider:tom-and-jerry:nonexistent"); err == nil {
			t.Error("Collapse(unknown key) = nil error, want an error")
		}
	})
}
