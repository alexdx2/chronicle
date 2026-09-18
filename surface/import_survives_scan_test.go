package surface

import (
	"testing"

	"github.com/alexdx2/chronicle-core/graph"
	"github.com/alexdx2/chronicle-core/store"
)

// countUI reports how many of this domain's ui nodes are active, and how many
// are not.
func countUI(t *testing.T, s *store.Store, domain string) (active, retired int) {
	t.Helper()
	for _, status := range []string{"active", "stale", "deleted"} {
		rows, err := s.ListNodes(store.NodeFilter{Layer: "ui", Domain: domain, Status: status})
		if err != nil {
			t.Fatalf("ListNodes %s: %v", status, err)
		}
		if status == "active" {
			active += len(rows)
		} else {
			retired += len(rows)
		}
	}
	return active, retired
}

// runScanSweep is what every full scan does last: open a revision at the new
// commit, re-assert the code it saw, and retire whatever it did not.
func runScanSweep(t *testing.T, g *graph.Graph, domain, sha string) {
	t.Helper()
	s := g.Store()
	revID, err := s.CreateRevision(domain, "", sha, "manual", "full", "{}")
	if err != nil {
		t.Fatalf("CreateRevision: %v", err)
	}
	if _, err := s.MarkStaleNodes(domain, revID); err != nil {
		t.Fatalf("MarkStaleNodes: %v", err)
	}
	if _, err := s.MarkStaleEdges(domain, revID); err != nil {
		t.Fatalf("MarkStaleEdges: %v", err)
	}
}

// A scan's closing sweep retires what the scan did not see. That proxy is right
// for code and wrong for an imported layer: no scan ever re-asserts a ui node,
// so every one of them has last_seen < the scan's revision and the sweep used
// to take the whole layer with it — nodes, edges and the evidence under them.
func TestScanSweepLeavesTheImportedLayerStanding(t *testing.T) {
	g := setupSeededGraph(t)
	f := loadFixture(t)
	if _, err := Import(g, f, ImportOptions{Domain: "mini"}); err != nil {
		t.Fatalf("Import: %v", err)
	}
	before, retiredBefore := countUI(t, g.Store(), "mini")
	if before == 0 || retiredBefore != 0 {
		t.Fatalf("fixture: %d active / %d retired ui nodes after the import", before, retiredBefore)
	}

	runScanSweep(t, g, "mini", "scan0001")

	after, retiredAfter := countUI(t, g.Store(), "mini")
	if after != before || retiredAfter != 0 {
		t.Errorf("after the scan sweep: %d active / %d retired, want %d / 0", after, retiredAfter, before)
	}

	// The impact answer is the point of the layer: a field must still reach the
	// control that edits it.
	imp, err := g.QueryImpact("data:field:mini:shop/sms-sender-id", graph.ImpactOptions{MaxDepth: 3})
	if err != nil {
		t.Fatalf("QueryImpact: %v", err)
	}
	if imp.TotalImpacted == 0 {
		t.Error("the sweep cut every edge out of the imported layer: impact reaches nothing")
	}
}

// "Already imported" has to mean the graph matches this extract, not merely
// that these bytes were seen once. Anything that retires ui nodes outside an
// import leaves the record saying done over a layer that is gone, and the
// obvious repair — re-run the import — used to do nothing at all.
func TestImportHealsALayerThatIsNoLongerStanding(t *testing.T) {
	g := setupSeededGraph(t)
	f := loadFixture(t)
	first, err := Import(g, f, ImportOptions{Domain: "mini"})
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	if first.Nodes == 0 {
		t.Fatal("fixture: the first import wrote nothing")
	}

	// Retire the layer behind the importer's back, the way anything that
	// bypasses the sweep's exemption would.
	rows, err := g.Store().ListNodes(store.NodeFilter{Layer: "ui", Domain: "mini", Status: "active"})
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range rows {
		if err := g.Store().DeleteNode(row.NodeKey); err != nil {
			t.Fatal(err)
		}
	}
	if active, _ := countUI(t, g.Store(), "mini"); active != 0 {
		t.Fatalf("fixture: %d ui nodes still active after the damage", active)
	}

	// Same file, same commit, same recorded hash — every reason to skip.
	again, err := Import(g, f, ImportOptions{Domain: "mini"})
	if err != nil {
		t.Fatalf("re-import: %v", err)
	}
	if again.AlreadyImported {
		t.Error("the import skipped over a layer that is not in the graph")
	}
	if !again.Healed {
		t.Error("the repair was not reported as one")
	}
	if active, _ := countUI(t, g.Store(), "mini"); active == 0 {
		t.Error("the re-import did not put the layer back")
	}
}

// The skip still has to happen when there is nothing to repair, or every hook
// and every re-run would rewrite a layer that is already correct.
func TestImportStillSkipsWhenTheLayerIsIntact(t *testing.T) {
	g := setupSeededGraph(t)
	f := loadFixture(t)
	if _, err := Import(g, f, ImportOptions{Domain: "mini"}); err != nil {
		t.Fatalf("Import: %v", err)
	}
	again, err := Import(g, f, ImportOptions{Domain: "mini"})
	if err != nil {
		t.Fatalf("re-import: %v", err)
	}
	if !again.AlreadyImported || again.Healed {
		t.Errorf("already_imported=%v healed=%v, want a plain skip", again.AlreadyImported, again.Healed)
	}
	if again.Nodes != 0 {
		t.Errorf("the skip wrote %d nodes", again.Nodes)
	}
}
