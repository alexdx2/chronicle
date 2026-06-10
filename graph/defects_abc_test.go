package graph

// defects_abc_test.go — TDD for Defect A, B, C per lab replay evidence (revision 12).
//
// Defect A: import fact handler resolves relative paths via class-name/alias lookup
//   instead of path-based resolution → cross-service CONTAINS edge possible when
//   alias lookup finds a node from a different service first.
//
// Defect B: tom.module→tom.controller CONTAINS deactivated when the import handler
//   also creates a CONTAINS edge to the same child (two parents detected by conflict
//   detector, but they ARE the same parent — the dedup should keep both or keep one).
//   Actually: self-edge from controller gets created; conflict detector sees self-edge
//   and the real edge as different-FromNodeID → deactivates one.
//
// Defect C: battle.gateway.ts stored with from_type="controller" when
//   RulesetsForTech(nestjs) includes WebSocket but still the merge produces wrong result.

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alexdx2/chronicle-core/extract/rules"
	"github.com/alexdx2/chronicle-core/store"
)

// ---------------------------------------------------------------------------
// Defect A — import handler uses alias, not relative path
// ---------------------------------------------------------------------------

// TestDefectA_ImportHandler_RelativePath_NoCrossServiceEdge is the primary
// failing test for Defect A.
//
// Scenario: 4 services each with prisma.service.ts (like tom-and-jerry fixture).
// tom.module.ts has:
//   import PrismaService from "../prisma/prisma.service"  (relative — deterministic)
//   provides PrismaService
//
// arena.service.ts (processed BEFORE tom.module) also imports PrismaService from
//   "../prisma/prisma.service" → registers alias "prisma.service" on ARENA's node.
//
// Without fix: import handler alias lookup finds arena's prisma.service first
//   (1 or ambiguous result depending on timing) → creates WRONG edge tom.module→arena's node.
// With fix: import handler resolves "./x" / "../x" via importMap → always hits tom's node.
func TestDefectA_ImportHandler_RelativePath_NoCrossServiceEdge(t *testing.T) {
	g, s, revID := setupTestGraph(t)
	domain := "testapp"

	// arena-api service imports PrismaService (creates alias on arena's prisma node)
	// Processed first (has endpoint facts → order=1, or just first in list for order=3)
	arenaServiceFacts := `[
		{"kind":"import","symbols":["PrismaService"],"to":"../prisma/prisma.service","from_type":"provider"},
		{"kind":"injects","to":"PrismaService","from_type":"provider"}
	]`
	g.SaveFileExtraction(revID, domain, "arena-api/src/arena/arena.service.ts", "extracted", "provider", arenaServiceFacts, "")
	g.SaveFileExtraction(revID, domain, "arena-api/src/prisma/prisma.service.ts", "extracted", "provider", `[]`, "")

	// arena.module — registers arena's prisma.service via provides+import
	arenaModuleFacts := `[
		{"kind":"import","symbols":["ArenaService"],"to":"./arena.service","from_type":"module"},
		{"kind":"import","symbols":["PrismaService"],"to":"../prisma/prisma.service","from_type":"module"},
		{"kind":"provides","to":"ArenaService","to_type":"provider","from_type":"module"},
		{"kind":"provides","to":"PrismaService","to_type":"provider","from_type":"module"}
	]`
	g.SaveFileExtraction(revID, domain, "arena-api/src/arena/arena.module.ts", "extracted", "module", arenaModuleFacts, "")

	// tom-api: prisma service exists
	g.SaveFileExtraction(revID, domain, "tom-api/src/prisma/prisma.service.ts", "extracted", "provider", `[]`, "")

	// tom.service.ts: imports PrismaService from "../prisma/prisma.service"
	tomServiceFacts := `[
		{"kind":"import","symbols":["PrismaService"],"to":"../prisma/prisma.service","from_type":"provider"},
		{"kind":"injects","to":"PrismaService","from_type":"provider"},
		{"kind":"endpoint","method":"GET","target":"/tom/status","from_type":"controller"}
	]`
	g.SaveFileExtraction(revID, domain, "tom-api/src/tom/tom.service.ts", "extracted", "provider", tomServiceFacts, "")

	// tom.module.ts: imports from relative paths (the critical test)
	moduleFacts := `[
		{"kind":"import","symbols":["TomService"],"to":"./tom.service","from_type":"module"},
		{"kind":"import","symbols":["PrismaService"],"to":"../prisma/prisma.service","from_type":"module"},
		{"kind":"provides","to":"TomService","to_type":"provider","from_type":"module"},
		{"kind":"provides","to":"PrismaService","to_type":"provider","from_type":"module"}
	]`
	g.SaveFileExtraction(revID, domain, "tom-api/src/tom/tom.module.ts", "extracted", "module", moduleFacts, "")

	_, err := g.ResolveExtractions(domain, revID)
	if err != nil {
		t.Fatalf("ResolveExtractions: %v", err)
	}

	moduleKey := "code:module:testapp:tom-api/src/tom/tom.module"
	tomPrismaKey := "code:provider:testapp:tom-api/src/prisma/prisma.service"
	arenaPrismaKey := "code:provider:testapp:arena-api/src/prisma/prisma.service"

	allEdges, _ := s.ListEdges(store.EdgeFilter{})

	// MUST NOT have any edge from tom.module → arena's prisma.service
	for _, e := range allEdges {
		if e.FromNodeKey == moduleKey && e.ToNodeKey == arenaPrismaKey {
			t.Errorf("DefectA: cross-service edge exists: %s → %s (active=%v); import handler resolved relative path via wrong alias",
				moduleKey, arenaPrismaKey, e.Active)
		}
	}

	// MUST have an active edge from tom.module → tom's prisma.service
	foundCorrect := false
	for _, e := range allEdges {
		if e.FromNodeKey == moduleKey && e.ToNodeKey == tomPrismaKey && e.Active {
			foundCorrect = true
		}
	}
	if !foundCorrect {
		t.Errorf("DefectA: no active edge from tom.module → tom's prisma.service; edges from module: %v",
			edgesFrom(allEdges, moduleKey))
	}
}

// TestDefectA_ImportHandler_RelativePath_StemNodeNotCreated verifies that when
// the import handler processes a relative path, it does NOT create an orphan stem
// node (e.g. "code:provider:domain:prisma.service") — it goes directly to the
// path-keyed node.
func TestDefectA_ImportHandler_RelativePath_StemNodeNotCreated(t *testing.T) {
	g, s, revID := setupTestGraph(t)
	domain := "testapp"

	// Two services each with prisma.service.ts
	g.SaveFileExtraction(revID, domain, "svc-a/src/prisma/prisma.service.ts", "extracted", "provider", `[]`, "")
	g.SaveFileExtraction(revID, domain, "svc-b/src/prisma/prisma.service.ts", "extracted", "provider", `[]`, "")

	// Module imports PrismaService from relative path
	moduleFacts := `[
		{"kind":"import","symbols":["PrismaService"],"to":"../prisma/prisma.service","from_type":"module"},
		{"kind":"provides","to":"PrismaService","to_type":"provider","from_type":"module"}
	]`
	g.SaveFileExtraction(revID, domain, "svc-a/src/tom/tom.module.ts", "extracted", "module", moduleFacts, "")

	_, err := g.ResolveExtractions(domain, revID)
	if err != nil {
		t.Fatalf("ResolveExtractions: %v", err)
	}

	// Must NOT have a stem node for "prisma.service" (without path)
	stemKey := "code:provider:testapp:prisma.service"
	nodes, _ := s.ListNodes(store.NodeFilter{Domain: domain})
	for _, n := range nodes {
		if n.NodeKey == stemKey {
			t.Errorf("DefectA: orphan stem node created: %s (import handler should use path-based resolution)", n.NodeKey)
		}
	}
}

// TestDefectA_TwoServices_NoCrossServiceContains reproduces the exact lab
// scenario: two NestJS services each with prisma.service.ts, both modules
// importing PrismaService via "../prisma/prisma.service".
//
// Without fix: arena.module's import handler resolved "prisma.service" via
// FindCodeNodesByAlias and found tom's prisma (registered first via injects),
// producing arena.module → tom's prisma CONTAINS (active) AND deactivating
// tom.module → tom's prisma (conflict detection killed the correct edge).
//
// With fix: relative import specifiers bypass alias lookup → each module
// resolves to its own prisma.service deterministically.
func TestDefectA_TwoServices_NoCrossServiceContains(t *testing.T) {
	g, s, revID := setupTestGraph(t)
	domain := "testapp"

	// tom.controller.ts has endpoints → order=1
	tomCtrlFacts := `[
		{"kind":"import","symbols":["TomService"],"to":"./tom.service"},
		{"kind":"injects","to":"TomService"},
		{"kind":"endpoint","method":"GET","target":"/tom/status"}
	]`
	g.SaveFileExtraction(revID, domain, "fixtures/tom-api/src/tom/tom.controller.ts", "extracted", "controller", tomCtrlFacts, "")

	// tom.service.ts has parent + injects + import for prisma
	tomSvcFacts := `[
		{"kind":"import","symbols":["PrismaService"],"to":"../prisma/prisma.service"},
		{"kind":"provides","to":"TomService"},
		{"kind":"parent","reason":"declared in @Module.providers","to":"tom.module"},
		{"kind":"injects","to":"PrismaService"}
	]`
	g.SaveFileExtraction(revID, domain, "fixtures/tom-api/src/tom/tom.service.ts", "extracted", "provider", tomSvcFacts, "")
	g.SaveFileExtraction(revID, domain, "fixtures/tom-api/src/prisma/prisma.service.ts", "extracted", "provider", `[]`, "")

	// tom.module.ts — has relative imports for all its providers
	tomModFacts := `[
		{"kind":"import","symbols":["TomController"],"to":"./tom.controller"},
		{"kind":"import","symbols":["TomService"],"to":"./tom.service"},
		{"kind":"import","symbols":["PrismaService"],"to":"../prisma/prisma.service"},
		{"kind":"provides","to":"TomController","to_type":"controller"},
		{"kind":"provides","to":"TomService","to_type":"provider"},
		{"kind":"provides","to":"PrismaService","to_type":"provider"}
	]`
	g.SaveFileExtraction(revID, domain, "fixtures/tom-api/src/tom/tom.module.ts", "extracted", "module", tomModFacts, "")

	// arena.controller.ts has endpoints → order=1
	arenaCtrlFacts := `[
		{"kind":"import","symbols":["ArenaService"],"to":"./arena.service"},
		{"kind":"injects","to":"ArenaService"},
		{"kind":"endpoint","method":"POST","target":"/arena/attack"}
	]`
	g.SaveFileExtraction(revID, domain, "fixtures/arena-api/src/arena/arena.controller.ts", "extracted", "controller", arenaCtrlFacts, "")

	// arena.service.ts imports prisma too
	arenaSvcFacts := `[
		{"kind":"import","symbols":["PrismaService"],"to":"../prisma/prisma.service"},
		{"kind":"provides","to":"ArenaService"},
		{"kind":"parent","to":"arena.module"},
		{"kind":"injects","to":"PrismaService"}
	]`
	g.SaveFileExtraction(revID, domain, "fixtures/arena-api/src/arena/arena.service.ts", "extracted", "provider", arenaSvcFacts, "")
	g.SaveFileExtraction(revID, domain, "fixtures/arena-api/src/prisma/prisma.service.ts", "extracted", "provider", `[]`, "")

	arenaModFacts := `[
		{"kind":"import","symbols":["ArenaController"],"to":"./arena.controller"},
		{"kind":"import","symbols":["ArenaService"],"to":"./arena.service"},
		{"kind":"import","symbols":["PrismaService"],"to":"../prisma/prisma.service"},
		{"kind":"provides","to":"ArenaController","to_type":"controller"},
		{"kind":"provides","to":"ArenaService","to_type":"provider"},
		{"kind":"provides","to":"PrismaService","to_type":"provider"}
	]`
	g.SaveFileExtraction(revID, domain, "fixtures/arena-api/src/arena/arena.module.ts", "extracted", "module", arenaModFacts, "")

	_, err := g.ResolveExtractions(domain, revID)
	if err != nil {
		t.Fatalf("ResolveExtractions: %v", err)
	}

	allEdges, _ := s.ListEdges(store.EdgeFilter{})
	containsEdges, _ := s.ListEdges(store.EdgeFilter{EdgeType: "CONTAINS"})

	tomModKey := "code:module:testapp:fixtures/tom-api/src/tom/tom.module"
	tomCtrlKey := "code:controller:testapp:fixtures/tom-api/src/tom/tom.controller"
	tomPrismaKey := "code:provider:testapp:fixtures/tom-api/src/prisma/prisma.service"
	arenaModKey := "code:module:testapp:fixtures/arena-api/src/arena/arena.module"
	arenaPrismaKey := "code:provider:testapp:fixtures/arena-api/src/prisma/prisma.service"

	// tom.module → tom's prisma must be ACTIVE
	tomPrismaActive := false
	for _, e := range containsEdges {
		if e.FromNodeKey == tomModKey && e.ToNodeKey == tomPrismaKey {
			if e.Active {
				tomPrismaActive = true
			} else {
				t.Errorf("DefectA: tom.module→tom's prisma CONTAINS exists but INACTIVE; cross-service edge poisoned conflict detection")
			}
		}
		// tom.module must NOT have edge to arena's prisma
		if e.FromNodeKey == tomModKey && e.ToNodeKey == arenaPrismaKey {
			t.Errorf("DefectA: cross-service CONTAINS: tom.module → arena's prisma (active=%v)", e.Active)
		}
		// arena.module must NOT have edge to tom's prisma
		if e.FromNodeKey == arenaModKey && e.ToNodeKey == tomPrismaKey {
			t.Errorf("DefectA: cross-service CONTAINS: arena.module → tom's prisma (active=%v)", e.Active)
		}
	}
	if !tomPrismaActive {
		t.Errorf("DefectA: tom.module→tom's prisma CONTAINS missing or inactive; all CONTAINS: %v", edgeSummary(containsEdges))
	}

	// tom.module → tom.controller must be ACTIVE (Defect B: was deactivated by self-edge conflict)
	tomCtrlActive := false
	for _, e := range containsEdges {
		if e.FromNodeKey == tomModKey && e.ToNodeKey == tomCtrlKey && e.Active {
			tomCtrlActive = true
		}
	}
	if !tomCtrlActive {
		t.Errorf("DefectB: tom.module→tom.controller CONTAINS missing or inactive; all CONTAINS: %v\nall edges from tom.module: %v",
			edgeSummary(containsEdges), edgesFrom(allEdges, tomModKey))
	}

	// No self-edges anywhere
	for _, e := range allEdges {
		if e.FromNodeID == e.ToNodeID {
			t.Errorf("self-edge: %s → %s : %s (active=%v)", e.FromNodeKey, e.ToNodeKey, e.EdgeType, e.Active)
		}
	}
}

// ---------------------------------------------------------------------------
// Defect B — module→controller CONTAINS active after conflict detection
// ---------------------------------------------------------------------------

// TestDefectB_ModuleControllerCONTAINS_StaysActive_WithSelfEdgeInput verifies that
// the exact rev-12 scenario does not produce a deactivated module→controller edge.
//
// Key inputs that trigger the bug:
//   - tom.controller.ts has provides("TomController") with from_type="" (inherited)
//   - tom.controller.ts has parent("tom.module")
//   - tom.module.ts has import("TomController") + provides("TomController")
//
// Without fix: provides fact from controller file with from_type="" is NOT blocked
//   by the guard (fact.FromType = "" before inheritance = passes guard) → creates
//   self-CONTAINS. OR: the import handler creates a CONTAINS edge from a stem-module
//   node to the controller → conflict with real module edge.
// With fix: all self-CONTAINS are blocked; module→controller stays active.
func TestDefectB_ModuleControllerCONTAINS_StaysActive_WithSelfEdgeInput(t *testing.T) {
	g, s, revID := setupTestGraph(t)
	domain := "testapp"

	// tom.controller.ts — exact rev-12 facts: includes provides(TomController) with NO from_type on fact
	// Note: fact has no "from_type" field → it gets inherited from file from_type="controller"
	// The provides guard checks fact.FromType AFTER inheritance → blocks it → no self-CONTAINS.
	// But the import handler also runs for "./tom.service" import.
	controllerFacts := `[
		{"kind":"import","symbols":["TomService"],"to":"./tom.service"},
		{"kind":"provides","to":"TomController"},
		{"kind":"parent","reason":"declared in @Module.controllers","to":"tom.module"},
		{"kind":"injects","to":"TomService"},
		{"kind":"endpoint","method":"GET","target":"/tom/status"},
		{"kind":"endpoint","method":"GET","target":"/tom/weapons"}
	]`
	g.SaveFileExtraction(revID, domain, "tom-api/src/tom/tom.controller.ts", "extracted", "controller", controllerFacts, "")

	// tom.service.ts — has parent + provides (both from non-module file → guard blocks provides)
	serviceFacts := `[
		{"kind":"import","symbols":["PrismaService"],"to":"../prisma/prisma.service"},
		{"kind":"provides","to":"TomService"},
		{"kind":"parent","reason":"declared in @Module.providers","to":"tom.module"},
		{"kind":"injects","to":"PrismaService"}
	]`
	g.SaveFileExtraction(revID, domain, "tom-api/src/tom/tom.service.ts", "extracted", "provider", serviceFacts, "")

	// logging.middleware.ts — has provides(LoggingMiddleware) from non-module → guard blocks
	g.SaveFileExtraction(revID, domain, "tom-api/src/tom/logging.middleware.ts", "extracted", "provider",
		`[{"kind":"provides","to":"LoggingMiddleware"}]`, "")

	// tom-api prisma
	g.SaveFileExtraction(revID, domain, "tom-api/src/prisma/prisma.service.ts", "extracted", "provider", `[]`, "")

	// tom.module.ts — exact rev-12 facts
	moduleFacts := `[
		{"kind":"import","symbols":["TomController"],"to":"./tom.controller"},
		{"kind":"import","symbols":["TomService"],"to":"./tom.service"},
		{"kind":"import","symbols":["PrismaService"],"to":"../prisma/prisma.service"},
		{"kind":"import","symbols":["LoggingMiddleware"],"to":"./logging.middleware"},
		{"kind":"provides","to":"TomController","to_type":"controller"},
		{"kind":"provides","to":"TomService","to_type":"provider"},
		{"kind":"provides","to":"PrismaService","to_type":"provider"}
	]`
	g.SaveFileExtraction(revID, domain, "tom-api/src/tom/tom.module.ts", "extracted", "module", moduleFacts, "")

	_, err := g.ResolveExtractions(domain, revID)
	if err != nil {
		t.Fatalf("ResolveExtractions: %v", err)
	}

	moduleKey := "code:module:testapp:tom-api/src/tom/tom.module"
	controllerKey := "code:controller:testapp:tom-api/src/tom/tom.controller"

	allEdges, _ := s.ListEdges(store.EdgeFilter{})
	containsEdges, _ := s.ListEdges(store.EdgeFilter{EdgeType: "CONTAINS"})

	// module→controller CONTAINS must be ACTIVE
	foundActive := false
	for _, e := range containsEdges {
		if e.FromNodeKey == moduleKey && e.ToNodeKey == controllerKey {
			if e.Active {
				foundActive = true
			} else {
				t.Errorf("DefectB: module→controller CONTAINS exists but is INACTIVE; all CONTAINS: %v",
					edgeSummary(containsEdges))
			}
		}
	}
	if !foundActive {
		t.Errorf("DefectB: module→controller CONTAINS not found or inactive; all edges from module: %v\nall CONTAINS: %v",
			edgesFrom(allEdges, moduleKey), edgeSummary(containsEdges))
	}

	// No self-CONTAINS on controller
	for _, e := range containsEdges {
		if e.FromNodeKey == controllerKey && e.ToNodeKey == controllerKey {
			t.Errorf("DefectB: self-CONTAINS on controller: %s active=%v", e.EdgeKey, e.Active)
		}
	}
}

// TestDefectB_FindNodeByNameInDomain_ExactNameMatch verifies that
// findNodeByNameInDomain("tom.module") returns the MODULE node, not controller.
func TestDefectB_FindNodeByNameInDomain_ExactNameMatch(t *testing.T) {
	g, s, revID := setupTestGraph(t)
	domain := "testapp"

	s.UpsertNode(store.NodeRow{
		NodeKey:            "code:controller:testapp:tom-api/src/tom/tom.controller",
		Layer:              "code",
		NodeType:           "controller",
		DomainKey:          domain,
		Name:               "tom.controller",
		FilePath:           "tom-api/src/tom/tom.controller.ts",
		Status:             "active",
		LastSeenRevisionID: revID,
	})
	s.UpsertNode(store.NodeRow{
		NodeKey:            "code:module:testapp:tom-api/src/tom/tom.module",
		Layer:              "code",
		NodeType:           "module",
		DomainKey:          domain,
		Name:               "tom.module",
		FilePath:           "tom-api/src/tom/tom.module.ts",
		Status:             "active",
		LastSeenRevisionID: revID,
	})

	result := g.findNodeByNameInDomain(domain, "tom.module")
	if result == nil {
		t.Fatal("DefectB: findNodeByNameInDomain(\"tom.module\") returned nil")
	}
	if result.NodeType != "module" {
		t.Errorf("DefectB: findNodeByNameInDomain(\"tom.module\") returned node_type=%q (key=%s), want \"module\"",
			result.NodeType, result.NodeKey)
	}

	result2 := g.findNodeByNameInDomain(domain, "tom.controller")
	if result2 == nil {
		t.Fatal("DefectB: findNodeByNameInDomain(\"tom.controller\") returned nil")
	}
	if result2.NodeType != "controller" {
		t.Errorf("DefectB: findNodeByNameInDomain(\"tom.controller\") returned node_type=%q (key=%s), want \"controller\"",
			result2.NodeType, result2.NodeKey)
	}
}

// TestDefectB_SelfEdge_NeverCreatedByProvides verifies that when a controller
// file has {"kind":"provides","to":"TomController"} with no from_type on the fact
// (gets inherited from file from_type="controller"), the provides guard blocks it
// and NO self-CONTAINS is created.
func TestDefectB_SelfEdge_NeverCreatedByProvides(t *testing.T) {
	g, s, revID := setupTestGraph(t)
	domain := "testapp"

	// Controller file with self-provides (from_type inherited = "controller")
	facts := `[
		{"kind":"provides","to":"TomController"},
		{"kind":"endpoint","method":"GET","target":"/tom/status"}
	]`
	g.SaveFileExtraction(revID, domain, "tom-api/src/tom/tom.controller.ts", "extracted", "controller", facts, "")

	_, err := g.ResolveExtractions(domain, revID)
	if err != nil {
		t.Fatalf("ResolveExtractions: %v", err)
	}

	containsEdges, _ := s.ListEdges(store.EdgeFilter{EdgeType: "CONTAINS"})
	for _, e := range containsEdges {
		if strings.Contains(e.FromNodeKey, "tom.controller") && strings.Contains(e.ToNodeKey, "tom.controller") {
			t.Errorf("DefectB: self-CONTAINS created for controller provides: %s active=%v", e.EdgeKey, e.Active)
		}
	}
}

// ---------------------------------------------------------------------------
// Defect C — battle.gateway.ts from_type = "provider" with manifest tech list
// ---------------------------------------------------------------------------

// TestDefectC_BattleGateway_FromType_Provider verifies that MergeASTFactsJSON
// on the real battle.gateway.ts fixture with tech=[typescript nestjs prisma kafka dotnet]
// returns from_type="provider" (from @WebSocketGateway), NOT "controller".
func TestDefectC_BattleGateway_FromType_Provider(t *testing.T) {
	root := repoRootFromCWD(t)
	if root == "" {
		t.Skip("could not locate repo root (.git)")
	}
	relPath := "fixtures/tom-and-jerry/arena-api/src/arena/battle.gateway.ts"
	if _, err := os.Stat(filepath.Join(root, relPath)); err != nil {
		t.Skipf("fixture absent: %s", relPath)
	}
	orig, _ := os.Getwd()
	if err := os.Chdir(root); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(orig) })

	// Manifest tech list from the tom-and-jerry scan (rev 12):
	tech := []string{"typescript", "nestjs", "prisma", "kafka", "dotnet"}

	// LLM (haiku) wrongly classified it as "controller"
	_, fromType := MergeASTFactsJSON(relPath, "[]", "controller", tech)
	if fromType != "provider" {
		t.Errorf("DefectC: MergeASTFactsJSON with tech=%v returned from_type=%q, want \"provider\" (@WebSocketGateway → provider)",
			tech, fromType)
	}
}

// TestDefectC_RulesetsForTech_NestJS_IncludesWebSocket verifies that
// RulesetsForTech with tech containing "nestjs" includes the WebSocket ruleset.
func TestDefectC_RulesetsForTech_NestJS_IncludesWebSocket(t *testing.T) {
	tech := []string{"typescript", "nestjs", "prisma", "kafka", "dotnet"}
	rulesets := rules.RulesetsForTech(tech)

	hasWebSocket := false
	for _, rs := range rulesets {
		if rs.Name == "websocket" {
			hasWebSocket = true
			break
		}
	}
	if !hasWebSocket {
		names := make([]string, len(rulesets))
		for i, rs := range rulesets {
			names[i] = rs.Name
		}
		t.Errorf("DefectC: RulesetsForTech(%v) missing 'websocket' ruleset; got: %v", tech, names)
	}
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func edgesFrom(edges []store.EdgeRow, fromKey string) []string {
	var result []string
	for _, e := range edges {
		if e.FromNodeKey == fromKey {
			result = append(result, fmt.Sprintf("%s→%s(%s,active=%v)", e.FromNodeKey, e.ToNodeKey, e.EdgeType, e.Active))
		}
	}
	return result
}

func edgeSummary(edges []store.EdgeRow) []string {
	var result []string
	for _, e := range edges {
		result = append(result, fmt.Sprintf("%s→%s(active=%v)", e.FromNodeKey, e.ToNodeKey, e.Active))
	}
	return result
}

// suppress unused import warning
var _ = strings.ToLower
