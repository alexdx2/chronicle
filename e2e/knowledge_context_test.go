package e2e

import (
	"path/filepath"
	"testing"

	"github.com/alexdx2/chronicle-core/graph"
	"github.com/alexdx2/chronicle-core/registry"
	"github.com/alexdx2/chronicle-core/store"
)

func setupContextTest(t *testing.T) (*graph.Graph, *store.Store) {
	t.Helper()
	dir := t.TempDir()
	s, err := store.Open(filepath.Join(dir, "ctx.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	reg, err := registry.LoadDefaults()
	if err != nil {
		t.Fatalf("LoadDefaults: %v", err)
	}
	return graph.New(s, reg), s
}

// TestContextLifecycleMainScan verifies that a full scan creates a main context,
// the context is active, head points to the revision, and all nodes are visible.
func TestContextLifecycleMainScan(t *testing.T) {
	g, s := setupContextTest(t)
	payload := loadTomAndJerryPayload(t)

	// Create main context.
	ctxID, err := s.CreateContext("tomandjerry", "main", "refs/heads/main", 0, 0)
	if err != nil {
		t.Fatalf("CreateContext: %v", err)
	}

	// Create revision linked to context.
	revID, err := s.CreateRevisionWithContext("tomandjerry", "", "lc-main-001", "manual", "full", "{}", ctxID)
	if err != nil {
		t.Fatalf("CreateRevisionWithContext: %v", err)
	}

	// Import payload.
	result, err := g.ImportAll(payload, revID)
	if err != nil {
		t.Fatalf("ImportAll: %v", err)
	}

	// Update context head.
	if err := s.UpdateContextHead(ctxID, revID, "lc-main-001"); err != nil {
		t.Fatalf("UpdateContextHead: %v", err)
	}

	// Verify context exists with status "active".
	ctx, err := s.GetContext(ctxID)
	if err != nil {
		t.Fatalf("GetContext: %v", err)
	}
	if ctx.Status != "active" {
		t.Errorf("context status = %q, want %q", ctx.Status, "active")
	}

	// Verify head points to the revision.
	if ctx.HeadRevisionID != revID {
		t.Errorf("head_revision_id = %d, want %d", ctx.HeadRevisionID, revID)
	}
	if ctx.HeadCommitSHA != "lc-main-001" {
		t.Errorf("head_commit_sha = %q, want %q", ctx.HeadCommitSHA, "lc-main-001")
	}

	// Verify all nodes visible.
	stats, err := g.QueryStats("tomandjerry")
	if err != nil {
		t.Fatalf("QueryStats: %v", err)
	}
	if stats.NodeCount != len(payload.Nodes) {
		t.Errorf("nodes = %d, want %d", stats.NodeCount, len(payload.Nodes))
	}

	t.Logf("Main scan imported %d nodes, %d edges", result.NodesCreated, result.EdgesCreated)
}

// TestContextLifecycleBranchIsolation verifies that a branch context does not
// see revisions added to main after the branch point.
func TestContextLifecycleBranchIsolation(t *testing.T) {
	_, s := setupContextTest(t)

	// Create main context.
	mainCtxID, err := s.CreateContext("tomandjerry", "main", "refs/heads/main", 0, 0)
	if err != nil {
		t.Fatalf("CreateContext main: %v", err)
	}

	// Create initial revision on main (the branch point).
	baseRevID, err := s.CreateRevisionWithContext("tomandjerry", "", "bi-main-001", "manual", "full", "{}", mainCtxID)
	if err != nil {
		t.Fatalf("CreateRevisionWithContext base: %v", err)
	}

	// Create branch context based on main at baseRevID.
	branchCtxID, err := s.CreateContext("tomandjerry", "feature-x", "refs/heads/feature-x", mainCtxID, baseRevID)
	if err != nil {
		t.Fatalf("CreateContext branch: %v", err)
	}

	// Add a new revision on main AFTER the branch point.
	postBranchRevID, err := s.CreateRevisionWithContext("tomandjerry", "bi-main-001", "bi-main-002", "manual", "incremental", "{}", mainCtxID)
	if err != nil {
		t.Fatalf("CreateRevisionWithContext post-branch: %v", err)
	}

	// Add a revision on the branch.
	branchRevID, err := s.CreateRevisionWithContext("tomandjerry", "bi-main-001", "bi-branch-001", "manual", "incremental", "{}", branchCtxID)
	if err != nil {
		t.Fatalf("CreateRevisionWithContext branch: %v", err)
	}

	// Verify branch's visible revisions.
	branchVis, err := s.VisibleRevisionIDs(branchCtxID, 0)
	if err != nil {
		t.Fatalf("VisibleRevisionIDs branch: %v", err)
	}

	// Branch should see: baseRevID (from main, up to branch point) + branchRevID.
	// It should NOT see postBranchRevID.
	visSet := make(map[int64]bool)
	for _, id := range branchVis {
		visSet[id] = true
	}

	if !visSet[baseRevID] {
		t.Errorf("branch should see base revision %d", baseRevID)
	}
	if !visSet[branchRevID] {
		t.Errorf("branch should see its own revision %d", branchRevID)
	}
	if visSet[postBranchRevID] {
		t.Errorf("branch should NOT see main's post-branch revision %d", postBranchRevID)
	}

	// Verify main sees all its revisions.
	mainVis, err := s.VisibleRevisionIDs(mainCtxID, 0)
	if err != nil {
		t.Fatalf("VisibleRevisionIDs main: %v", err)
	}
	mainSet := make(map[int64]bool)
	for _, id := range mainVis {
		mainSet[id] = true
	}
	if !mainSet[baseRevID] {
		t.Errorf("main should see base revision %d", baseRevID)
	}
	if !mainSet[postBranchRevID] {
		t.Errorf("main should see post-branch revision %d", postBranchRevID)
	}

	t.Logf("Branch visible: %v, Main visible: %v", branchVis, mainVis)
}

// TestContextLifecycleVersionedEvidence verifies that MarkEvidenceStaleByFilesVersioned
// closes old evidence rows and inserts new stale rows.
func TestContextLifecycleVersionedEvidence(t *testing.T) {
	g, s := setupContextTest(t)
	payload := loadTomAndJerryPayload(t)

	// Create main context.
	ctxID, err := s.CreateContext("tomandjerry", "main", "refs/heads/main", 0, 0)
	if err != nil {
		t.Fatalf("CreateContext: %v", err)
	}

	// Create revision and import.
	revID, err := s.CreateRevisionWithContext("tomandjerry", "", "ve-main-001", "manual", "full", "{}", ctxID)
	if err != nil {
		t.Fatalf("CreateRevisionWithContext: %v", err)
	}
	if _, err := g.ImportAll(payload, revID); err != nil {
		t.Fatalf("ImportAll: %v", err)
	}

	// Get a node whose evidence lives in the target file.
	// TomService (code:provider:tomandjerry:tomservice) has evidence in src/tom/tom.service.ts.
	nodeID, err := s.GetNodeIDByKey("code:provider:tomandjerry:tomservice")
	if err != nil {
		t.Fatalf("GetNodeIDByKey: %v", err)
	}

	// Count evidence rows before invalidation.
	beforeRows, err := s.ListEvidenceByNode(nodeID)
	if err != nil {
		t.Fatalf("ListEvidenceByNode before: %v", err)
	}

	// Filter to only evidence from target file.
	targetFile := "src/tom/tom.service.ts"
	var beforeFileRows []store.EvidenceRow
	for _, r := range beforeRows {
		if r.FilePath == targetFile {
			beforeFileRows = append(beforeFileRows, r)
		}
	}
	beforeCount := len(beforeFileRows)
	if beforeCount == 0 {
		t.Fatal("expected evidence rows for TomService in target file before invalidation")
	}

	// Create a new revision for the invalidation.
	invalidateRevID, err := s.CreateRevisionWithContext("tomandjerry", "ve-main-001", "ve-main-002", "manual", "incremental", "{}", ctxID)
	if err != nil {
		t.Fatalf("CreateRevisionWithContext invalidate: %v", err)
	}

	// Invalidate evidence for the file.
	staleCount, _, _, err := s.MarkEvidenceStaleByFilesVersioned([]string{targetFile}, invalidateRevID, ctxID)
	if err != nil {
		t.Fatalf("MarkEvidenceStaleByFilesVersioned: %v", err)
	}
	if staleCount == 0 {
		t.Fatal("expected at least one evidence row to be marked stale")
	}

	// Count evidence rows after invalidation for the same node.
	afterRows, err := s.ListEvidenceByNode(nodeID)
	if err != nil {
		t.Fatalf("ListEvidenceByNode after: %v", err)
	}
	var afterFileRows []store.EvidenceRow
	for _, r := range afterRows {
		if r.FilePath == targetFile {
			afterFileRows = append(afterFileRows, r)
		}
	}
	afterCount := len(afterFileRows)

	// Total rows should have increased: old rows closed (valid_to set) + new stale rows inserted.
	if afterCount != beforeCount+beforeCount {
		t.Errorf("after count = %d, want %d (before %d doubled: old closed + new stale)", afterCount, beforeCount*2, beforeCount)
	}

	// Check that old rows have valid_to_revision_id set.
	closedCount := 0
	staleNewCount := 0
	for _, r := range afterFileRows {
		if r.ValidToRevisionID == invalidateRevID {
			closedCount++
		}
		if r.EvidenceStatus == "stale" && r.ValidToRevisionID == 0 {
			staleNewCount++
		}
	}
	if closedCount == 0 {
		t.Error("expected old evidence rows to have valid_to_revision_id set")
	}
	if staleNewCount == 0 {
		t.Error("expected new stale evidence rows with valid_to_revision_id NULL/0")
	}

	t.Logf("Before: %d rows, stale: %d, after: %d rows (closed: %d, new stale: %d)",
		beforeCount, staleCount, afterCount, closedCount, staleNewCount)
}

// TestContextLifecycleRevisionChain verifies that multiple revisions created with
// a context are all visible via VisibleRevisionIDs.
func TestContextLifecycleRevisionChain(t *testing.T) {
	_, s := setupContextTest(t)

	// Create main context.
	ctxID, err := s.CreateContext("tomandjerry", "main", "refs/heads/main", 0, 0)
	if err != nil {
		t.Fatalf("CreateContext: %v", err)
	}

	// Create 3 revisions linked to the context.
	var revIDs []int64
	shas := []string{"rc-main-001", "rc-main-002", "rc-main-003"}
	for i, sha := range shas {
		before := ""
		if i > 0 {
			before = shas[i-1]
		}
		revID, err := s.CreateRevisionWithContext("tomandjerry", before, sha, "manual", "incremental", "{}", ctxID)
		if err != nil {
			t.Fatalf("CreateRevisionWithContext[%d]: %v", i, err)
		}
		revIDs = append(revIDs, revID)
	}

	// VisibleRevisionIDs should return all 3.
	visible, err := s.VisibleRevisionIDs(ctxID, 0)
	if err != nil {
		t.Fatalf("VisibleRevisionIDs: %v", err)
	}

	if len(visible) != 3 {
		t.Fatalf("visible revisions = %d, want 3", len(visible))
	}

	visSet := make(map[int64]bool)
	for _, id := range visible {
		visSet[id] = true
	}
	for i, revID := range revIDs {
		if !visSet[revID] {
			t.Errorf("revision[%d] (%d) not in visible set", i, revID)
		}
	}

	// Also verify ordering (should be ascending).
	for i := 1; i < len(visible); i++ {
		if visible[i] <= visible[i-1] {
			t.Errorf("visible revisions not in ascending order: %v", visible)
			break
		}
	}

	t.Logf("Revision chain: %v, visible: %v", revIDs, visible)
}

// TestContextLifecycleChangelog verifies that changelog entries can be appended
// and queried back with entity_key and revision range filtering.
func TestContextLifecycleChangelog(t *testing.T) {
	_, s := setupContextTest(t)

	// Create context and revisions.
	ctxID, err := s.CreateContext("tomandjerry", "main", "refs/heads/main", 0, 0)
	if err != nil {
		t.Fatalf("CreateContext: %v", err)
	}

	rev1, err := s.CreateRevisionWithContext("tomandjerry", "", "cl-main-001", "manual", "full", "{}", ctxID)
	if err != nil {
		t.Fatalf("CreateRevisionWithContext 1: %v", err)
	}
	rev2, err := s.CreateRevisionWithContext("tomandjerry", "cl-main-001", "cl-main-002", "manual", "incremental", "{}", ctxID)
	if err != nil {
		t.Fatalf("CreateRevisionWithContext 2: %v", err)
	}
	rev3, err := s.CreateRevisionWithContext("tomandjerry", "cl-main-002", "cl-main-003", "manual", "incremental", "{}", ctxID)
	if err != nil {
		t.Fatalf("CreateRevisionWithContext 3: %v", err)
	}

	// Append changelog entries.
	entries := []store.ChangelogRow{
		{RevisionID: rev1, ContextID: ctxID, EntityType: "node", EntityKey: "data:model:tomandjerry:cat", ChangeType: "created"},
		{RevisionID: rev1, ContextID: ctxID, EntityType: "node", EntityKey: "data:model:tomandjerry:mouse", ChangeType: "created"},
		{RevisionID: rev2, ContextID: ctxID, EntityType: "node", EntityKey: "data:model:tomandjerry:cat", ChangeType: "updated", FieldChanges: `{"name":"Cat2"}`},
		{RevisionID: rev2, ContextID: ctxID, EntityType: "edge", EntityKey: "edge:cat-weapon", ChangeType: "created"},
		{RevisionID: rev3, ContextID: ctxID, EntityType: "node", EntityKey: "data:model:tomandjerry:cat", ChangeType: "updated", FieldChanges: `{"name":"Cat3"}`},
	}

	for i, e := range entries {
		_, err := s.AppendChangelog(e)
		if err != nil {
			t.Fatalf("AppendChangelog[%d]: %v", i, err)
		}
	}

	// Query all entries for this context.
	all, err := s.QueryChangelog(ctxID, "", 0, 0)
	if err != nil {
		t.Fatalf("QueryChangelog all: %v", err)
	}
	if len(all) != 5 {
		t.Errorf("all changelog entries = %d, want 5", len(all))
	}

	// Filter by entity_key — only "cat" entries.
	catEntries, err := s.QueryChangelog(ctxID, "data:model:tomandjerry:cat", 0, 0)
	if err != nil {
		t.Fatalf("QueryChangelog cat: %v", err)
	}
	if len(catEntries) != 3 {
		t.Errorf("cat changelog entries = %d, want 3", len(catEntries))
	}

	// Filter by revision range — only rev2.
	rev2Entries, err := s.QueryChangelog(ctxID, "", rev2, rev2)
	if err != nil {
		t.Fatalf("QueryChangelog rev2: %v", err)
	}
	if len(rev2Entries) != 2 {
		t.Errorf("rev2 changelog entries = %d, want 2", len(rev2Entries))
	}

	// Filter by both entity_key and revision range.
	catRev2, err := s.QueryChangelog(ctxID, "data:model:tomandjerry:cat", rev2, rev2)
	if err != nil {
		t.Fatalf("QueryChangelog cat+rev2: %v", err)
	}
	if len(catRev2) != 1 {
		t.Errorf("cat+rev2 changelog entries = %d, want 1", len(catRev2))
	}

	// Filter from rev2 to rev3.
	rev2to3, err := s.QueryChangelog(ctxID, "", rev2, rev3)
	if err != nil {
		t.Fatalf("QueryChangelog rev2-3: %v", err)
	}
	if len(rev2to3) != 3 {
		t.Errorf("rev2-3 changelog entries = %d, want 3", len(rev2to3))
	}

	t.Logf("Changelog: all=%d, cat=%d, rev2=%d, cat+rev2=%d, rev2to3=%d",
		len(all), len(catEntries), len(rev2Entries), len(catRev2), len(rev2to3))
}
