package surface

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/alexdx2/chronicle-core/freshness"
	"github.com/alexdx2/chronicle-core/validate"
)

// The commonest real flow is scan-at-HEAD then import-the-surface-at-HEAD, so
// the import has no revision row of its own: it rides along on the scan's.
// That must not cost the ui layer its identity — "we have a ui layer, imported
// at this commit, from this file" is the whole answer chronicle_freshness
// gives about the surface, and in the live okeep run it simply vanished.
func TestReusedRevisionStillCarriesTheUILayer(t *testing.T) {
	g := setupSeededGraph(t)
	f := loadFixture(t)
	scanRev := seedScanRevisionAt(t, g.Store(), f.Commit)

	if _, err := Import(g, f, ImportOptions{Domain: "mini"}); err != nil {
		t.Fatalf("Import: %v", err)
	}

	rep, err := freshness.Compute("", "mini", "mini", g.Store())
	if err != nil {
		t.Fatalf("freshness: %v", err)
	}
	ui := rep.Layers["ui"]
	if ui == nil {
		t.Fatal("the ui layer disappeared when the import reused a scan revision")
	}
	if ui.SHA != f.Commit {
		t.Errorf("ui layer at %s, want the extract's commit %s", ui.SHA, f.Commit)
	}
	if ui.Source != fixturePath {
		t.Errorf("ui layer source = %q, want %q", ui.Source, fixturePath)
	}

	// The scan is still a scan: the row it shares must not be relabelled as a
	// layer import, or the code layer loses its own commit.
	if rep.Scanned == nil || rep.Scanned.RevisionID != scanRev {
		t.Fatalf("scanned = %+v, want the scan revision %d", rep.Scanned, scanRev)
	}

	rev, err := g.Store().GetRevision(scanRev)
	if err != nil {
		t.Fatal(err)
	}
	var md map[string]any
	if err := json.Unmarshal([]byte(rev.Metadata), &md); err != nil {
		t.Fatalf("metadata %q: %v", rev.Metadata, err)
	}
	if md["layer"] != nil {
		t.Errorf(`a scan row must not be stamped layer=%v — LatestScanRevision would skip it`, md["layer"])
	}
	if md["kind"] != "scan" {
		t.Errorf("the scan's own metadata was overwritten: %v", md)
	}
	surf, _ := md["surface"].(map[string]any)
	if surf == nil || surf["product"] != "mini" || surf["content_hash"] != f.ContentHash {
		t.Errorf("surface metadata = %v", md["surface"])
	}
}

// An importer whose resolution rules changed must re-import, not report
// "already imported" from a mark its previous self wrote.
func TestTheImportedMarkIsScopedToTheImporterVersion(t *testing.T) {
	key := importedHashKey("auto", "shop", "abc123")
	if !strings.HasSuffix(key, ":v"+itoa(int64(ImporterVersion))) {
		t.Errorf("hash key %q does not name the importer version", key)
	}
}

func TestForceReimportsAnUnchangedExtract(t *testing.T) {
	g := setupSeededGraph(t)
	if _, err := Import(g, loadFixture(t), ImportOptions{Domain: "mini"}); err != nil {
		t.Fatal(err)
	}
	res, err := Import(g, loadFixture(t), ImportOptions{Domain: "mini", Force: true})
	if err != nil {
		t.Fatalf("forced Import: %v", err)
	}
	if res.AlreadyImported {
		t.Fatal("--force must bypass the content-hash gate")
	}
	if res.Nodes == 0 && res.Evidence == 0 {
		t.Fatalf("forced import wrote nothing: %+v", res)
	}
}

// The canonical key is a fast path, not a different rule: a tombstoned node is
// not something the surface may point at, and the slower name index already
// refuses one. Resolving to it would hang new ui edges off a deleted node.
func TestCanonicalFastPathsSkipTombstonedNodes(t *testing.T) {
	g := setupSeededGraph(t)
	s := g.Store()
	for _, key := range []string{
		"data:field:mini:shop/quiet-from-hour",
		"contract:endpoint:mini:mutation:/saveshopsettings",
		"contract:endpoint:mini:get:/panel/ustawienia",
	} {
		if err := s.DeleteNode(key); err != nil {
			t.Fatalf("tombstone %s: %v", key, err)
		}
	}

	r := &Resolver{Store: s, Domain: "mini"}
	if key, err := r.FieldKey("Shop.quietFromHour"); err == nil {
		t.Errorf("FieldKey resolved to a tombstoned node %s", key)
	}
	if key, err := r.MutationKey("saveShopSettings"); err == nil {
		t.Errorf("MutationKey resolved to a tombstoned node %s", key)
	}
	if key, err := r.RouteKey("/panel/ustawienia"); err == nil {
		t.Errorf("RouteKey resolved to a tombstoned node %s", key)
	}
}

// Closed world means the file is the whole truth about this product's surface.
// A control that was tombstoned by an earlier import and is still absent must
// stay tombstoned — and must carry the revision that removed it, so a reader
// can date the removal instead of finding a status with no story.
func TestCloseWorldStampsTheRemovingRevisionAndConsidersStaleNodes(t *testing.T) {
	g := setupSeededGraph(t)
	s := g.Store()

	// A ui node of this product that no extract will ever mention, already
	// marked stale by something else (a stale-mark sweep, an interrupted run).
	orphan := "ui:control:mini:orphan"
	rev, err := s.CreateRevision("mini", "", "seedaaa", "manual", "full", "{}")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := g.UpsertNode(validate.NodeInput{
		NodeKey: orphan, Layer: "ui", NodeType: "control", DomainKey: "mini",
		Name: "Orphan", Metadata: `{"product":"mini"}`,
	}, rev); err != nil {
		t.Fatal(err)
	}
	row, err := s.GetNodeByKey(orphan)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetNodeStatus(row.NodeID, "stale"); err != nil {
		t.Fatalf("SetNodeStatus: %v", err)
	}

	res, err := Import(g, loadFixture(t), ImportOptions{Domain: "mini"})
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	found := false
	for _, k := range res.Deleted {
		if k == orphan {
			found = true
		}
	}
	if !found {
		t.Fatalf("a stale ui node of this product was left behind: deleted %v", res.Deleted)
	}

	row, err = s.GetNodeByKey(orphan)
	if err != nil {
		t.Fatal(err)
	}
	if row.Status != "deleted" {
		t.Errorf("orphan status = %q, want deleted", row.Status)
	}
	var md map[string]any
	_ = json.Unmarshal([]byte(row.Metadata), &md)
	if md["removed_in_revision"] == nil {
		t.Errorf("a tombstone must name the revision that made it: %v", row.Metadata)
	}
}
