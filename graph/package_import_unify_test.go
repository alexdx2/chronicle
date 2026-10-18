package graph

import (
	"strings"
	"testing"

	"github.com/alexdx2/chronicle-core/store"
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
