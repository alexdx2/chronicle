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

// A package the domain publishes itself is its service node for imports too,
// as it is for manifest dependencies (declares_service wins, either file order).
func TestBareImportOfOwnPublishedPackageLandsOnServiceNode(t *testing.T) {
	cases := []struct {
		name, decl, declFile, impFile, impTo, needle string
	}{
		{"npm", `[{"kind":"declares_service","to":"@okeep/tokens"}]`, "packages/tokens/package.json",
			"src/app.ts", "@okeep/tokens/colors", "@okeep/tokens"},
		{"python", `[{"kind":"declares_service","to":"myapp"}]`, "pyproject.toml",
			"worker/job.py", "myapp.core", "myapp"},
	}
	for _, c := range cases {
		for _, declFirst := range []bool{true, false} {
			t.Run(c.name, func(t *testing.T) {
				g, s, revID := setupTestGraph(t)
				imp := `[{"kind":"import","to":"` + c.impTo + `","symbols":["x"]}]`
				if declFirst {
					g.SaveFileExtraction(revID, "testapp", c.declFile, "extracted", "manifest", c.decl, "")
					if _, err := g.ResolveExtractions("testapp", revID); err != nil {
						t.Fatal(err)
					}
					g.SaveFileExtraction(revID, "testapp", c.impFile, "extracted", "provider", imp, "")
				} else {
					// Same batch: the snapshot covers the declaration even though
					// the importing file may resolve first.
					g.SaveFileExtraction(revID, "testapp", c.impFile, "extracted", "provider", imp, "")
					g.SaveFileExtraction(revID, "testapp", c.declFile, "extracted", "manifest", c.decl, "")
				}
				if _, err := g.ResolveExtractions("testapp", revID); err != nil {
					t.Fatal(err)
				}
				for _, n := range pkgNodesNamed(t, s, c.needle) {
					if strings.HasPrefix(n.NodeKey, "code:package:") || strings.HasPrefix(n.NodeKey, "code:provider:") {
						t.Fatalf("twin node minted for the own package: %s", n.NodeKey)
					}
				}
				edges, _ := s.ListEdges(store.EdgeFilter{})
				var found bool
				for _, e := range edges {
					if e.Active && strings.Contains(e.FromNodeKey, strings.TrimSuffix(strings.TrimSuffix(c.impFile, ".ts"), ".py")) {
						if !strings.HasPrefix(e.ToNodeKey, "service:service:testapp:") || !strings.Contains(e.ToNodeKey, c.needle) {
							t.Fatalf("import edge target = %s, want the service node", e.ToNodeKey)
						}
						found = true
					}
				}
				if !found {
					t.Fatalf("no import edge from %s: %+v", c.impFile, edges)
				}
			})
		}
	}
}

// A className with a slash or a leading @ is a package only when it is written
// in an npm file and is not a path.
func TestResolveClassNameTargetPackageShapeIsGated(t *testing.T) {
	cases := []struct {
		file, name string
		pkg        bool
	}{
		{"src/app.ts", "@acme/ui", true},
		{"src/app.ts", "@acme/ui/button", true},
		{"src/app.ts", "lodash/fp", true},
		{"src/app.ts", "@scope/chart.js", true},
		{"src/app.ts", "src/user/user.service", false},
		{"src/app.ts", "./user.service", false},
		{"src/app.ts", "../shared/user", false},
		{"src/app.ts", "a/b/c", false},
		{"src/app.ts", "@acme/ui/button.tsx", false},
		{"worker/job.py", "orders/OrderService", false},
		{"worker/job.py", "@acme/ui", false},
		{"src/Orders.cs", "orders/OrderService", false},
	}
	for _, c := range cases {
		g, s, revID := setupTestGraph(t)
		g.currentFilePath = c.file
		key, id := g.resolveClassNameTarget("testapp", revID, c.name)
		if c.pkg {
			if !strings.HasPrefix(key, "code:package:testapp:npm:") || id == 0 {
				t.Errorf("%s from %s: key %q, want an npm package node", c.name, c.file, key)
			}
			continue
		}
		if strings.HasPrefix(key, "code:package:") {
			t.Errorf("%s from %s became a package: %s", c.name, c.file, key)
		}
		for _, n := range pkgNodesNamed(t, s, "code:package:") {
			t.Errorf("%s from %s minted package node %s", c.name, c.file, n.NodeKey)
		}
	}
}

// A real in-repo provider whose key equals the package-shaped key (a root-level
// config.ts is code:provider:<d>:config) is not the legacy twin: it has a file,
// so a bare import "config" keeps going to it.
func TestBareImportOfRootLevelInRepoProviderStaysInRepo(t *testing.T) {
	g, s, revID := setupTestGraph(t)
	g.SaveFileExtraction(revID, "testapp", "config.ts", "extracted", "provider",
		`[{"kind":"import","to":"@okeep/ui"}]`, "")
	g.SaveFileExtraction(revID, "testapp", "app/main.ts", "extracted", "provider",
		`[{"kind":"import","to":"config","symbols":["x"]}]`, "")
	if _, err := g.ResolveExtractions("testapp", revID); err != nil {
		t.Fatal(err)
	}
	if n := pkgNodesNamed(t, s, ":npm:config"); len(n) != 0 {
		t.Fatalf("in-repo config.ts became a package: %+v", n)
	}
	edges, _ := s.ListEdges(store.EdgeFilter{})
	var found bool
	for _, e := range edges {
		if strings.Contains(e.FromNodeKey, "app/main") && e.ToNodeKey == "code:provider:testapp:config" {
			found = true
		}
	}
	if !found {
		t.Fatalf("no app/main -> code:provider:testapp:config edge: %+v", edges)
	}
}

// The parent retry pass runs with the importing file set, like the deferred
// pass; an @scope/pkg parent target that only exists after the first pass
// lands on the package node.
func TestParentRetryResolvesScopedPackageTarget(t *testing.T) {
	g, s, revID := setupTestGraph(t)
	g.SaveFileExtraction(revID, "testapp", "src/a.ts", "extracted", "provider",
		`[{"kind":"parent","to":"@acme/ui","reason":"registered"}]`, "")
	g.SaveFileExtraction(revID, "testapp", "src/z.ts", "extracted", "provider",
		`[{"kind":"import","to":"@acme/ui"}]`, "")
	if _, err := g.ResolveExtractions("testapp", revID); err != nil {
		t.Fatal(err)
	}
	edges, _ := s.ListEdges(store.EdgeFilter{})
	var found bool
	for _, e := range edges {
		if e.EdgeType == "CONTAINS" && e.FromNodeKey == "code:package:testapp:npm:@acme/ui" {
			found = true
		}
	}
	if !found {
		t.Fatalf("no CONTAINS from the package node: %+v", edges)
	}
}
