package graph

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alexdx2/chronicle-core/registry"
	"github.com/alexdx2/chronicle-core/store"
)

// L3 — Full scan integration test with mocked LLM responses.
// Simulates a complete tom-and-jerry project scan by feeding pre-built facts
// for all key files, then verifying the resulting graph against must-have
// and must-not-have assertions.

// mockExtraction holds the simulated scan output for one file.
type mockExtraction struct {
	FilePath string
	FromType string // "module", "controller", or "" (provider)
	Facts    string // JSON facts
}

// tomAndJerryExtractions returns realistic facts for all key files.
// These simulate what AST + LLM enrichment would produce.
func tomAndJerryExtractions() []mockExtraction {
	return []mockExtraction{
		// === PRISMA SCHEMAS ===
		{FilePath: "tom-api/prisma/schema.prisma", Facts: `[
			{"kind":"model","to":"Cat"},
			{"kind":"model","to":"CatWeapon"},
			{"kind":"enum","to":"CatMood"},
			{"kind":"enum","to":"WeaponType"},
			{"kind":"model_relation","from":"Cat","to":"CatWeapon"}
		]`},
		{FilePath: "jerry-api/prisma/schema.prisma", Facts: `[
			{"kind":"model","to":"Mouse"},
			{"kind":"model","to":"Trap"},
			{"kind":"enum","to":"TrapEffect"},
			{"kind":"model_relation","from":"Mouse","to":"Trap"}
		]`},
		{FilePath: "arena-api/prisma/schema.prisma", Facts: `[
			{"kind":"model","to":"BattleEvent"},
			{"kind":"enum","to":"BattleResult"}
		]`},

		// === CONTROLLERS ===
		{FilePath: "tom-api/src/tom/tom.controller.ts", FromType: "controller", Facts: `[
			{"kind":"endpoint","method":"GET","target":"/tom/status"},
			{"kind":"endpoint","method":"GET","target":"/tom/weapons"},
			{"kind":"endpoint","method":"POST","target":"/tom/arm"},
			{"kind":"injects","to":"TomService"}
		]`},
		{FilePath: "jerry-api/src/jerry/jerry.controller.ts", FromType: "controller", Facts: `[
			{"kind":"endpoint","method":"GET","target":"/jerry/status"},
			{"kind":"endpoint","method":"GET","target":"/jerry/traps"},
			{"kind":"endpoint","method":"POST","target":"/jerry/set-trap"},
			{"kind":"injects","to":"JerryService"}
		]`},
		{FilePath: "arena-api/src/arena/arena.controller.ts", FromType: "controller", Facts: `[
			{"kind":"endpoint","method":"POST","target":"/arena/attack"},
			{"kind":"endpoint","method":"POST","target":"/arena/trap"},
			{"kind":"endpoint","method":"GET","target":"/arena/history"},
			{"kind":"endpoint","method":"GET","target":"/arena/score"},
			{"kind":"injects","to":"ArenaService"}
		]`},
		{FilePath: "spectators-api/src/spectators/stats.controller.ts", FromType: "controller", Facts: `[
			{"kind":"endpoint","method":"GET","target":"/stats/leaderboard"},
			{"kind":"endpoint","method":"GET","target":"/stats/recent"},
			{"kind":"injects","to":"SpectatorService"}
		]`},

		// === SERVICES ===
		{FilePath: "tom-api/src/tom/tom.service.ts", Facts: `[
			{"kind":"injects","to":"PrismaService"},
			{"kind":"uses_model","to":"Cat"},
			{"kind":"uses_model","to":"CatWeapon"}
		]`},
		{FilePath: "jerry-api/src/jerry/jerry.service.ts", Facts: `[
			{"kind":"injects","to":"PrismaService"},
			{"kind":"uses_model","to":"Mouse"},
			{"kind":"uses_model","to":"Trap"}
		]`},
		{FilePath: "arena-api/src/arena/arena.service.ts", Facts: `[
			{"kind":"injects","to":"PrismaService"},
			{"kind":"injects","to":"TomClient"},
			{"kind":"injects","to":"JerryClient"},
			{"kind":"injects","to":"BattleResultProducer"},
			{"kind":"uses_model","to":"BattleEvent"},
			{"kind":"calls_service","to":"TomClient","method":"getStatus"},
			{"kind":"calls_service","to":"JerryClient","method":"getStatus"},
			{"kind":"calls_service","to":"BattleResultProducer","method":"publish"}
		]`},
		{FilePath: "spectators-api/src/spectators/spectator.service.ts", Facts: `[
			{"kind":"http_call","method":"GET","target":"http://tom-api:3001/tom/status"},
			{"kind":"http_call","method":"GET","target":"http://jerry-api:3002/jerry/status"}
		]`},

		// === PRODUCERS / CONSUMERS ===
		{FilePath: "arena-api/src/arena/battle-result.producer.ts", Facts: `[
			{"kind":"produces","to":"battle-results","method":"publish"}
		]`},
		{FilePath: "spectators-api/src/spectators/battle-result.consumer.ts", Facts: `[
			{"kind":"consumes","to":"battle-results","method":"handleBattleResult"},
			{"kind":"injects","to":"SpectatorService"},
			{"kind":"calls_service","to":"SpectatorService","method":"recordBattle"}
		]`},

		// === MODULES ===
		{FilePath: "tom-api/src/tom/tom.module.ts", FromType: "module", Facts: `[
			{"kind":"provides","to":"TomController"},
			{"kind":"provides","to":"TomService"},
			{"kind":"provides","to":"PrismaService"}
		]`},
		{FilePath: "jerry-api/src/jerry/jerry.module.ts", FromType: "module", Facts: `[
			{"kind":"provides","to":"JerryController"},
			{"kind":"provides","to":"JerryService"},
			{"kind":"provides","to":"PrismaService"}
		]`},
		{FilePath: "arena-api/src/arena/arena.module.ts", FromType: "module", Facts: `[
			{"kind":"provides","to":"ArenaController"},
			{"kind":"provides","to":"ArenaService"},
			{"kind":"provides","to":"TomClient"},
			{"kind":"provides","to":"JerryClient"},
			{"kind":"provides","to":"BattleResultProducer"},
			{"kind":"provides","to":"PrismaService"}
		]`},
		{FilePath: "spectators-api/src/spectators/spectators.module.ts", FromType: "module", Facts: `[
			{"kind":"provides","to":"StatsController"},
			{"kind":"provides","to":"SpectatorService"},
			{"kind":"provides","to":"BattleResultConsumer"},
			{"kind":"provides","to":"CleanupTask"}
		]`},

		// === GATEWAY ===
		{FilePath: "arena-api/src/arena/battle.gateway.ts", FromType: "controller", Facts: `[
			{"kind":"endpoint","method":"WS","target":"watch-battle"},
			{"kind":"injects","to":"ArenaService"}
		]`},

		// === HTTP CLIENTS ===
		{FilePath: "arena-api/src/arena/tom.client.ts", Facts: `[
			{"kind":"http_call","method":"GET","target":"http://tom-api:3001/tom/status"}
		]`},
		{FilePath: "arena-api/src/arena/jerry.client.ts", Facts: `[
			{"kind":"http_call","method":"GET","target":"http://jerry-api:3002/jerry/status"},
			{"kind":"http_call","method":"GET","target":"http://jerry-api:3002/jerry/traps"}
		]`},

		// === MISC (no facts / type-only) ===
		{FilePath: "arena-api/src/arena/cache.service.ts", Facts: `[]`},
		{FilePath: "arena-api/src/arena/battle.guard.ts", Facts: `[]`},
		{FilePath: "arena-api/src/arena/battle.queue.ts", Facts: `[
			{"kind":"injects","to":"ArenaService"}
		]`},
		{FilePath: "tom-api/src/tom/health.pipe.ts", Facts: `[]`},
		{FilePath: "tom-api/src/tom/logging.middleware.ts", Facts: `[]`},
		{FilePath: "jerry-api/src/jerry/clever.decorator.ts", Facts: `[]`},
		{FilePath: "jerry-api/src/jerry/trap.interceptor.ts", Facts: `[]`},
		{FilePath: "spectators-api/src/spectators/cleanup.task.ts", Facts: `[
			{"kind":"injects","to":"SpectatorService"}
		]`},
		{FilePath: "spectators-api/src/spectators/notification.service.ts", Facts: `[
			{"kind":"http_call","method":"POST","target":"http://notifications:3005/push"}
		]`},
	}
}

func TestL3_FullScan_MockedLLM(t *testing.T) {
	tmpDir := t.TempDir()
	s, err := store.Open(filepath.Join(tmpDir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	reg, _ := registry.LoadDefaults()
	g := New(s, reg)

	domain := "tomandjerry"
	revID, _ := s.CreateRevision(domain, "scan_1", "", "manual", "full", "{}")

	// === Phase 1: Import all extractions ===
	extractions := tomAndJerryExtractions()
	for _, ext := range extractions {
		_, err := g.SaveFileExtraction(revID, domain, ext.FilePath, "extracted", ext.FromType, ext.Facts, "")
		if err != nil {
			t.Fatalf("SaveExtraction %s: %v", ext.FilePath, err)
		}
	}

	// === Phase 2: Resolve ===
	result, err := g.ResolveExtractions(domain, revID)
	if err != nil {
		t.Fatal(err)
	}

	t.Logf("Resolved: %d files, %d nodes created, %d edges created",
		result.FilesProcessed, result.NodesCreated, result.EdgesCreated)

	if len(result.Unresolved) > 0 {
		for _, u := range result.Unresolved {
			t.Logf("  unresolved: %s %s -> %s (%s)", u.Kind, u.FromFile, u.Target, u.Reason)
		}
	}

	// === Phase 3: Assert graph ===
	nodes, _ := s.ListNodes(store.NodeFilter{Domain: domain})
	edges, _ := s.ListEdges(store.EdgeFilter{})

	t.Logf("Graph: %d nodes, %d edges", len(nodes), len(edges))

	// --- Aggregate assertions ---
	if len(nodes) < 40 {
		t.Errorf("expected at least 40 nodes, got %d", len(nodes))
	}
	if len(edges) < 30 {
		t.Errorf("expected at least 30 edges, got %d", len(edges))
	}

	// Count by type
	edgesByType := map[string]int{}
	for _, e := range edges {
		edgesByType[e.EdgeType]++
	}
	nodesByLayer := map[string]int{}
	for _, n := range nodes {
		nodesByLayer[n.Layer+":"+n.NodeType]++
	}

	t.Logf("Edges by type: %v", edgesByType)
	t.Logf("Nodes by layer:type: %v", nodesByLayer)

	// Must-have edge types
	mustHaveEdgeTypes := []string{
		"INJECTS",
		"EXPOSES_ENDPOINT",
		"PUBLISHES_TOPIC",
		"CONSUMES_TOPIC",
		"REFERENCES_MODEL",
		"USES_MODEL",
		"CALLS_SERVICE",
	}
	for _, et := range mustHaveEdgeTypes {
		if edgesByType[et] == 0 {
			t.Errorf("MUST HAVE edge type %s — not found", et)
		}
	}

	// Must-NOT-have edge types
	if edgesByType["DEPENDS_ON"] > 0 {
		t.Errorf("MUST NOT have DEPENDS_ON edges — found %d", edgesByType["DEPENDS_ON"])
	}

	// --- Specific edge count assertions ---
	assertEdgeCount(t, edgesByType, "EXPOSES_ENDPOINT", 12, 14) // 12 HTTP + optional WS
	assertEdgeCount(t, edgesByType, "PUBLISHES_TOPIC", 1, 2)
	assertEdgeCount(t, edgesByType, "CONSUMES_TOPIC", 1, 2)
	assertEdgeCount(t, edgesByType, "REFERENCES_MODEL", 2, 4)
	assertEdgeCount(t, edgesByType, "USES_MODEL", 4, 7)
	assertEdgeCount(t, edgesByType, "INJECTS", 8, 16)

	// --- Must-have specific nodes ---
	nodeKeys := map[string]bool{}
	for _, n := range nodes {
		nodeKeys[n.NodeKey] = true
	}

	mustHaveNodes := []string{
		// Models
		"data:model:" + domain + ":cat",
		"data:model:" + domain + ":catweapon",
		"data:model:" + domain + ":mouse",
		"data:model:" + domain + ":trap",
		"data:model:" + domain + ":battleevent",
		// Enums
		"data:enum:" + domain + ":catmood",
		"data:enum:" + domain + ":weapontype",
		"data:enum:" + domain + ":trapeffect",
		"data:enum:" + domain + ":battleresult",
	}
	for _, nk := range mustHaveNodes {
		if !nodeKeys[nk] {
			t.Errorf("MUST HAVE node %s — not found", nk)
		}
	}

	// --- Must-have specific edges ---
	edgeKeys := map[string]bool{}
	for _, e := range edges {
		edgeKeys[e.EdgeKey] = true
		// Also index by simplified form: from_type:to_type:edge_type
		edgeKeys[e.EdgeType+":"+e.FromNodeKey+"->"+e.ToNodeKey] = true
	}

	mustHaveEdges := []struct {
		edgeType string
		from     string // substring match in from_node_key (case-insensitive)
		to       string // substring match in to_node_key (case-insensitive)
	}{
		// Controller -> Service INJECTS (PascalCase normalized to dot-case)
		{"INJECTS", "tom.controller", "tom.service"},
		{"INJECTS", "jerry.controller", "jerry.service"},
		{"INJECTS", "arena.controller", "arena.service"},
		{"INJECTS", "stats.controller", "spectator.service"},
		// Service -> PrismaService INJECTS
		{"INJECTS", "tom.service", "prisma.service"},
		{"INJECTS", "jerry.service", "prisma.service"},
		{"INJECTS", "arena.service", "prisma.service"},
		// ArenaService -> clients INJECTS
		{"INJECTS", "arena.service", "tom.client"},
		{"INJECTS", "arena.service", "jerry.client"},
		{"INJECTS", "arena.service", "battle-result.producer"}, // fuzzy: BattleResultProducer → battle-result.producer
		// Kafka
		{"PUBLISHES_TOPIC", "battle-result.producer", "battle-results"},
		{"CONSUMES_TOPIC", "battle-result.consumer", "battle-results"},
		// Prisma relations
		{"REFERENCES_MODEL", "cat", "catweapon"},
		{"REFERENCES_MODEL", "mouse", "trap"},
		// USES_MODEL
		{"USES_MODEL", "tom.service", "cat"},
		{"USES_MODEL", "jerry.service", "mouse"},
		{"USES_MODEL", "arena.service", "battleevent"},
	}

	for _, me := range mustHaveEdges {
		found := false
		for _, e := range edges {
			if e.EdgeType == me.edgeType &&
				strings.Contains(strings.ToLower(e.FromNodeKey), me.from) &&
				strings.Contains(strings.ToLower(e.ToNodeKey), me.to) {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("MUST HAVE edge: %s from *%s* -> *%s* — not found", me.edgeType, me.from, me.to)
		}
	}

	// --- Must-NOT-have specific edges ---
	mustNotHaveEdges := []struct {
		edgeType string
		from     string
		to       string
		reason   string
	}{
		{"USES_MODEL", "tom.service", "mouse", "TomService never touches Mouse"},
		{"USES_MODEL", "jerry.service", "cat", "JerryService never touches Cat"},
		{"USES_MODEL", "tom.service", "battleevent", "TomService doesn't use BattleEvent"},
		{"PUBLISHES_TOPIC", "tom.service", "battle-results", "TomService doesn't publish Kafka"},
	}

	for _, mne := range mustNotHaveEdges {
		for _, e := range edges {
			if e.EdgeType == mne.edgeType &&
				strings.Contains(strings.ToLower(e.FromNodeKey), mne.from) &&
				strings.Contains(strings.ToLower(e.ToNodeKey), mne.to) {
				t.Errorf("MUST NOT HAVE edge: %s from *%s* -> *%s* — %s", mne.edgeType, mne.from, mne.to, mne.reason)
			}
		}
	}
}

func TestL3_FullScan_Idempotent(t *testing.T) {
	tmpDir := t.TempDir()
	s, err := store.Open(filepath.Join(tmpDir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	reg, _ := registry.LoadDefaults()
	g := New(s, reg)

	domain := "tomandjerry"

	// First scan
	revID1, _ := s.CreateRevision(domain, "scan_1", "", "manual", "full", "{}")
	for _, ext := range tomAndJerryExtractions() {
		g.SaveFileExtraction(revID1, domain, ext.FilePath, "extracted", ext.FromType, ext.Facts, "")
	}
	g.ResolveExtractions(domain, revID1)

	nodes1, _ := s.ListNodes(store.NodeFilter{Domain: domain})
	edges1, _ := s.ListEdges(store.EdgeFilter{})

	// Second scan — same facts
	revID2, _ := s.CreateRevision(domain, "scan_2", "", "manual", "full", "{}")
	for _, ext := range tomAndJerryExtractions() {
		g.SaveFileExtraction(revID2, domain, ext.FilePath, "extracted", ext.FromType, ext.Facts, "")
	}
	g.ResolveExtractions(domain, revID2)

	nodes2, _ := s.ListNodes(store.NodeFilter{Domain: domain})
	edges2, _ := s.ListEdges(store.EdgeFilter{})

	if len(nodes2) != len(nodes1) {
		t.Errorf("idempotent scan changed node count: %d -> %d", len(nodes1), len(nodes2))
	}
	if len(edges2) != len(edges1) {
		t.Errorf("idempotent scan changed edge count: %d -> %d", len(edges1), len(edges2))
	}
}

func assertEdgeCount(t *testing.T, counts map[string]int, edgeType string, min, max int) {
	t.Helper()
	got := counts[edgeType]
	if got < min || got > max {
		t.Errorf("%s: expected %d-%d, got %d", edgeType, min, max, got)
	}
}

func TestL3_FullScan_NoParseErrors(t *testing.T) {
	tmpDir := t.TempDir()
	s, err := store.Open(filepath.Join(tmpDir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	reg, _ := registry.LoadDefaults()
	g := New(s, reg)
	domain := "tomandjerry"
	revID, _ := s.CreateRevision(domain, "scan_1", "", "manual", "full", "{}")

	for _, ext := range tomAndJerryExtractions() {
		g.SaveFileExtraction(revID, domain, ext.FilePath, "extracted", ext.FromType, ext.Facts, "")
	}

	result, err := g.ResolveExtractions(domain, revID)
	if err != nil {
		t.Fatal(err)
	}

	parseErrors := 0
	for _, u := range result.Unresolved {
		if u.Kind == "parse_error" {
			parseErrors++
			t.Errorf("parse error in %s: %s", u.FromFile, u.Reason)
		}
	}
	if parseErrors > 0 {
		t.Errorf("got %d parse errors, expected 0", parseErrors)
	}

	// Also verify resolve actually produced something
	if result.NodesCreated == 0 {
		t.Error("resolve created 0 nodes — resolve may not have run")
	}
	if result.EdgesCreated == 0 {
		t.Error("resolve created 0 edges — resolve may not have run")
	}

	fmt.Printf("L3 stats: %d files, %d nodes, %d edges, %d unresolved\n",
		result.FilesProcessed, result.NodesCreated, result.EdgesCreated, len(result.Unresolved))
}
