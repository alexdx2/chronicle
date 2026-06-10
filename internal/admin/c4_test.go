package admin

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alexdx2/chronicle-core/graph"
	"github.com/alexdx2/chronicle-core/registry"
	"github.com/alexdx2/chronicle-core/store"
)

// setupC4Graph seeds a realistic mini-fixture graph and returns the server and
// a map of node name → NodeRow for use in assertions.
//
// Topology:
//
//	svc-a: arena.module → arena.controller, arena.service
//	  arena.controller EXPOSES_ENDPOINT GET /arena/score, POST /arena/attack
//	  arena.controller INJECTS arena.service
//	  arena.service PUBLISHES_TOPIC battle-results
//	  arena.service USES_MODEL BattleEvent
//
//	svc-b: spectators.module → stats.controller, battle-result.consumer, notification.service
//	  stats.controller EXPOSES_ENDPOINT GET /stats/leaderboard
//	  battle-result.consumer CONSUMES_TOPIC battle-results
//	  battle-result.consumer CALLS_SERVICE → svc-a
//	  notification.service CALLS_SERVICE → notifications (external)
//
//	Stale: stale.controller (status=stale) — must be excluded everywhere
//	Inactive edge: active=false between active nodes — must be excluded everywhere
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
	edge(battleConsumer, svcA, "CALLS_SERVICE", true)       // cross-service HTTP (lifting test)
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

// ---- C1 tests ----

func TestC1Stats(t *testing.T) {
	srv, _ := setupC4Graph(t)

	req := httptest.NewRequest("GET", "/api/c4/c1?domain=test-domain", nil)
	w := httptest.NewRecorder()
	srv.handleC1(w, req)

	if w.Code != 200 {
		t.Fatalf("C1 status=%d body=%s", w.Code, w.Body.String())
	}

	var resp c1Response
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if resp.Stats.Services != 2 {
		t.Errorf("services=%d want 2", resp.Stats.Services)
	}
	if resp.Stats.Endpoints != 3 {
		t.Errorf("endpoints=%d want 3", resp.Stats.Endpoints)
	}
	if resp.Stats.Models != 1 {
		t.Errorf("models=%d want 1", resp.Stats.Models)
	}
	if resp.Stats.Topics != 1 {
		t.Errorf("topics=%d want 1", resp.Stats.Topics)
	}
}

func TestC1Externals(t *testing.T) {
	srv, _ := setupC4Graph(t)

	req := httptest.NewRequest("GET", "/api/c4/c1?domain=test-domain", nil)
	w := httptest.NewRecorder()
	srv.handleC1(w, req)

	var resp c1Response
	json.NewDecoder(w.Body).Decode(&resp)

	if len(resp.Externals) != 1 {
		t.Fatalf("externals=%d want 1", len(resp.Externals))
	}
	ext := resp.Externals[0]
	if ext.Name != "notifications" {
		t.Errorf("external.name=%q want 'notifications'", ext.Name)
	}
	if len(ext.CallsFrom) != 1 || ext.CallsFrom[0] != "svc-b" {
		t.Errorf("calls_from=%v want [svc-b]", ext.CallsFrom)
	}
}

func TestC1InfraFromManifest(t *testing.T) {
	srv, _ := setupC4Graph(t)

	req := httptest.NewRequest("GET", "/api/c4/c1?domain=test-domain", nil)
	w := httptest.NewRecorder()
	srv.handleC1(w, req)

	var resp c1Response
	json.NewDecoder(w.Body).Decode(&resp)

	if len(resp.Infra) != 2 {
		t.Fatalf("infra=%d want 2", len(resp.Infra))
	}
	infByName := make(map[string]c1Infra)
	for _, inf := range resp.Infra {
		infByName[inf.Name] = inf
	}
	kafka, ok := infByName["kafka"]
	if !ok {
		t.Fatal("kafka infra missing")
	}
	if !kafka.Used {
		t.Error("kafka used=false want true (battle-results is kafka topic)")
	}
	redis, ok := infByName["redis"]
	if !ok {
		t.Fatal("redis infra missing")
	}
	if redis.Used {
		t.Error("redis used=true want false")
	}
}

func TestC1StaleNodeExcluded(t *testing.T) {
	srv, _ := setupC4Graph(t)

	req := httptest.NewRequest("GET", "/api/c4/c1?domain=test-domain", nil)
	w := httptest.NewRecorder()
	srv.handleC1(w, req)

	var resp c1Response
	json.NewDecoder(w.Body).Decode(&resp)

	// stale.controller is status=stale and must not appear in any counts.
	// Verify stats are not inflated by the stale node.
	if resp.Stats.Services != 2 {
		t.Errorf("stale inflated services: got %d", resp.Stats.Services)
	}
	if len(resp.Externals) != 1 {
		t.Errorf("stale inflated externals: got %d", len(resp.Externals))
	}
}

// ---- C2 tests ----

func TestC2Services(t *testing.T) {
	srv, _ := setupC4Graph(t)

	req := httptest.NewRequest("GET", "/api/c4/c2?domain=test-domain", nil)
	w := httptest.NewRecorder()
	srv.handleC2(w, req)

	if w.Code != 200 {
		t.Fatalf("C2 status=%d body=%s", w.Code, w.Body.String())
	}

	var resp c2Response
	json.NewDecoder(w.Body).Decode(&resp)

	if len(resp.Services) != 2 {
		t.Fatalf("services=%d want 2", len(resp.Services))
	}

	svcByName := make(map[string]*c2Service)
	for i := range resp.Services {
		svcByName[resp.Services[i].Name] = &resp.Services[i]
	}

	svcA := svcByName["svc-a"]
	if svcA == nil {
		t.Fatal("svc-a missing")
	}
	if svcA.Stats.Endpoints != 2 {
		t.Errorf("svc-a endpoints=%d want 2", svcA.Stats.Endpoints)
	}
	if svcA.Stats.Models != 1 {
		t.Errorf("svc-a models=%d want 1", svcA.Stats.Models)
	}
	if svcA.Stats.Modules != 1 {
		t.Errorf("svc-a modules=%d want 1", svcA.Stats.Modules)
	}

	svcB := svcByName["svc-b"]
	if svcB == nil {
		t.Fatal("svc-b missing")
	}
	if svcB.Stats.Endpoints != 1 {
		t.Errorf("svc-b endpoints=%d want 1", svcB.Stats.Endpoints)
	}
	if svcB.Stats.Modules != 1 {
		t.Errorf("svc-b modules=%d want 1", svcB.Stats.Modules)
	}
}

func TestC2LiftedEdges(t *testing.T) {
	srv, _ := setupC4Graph(t)

	req := httptest.NewRequest("GET", "/api/c4/c2?domain=test-domain", nil)
	w := httptest.NewRecorder()
	srv.handleC2(w, req)

	var resp c2Response
	json.NewDecoder(w.Body).Decode(&resp)

	type sig struct{ from, to, kind string }
	found := make(map[sig]bool)
	for _, e := range resp.Edges {
		found[sig{e.From, e.To, e.Kind}] = true
	}

	// svc-b's battle-result.consumer CALLS_SERVICE → svc-a: lifted to svc-b → svc-a (http).
	svcAKey := "service:service:test:svc-a"
	svcBKey := "service:service:test:svc-b"
	topicKey := "contract:topic:test:battle-results"
	notifKey := "service:external_system:test:notifications"

	expects := []sig{
		{svcBKey, svcAKey, "http"},
		{svcAKey, topicKey, "async"},
		{topicKey, svcBKey, "async"},
		{svcBKey, notifKey, "http"},
	}
	for _, want := range expects {
		if !found[want] {
			t.Errorf("missing edge from=%s to=%s kind=%s; got %+v", want.from, want.to, want.kind, resp.Edges)
		}
	}
}

func TestC2TopicDetails(t *testing.T) {
	srv, _ := setupC4Graph(t)

	req := httptest.NewRequest("GET", "/api/c4/c2?domain=test-domain", nil)
	w := httptest.NewRecorder()
	srv.handleC2(w, req)

	var resp c2Response
	json.NewDecoder(w.Body).Decode(&resp)

	if len(resp.Topics) != 1 {
		t.Fatalf("topics=%d want 1", len(resp.Topics))
	}
	tp := resp.Topics[0]
	if tp.Name != "battle-results" {
		t.Errorf("topic name=%q", tp.Name)
	}
	if tp.Transport != "kafka" {
		t.Errorf("transport=%q want kafka", tp.Transport)
	}
	if len(tp.Publishers) != 1 || tp.Publishers[0] != "svc-a" {
		t.Errorf("publishers=%v want [svc-a]", tp.Publishers)
	}
	if len(tp.Consumers) != 1 || tp.Consumers[0] != "svc-b" {
		t.Errorf("consumers=%v want [svc-b]", tp.Consumers)
	}
	if tp.Internal {
		t.Error("internal=true want false")
	}
}

func TestC2InternalTopic(t *testing.T) {
	// Same service publishes AND consumes the same topic → internal=true.
	dir := t.TempDir()
	st, _ := store.Open(filepath.Join(dir, "test.db"))
	t.Cleanup(func() { st.Close() })
	reg, _ := registry.LoadDefaults()
	g := graph.New(st, reg)
	mpath := filepath.Join(dir, "m.yaml")
	os.WriteFile(mpath, []byte("domains:\n  - name: d\ntech: []\n"), 0644)
	srv := NewServer(g, st, 0, mpath, false, dir)

	up := func(n store.NodeRow) store.NodeRow {
		id, _ := st.UpsertNode(n)
		n.NodeID = id
		return n
	}

	svcA := up(store.NodeRow{NodeKey: "service:service:d:svc", Layer: "service", NodeType: "service", DomainKey: "d", Name: "svc", FilePath: "svc/package.json", Status: "active"})
	queue := up(store.NodeRow{NodeKey: "contract:topic:d:battle-queue", Layer: "contract", NodeType: "topic", DomainKey: "d", Name: "battle-queue", Status: "active"})
	provider := up(store.NodeRow{NodeKey: "code:provider:d:battle.queue", Layer: "code", NodeType: "provider", DomainKey: "d", Name: "battle.queue", FilePath: "svc/src/battle.queue.ts", Status: "active"})
	_ = svcA

	st.UpsertEdge(store.EdgeRow{EdgeKey: "pub", FromNodeID: provider.NodeID, ToNodeID: queue.NodeID, EdgeType: "PUBLISHES_TOPIC", DerivationKind: "hard", Active: true, Confidence: 0.9})
	st.UpsertEdge(store.EdgeRow{EdgeKey: "sub", FromNodeID: provider.NodeID, ToNodeID: queue.NodeID, EdgeType: "CONSUMES_TOPIC", DerivationKind: "hard", Active: true, Confidence: 0.9})

	req := httptest.NewRequest("GET", "/api/c4/c2?domain=d", nil)
	w := httptest.NewRecorder()
	srv.handleC2(w, req)

	var resp c2Response
	json.NewDecoder(w.Body).Decode(&resp)

	var found *c2Topic
	for i := range resp.Topics {
		if resp.Topics[i].Name == "battle-queue" {
			found = &resp.Topics[i]
		}
	}
	if found == nil {
		t.Fatal("battle-queue not found")
	}
	if !found.Internal {
		t.Error("internal=false want true")
	}
	if found.Transport != "queue" {
		t.Errorf("transport=%q want queue", found.Transport)
	}
}

func TestC2DedupeEdges(t *testing.T) {
	srv, _ := setupC4Graph(t)

	req := httptest.NewRequest("GET", "/api/c4/c2?domain=test-domain", nil)
	w := httptest.NewRecorder()
	srv.handleC2(w, req)

	var resp c2Response
	json.NewDecoder(w.Body).Decode(&resp)

	// Check no duplicate edges with same (from, to, kind).
	type sig struct{ from, to, kind string }
	seen := make(map[sig]int)
	for _, e := range resp.Edges {
		seen[sig{e.From, e.To, e.Kind}]++
	}
	for s, count := range seen {
		if count > 1 {
			t.Errorf("duplicate edge from=%s to=%s kind=%s count=%d", s.from, s.to, s.kind, count)
		}
	}
}

// ---- C3 tests ----

func TestC3SvcA(t *testing.T) {
	srv, _ := setupC4Graph(t)

	req := httptest.NewRequest("GET", "/api/c4/c3?domain=test-domain&service=svc-a", nil)
	w := httptest.NewRecorder()
	srv.handleC3(w, req)

	if w.Code != 200 {
		t.Fatalf("C3 status=%d body=%s", w.Code, w.Body.String())
	}

	var resp c3Response
	json.NewDecoder(w.Body).Decode(&resp)

	if resp.Service["name"] != "svc-a" {
		t.Errorf("service.name=%q", resp.Service["name"])
	}

	// Modules.
	if len(resp.Modules) != 1 {
		t.Fatalf("modules=%d want 1", len(resp.Modules))
	}
	if resp.Modules[0].Name != "arena.module" {
		t.Errorf("module[0].name=%q", resp.Modules[0].Name)
	}
	if len(resp.Modules[0].Members) != 2 {
		t.Errorf("module members=%d want 2", len(resp.Modules[0].Members))
	}

	// Components (controllers + providers, NOT modules).
	if len(resp.Components) != 2 {
		t.Fatalf("components=%d want 2 (arena.controller + arena.service)", len(resp.Components))
	}
	byName := make(map[string]c3Component)
	for _, c := range resp.Components {
		byName[c.Name] = c
	}
	ctrl := byName["arena.controller"]
	if ctrl.Type != "controller" {
		t.Errorf("arena.controller type=%q", ctrl.Type)
	}
	if len(ctrl.Endpoints) != 2 {
		t.Errorf("arena.controller endpoints=%d want 2", len(ctrl.Endpoints))
	}
	svc := byName["arena.service"]
	if svc.Type != "provider" {
		t.Errorf("arena.service type=%q", svc.Type)
	}
	if len(svc.UsesModels) != 1 || svc.UsesModels[0] != "BattleEvent" {
		t.Errorf("arena.service uses_models=%v want [BattleEvent]", svc.UsesModels)
	}

	// Internal edges: arena.controller INJECTS arena.service.
	if len(resp.InternalEdges) != 1 {
		t.Fatalf("internal_edges=%d want 1", len(resp.InternalEdges))
	}
	if resp.InternalEdges[0].Kind != "injects" {
		t.Errorf("internal_edge kind=%q", resp.InternalEdges[0].Kind)
	}

	// Boundary outgoing: publishes battle-results.
	outByTarget := make(map[string]c3BoundaryOut)
	for _, o := range resp.Boundary.Outgoing {
		outByTarget[o.ToName] = o
	}
	pub, ok := outByTarget["battle-results"]
	if !ok {
		t.Error("outgoing battle-results missing")
	} else if pub.Kind != "async" {
		t.Errorf("outgoing battle-results kind=%q", pub.Kind)
	}

	// Boundary incoming: svc-b calls svc-a.
	inByFrom := make(map[string]c3BoundaryIn)
	for _, i := range resp.Boundary.Incoming {
		inByFrom[i.FromName] = i
	}
	if _, ok := inByFrom["svc-b"]; !ok {
		t.Errorf("incoming svc-b missing; got %v", resp.Boundary.Incoming)
	}
}

func TestC3SvcB(t *testing.T) {
	srv, _ := setupC4Graph(t)

	req := httptest.NewRequest("GET", "/api/c4/c3?domain=test-domain&service=svc-b", nil)
	w := httptest.NewRecorder()
	srv.handleC3(w, req)

	if w.Code != 200 {
		t.Fatalf("C3 status=%d body=%s", w.Code, w.Body.String())
	}

	var resp c3Response
	json.NewDecoder(w.Body).Decode(&resp)

	outByTarget := make(map[string]c3BoundaryOut)
	for _, o := range resp.Boundary.Outgoing {
		outByTarget[o.ToName] = o
	}
	if _, ok := outByTarget["svc-a"]; !ok {
		t.Errorf("svc-b outgoing svc-a missing; got %v", resp.Boundary.Outgoing)
	}
	if _, ok := outByTarget["notifications"]; !ok {
		t.Errorf("svc-b outgoing notifications missing; got %v", resp.Boundary.Outgoing)
	}

	inByFrom := make(map[string]c3BoundaryIn)
	for _, i := range resp.Boundary.Incoming {
		inByFrom[i.FromName] = i
	}
	if in, ok := inByFrom["battle-results"]; !ok {
		t.Errorf("svc-b incoming battle-results missing; got %v", resp.Boundary.Incoming)
	} else if in.Kind != "async" {
		t.Errorf("incoming battle-results kind=%q want async", in.Kind)
	}
}

func TestC3NotFound(t *testing.T) {
	srv, _ := setupC4Graph(t)
	req := httptest.NewRequest("GET", "/api/c4/c3?domain=test-domain&service=nope", nil)
	w := httptest.NewRecorder()
	srv.handleC3(w, req)
	if w.Code != 404 {
		t.Errorf("status=%d want 404", w.Code)
	}
}

func TestC3MissingParam(t *testing.T) {
	srv, _ := setupC4Graph(t)
	req := httptest.NewRequest("GET", "/api/c4/c3?domain=test-domain", nil)
	w := httptest.NewRecorder()
	srv.handleC3(w, req)
	if w.Code != 400 {
		t.Errorf("status=%d want 400", w.Code)
	}
}

func TestC3StaleExcluded(t *testing.T) {
	srv, _ := setupC4Graph(t)

	// stale.controller is in svc-a's path but status=stale.
	req := httptest.NewRequest("GET", "/api/c4/c3?domain=test-domain&service=svc-a", nil)
	w := httptest.NewRecorder()
	srv.handleC3(w, req)

	var resp c3Response
	json.NewDecoder(w.Body).Decode(&resp)

	for _, c := range resp.Components {
		if c.Name == "stale.controller" {
			t.Error("stale.controller appears in C3 components; should be excluded")
		}
	}
}

// ---- GetNodesByKeys active filter test ----

func TestGetNodesByKeysActiveFilter(t *testing.T) {
	dir := t.TempDir()
	st, _ := store.Open(filepath.Join(dir, "test.db"))
	t.Cleanup(func() { st.Close() })

	st.UpsertNode(store.NodeRow{NodeKey: "code:provider:d:active", Layer: "code", NodeType: "provider", DomainKey: "d", Name: "active", Status: "active"})
	st.UpsertNode(store.NodeRow{NodeKey: "code:provider:d:stale", Layer: "code", NodeType: "provider", DomainKey: "d", Name: "stale", Status: "stale"})
	st.UpsertNode(store.NodeRow{NodeKey: "code:provider:d:deleted", Layer: "code", NodeType: "provider", DomainKey: "d", Name: "deleted", Status: "deleted"})

	found, missing, err := st.GetNodesByKeys([]string{
		"code:provider:d:active",
		"code:provider:d:stale",
		"code:provider:d:deleted",
	})
	if err != nil {
		t.Fatalf("GetNodesByKeys: %v", err)
	}
	if len(found) != 1 {
		t.Errorf("found=%d want 1 (only active)", len(found))
	}
	if len(found) > 0 && found[0].NodeKey != "code:provider:d:active" {
		t.Errorf("found[0]=%q want active", found[0].NodeKey)
	}
	if len(missing) != 2 {
		t.Errorf("missing=%d want 2", len(missing))
	}
}

// ---- Diagram dedupe test ----

func TestDiagramBuildDedupeEdges(t *testing.T) {
	srv := setupTestServer(t)

	idA, _ := srv.store.UpsertNode(store.NodeRow{
		NodeKey: "code:provider:d:svc-a", Layer: "code", NodeType: "provider",
		DomainKey: "d", Name: "svc-a", Status: "active",
	})
	idB, _ := srv.store.UpsertNode(store.NodeRow{
		NodeKey: "code:provider:d:svc-b", Layer: "code", NodeType: "provider",
		DomainKey: "d", Name: "svc-b", Status: "active",
	})

	// Two separate edge_keys but same (from_node_id, to_node_id, edge_type).
	srv.store.UpsertEdge(store.EdgeRow{
		EdgeKey: "e1", FromNodeID: idA, ToNodeID: idB,
		EdgeType: "CALLS_SERVICE", DerivationKind: "hard", Active: true,
		FromNodeKey: "code:provider:d:svc-a", ToNodeKey: "code:provider:d:svc-b",
	})
	srv.store.UpsertEdge(store.EdgeRow{
		EdgeKey: "e2", FromNodeID: idA, ToNodeID: idB,
		EdgeType: "CALLS_SERVICE", DerivationKind: "rollup", Active: true,
		FromNodeKey: "code:provider:d:svc-a", ToNodeKey: "code:provider:d:svc-b",
	})

	body := `{"title":"Dedup","node_keys":["code:provider:d:svc-a","code:provider:d:svc-b"]}`
	req := httptest.NewRequest("POST", "/api/diagram/build", strings.NewReader(body))
	w := httptest.NewRecorder()
	srv.handleDiagram(w, req)

	if w.Code != 200 {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	var result map[string]any
	json.NewDecoder(w.Body).Decode(&result)

	// GetEdgesBetweenNodes returns both e1 and e2, but dedup should collapse them.
	edgeCount := int(result["edge_count"].(float64))
	if edgeCount != 1 {
		t.Errorf("edge_count=%d want 1 (duplicates deduped)", edgeCount)
	}
}
