package graph

import (
	"strings"
	"testing"

	"github.com/alexdx2/chronicle-core/deps"
	"github.com/alexdx2/chronicle-core/extract/structural"
	"github.com/alexdx2/chronicle-core/store"
	"github.com/alexdx2/chronicle-core/validate"
)

// SQ-Contract 1: one node per package whether it is reached by a bare import
// or a manifest dependency.

func pkgNodesNamed(t *testing.T, s *store.Store, needle string) []store.NodeRow {
	t.Helper()
	all, err := s.ListNodes(store.NodeFilter{})
	if err != nil {
		t.Fatal(err)
	}
	var out []store.NodeRow
	for _, n := range all {
		if strings.Contains(n.NodeKey, needle) {
			out = append(out, n)
		}
	}
	return out
}

func TestBareImportAndManifestShareOnePackageNode(t *testing.T) {
	const manifest = `[{"kind":"dependency","to":"@okeep/ui","section":"dependencies"}]`
	const imp = `[{"kind":"import","to":"@okeep/ui/button","symbols":["Button"]}]`
	for _, order := range []string{"import-first", "manifest-first"} {
		t.Run(order, func(t *testing.T) {
			g, s, revID := setupTestGraph(t)
			saveManifest := func() {
				g.SaveFileExtraction(revID, "testapp", "package.json", "extracted", "manifest", manifest, "")
			}
			saveImport := func() {
				g.SaveFileExtraction(revID, "testapp", "src/app.ts", "extracted", "provider", imp, "")
			}
			if order == "import-first" {
				saveImport()
				if _, err := g.ResolveExtractions("testapp", revID); err != nil {
					t.Fatal(err)
				}
				saveManifest()
			} else {
				saveManifest()
				if _, err := g.ResolveExtractions("testapp", revID); err != nil {
					t.Fatal(err)
				}
				saveImport()
			}
			if _, err := g.ResolveExtractions("testapp", revID); err != nil {
				t.Fatal(err)
			}

			nodes := pkgNodesNamed(t, s, "@okeep/ui")
			if len(nodes) != 1 {
				var keys []string
				for _, n := range nodes {
					keys = append(keys, n.NodeKey)
				}
				t.Fatalf("want exactly 1 node for @okeep/ui, got %v", keys)
			}
			n := nodes[0]
			if n.NodeKey != "code:package:testapp:npm:@okeep/ui" || n.NodeType != "package" || n.Name != "@okeep/ui" {
				t.Fatalf("node = key %q type %q name %q", n.NodeKey, n.NodeType, n.Name)
			}

			edges, _ := s.ListEdges(store.EdgeFilter{})
			var sawCode, sawManifest bool
			for _, e := range edges {
				if e.ToNodeKey != n.NodeKey {
					continue
				}
				if e.EdgeType != "DEPENDS_ON" {
					t.Errorf("edge type = %s, want DEPENDS_ON", e.EdgeType)
				}
				ev, _ := s.ListEvidenceByEdge(e.EdgeID)
				if e.DependencySource == "manifest" {
					sawManifest = true
				}
				for _, x := range ev {
					if x.AssertionKind == "import_specifier" && e.DependencySource == "code" {
						sawCode = true
					}
				}
			}
			if !sawCode || !sawManifest {
				t.Fatalf("inbound edges: code import evidence=%v manifest=%v (%+v)", sawCode, sawManifest, edges)
			}
		})
	}
}

func TestBareImportOfUndeclaredPackageLandsOnPackageNode(t *testing.T) {
	g, s, revID := setupTestGraph(t)
	g.SaveFileExtraction(revID, "testapp", "src/app.ts", "extracted", "provider",
		`[{"kind":"import","to":"@okeep/other/sub","symbols":["map"]}]`, "")
	g.SaveFileExtraction(revID, "testapp", "worker/job.py", "extracted", "provider",
		`[{"kind":"import","to":"acme_lib.adapters"}]`, "")
	if _, err := g.ResolveExtractions("testapp", revID); err != nil {
		t.Fatal(err)
	}
	if n := pkgNodesNamed(t, s, "@okeep/other"); len(n) != 1 || n[0].NodeKey != "code:package:testapp:npm:@okeep/other" || n[0].NodeType != "package" {
		t.Fatalf("@okeep/other nodes = %+v", n)
	}
	if n := pkgNodesNamed(t, s, "acme-lib"); len(n) != 1 || n[0].NodeKey != "code:package:testapp:python:acme-lib" {
		t.Fatalf("acme_lib nodes = %+v", n)
	}
}

func TestBareImportMatchingInRepoAliasStaysInRepo(t *testing.T) {
	g, s, revID := setupTestGraph(t)
	// helpers.ts is in-repo code: its node carries the alias "helpers".
	g.SaveFileExtraction(revID, "testapp", "src/helpers.ts", "extracted", "provider",
		`[{"kind":"import","to":"@okeep/ui"}]`, "")
	g.SaveFileExtraction(revID, "testapp", "src/app.ts", "extracted", "provider",
		`[{"kind":"import","to":"helpers","symbols":["x"]}]`, "")
	if _, err := g.ResolveExtractions("testapp", revID); err != nil {
		t.Fatal(err)
	}
	if n := pkgNodesNamed(t, s, ":npm:helpers"); len(n) != 0 {
		t.Fatalf("in-repo alias became a package: %+v", n)
	}
	edges, _ := s.ListEdges(store.EdgeFilter{})
	var found bool
	for _, e := range edges {
		if strings.Contains(e.FromNodeKey, "src/app") && strings.Contains(e.ToNodeKey, "helpers") {
			found = true
			if strings.Contains(e.ToNodeKey, "code:package:") {
				t.Fatalf("edge target = %s", e.ToNodeKey)
			}
		}
	}
	if !found {
		t.Fatalf("no app -> helpers edge: %+v", edges)
	}
}

func TestCSharpUsingKeepsProviderPath(t *testing.T) {
	g, s, revID := setupTestGraph(t)
	g.SaveFileExtraction(revID, "testapp", "src/Program.cs", "extracted", "provider",
		`[{"kind":"import","to":"Foo.Bar"}]`, "")
	if _, err := g.ResolveExtractions("testapp", revID); err != nil {
		t.Fatal(err)
	}
	nodes := pkgNodesNamed(t, s, "foo-bar")
	if len(nodes) != 1 || !strings.HasPrefix(nodes[0].NodeKey, "code:provider:") {
		t.Fatalf("nodes = %+v", nodes)
	}
}

// seedLegacyPackageProvider writes what a graph built before packages had their
// own node holds: code:provider:<d>:<pkg> as the target of an import edge whose
// evidence is the importing file. It goes through the same resolve path a
// non-package-routed file takes (a .cs file does not route imports to
// code:package), then re-homes the evidence to the .ts file the way the old
// resolver recorded it.
func seedLegacyPackageProvider(t *testing.T, g *Graph, s *store.Store, revID int64, pkg, file string) store.NodeRow {
	t.Helper()
	// The structural phase resolved this file deterministically: creation and
	// edge evidence both carry its extractor identity and name the file.
	end := g.detBegin(ResolveOptions{Deterministic: true, ExtractorID: structural.ExtractorID, ExtractorVersion: "1.0"})
	defer end()
	g.detSetFile(file, 0)
	legacyKey := "code:provider:testapp:" + pkg
	toID := g.ensureNodeID("testapp", revID, legacyKey, pkg, "")
	fromKey := typedNodeKeyFromFile("testapp", file, "provider")
	fromID := g.ensureNodeID("testapp", revID, fromKey, inferNameFromPath(file), file)
	edgeKey := fromKey + "->" + legacyKey + ":DEPENDS_ON"
	if _, err := s.UpsertEdge(store.EdgeRow{
		EdgeKey: edgeKey, FromNodeID: fromID, ToNodeID: toID, FromNodeKey: fromKey, ToNodeKey: legacyKey,
		EdgeType: "DEPENDS_ON", DerivationKind: "hard", Active: true, LastSeenRevisionID: revID,
		Confidence: 0.9, Freshness: 1, TrustScore: 0.9, Metadata: "{}",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := g.resolverEdgeEvidence(edgeKey, validate.EvidenceInput{
		TargetKind: "edge", SourceKind: "file", FilePath: file, ExtractorID: structural.ExtractorID, ExtractorVersion: "1.0",
		Confidence: 0.95, RevisionID: revID, AssertionKind: "import_specifier", Assertion: `{"module":"` + pkg + `"}`,
	}); err != nil {
		t.Fatal(err)
	}
	n, err := s.GetNodeByKey(legacyKey)
	if err != nil {
		t.Fatal(err)
	}
	return *n
}

// rescanImporter replays one importing file the way the structural phase does:
// resolve its standing row deterministically, then close what it no longer says.
func rescanImporter(t *testing.T, g *Graph, s *store.Store, rev int64, file, facts string) {
	t.Helper()
	if _, err := s.SaveStructuralExtraction(rev, "testapp", file, "extracted", "provider", facts, "", ""); err != nil {
		t.Fatal(err)
	}
	rr, err := g.ResolveExtractionsWithOptions("testapp", rev, ResolveOptions{
		Deterministic: true, ExtractorID: structural.ExtractorID, ExtractorVersion: "test",
		ExtractionRole: store.StructuralExtractionRole, AllowDegraded: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	keep, err := g.structuralKeep(file, rev, rr.EvidenceIDsByFile[file])
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.SupersedeEvidenceNotIn(file, structural.ExtractorID, keep, rev); err != nil {
		t.Fatal(err)
	}
	if err := g.recalculateSupersededTrust(rev); err != nil {
		t.Fatal(err)
	}
}

func activeKeysNamed(t *testing.T, s *store.Store, needle string) []string {
	var out []string
	for _, n := range pkgNodesNamed(t, s, needle) {
		if n.Status == "active" {
			out = append(out, n.NodeKey)
		}
	}
	return out
}

// The legacy node is retired by the loss of its evidence, which is file-scoped:
// it goes only once EVERY importing file has been rescanned.
func TestLegacyProviderTwinNeedsEveryImporterRescanned(t *testing.T) {
	g, s, revID := setupTestGraph(t)
	legacy := seedLegacyPackageProvider(t, g, s, revID, "@okeep/ui", "src/a.ts")
	seedLegacyPackageProvider(t, g, s, revID, "@okeep/ui", "src/b.ts")
	g.registerNodeAlias(legacy.NodeID, "@okeep/ui", "name")
	rev2, _ := s.CreateRevision("testapp", "scan_2", "", "manual", "full", "{}")
	imp := `[{"kind":"import","to":"@okeep/ui","symbols":["Button"]}]`

	rescanImporter(t, g, s, rev2, "src/a.ts", imp)
	if got := activeKeysNamed(t, s, "code:provider:testapp:@okeep/ui"); len(got) != 1 {
		t.Fatalf("legacy node must stay while src/b.ts still points at it, active = %v", got)
	}
	rescanImporter(t, g, s, rev2, "src/b.ts", imp)
	if got := activeKeysNamed(t, s, "@okeep/ui"); len(got) != 1 || got[0] != "code:package:testapp:npm:@okeep/ui" {
		t.Fatalf("after every importer is rescanned want only the package node, active = %v", got)
	}
}

func TestLegacyProviderTwinRetiresOnRescan(t *testing.T) {
	g, s, revID := setupTestGraph(t)
	legacy := seedLegacyPackageProvider(t, g, s, revID, "@okeep/ui", "src/app.ts")
	g.registerNodeAlias(legacy.NodeID, "@okeep/ui", "name")
	if c, _ := s.FindCodeNodesByAlias("testapp", "@okeep/ui", ""); len(c) != 1 {
		t.Fatalf("seed must make the legacy node the single alias candidate, got %d", len(c))
	}

	// Rescan: the manifest (deps pass) and the importing file (structural phase).
	rev2, _ := s.CreateRevision("testapp", "scan_2", "", "manual", "full", "{}")
	rep := deps.Report{Depends: []deps.Dependency{
		{Name: "@okeep/ui", Version: "1.0.0", Section: "dependencies", Ecosystem: "npm", File: "package.json", Line: 4, Text: `"@okeep/ui"`},
	}}
	if _, err := g.WriteDepsExtractions("testapp", rev2, rep); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SaveStructuralExtraction(rev2, "testapp", "src/app.ts", "extracted", "provider",
		`[{"kind":"import","to":"@okeep/ui/button","symbols":["Button"]}]`, "", ""); err != nil {
		t.Fatal(err)
	}
	rr, err := g.ResolveExtractionsWithOptions("testapp", rev2, ResolveOptions{
		Deterministic: true, ExtractorID: structural.ExtractorID, ExtractorVersion: "test",
		ExtractionRole: store.StructuralExtractionRole, AllowDegraded: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	keep, err := g.structuralKeep("src/app.ts", rev2, rr.EvidenceIDsByFile["src/app.ts"])
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.SupersedeEvidenceNotIn("src/app.ts", structural.ExtractorID, keep, rev2); err != nil {
		t.Fatal(err)
	}
	if err := g.recalculateSupersededTrust(rev2); err != nil {
		t.Fatal(err)
	}

	const pkgKey = "code:package:testapp:npm:@okeep/ui"
	var active []string
	for _, n := range pkgNodesNamed(t, s, "@okeep/ui") {
		if n.Status == "active" {
			active = append(active, n.NodeKey)
		}
	}
	if len(active) != 1 || active[0] != pkgKey {
		t.Fatalf("active nodes for @okeep/ui = %v, want only %s", active, pkgKey)
	}
	edges, _ := s.ListEdges(store.EdgeFilter{})
	var importEdge bool
	for _, e := range edges {
		if e.Active && e.ToNodeKey == legacy.NodeKey {
			t.Errorf("active edge still into the legacy provider node: %s", e.EdgeKey)
		}
		if e.Active && e.ToNodeKey == pkgKey && strings.Contains(e.FromNodeKey, "src/app") {
			importEdge = true
		}
	}
	if !importEdge {
		t.Fatalf("src/app.ts does not import the package node: %+v", edges)
	}
}

// First writer wins the node Name, so both paths must write the package name
// as the source spells it.
func TestPackageNodeNameIsAsWrittenWhicheverPathMintsIt(t *testing.T) {
	const manifest = `[{"kind":"dependency","to":"Foo-Lib","section":"dependencies"}]`
	const imp = `[{"kind":"import","to":"Foo-Lib/sub","symbols":["x"]}]`
	for _, importFirst := range []bool{true, false} {
		g, s, revID := setupTestGraph(t)
		first, second := manifest, imp
		firstFile, secondFile := "package.json", "src/app.ts"
		if importFirst {
			first, second, firstFile, secondFile = imp, manifest, secondFile, firstFile
		}
		g.SaveFileExtraction(revID, "testapp", firstFile, "extracted", "provider", first, "")
		if _, err := g.ResolveExtractions("testapp", revID); err != nil {
			t.Fatal(err)
		}
		g.SaveFileExtraction(revID, "testapp", secondFile, "extracted", "provider", second, "")
		if _, err := g.ResolveExtractions("testapp", revID); err != nil {
			t.Fatal(err)
		}
		nodes := pkgNodesNamed(t, s, "foo-lib")
		if len(nodes) != 1 || nodes[0].Name != "Foo-Lib" {
			t.Fatalf("importFirst=%v: nodes = %+v, want one named Foo-Lib", importFirst, nodes)
		}
	}
}
