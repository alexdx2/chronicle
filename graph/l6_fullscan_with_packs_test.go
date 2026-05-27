package graph

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alexdx2/chronicle-core/graph/prompts"
	"github.com/alexdx2/chronicle-core/registry"
	"github.com/alexdx2/chronicle-core/store"
)

// L6 — Full scan with custom pack creation flow.
// Simulates the complete orchestrator flow:
//   1. Detect manifest tech with no built-in packs
//   2. Create custom packs (using pre-built content, simulating sonnet output)
//   3. Scan all files with core + custom packs
//   4. Verify graph quality

// Simulated sonnet-created pack for Bull queue system
const bullQueuePack = `# BULL QUEUE EXTRACTION RULES

Apply when file uses @nestjs/bull decorators or bull Queue types.

## Queue producers
Bull queue add/dispatch calls produce to a named queue:
- ` + "`" + `@InjectQueue('battle-queue')` + "`" + ` => queue name is "battle-queue"
- ` + "`" + `this.queue.add('attack', data)` + "`" + ` => ` + "`" + `{"kind":"produces","to":"battle-queue:attack","method":"add"}` + "`" + `

## Queue consumers
Bull processors consume from a named queue:
- ` + "`" + `@Processor('battle-queue')` + "`" + ` marks the class as a consumer
- ` + "`" + `@Process('attack')` + "`" + ` => ` + "`" + `{"kind":"consumes","to":"battle-queue:attack","method":"handleAttack"}` + "`" + `
- ` + "`" + `@Process('combo')` + "`" + ` => ` + "`" + `{"kind":"consumes","to":"battle-queue:combo","method":"handleCombo"}` + "`" + `

## DI
- Constructor injection of Queue: ` + "`" + `@InjectQueue('name') queue: Queue` + "`" + ` => ` + "`" + `{"kind":"injects","to":"BullQueue:battle-queue"}` + "`" + `
- Service injection in processor: constructor params => ` + "`" + `{"kind":"injects","to":"ServiceName"}` + "`" + `

## Do NOT extract
- Bull board admin UI
- Queue event listeners for monitoring
- Rate limiter config
`

// Simulated sonnet-created pack for Socket.IO
const socketIOPack = `# SOCKET.IO EXTRACTION RULES

Apply when file uses socket.io Server/Socket types or @WebSocketGateway.

## WebSocket endpoints
- ` + "`" + `@SubscribeMessage('watch-battle')` + "`" + ` => ` + "`" + `{"kind":"endpoint","method":"WS","target":"watch-battle"}` + "`" + `
- ` + "`" + `@WebSocketGateway()` + "`" + ` marks the class as a controller (from_type="controller")

## Server emit (outbound events)
- ` + "`" + `server.emit('battle-result', data)` + "`" + ` => ` + "`" + `{"kind":"produces","to":"battle-result","method":"emit"}` + "`" + `
- ` + "`" + `client.emit('update', data)` + "`" + ` => ` + "`" + `{"kind":"produces","to":"update","method":"emit"}` + "`" + `

## Do NOT extract
- Connection/disconnect lifecycle handlers
- Room join/leave operations (unless business-meaningful)
- Acknowledgment callbacks
`

func TestL6_FullScanWithCustomPacks(t *testing.T) {
	tmpDir := t.TempDir()
	s, err := store.Open(filepath.Join(tmpDir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	reg, _ := registry.LoadDefaults()
	g := New(s, reg)
	domain := "tomandjerry"

	// === STEP 1: Detect instruction gaps ===
	manifestTech := []string{"nestjs", "prisma", "kafkajs", "bull", "socketio", "event-emitter"}
	gaps := prompts.DetectInstructionGaps(manifestTech)

	t.Logf("Manifest tech: %v", manifestTech)
	t.Logf("Detected gaps: %v", gapNames(gaps))

	// nestjs and prisma should be covered
	for _, g := range gaps {
		if g.Tech == "nestjs" || g.Tech == "prisma" {
			t.Errorf("should NOT have gap for %s — built-in pack exists", g.Tech)
		}
	}

	// bull, socketio, event-emitter, kafkajs should be gaps
	expectedGaps := []string{"bull", "socketio", "event-emitter", "kafkajs"}
	for _, expected := range expectedGaps {
		found := false
		for _, g := range gaps {
			if g.Tech == expected {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("expected gap for %s — not detected", expected)
		}
	}

	// === STEP 2: Validate & save custom packs (simulating sonnet output) ===
	customPacks := map[string]string{
		"custom/bull":     bullQueuePack,
		"custom/socketio": socketIOPack,
	}

	for id, content := range customPacks {
		// Validate ID
		if idErr := prompts.ValidateCustomPackID(id); idErr != "" {
			t.Fatalf("bad pack ID %s: %s", id, idErr)
		}

		// Validate content (no unknown kinds)
		invalid := prompts.ValidateCustomPack(content)
		if len(invalid) > 0 {
			t.Fatalf("pack %s has invalid kinds: %v", id, invalid)
		}

		// Save to settings (simulating chronicle_save_custom_pack)
		err := s.SetSetting("custom_pack_"+id, content)
		if err != nil {
			t.Fatalf("save pack %s: %v", id, err)
		}
		t.Logf("Saved custom pack: %s", id)
	}

	// Verify packs are loadable
	for id := range customPacks {
		loaded, err := s.GetSetting("custom_pack_" + id)
		if err != nil || loaded == "" {
			t.Fatalf("failed to load saved pack %s", id)
		}
	}

	// === STEP 3: Full scan with all extractions ===
	revID, _ := s.CreateRevision(domain, "scan_1", "", "manual", "full", "{}")

	// Use the same tom-and-jerry extractions + add Bull/Socket.IO specific facts
	extractions := tomAndJerryExtractions()

	// Add facts that the custom packs would help agents extract:
	// Bull queue facts from battle.queue.ts (currently has just injects)
	extractions = append(extractions, mockExtraction{
		FilePath: "arena-api/src/arena/battle.queue.ts.enriched",
		Facts: `[
			{"kind":"produces","to":"battle-queue:attack","method":"add"},
			{"kind":"produces","to":"battle-queue:combo","method":"add"},
			{"kind":"consumes","to":"battle-queue:attack","method":"handleAttack"},
			{"kind":"consumes","to":"battle-queue:combo","method":"handleCombo"}
		]`,
	})

	// Socket.IO facts from battle.gateway.ts (currently has endpoint + injects)
	extractions = append(extractions, mockExtraction{
		FilePath: "arena-api/src/arena/battle.gateway.ts.enriched",
		Facts: `[
			{"kind":"produces","to":"battle-update","method":"emit"}
		]`,
	})

	for _, ext := range extractions {
		g.SaveFileExtraction(revID, domain, ext.FilePath, "extracted", ext.FromType, ext.Facts, "")
	}

	// === STEP 4: Resolve ===
	result, err := g.ResolveExtractions(domain, revID)
	if err != nil {
		t.Fatal(err)
	}

	t.Logf("Resolved: %d files, %d nodes, %d edges, %d unresolved",
		result.FilesProcessed, result.NodesCreated, result.EdgesCreated, len(result.Unresolved))

	// === STEP 5: Assert graph quality ===
	nodes, _ := s.ListNodes(store.NodeFilter{Domain: domain})
	edges, _ := s.ListEdges(store.EdgeFilter{})

	edgesByType := map[string]int{}
	for _, e := range edges {
		edgesByType[e.EdgeType]++
	}

	t.Logf("Graph: %d nodes, %d edges", len(nodes), len(edges))
	t.Logf("Edges by type: %v", edgesByType)

	// --- Must-have edge types (including those from custom packs) ---
	mustHaveEdgeTypes := []string{
		"INJECTS",
		"EXPOSES_ENDPOINT",
		"CALLS_SERVICE",
		"USES_MODEL",
		"PUBLISHES_TOPIC",  // Kafka + Bull queues + Socket.IO
		"CONSUMES_TOPIC",   // Kafka + Bull queues
		"REFERENCES_MODEL",
	}
	for _, et := range mustHaveEdgeTypes {
		if edgesByType[et] == 0 {
			t.Errorf("MUST HAVE edge type %s — not found", et)
		}
	}

	// DEPENDS_ON must NOT exist
	if edgesByType["DEPENDS_ON"] > 0 {
		t.Errorf("MUST NOT have DEPENDS_ON edges — found %d", edgesByType["DEPENDS_ON"])
	}

	// --- Bull queue facts should produce extra PUBLISHES/CONSUMES ---
	// Original: 1 PUBLISHES (Kafka), 1 CONSUMES (Kafka)
	// With Bull: +2 PUBLISHES (attack, combo), +2 CONSUMES (attack, combo)
	// With Socket.IO: +1 PUBLISHES (battle-update)
	if edgesByType["PUBLISHES_TOPIC"] < 3 {
		t.Errorf("expected at least 3 PUBLISHES_TOPIC (Kafka + Bull + Socket.IO), got %d", edgesByType["PUBLISHES_TOPIC"])
	}
	if edgesByType["CONSUMES_TOPIC"] < 3 {
		t.Errorf("expected at least 3 CONSUMES_TOPIC (Kafka + Bull), got %d", edgesByType["CONSUMES_TOPIC"])
	}

	// --- Must-have data nodes ---
	nodeKeys := map[string]bool{}
	for _, n := range nodes {
		nodeKeys[n.NodeKey] = true
	}

	mustHaveNodes := []string{
		"data:model:" + domain + ":cat",
		"data:model:" + domain + ":mouse",
		"data:model:" + domain + ":battleevent",
		"data:enum:" + domain + ":catmood",
		"data:enum:" + domain + ":battleresult",
	}
	for _, nk := range mustHaveNodes {
		if !nodeKeys[nk] {
			t.Errorf("MUST HAVE node %s — not found", nk)
		}
	}

	// --- Must-have specific edges (core + custom pack enriched) ---
	mustHaveEdges := []struct {
		edgeType string
		from     string
		to       string
	}{
		// Core scan edges (PascalCase normalized to dot-case by resolveInjectTarget)
		{"INJECTS", "arena.controller", "arena.service"},
		{"INJECTS", "arena.service", "battle-result.producer"},
		{"PUBLISHES_TOPIC", "battle-result.producer", "battle-results"},
		{"CONSUMES_TOPIC", "battle-result.consumer", "battle-results"},
		{"USES_MODEL", "arena.service", "battleevent"},
		// Custom pack enriched edges (Bull)
		{"PUBLISHES_TOPIC", "battle.queue", "battle-queue:attack"},
		{"CONSUMES_TOPIC", "battle.queue", "battle-queue:attack"},
		// Custom pack enriched edges (Socket.IO)
		{"PUBLISHES_TOPIC", "battle.gateway", "battle-update"},
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

	// Print summary
	fmt.Printf("\n=== L6 Full Scan with Custom Packs ===\n")
	fmt.Printf("Gaps detected: %d (%s)\n", len(gaps), strings.Join(gapNames(gaps), ", "))
	fmt.Printf("Custom packs created: %d\n", len(customPacks))
	fmt.Printf("Files processed: %d\n", result.FilesProcessed)
	fmt.Printf("Nodes: %d, Edges: %d\n", len(nodes), len(edges))
	fmt.Printf("PUBLISHES_TOPIC: %d (Kafka + Bull + Socket.IO)\n", edgesByType["PUBLISHES_TOPIC"])
	fmt.Printf("CONSUMES_TOPIC: %d (Kafka + Bull)\n", edgesByType["CONSUMES_TOPIC"])
	fmt.Printf("Unresolved: %d\n", len(result.Unresolved))
	fmt.Printf("=====================================\n")
}

func gapNames(gaps []prompts.InstructionGap) []string {
	var names []string
	for _, g := range gaps {
		names = append(names, g.Tech)
	}
	return names
}
