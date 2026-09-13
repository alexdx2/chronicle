package graph

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/alexdx2/chronicle-core/registry"
	"github.com/alexdx2/chronicle-core/store"
	"github.com/alexdx2/chronicle-core/validate"
)

// importerOwnedFixture: one screen node whose evidence phase-1 verification
// does not own — a surface import (surface_extract for the node itself,
// declared for the human verdict on it) and an `ast` row from the structural
// phase — all anchored at a product source file, the file a code refresh will
// see change.
func importerOwnedFixture(t *testing.T) (*Graph, string, string) {
	t.Helper()
	dir := t.TempDir()
	s, err := store.Open(filepath.Join(dir, "chronicle.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	reg, err := registry.LoadDefaults()
	if err != nil {
		t.Fatal(err)
	}
	g := New(s, reg)

	rev, err := s.CreateRevision("auto", "", "aaa", "manual", "full", "{}")
	if err != nil {
		t.Fatal(err)
	}

	const screenFile = "app/admin/page.tsx"
	if err := os.MkdirAll(filepath.Join(dir, filepath.Dir(screenFile)), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, screenFile), []byte("export default function Page(){}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	key := "ui:screen:auto:admin"
	if _, err := g.UpsertNode(validate.NodeInput{
		NodeKey: key, Layer: "ui", NodeType: "screen", DomainKey: "auto", Name: "Admin",
	}, rev); err != nil {
		t.Fatal(err)
	}
	// The third row is owned by the structural phase, not by an importer, but
	// the rule is the same one: phase 1 must not stale-mark it, because phase 2
	// re-extracts the file in the same run and supersedes exactly what it no
	// longer asserts. It is recognised by its EXTRACTOR, not by the `ast`
	// kind — complexity and similarity write `ast` rows too and must keep
	// refreshing with the file (store.StructuralOwnedExtractorIDs).
	owners := map[string]string{
		"surface_extract": "surface-import",
		"declared":        "surface-import",
		"ast":             "chronicle-structural",
	}
	for _, kind := range []string{"surface_extract", "declared", "ast"} {
		if _, err := g.AddNodeEvidence(key, validate.EvidenceInput{
			TargetKind: "node", SourceKind: kind,
			FilePath: screenFile, LineStart: 1,
			ExtractorID: owners[kind], ExtractorVersion: "1",
			Assertion: `{"x":1}`, AssertionKind: "decision", AssertionVersion: "1",
			Confidence: 0.9, Polarity: "positive", RevisionID: rev,
		}); err != nil {
			t.Fatalf("%s evidence: %v", kind, err)
		}
	}
	return g, dir, screenFile
}

func evidenceStatuses(t *testing.T, s *store.Store, nodeKey string) map[string]string {
	t.Helper()
	node, err := s.GetNodeByKey(nodeKey)
	if err != nil {
		t.Fatal(err)
	}
	rows, err := s.ListEvidenceByNode(node.NodeID)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for _, r := range rows {
		out[r.SourceKind] = r.EvidenceStatus
	}
	return out
}

// A code refresh re-verifies what a code scan can re-emit. Surface evidence is
// neither: the importer owns it, no scan ever writes it back, and the file
// verifier cannot check a human decision — so touching it means the evidence
// goes stale, gets "refuted" by a verifier that was never going to find it,
// and stays that way forever. Live measurement: 146 of 146 declared rows read
// stale/missing after ONE refresh.
func TestARefreshLeavesImporterOwnedEvidenceAlone(t *testing.T) {
	g, dir, screenFile := importerOwnedFixture(t)
	cwd, _ := os.Getwd()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(cwd)

	if _, err := g.RefreshFromDiff("auto", "bbb", []string{screenFile}, nil); err != nil {
		t.Fatalf("RefreshFromDiff: %v", err)
	}

	statuses := evidenceStatuses(t, g.Store(), "ui:screen:auto:admin")
	for _, kind := range []string{"surface_extract", "declared", "ast"} {
		if statuses[kind] != "valid" {
			t.Errorf("%s evidence is %q after a code refresh — phase 1 does not own it", kind, statuses[kind])
		}
	}
}

// The same rows must not be counted as "touched" by freshness, nor listed as
// files an agent should rescan: a rescan cannot fix them.
func TestImporterOwnedEvidenceIsNotReportedAsWorkToDo(t *testing.T) {
	g, dir, screenFile := importerOwnedFixture(t)
	cwd, _ := os.Getwd()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(cwd)

	res, err := g.RefreshFromDiff("auto", "bbb", []string{screenFile}, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range res.Invalidated.FilesToRescan {
		if f == screenFile {
			t.Errorf("%s is listed for rescan on the strength of importer-owned evidence", f)
		}
	}

	counts, err := g.Store().CountCodeEvidenceByStatus("auto")
	if err != nil {
		t.Fatal(err)
	}
	if counts["stale"] != 0 {
		t.Errorf("freshness would report %d stale rows that no scan can refresh", counts["stale"])
	}
}
