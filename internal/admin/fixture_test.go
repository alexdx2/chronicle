package admin

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/alexdx2/chronicle-core/graph"
	"github.com/alexdx2/chronicle-core/registry"
	"github.com/alexdx2/chronicle-core/store"
)

func setupC4Graph(t *testing.T) (*Server, map[string]store.NodeRow) {
	t.Helper()

	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { st.Close() })

	reg, _ := registry.LoadDefaults()
	g := graph.New(st, reg)

	manifestPath := filepath.Join(dir, "chronicle.domain.yaml")
	manifestContent := "domains:\n  - name: test-domain\n    scan:\n      include: [\"svc-a/**\", \"svc-b/**\"]\ntech: [nestjs]\ninfrastructure:\n  - name: kafka\n    type: broker\n    address: kafka:9092\n  - name: redis\n    type: cache\n    address: redis:6379\n"
	if err := os.WriteFile(manifestPath, []byte(manifestContent), 0644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}

	srv := NewServer(g, st, 0, manifestPath, false, dir)

	up := func(n store.NodeRow) store.NodeRow {
		t.Helper()
		id, err := st.UpsertNode(n)
		if err != nil {
			t.Fatalf("UpsertNode %q: %v", n.NodeKey, err)
		}
		n.NodeID = id
		return n
	}

	edge := func(from, to store.NodeRow, edgeType string, active bool) {
		t.Helper()
		e := store.EdgeRow{
			EdgeKey:        from.NodeKey + "->" + to.NodeKey + ":" + edgeType,
			FromNodeID:     from.NodeID,
			ToNodeID:       to.NodeID,
			EdgeType:       edgeType,
			DerivationKind: "hard",
			Active:         active,
			FromNodeKey:    from.NodeKey,
			ToNodeKey:      to.NodeKey,
			Confidence:     0.9,
		}
		if _, err := st.UpsertEdge(e); err != nil {
			t.Fatalf("UpsertEdge %q: %v", e.EdgeKey, err)
		}
	}

	nodes := map[string]store.NodeRow{}

	// ---- Services ----
	svcA := up(store.NodeRow{
		NodeKey: "service:service:test:svc-a", Layer: "service", NodeType: "service",
		DomainKey: "test-domain", Name: "svc-a", FilePath: "svc-a/package.json", Status: "active",
	})
	svcB := up(store.NodeRow{
		NodeKey: "service:service:test:svc-b", Layer: "service", NodeType: "service",
		DomainKey: "test-domain", Name: "svc-b", FilePath: "svc-b/package.json", Status: "active",
	})
	notifications := up(store.NodeRow{
		NodeKey: "service:external_system:test:notifications", Layer: "service", NodeType: "external_system",
		DomainKey: "test-domain", Name: "notifications", Status: "active",
	})

	// ---- svc-a code ----
	arenaModule := up(store.NodeRow{
		NodeKey: "code:module:test:arena.module", Layer: "code", NodeType: "module",
		DomainKey: "test-domain", Name: "arena.module",
		FilePath: "svc-a/src/arena/arena.module.ts", Status: "active",
	})
	arenaCtrl := up(store.NodeRow{
		NodeKey: "code:controller:test:arena.controller", Layer: "code", NodeType: "controller",
		DomainKey: "test-domain", Name: "arena.controller",
		FilePath: "svc-a/src/arena/arena.controller.ts", Status: "active",
	})
	arenaService := up(store.NodeRow{
		NodeKey: "code:provider:test:arena.service", Layer: "code", NodeType: "provider",
		DomainKey: "test-domain", Name: "arena.service",
		FilePath: "svc-a/src/arena/arena.service.ts", Status: "active",
	})

	// ---- svc-b code ----
	spectatorsModule := up(store.NodeRow{
		NodeKey: "code:module:test:spectators.module", Layer: "code", NodeType: "module",
		DomainKey: "test-domain", Name: "spectators.module",
		FilePath: "svc-b/src/spectators/spectators.module.ts", Status: "active",
	})
	statsCtrl := up(store.NodeRow{
		NodeKey: "code:controller:test:stats.controller", Layer: "code", NodeType: "controller",
		DomainKey: "test-domain", Name: "stats.controller",
		FilePath: "svc-b/src/spectators/stats.controller.ts", Status: "active",
	})
	battleConsumer := up(store.NodeRow{
		NodeKey: "code:provider:test:battle-result.consumer", Layer: "code", NodeType: "provider",
		DomainKey: "test-domain", Name: "battle-result.consumer",
		FilePath: "svc-b/src/spectators/battle-result.consumer.ts", Status: "active",
	})
	notifService := up(store.NodeRow{
		NodeKey: "code:provider:test:notification.service", Layer: "code", NodeType: "provider",
		DomainKey: "test-domain", Name: "notification.service",
		FilePath: "svc-b/src/spectators/notification.service.ts", Status: "active",
	})

	// ---- Contracts ----
	epScore := up(store.NodeRow{
		NodeKey: "contract:endpoint:test:get:/arena/score", Layer: "contract", NodeType: "endpoint",
		DomainKey: "test-domain", Name: "GET /arena/score", Status: "active",
	})
	epAttack := up(store.NodeRow{
		NodeKey: "contract:endpoint:test:post:/arena/attack", Layer: "contract", NodeType: "endpoint",
		DomainKey: "test-domain", Name: "POST /arena/attack", Status: "active",
	})
	epLeaderboard := up(store.NodeRow{
		NodeKey: "contract:endpoint:test:get:/stats/leaderboard", Layer: "contract", NodeType: "endpoint",
		DomainKey: "test-domain", Name: "GET /stats/leaderboard", Status: "active",
	})
	battleResults := up(store.NodeRow{
		NodeKey: "contract:topic:test:battle-results", Layer: "contract", NodeType: "topic",
		DomainKey: "test-domain", Name: "battle-results", Status: "active",
	})

	// ---- Data ----
	battleEvent := up(store.NodeRow{
		NodeKey: "data:model:test:BattleEvent", Layer: "data", NodeType: "model",
		DomainKey: "test-domain", Name: "BattleEvent", Status: "active",
	})

	// ---- Stale node (excluded) ----
	staleCtrl := up(store.NodeRow{
		NodeKey: "code:controller:test:stale.controller", Layer: "code", NodeType: "controller",
		DomainKey: "test-domain", Name: "stale.controller",
		FilePath: "svc-a/src/arena/stale.controller.ts", Status: "stale",
	})

	// ---- Record ----
	nodes["svc-a"] = svcA
	nodes["svc-b"] = svcB
	nodes["notifications"] = notifications
	nodes["arena.module"] = arenaModule
	nodes["arena.controller"] = arenaCtrl
	nodes["arena.service"] = arenaService
	nodes["spectators.module"] = spectatorsModule
	nodes["stats.controller"] = statsCtrl
	nodes["battle-result.consumer"] = battleConsumer
	nodes["notification.service"] = notifService
	nodes["ep.score"] = epScore
	nodes["ep.attack"] = epAttack
	nodes["ep.leaderboard"] = epLeaderboard
	nodes["battle-results"] = battleResults
	nodes["battle-event"] = battleEvent
	nodes["stale.controller"] = staleCtrl

	// ---- svc-a edges ----
	edge(arenaModule, arenaCtrl, "CONTAINS", true)
	edge(arenaModule, arenaService, "CONTAINS", true)
	edge(arenaCtrl, epScore, "EXPOSES_ENDPOINT", true)
	edge(arenaCtrl, epAttack, "EXPOSES_ENDPOINT", true)
	edge(arenaCtrl, arenaService, "INJECTS", true)
	edge(arenaService, battleResults, "PUBLISHES_TOPIC", true)
	edge(arenaService, battleEvent, "USES_MODEL", true)

	// ---- svc-b edges ----
	edge(spectatorsModule, statsCtrl, "CONTAINS", true)
	edge(spectatorsModule, battleConsumer, "CONTAINS", true)
	edge(spectatorsModule, notifService, "CONTAINS", true)
	edge(statsCtrl, epLeaderboard, "EXPOSES_ENDPOINT", true)
	edge(battleConsumer, battleResults, "CONSUMES_TOPIC", true)
	edge(battleConsumer, svcA, "CALLS_SERVICE", true)        // cross-service HTTP (lifting test)
	edge(notifService, notifications, "CALLS_SERVICE", true) // external

	// Inactive edge (active=false) between two active nodes — must be excluded by activeEdges().
	// Use a distinct pair of nodes so it doesn't overwrite the active edge above.
	// arenaService → epAttack with edge_type=USES_MODEL is an artificial inactive edge.
	inactiveEdge := store.EdgeRow{
		EdgeKey:        "inactive-edge-test",
		FromNodeID:     arenaService.NodeID,
		ToNodeID:       epAttack.NodeID,
		EdgeType:       "CALLS_ENDPOINT",
		DerivationKind: "hard",
		Active:         false,
		FromNodeKey:    arenaService.NodeKey,
		ToNodeKey:      epAttack.NodeKey,
		Confidence:     0.9,
	}
	st.UpsertEdge(inactiveEdge)

	// Stale node edge — stale node is excluded by activeOnly(), so this edge's
	// fromNodeID won't be in nodeByID and will be safely skipped.
	edge(staleCtrl, epScore, "EXPOSES_ENDPOINT", true)

	return srv, nodes
}
