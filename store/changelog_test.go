package store

import "testing"

func TestChangelogAppend(t *testing.T) {
	s := openTestDB(t)
	// Create context directly via SQL since CreateContext may not exist yet.
	res, _ := s.db.Exec(`INSERT INTO knowledge_contexts (domain_key, name, git_ref, status) VALUES (?, ?, ?, 'active')`, "dom", "main", "main")
	ctxID, _ := res.LastInsertId()
	revID, _ := s.CreateRevision("dom", "", "abc", "manual", "full", "{}")

	id, err := s.AppendChangelog(ChangelogRow{
		RevisionID: revID,
		ContextID:  ctxID,
		EntityType: "node",
		EntityKey:  "code:provider:dom:svc",
		ChangeType: "created",
	})
	if err != nil {
		t.Fatalf("AppendChangelog: %v", err)
	}
	if id == 0 {
		t.Fatal("expected non-zero changelog_id")
	}
}

func TestChangelogQuery(t *testing.T) {
	s := openTestDB(t)
	res, _ := s.db.Exec(`INSERT INTO knowledge_contexts (domain_key, name, git_ref, status) VALUES (?, ?, ?, 'active')`, "dom", "main", "main")
	ctxID, _ := res.LastInsertId()
	revID, _ := s.CreateRevision("dom", "", "abc", "manual", "full", "{}")

	s.AppendChangelog(ChangelogRow{RevisionID: revID, ContextID: ctxID, EntityType: "node", EntityKey: "k1", ChangeType: "created"})
	s.AppendChangelog(ChangelogRow{RevisionID: revID, ContextID: ctxID, EntityType: "node", EntityKey: "k1", ChangeType: "stale"})
	s.AppendChangelog(ChangelogRow{RevisionID: revID, ContextID: ctxID, EntityType: "edge", EntityKey: "k2", ChangeType: "created"})

	rows, err := s.QueryChangelog(ctxID, "k1", 0, 0)
	if err != nil {
		t.Fatalf("QueryChangelog: %v", err)
	}
	if len(rows) != 2 {
		t.Errorf("len = %d, want 2 (both k1 entries)", len(rows))
	}
}
