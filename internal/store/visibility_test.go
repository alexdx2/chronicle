package store

import "testing"

func TestVisibleRevisionsMainContext(t *testing.T) {
	s := openTestDB(t)
	ctxID, _ := s.CreateContext("dom", "main", "main", 0, 0)
	r1, _ := s.CreateRevisionWithContext("dom", "", "aaa", "manual", "full", "{}", ctxID)
	r2, _ := s.CreateRevisionWithContext("dom", "aaa", "bbb", "manual", "incremental", "{}", ctxID)
	r3, _ := s.CreateRevisionWithContext("dom", "bbb", "ccc", "manual", "incremental", "{}", ctxID)

	revs, err := s.VisibleRevisionIDs(ctxID, 0)
	if err != nil {
		t.Fatalf("VisibleRevisionIDs: %v", err)
	}
	if len(revs) != 3 {
		t.Fatalf("len = %d, want 3", len(revs))
	}

	// With as_of ceiling: only first 2
	revs2, _ := s.VisibleRevisionIDs(ctxID, r2)
	if len(revs2) != 2 {
		t.Errorf("with ceiling: len = %d, want 2", len(revs2))
	}
	_ = r1
	_ = r3
}

func TestVisibleRevisionsBranchContext(t *testing.T) {
	s := openTestDB(t)
	mainCtx, _ := s.CreateContext("dom", "main", "main", 0, 0)
	r1, _ := s.CreateRevisionWithContext("dom", "", "aaa", "manual", "full", "{}", mainCtx)
	r2, _ := s.CreateRevisionWithContext("dom", "aaa", "bbb", "manual", "incremental", "{}", mainCtx)
	s.UpdateContextHead(mainCtx, r2, "bbb")

	branchCtx, _ := s.CreateContext("dom", "feature/x", "feature/x", mainCtx, r2)
	r3, _ := s.CreateRevisionWithContext("dom", "bbb", "ccc", "manual", "incremental", "{}", branchCtx)

	// Main advances further (should NOT be visible to branch)
	r4, _ := s.CreateRevisionWithContext("dom", "bbb", "ddd", "manual", "incremental", "{}", mainCtx)

	branchRevs, err := s.VisibleRevisionIDs(branchCtx, 0)
	if err != nil {
		t.Fatalf("VisibleRevisionIDs: %v", err)
	}

	revSet := map[int64]bool{}
	for _, r := range branchRevs {
		revSet[r] = true
	}

	if !revSet[r1] || !revSet[r2] {
		t.Error("branch should see main revisions up to branch point")
	}
	if !revSet[r3] {
		t.Error("branch should see its own revisions")
	}
	if revSet[r4] {
		t.Error("branch should NOT see main revisions after branch point")
	}
}

func TestVisibleRevisionsCTE(t *testing.T) {
	s := openTestDB(t)
	mainCtx, _ := s.CreateContext("dom", "main", "main", 0, 0)
	s.CreateRevisionWithContext("dom", "", "aaa", "manual", "full", "{}", mainCtx)

	cte, args := s.BuildVisibleRevisionsCTE(mainCtx, 0)
	if cte == "" {
		t.Fatal("expected non-empty CTE")
	}
	if len(args) == 0 {
		t.Fatal("expected non-empty args")
	}

	q := cte + ` SELECT COUNT(*) FROM visible_revisions`
	var count int
	err := s.db.QueryRow(q, args...).Scan(&count)
	if err != nil {
		t.Fatalf("CTE query failed: %v", err)
	}
	if count != 1 {
		t.Errorf("count = %d, want 1", count)
	}
}
