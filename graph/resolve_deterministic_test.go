package graph

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/alexdx2/chronicle-core/store"
)

// --- Snapshot: non-deterministic resolve must stay byte-for-byte identical ---
//
// This test was written and made green BEFORE ResolveOptions.Deterministic
// existed. It pins the shape of a legacy resolve (counts + derivation kinds +
// evidence stamping) so the deterministic branch cannot leak into the default
// path. If it ever needs updating, the deterministic work broke the contract.

const snapshotDomain = "snapapp"

// Canonical node keys the fixture resolves to (canonicalNodeKey flattens dots).
const (
	snapModule         = "code:module:" + snapshotDomain + ":src/tom-module"
	snapController     = "code:controller:" + snapshotDomain + ":src/tom-controller"
	snapService        = "code:provider:" + snapshotDomain + ":src/tom-service"
	snapArmEndpoint    = "contract:endpoint:" + snapshotDomain + ":post:/tom/arm"
	snapStatusEndpoint = "contract:endpoint:" + snapshotDomain + ":get:/jerry/status"
	snapSpikeEndpoint  = "contract:endpoint:" + snapshotDomain + ":put:/spike/deploy"
	snapRepository     = "code:provider:" + snapshotDomain + ":src/tom-repository"
	snapCatModel       = "data:model:" + snapshotDomain + ":cat"
)

// snapshotFixture seeds one revision with a small but representative batch:
// module/controller/service files carrying hard facts (import, provides,
// injects, endpoint, decorator, model, model_relation) and linked facts
// (http_call, call, member_call, calls_service, calls_endpoint).
func snapshotFixture(t *testing.T, g *Graph) int64 {
	t.Helper()
	revID, err := g.Store().CreateRevision(snapshotDomain, "", "snap1", "manual", "full", "{}")
	if err != nil {
		t.Fatalf("CreateRevision: %v", err)
	}

	save := func(path, fromType string, facts []map[string]any) {
		buf, _ := json.Marshal(facts)
		if _, err := g.Store().SaveExtraction(revID, snapshotDomain, path, "extracted", fromType, string(buf), ""); err != nil {
			t.Fatalf("SaveExtraction(%s): %v", path, err)
		}
	}

	save("src/tom.module.ts", "module", []map[string]any{
		{"kind": "import", "to": "./tom.service", "symbols": []string{"TomService"}},
		{"kind": "provides", "to": "TomService", "from_type": "module"},
	})
	save("src/tom.service.ts", "provider", []map[string]any{
		{"kind": "import", "to": "./tom.repository", "symbols": []string{"TomRepository"}},
		{"kind": "model", "to": "Cat"},
		{"kind": "model_relation", "from": "Cat", "to": "Mouse"},
	})
	save("src/tom.controller.ts", "controller", []map[string]any{
		{"kind": "decorator", "decorator": "Controller", "from": "TomController"},
		{"kind": "endpoint", "method": "POST", "from": "tom", "target": "arm"},
		{"kind": "injects", "to": "TomService"},
		{"kind": "call", "object": "TomService", "method": "arm"},
		{"kind": "member_call", "to": "cat"},
		{"kind": "http_call", "method": "GET", "target": "http://jerry-api/jerry/status"},
		// An endpoint no file in this batch exposes — the interesting case:
		// legacy mode mints it, a deterministic resolve refuses to.
		{"kind": "calls_endpoint", "method": "PUT", "target": "/spike/deploy"},
	})
	return revID
}

func edgeOrFail(t *testing.T, g *Graph, key string) *store.EdgeRow {
	t.Helper()
	e, err := g.Store().GetEdgeByKey(key)
	if err != nil {
		t.Fatalf("edge %s: %v", key, err)
	}
	return e
}

func TestResolveSnapshot_LegacyModeUnchanged(t *testing.T) {
	g, _, _ := setupTestGraph(t)
	revID := snapshotFixture(t, g)

	result, err := g.ResolveExtractions(snapshotDomain, revID)
	if err != nil {
		t.Fatalf("ResolveExtractions: %v", err)
	}

	// Exact counts — the snapshot. Any drift means the default path changed.
	if result.FilesProcessed != 3 {
		t.Errorf("FilesProcessed = %d, want 3", result.FilesProcessed)
	}
	if result.NodesCreated != 1 {
		t.Errorf("NodesCreated = %d, want 1", result.NodesCreated)
	}
	if result.EdgesCreated != 9 {
		t.Errorf("EdgesCreated = %d, want 9", result.EdgesCreated)
	}
	if result.EvidenceCreated != 12 {
		t.Errorf("EvidenceCreated = %d, want 12", result.EvidenceCreated)
	}
	if len(result.Unresolved) != 0 {
		t.Errorf("Unresolved = %v, want none", result.Unresolved)
	}

	// Derivation kinds by fact family.
	for key, want := range map[string]string{
		snapService + "->" + snapRepository + ":INJECTS":                                                  "hard",
		snapModule + "->" + snapService + ":CONTAINS":                                                     "hard",
		snapController + "->" + snapService + ":INJECTS":                                                  "hard",
		snapController + "->" + snapArmEndpoint + ":EXPOSES_ENDPOINT":                                     "hard",
		snapController + "->" + snapCatModel + ":USES_MODEL":                                              "hard",
		snapController + "->service:external_system:" + snapshotDomain + ":jerry-api:CALLS_SERVICE":       "linked",
		snapController + "->" + snapStatusEndpoint + ":CALLS_ENDPOINT":                                    "linked",
		snapController + "->" + snapSpikeEndpoint + ":CALLS_ENDPOINT":                                     "hard",
		"data:model:" + snapshotDomain + ":cat->data:model:" + snapshotDomain + ":mouse:REFERENCES_MODEL": "hard",
	} {
		if got := edgeOrFail(t, g, key).DerivationKind; got != want {
			t.Errorf("edge %s derivation_kind = %q, want %q", key, got, want)
		}
	}

	// Evidence stamping in legacy mode: source_kind "file", extractor id from
	// the fact's origin ("" → chronicle-scan), version "1.0", metadata "{}".
	imp := edgeOrFail(t, g, snapService+"->"+snapRepository+":INJECTS")
	ev, err := g.Store().ListEvidenceByEdge(imp.EdgeID)
	if err != nil || len(ev) == 0 {
		t.Fatalf("ListEvidenceByEdge: %v (%d rows)", err, len(ev))
	}
	for _, e := range ev {
		if e.SourceKind != "file" {
			t.Errorf("legacy evidence source_kind = %q, want file", e.SourceKind)
		}
		if e.ExtractorID != "chronicle-scan" {
			t.Errorf("legacy evidence extractor_id = %q, want chronicle-scan", e.ExtractorID)
		}
		if e.ExtractorVersion != "1.0" {
			t.Errorf("legacy evidence extractor_version = %q, want 1.0", e.ExtractorVersion)
		}
		if e.Metadata != "{}" {
			t.Errorf("legacy evidence metadata = %q, want {}", e.Metadata)
		}
	}

	// Legacy mode fills none of the deterministic result fields.
	if result.UnresolvedCount != 0 {
		t.Errorf("UnresolvedCount = %d, want 0 in legacy mode", result.UnresolvedCount)
	}
	if result.EvidenceIDsByFile != nil {
		t.Errorf("EvidenceIDsByFile = %v, want nil in legacy mode", result.EvidenceIDsByFile)
	}
	// And it writes nothing onto the extraction rows.
	rows, _ := g.Store().ListExtractions(revID, snapshotDomain)
	for _, r := range rows {
		if r.Metadata != "" && r.Metadata != "{}" {
			t.Errorf("legacy resolve wrote extraction metadata %q for %s", r.Metadata, r.FilePath)
		}
	}
}

// --- Deterministic mode ---

const detDomain = "detapp"

const (
	detModule      = "code:module:" + detDomain + ":src/tom-module"
	detController  = "code:controller:" + detDomain + ":src/tom-controller"
	detService     = "code:provider:" + detDomain + ":src/tom-service"
	detPrismaA     = "code:provider:" + detDomain + ":src/a/prisma-service"
	detPrismaB     = "code:provider:" + detDomain + ":src/b/prisma-service"
	detArmEndpoint = "contract:endpoint:" + detDomain + ":post:/tom/arm"
)

// detFixture seeds the batch Task S3 step 1 calls for: an import to an existing
// module node, a decorator + route, a call with exactly one candidate, a call
// with two candidates, and a call with none.
func detFixture(t *testing.T, g *Graph) int64 {
	t.Helper()
	revID, err := g.Store().CreateRevision(detDomain, "", "det1", "manual", "full", "{}")
	if err != nil {
		t.Fatalf("CreateRevision: %v", err)
	}
	save := func(path, fromType string, facts []map[string]any) {
		buf, _ := json.Marshal(facts)
		if _, err := g.Store().SaveExtraction(revID, detDomain, path, "extracted", fromType, string(buf), ""); err != nil {
			t.Fatalf("SaveExtraction(%s): %v", path, err)
		}
	}

	save("src/tom.module.ts", "module", []map[string]any{
		{"kind": "import", "to": "./tom.service", "symbols": []string{"TomService"}},
	})
	save("src/tom.service.ts", "provider", []map[string]any{
		{"kind": "import", "to": "./db.client", "symbols": []string{"DbClient"}},
	})
	// Two same-class-name twins: any name lookup for "PrismaService" is ambiguous.
	save("src/a/prisma.service.ts", "provider", []map[string]any{
		{"kind": "import", "to": "./a.client", "symbols": []string{"AClient"}},
	})
	save("src/b/prisma.service.ts", "provider", []map[string]any{
		{"kind": "import", "to": "./b.client", "symbols": []string{"BClient"}},
	})
	save("src/tom.controller.ts", "controller", []map[string]any{
		{"kind": "decorator", "decorator": "Controller", "from": "TomController"},
		{"kind": "endpoint", "method": "POST", "from": "tom", "target": "arm"},
		{"kind": "call", "object": "TomService", "method": "arm"},      // exactly one candidate
		{"kind": "call", "object": "PrismaService", "method": "find"},  // two candidates
		{"kind": "call", "object": "GhostService", "method": "vanish"}, // no candidate
	})
	return revID
}

func detOptions() ResolveOptions {
	return ResolveOptions{
		Deterministic:    true,
		ExtractorVersion: "1",
		ContentHashes: map[string]string{
			"src/tom.module.ts":     "hash-module",
			"src/tom.controller.ts": "hash-controller",
		},
	}
}

func TestResolveDeterministic_EvidenceIsAST(t *testing.T) {
	g, _, _ := setupTestGraph(t)
	revID := detFixture(t, g)

	if _, err := g.ResolveExtractionsWithOptions(detDomain, revID, detOptions()); err != nil {
		t.Fatalf("ResolveExtractionsWithOptions: %v", err)
	}

	// The import edge the module fact built, plus its evidence stamping.
	imp := edgeOrFail(t, g, detModule+"->"+detService+":CONTAINS")
	if imp.DerivationKind != "hard" {
		t.Errorf("import edge derivation_kind = %q, want hard", imp.DerivationKind)
	}
	ev, err := g.Store().ListEvidenceByEdge(imp.EdgeID)
	if err != nil || len(ev) == 0 {
		t.Fatalf("ListEvidenceByEdge: %v (%d rows)", err, len(ev))
	}
	for _, e := range ev {
		if e.SourceKind != "ast" {
			t.Errorf("evidence source_kind = %q, want ast", e.SourceKind)
		}
		if e.ExtractorID != detDefaultExtractorID {
			t.Errorf("evidence extractor_id = %q, want %s", e.ExtractorID, detDefaultExtractorID)
		}
		if e.ExtractorVersion != "1" {
			t.Errorf("evidence extractor_version = %q, want 1", e.ExtractorVersion)
		}
		var meta map[string]string
		if err := json.Unmarshal([]byte(e.Metadata), &meta); err != nil {
			t.Fatalf("evidence metadata %q: %v", e.Metadata, err)
		}
		if meta["content_hash"] != "hash-module" {
			t.Errorf("evidence metadata content_hash = %q, want hash-module", meta["content_hash"])
		}
	}
}

func TestResolveDeterministic_RouteIsHard(t *testing.T) {
	g, _, _ := setupTestGraph(t)
	revID := detFixture(t, g)

	if _, err := g.ResolveExtractionsWithOptions(detDomain, revID, detOptions()); err != nil {
		t.Fatalf("ResolveExtractionsWithOptions: %v", err)
	}

	route := edgeOrFail(t, g, detController+"->"+detArmEndpoint+":EXPOSES_ENDPOINT")
	if route.DerivationKind != "hard" {
		t.Errorf("route edge derivation_kind = %q, want hard", route.DerivationKind)
	}
	// The decorator's node evidence is AST too.
	nodeID, err := g.Store().GetNodeIDByKey(detController)
	if err != nil {
		t.Fatalf("controller node: %v", err)
	}
	rows, _ := g.Store().ListEvidenceByNode(nodeID)
	sawDecorator := false
	for _, e := range rows {
		if e.AssertionKind != "decorator" {
			continue
		}
		sawDecorator = true
		if e.SourceKind != "ast" || e.ExtractorID != detDefaultExtractorID {
			t.Errorf("decorator evidence = (%s, %s), want (ast, %s)", e.SourceKind, e.ExtractorID, detDefaultExtractorID)
		}
	}
	if !sawDecorator {
		t.Error("no decorator evidence on the controller node")
	}
}

func TestResolveDeterministic_UniqueNameMatchIsInferred(t *testing.T) {
	g, _, _ := setupTestGraph(t)
	revID := detFixture(t, g)

	if _, err := g.ResolveExtractionsWithOptions(detDomain, revID, detOptions()); err != nil {
		t.Fatalf("ResolveExtractionsWithOptions: %v", err)
	}

	call := edgeOrFail(t, g, detController+"->"+detService+":INJECTS")
	if call.DerivationKind != "inferred" {
		t.Errorf("name-resolved call derivation_kind = %q, want inferred", call.DerivationKind)
	}
	if call.Confidence > 0.7 {
		t.Errorf("name-resolved call confidence = %.2f, want <= 0.70", call.Confidence)
	}
}

func TestResolveDeterministic_NoEdgeWithoutUniqueTarget(t *testing.T) {
	g, _, _ := setupTestGraph(t)
	revID := detFixture(t, g)

	result, err := g.ResolveExtractionsWithOptions(detDomain, revID, detOptions())
	if err != nil {
		t.Fatalf("ResolveExtractionsWithOptions: %v", err)
	}

	for _, key := range []string{
		detController + "->" + detPrismaA + ":INJECTS",
		detController + "->" + detPrismaB + ":INJECTS",
		detController + "->code:provider:" + detDomain + ":ghostservice:INJECTS",
		detController + "->code:provider:" + detDomain + ":ghost-service:INJECTS",
	} {
		if _, err := g.Store().GetEdgeByKey(key); err == nil {
			t.Errorf("edge %s exists — an ambiguous or missing target must create no edge", key)
		}
	}
	// No phantom node minted for the unresolvable name either.
	for _, key := range []string{
		"code:provider:" + detDomain + ":ghostservice",
		"code:provider:" + detDomain + ":ghost-service",
	} {
		if _, err := g.Store().GetNodeIDByKey(key); err == nil {
			t.Errorf("node %s was minted for an unresolvable call target", key)
		}
	}

	if result.UnresolvedCount != 2 {
		t.Errorf("UnresolvedCount = %d, want 2", result.UnresolvedCount)
	}
}

func TestResolveDeterministic_UnresolvedRecordedOnExtraction(t *testing.T) {
	g, _, _ := setupTestGraph(t)
	revID := detFixture(t, g)

	if _, err := g.ResolveExtractionsWithOptions(detDomain, revID, detOptions()); err != nil {
		t.Fatalf("ResolveExtractionsWithOptions: %v", err)
	}

	rows, err := g.Store().ListExtractions(revID, detDomain)
	if err != nil {
		t.Fatalf("ListExtractions: %v", err)
	}
	var meta string
	for _, r := range rows {
		if r.FilePath == "src/tom.controller.ts" {
			meta = r.Metadata
		}
	}
	if meta == "" {
		t.Fatal("controller extraction carries no metadata")
	}
	var parsed struct {
		Unresolved []struct {
			Kind       string   `json:"kind"`
			Name       string   `json:"name"`
			Candidates []string `json:"candidates"`
		} `json:"unresolved"`
	}
	if err := json.Unmarshal([]byte(meta), &parsed); err != nil {
		t.Fatalf("extraction metadata %q: %v", meta, err)
	}
	if len(parsed.Unresolved) != 2 {
		t.Fatalf("extraction metadata lists %d unresolved, want 2 (%s)", len(parsed.Unresolved), meta)
	}
	byName := map[string]int{}
	for _, u := range parsed.Unresolved {
		byName[u.Name] = len(u.Candidates)
		if u.Kind != "call" {
			t.Errorf("unresolved kind = %q, want call", u.Kind)
		}
	}
	if byName["PrismaService"] != 2 {
		t.Errorf("PrismaService candidates = %d, want 2 (%s)", byName["PrismaService"], meta)
	}
	if n, ok := byName["GhostService"]; !ok || n != 0 {
		t.Errorf("GhostService candidates = %d (present=%v), want 0 (%s)", n, ok, meta)
	}
}

func TestResolveDeterministic_EvidenceIDsByFile(t *testing.T) {
	g, _, _ := setupTestGraph(t)
	revID := detFixture(t, g)

	result, err := g.ResolveExtractionsWithOptions(detDomain, revID, detOptions())
	if err != nil {
		t.Fatalf("ResolveExtractionsWithOptions: %v", err)
	}
	if len(result.EvidenceIDsByFile) == 0 {
		t.Fatal("EvidenceIDsByFile is empty")
	}
	ids := result.EvidenceIDsByFile["src/tom.controller.ts"]
	if len(ids) == 0 {
		t.Fatalf("no evidence ids for the controller file: %v", result.EvidenceIDsByFile)
	}
	// Every id must name a real AST evidence row, and node + edge evidence
	// both appear (the caller supersedes everything it is not handed back).
	byID := map[int64]store.EvidenceRow{}
	sawAST := false
	// "ast" is every row read out of a file; "synthetic" is the creation
	// evidence for a node with no file of its own (an endpoint), still
	// written by this resolve while the controller's extraction was open.
	for _, kind := range []string{"ast", "synthetic"} {
		rows, err := g.Store().ListEvidenceBySourceKind(kind)
		if err != nil {
			t.Fatalf("ListEvidenceBySourceKind(%s): %v", kind, err)
		}
		for _, r := range rows {
			if kind == "ast" {
				sawAST = true
			}
			byID[r.EvidenceID] = r
		}
	}
	if !sawAST {
		t.Fatal("no ast evidence written at all")
	}
	sawNode, sawEdge := false, false
	for _, id := range ids {
		row, ok := byID[id]
		if !ok {
			t.Fatalf("evidence id %d is not a row written by this resolve", id)
		}
		switch row.TargetKind {
		case "node":
			sawNode = true
		case "edge":
			sawEdge = true
		}
	}
	if !sawNode {
		t.Error("EvidenceIDsByFile carries no node evidence")
	}
	if !sawEdge {
		t.Error("EvidenceIDsByFile carries no edge evidence")
	}
}

// The same fixture the legacy snapshot pins, resolved deterministically: the
// certainty of every edge changes with how its target was found, and nothing
// the graph does not already hold is minted to receive a link.
func TestResolveDeterministic_SameFixtureRefusesPhantoms(t *testing.T) {
	g, _, _ := setupTestGraph(t)
	revID := snapshotFixture(t, g)

	result, err := g.ResolveExtractionsWithOptions(snapshotDomain, revID, ResolveOptions{
		Deterministic:    true,
		ExtractorVersion: "2",
	})
	if err != nil {
		t.Fatalf("ResolveExtractionsWithOptions: %v", err)
	}

	// Construction-fixed facts keep their certainty.
	for key, want := range map[string]string{
		snapService + "->" + snapRepository + ":INJECTS":                                                  "hard",
		snapModule + "->" + snapService + ":CONTAINS":                                                     "hard",
		snapController + "->" + snapService + ":INJECTS":                                                  "hard",
		snapController + "->" + snapArmEndpoint + ":EXPOSES_ENDPOINT":                                     "hard",
		"data:model:" + snapshotDomain + ":cat->data:model:" + snapshotDomain + ":mouse:REFERENCES_MODEL": "hard",
	} {
		if got := edgeOrFail(t, g, key).DerivationKind; got != want {
			t.Errorf("edge %s derivation_kind = %q, want %q", key, got, want)
		}
	}

	// member_call "cat" found exactly one model — linked, but only inferred.
	uses := edgeOrFail(t, g, snapController+"->"+snapCatModel+":USES_MODEL")
	if uses.DerivationKind != "inferred" {
		t.Errorf("member_call edge derivation_kind = %q, want inferred", uses.DerivationKind)
	}
	if uses.Confidence > detNameMatchConfidence {
		t.Errorf("member_call edge confidence = %.2f, want <= %.2f", uses.Confidence, detNameMatchConfidence)
	}

	// calls_endpoint named an endpoint no file in this batch exposes: no edge
	// from the fact, and the client's guess never became a contract node of
	// its own accord.
	if _, err := g.Store().GetEdgeByKey(snapController + "->" + snapSpikeEndpoint + ":CALLS_ENDPOINT"); err == nil {
		t.Error("calls_endpoint linked an endpoint nothing exposes")
	}
	if _, err := g.Store().GetNodeIDByKey(snapSpikeEndpoint); err == nil {
		t.Error("calls_endpoint minted the endpoint node it could not find")
	}
	if result.UnresolvedCount == 0 {
		t.Error("UnresolvedCount = 0 — the undeclared endpoint should have been recorded")
	}
	var sawEndpointRefusal bool
	for _, u := range result.Unresolved {
		if u.Kind == "calls_endpoint" {
			sawEndpointRefusal = true
		}
	}
	if !sawEndpointRefusal {
		t.Errorf("no calls_endpoint refusal in %v", result.Unresolved)
	}

	// Every evidence row this resolve wrote carries the given pack version.
	astRows, _ := g.Store().ListEvidenceBySourceKind("ast")
	if len(astRows) == 0 {
		t.Fatal("no ast evidence written")
	}
	for _, r := range astRows {
		if r.ExtractorVersion != "2" {
			t.Errorf("evidence %d extractor_version = %q, want 2", r.EvidenceID, r.ExtractorVersion)
		}
	}
}

// --- Fix round 1 ---

// detAllEvidence returns every evidence row reachable from a node or an edge.
func detAllEvidence(t *testing.T, g *Graph) []store.EvidenceRow {
	t.Helper()
	var out []store.EvidenceRow
	nodes, _ := g.Store().ListNodes(store.NodeFilter{})
	for _, n := range nodes {
		rows, _ := g.Store().ListEvidenceByNode(n.NodeID)
		out = append(out, rows...)
	}
	edges, _ := g.Store().ListEdges(store.EdgeFilter{})
	for _, e := range edges {
		rows, _ := g.Store().ListEvidenceByEdge(e.EdgeID)
		out = append(out, rows...)
	}
	return out
}

// An http_call whose path names no endpoint the graph exposes must not
// materialize one. Legacy mode keeps the boundary node (status "external")
// plus a linked CALLS_ENDPOINT edge; a deterministic resolve records the name
// and writes nothing.
func TestResolveDeterministic_NoPhantomExternalEndpoint(t *testing.T) {
	g, _, _ := setupTestGraph(t)
	revID := snapshotFixture(t, g)

	result, err := g.ResolveExtractionsWithOptions(snapshotDomain, revID, ResolveOptions{
		Deterministic: true, ExtractorVersion: "1",
	})
	if err != nil {
		t.Fatalf("ResolveExtractionsWithOptions: %v", err)
	}

	if _, err := g.Store().GetNodeIDByKey(snapStatusEndpoint); err == nil {
		t.Errorf("http_call materialized the phantom endpoint node %s", snapStatusEndpoint)
	}
	if _, err := g.Store().GetEdgeByKey(snapController + "->" + snapStatusEndpoint + ":CALLS_ENDPOINT"); err == nil {
		t.Errorf("http_call linked a CALLS_ENDPOINT edge to a node nothing exposes")
	}
	var sawPath bool
	for _, u := range result.Unresolved {
		if u.Kind == "http_call" && strings.Contains(u.Target, "/jerry/status") {
			sawPath = true
		}
	}
	if !sawPath {
		t.Errorf("the unlinkable endpoint path was not recorded: %v", result.Unresolved)
	}
}

// An http_call the alias table could not resolve is still a guess about which
// service that host is — never "hard", or adding the alias later would look
// like a downgrade.
func TestResolveDeterministic_UnaliasedHostIsInferred(t *testing.T) {
	g, _, _ := setupTestGraph(t)
	revID := snapshotFixture(t, g)

	if _, err := g.ResolveExtractionsWithOptions(snapshotDomain, revID, ResolveOptions{
		Deterministic: true, ExtractorVersion: "1",
	}); err != nil {
		t.Fatalf("ResolveExtractionsWithOptions: %v", err)
	}
	ext := edgeOrFail(t, g, snapController+"->service:external_system:"+snapshotDomain+":jerry-api:CALLS_SERVICE")
	if ext.DerivationKind != "inferred" {
		t.Errorf("unaliased http_call derivation_kind = %q, want inferred", ext.DerivationKind)
	}
	if ext.Confidence > detNameMatchConfidence {
		t.Errorf("unaliased http_call confidence = %.2f, want <= %.2f", ext.Confidence, detNameMatchConfidence)
	}
}

// The deterministic stamp belongs to rows read out of a file. Post-passes
// (external endpoints, service containment, derived flows) keep their own
// identity — the integration lane supersedes "rows of this extractor id not
// handed back for the file", so a derived row wearing the structural id would
// be superseded on the next refresh.
func TestResolveDeterministic_PostPassEvidenceKeepsOwnIdentity(t *testing.T) {
	g, _, _ := setupTestGraph(t)
	revID := snapshotFixture(t, g)

	result, err := g.ResolveExtractionsWithOptions(snapshotDomain, revID, ResolveOptions{
		Deterministic: true, ExtractorVersion: "1", ExtractorID: "chronicle-structural",
	})
	if err != nil {
		t.Fatalf("ResolveExtractionsWithOptions: %v", err)
	}

	handedBack := map[int64]bool{}
	for _, ids := range result.EvidenceIDsByFile {
		for _, id := range ids {
			handedBack[id] = true
		}
	}
	if len(handedBack) == 0 {
		t.Fatal("EvidenceIDsByFile is empty")
	}
	for _, e := range detAllEvidence(t, g) {
		if e.ExtractorID != "chronicle-structural" {
			continue
		}
		if !handedBack[e.EvidenceID] {
			t.Errorf("evidence %d (%s on %s) carries the structural extractor id but is not in EvidenceIDsByFile",
				e.EvidenceID, e.AssertionKind, e.FilePath)
		}
	}
}

// The extractor id is the caller's to declare; the default names the
// structural phase rather than a package constant.
func TestResolveDeterministic_ExtractorIDIsAnOption(t *testing.T) {
	for _, tc := range []struct{ given, want string }{
		{"", detDefaultExtractorID},
		{"chronicle-ast", "chronicle-ast"},
	} {
		g, _, _ := setupTestGraph(t)
		revID := detFixture(t, g)
		opts := detOptions()
		opts.ExtractorID = tc.given
		if _, err := g.ResolveExtractionsWithOptions(detDomain, revID, opts); err != nil {
			t.Fatalf("ResolveExtractionsWithOptions(%q): %v", tc.given, err)
		}
		rows, err := g.Store().ListEvidenceBySourceKind("ast")
		if err != nil || len(rows) == 0 {
			t.Fatalf("no ast evidence for ExtractorID=%q: %v", tc.given, err)
		}
		for _, r := range rows {
			if r.ExtractorID != tc.want {
				t.Errorf("ExtractorID=%q → evidence extractor_id %q, want %q", tc.given, r.ExtractorID, tc.want)
			}
		}
	}
}

// --- Fix round 2 ---

const rekeyDomain = "rekeyapp"

// A bare import mints a stem-keyed node with no file of its own; when the file
// that defines it is finally resolved, ensureNodeID rekeys that node in place
// rather than minting a twin. If the rekey happens during the third pass, the
// name index is holding the node under its OLD key — and the next
// name-resolved edge would point at a key that no longer exists.
func TestResolveDeterministic_RekeyDuringThirdPassInvalidatesIndex(t *testing.T) {
	g, _, _ := setupTestGraph(t)
	revID, err := g.Store().CreateRevision(rekeyDomain, "", "rekey1", "manual", "full", "{}")
	if err != nil {
		t.Fatalf("CreateRevision: %v", err)
	}
	save := func(path, fromType string, facts []map[string]any) {
		buf, _ := json.Marshal(facts)
		if _, err := g.Store().SaveExtraction(revID, rekeyDomain, path, "extracted", fromType, string(buf), ""); err != nil {
			t.Fatalf("SaveExtraction(%s): %v", path, err)
		}
	}

	// Resolved first (schema files sort ahead): gives member_call a target.
	save("prisma/schema.prisma", "model", []map[string]any{
		{"kind": "model", "to": "Cat"},
	})
	// A bare import — mints code:provider:rekeyapp:ghost-service, file path empty.
	save("src/tom.module.ts", "module", []map[string]any{
		{"kind": "import", "to": "ghost.service", "symbols": []string{"GhostService"}},
	})
	// Deferred, and resolved BEFORE the controller's call: its ensureNodeID
	// rekeys the stem node onto this file's path key.
	save("src/ghost.service.ts", "provider", []map[string]any{
		{"kind": "member_call", "to": "cat"},
	})
	// Deferred after it: the name must resolve to the node's new key. The
	// injects fact is here so the controller's own node already exists — a
	// node created during the third pass resets the index by itself, which
	// would hide the bug this test is about.
	save("src/tom.controller.ts", "controller", []map[string]any{
		{"kind": "injects", "to": "TomService"},
		{"kind": "call", "object": "GhostService", "method": "vanish"},
	})

	if _, err := g.ResolveExtractionsWithOptions(rekeyDomain, revID, ResolveOptions{
		Deterministic: true, ExtractorVersion: "1",
	}); err != nil {
		t.Fatalf("ResolveExtractionsWithOptions: %v", err)
	}

	const (
		stemKey       = "code:provider:" + rekeyDomain + ":ghost-service"
		rekeyedKey    = "code:provider:" + rekeyDomain + ":src/ghost-service"
		controllerKey = "code:controller:" + rekeyDomain + ":src/tom-controller"
	)
	if _, err := g.Store().GetNodeIDByKey(rekeyedKey); err != nil {
		t.Fatalf("the stem node was not rekeyed onto its file — fixture no longer exercises the path: %v", err)
	}
	if _, err := g.Store().GetEdgeByKey(controllerKey + "->" + stemKey + ":INJECTS"); err == nil {
		t.Error("name-resolved edge points at the pre-rekey key — the name index went stale")
	}
	edge := edgeOrFail(t, g, controllerKey+"->"+rekeyedKey+":INJECTS")
	if edge.ToNodeKey != rekeyedKey {
		t.Errorf("edge to_node_key = %q, want %q", edge.ToNodeKey, rekeyedKey)
	}
}
