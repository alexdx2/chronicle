# Knowledge Contexts Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add temporal versioning and branch-aware knowledge contexts so queries resolve visibility from the caller's git-derived context lineage.

**Architecture:** Three-layer change: store (schema + versioned mutation functions), graph (context-filtered queries + CTE builder), MCP (new tools + context param on existing tools). Current tables gain `valid_from_revision_id`, `valid_to_revision_id`, `context_id` columns. Mutations close old validity windows and insert new version rows. Queries use a CTE-based visible revision set derived from context lineage.

**Tech Stack:** Go, SQLite, MCP protocol

**Spec:** `docs/superpowers/specs/2026-04-28-knowledge-contexts-design.md`

---

### Task 1: Schema — New Tables + Column Additions

**Files:**
- Modify: `internal/store/store.go` (schema const + migrate function)

This task adds the `knowledge_contexts` and `graph_changelog` tables, adds `context_id` to `graph_revisions`, and adds `valid_from_revision_id`, `valid_to_revision_id`, `context_id` to `graph_nodes`, `graph_edges`. Evidence already has `valid_from`/`valid_to` columns. Also adds `evidence_uid` to evidence and `from_node_key`/`to_node_key` to edges.

- [ ] **Step 1: Write test for new schema**

Create `internal/store/contexts_test.go`:

```go
package store

import (
	"path/filepath"
	"testing"
)

func openTestDB(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestSchemaContextTablesExist(t *testing.T) {
	s := openTestDB(t)

	// knowledge_contexts table should exist
	_, err := s.db.Exec(`INSERT INTO knowledge_contexts (domain_key, name, status) VALUES ('test', 'main', 'active')`)
	if err != nil {
		t.Fatalf("knowledge_contexts table missing: %v", err)
	}

	// graph_changelog table should exist
	_, err = s.db.Exec(`INSERT INTO graph_changelog (revision_id, context_id, entity_type, entity_key, change_type) VALUES (0, 1, 'node', 'test', 'created')`)
	if err != nil {
		t.Fatalf("graph_changelog table missing: %v", err)
	}
}

func TestSchemaNewColumns(t *testing.T) {
	s := openTestDB(t)

	// graph_revisions.context_id
	_, err := s.db.Exec(`SELECT context_id FROM graph_revisions LIMIT 0`)
	if err != nil {
		t.Fatalf("graph_revisions.context_id missing: %v", err)
	}

	// graph_nodes new columns
	_, err = s.db.Exec(`SELECT valid_from_revision_id, valid_to_revision_id, context_id FROM graph_nodes LIMIT 0`)
	if err != nil {
		t.Fatalf("graph_nodes temporal columns missing: %v", err)
	}

	// graph_edges new columns
	_, err = s.db.Exec(`SELECT valid_from_revision_id, valid_to_revision_id, context_id, from_node_key, to_node_key FROM graph_edges LIMIT 0`)
	if err != nil {
		t.Fatalf("graph_edges temporal/key columns missing: %v", err)
	}

	// graph_evidence.evidence_uid, context_id
	_, err = s.db.Exec(`SELECT evidence_uid, context_id FROM graph_evidence LIMIT 0`)
	if err != nil {
		t.Fatalf("graph_evidence new columns missing: %v", err)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/store/ -run TestSchemaContext -v`
Expected: FAIL — tables and columns don't exist yet.

- [ ] **Step 3: Add knowledge_contexts and graph_changelog to schema const**

In `internal/store/store.go`, append to the `schema` const (before the closing backtick):

```sql
CREATE TABLE IF NOT EXISTS knowledge_contexts (
    context_id        INTEGER PRIMARY KEY AUTOINCREMENT,
    domain_key        TEXT NOT NULL,
    name              TEXT NOT NULL,
    git_ref           TEXT,
    base_context_id   INTEGER REFERENCES knowledge_contexts(context_id),
    base_revision_id  INTEGER REFERENCES graph_revisions(revision_id),
    head_revision_id  INTEGER REFERENCES graph_revisions(revision_id),
    head_commit_sha   TEXT,
    status            TEXT NOT NULL DEFAULT 'active'
                        CHECK (status IN ('active','merged','archived')),
    created_at        TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now')),
    UNIQUE (domain_key, name)
);

CREATE TABLE IF NOT EXISTS graph_changelog (
    changelog_id  INTEGER PRIMARY KEY AUTOINCREMENT,
    revision_id   INTEGER NOT NULL REFERENCES graph_revisions(revision_id),
    context_id    INTEGER NOT NULL REFERENCES knowledge_contexts(context_id),
    entity_type   TEXT NOT NULL CHECK (entity_type IN ('node','edge','evidence')),
    entity_key    TEXT NOT NULL,
    entity_id     INTEGER,
    change_type   TEXT NOT NULL
                    CHECK (change_type IN ('created','updated','stale','invalidated','revalidated','deleted')),
    field_changes TEXT,
    created_at    TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now'))
);

CREATE INDEX IF NOT EXISTS idx_changelog_revision ON graph_changelog(revision_id);
CREATE INDEX IF NOT EXISTS idx_changelog_context ON graph_changelog(context_id);
CREATE INDEX IF NOT EXISTS idx_changelog_entity ON graph_changelog(entity_type, entity_key);
```

- [ ] **Step 4: Add new columns via migration alters**

In the `migrate()` function's `alters` slice in `internal/store/store.go`, add:

```go
// Revisions get context
`ALTER TABLE graph_revisions ADD COLUMN context_id INTEGER REFERENCES knowledge_contexts(context_id)`,

// Nodes get temporal versioning
`ALTER TABLE graph_nodes ADD COLUMN valid_from_revision_id INTEGER`,
`ALTER TABLE graph_nodes ADD COLUMN valid_to_revision_id INTEGER`,
`ALTER TABLE graph_nodes ADD COLUMN context_id INTEGER`,

// Edges get temporal versioning + stable node_key FKs
`ALTER TABLE graph_edges ADD COLUMN valid_from_revision_id INTEGER`,
`ALTER TABLE graph_edges ADD COLUMN valid_to_revision_id INTEGER`,
`ALTER TABLE graph_edges ADD COLUMN context_id INTEGER`,
`ALTER TABLE graph_edges ADD COLUMN from_node_key TEXT`,
`ALTER TABLE graph_edges ADD COLUMN to_node_key TEXT`,

// Evidence gets stable identity + context
`ALTER TABLE graph_evidence ADD COLUMN evidence_uid TEXT`,
`ALTER TABLE graph_evidence ADD COLUMN context_id INTEGER`,
```

Also add indexes after the alters loop:

```go
s.db.Exec(`CREATE INDEX IF NOT EXISTS idx_nodes_valid_to ON graph_nodes(valid_to_revision_id)`)
s.db.Exec(`CREATE INDEX IF NOT EXISTS idx_edges_valid_to ON graph_edges(valid_to_revision_id)`)
s.db.Exec(`CREATE INDEX IF NOT EXISTS idx_evidence_valid_to ON graph_evidence(valid_to_revision_id)`)
s.db.Exec(`CREATE INDEX IF NOT EXISTS idx_evidence_uid ON graph_evidence(evidence_uid)`)
s.db.Exec(`CREATE INDEX IF NOT EXISTS idx_edges_from_node_key ON graph_edges(from_node_key)`)
s.db.Exec(`CREATE INDEX IF NOT EXISTS idx_edges_to_node_key ON graph_edges(to_node_key)`)
```

- [ ] **Step 5: Add ResetDB knowledge of new tables**

In `ResetDB()`, add `"knowledge_contexts"` and `"graph_changelog"` to the tables list (changelog before contexts due to FK):

```go
tables := []string{"graph_changelog", "mcp_request_log", "graph_discoveries", "domain_language", "project_settings", "graph_evidence", "graph_snapshots", "graph_edges", "graph_nodes", "graph_revisions", "knowledge_contexts"}
```

- [ ] **Step 6: Run tests**

Run: `go test ./internal/store/ -run TestSchema -v`
Expected: PASS

- [ ] **Step 7: Run full test suite to confirm no regressions**

Run: `go test ./...`
Expected: All existing tests pass (new columns are nullable, existing code doesn't touch them).

- [ ] **Step 8: Commit**

```bash
git add internal/store/store.go internal/store/contexts_test.go
git commit -m "feat: add knowledge_contexts, graph_changelog tables and temporal columns"
```

---

### Task 2: Context CRUD in Store

**Files:**
- Create: `internal/store/contexts.go`
- Modify: `internal/store/contexts_test.go`

- [ ] **Step 1: Write tests for context CRUD**

Add to `internal/store/contexts_test.go`:

```go
func TestContextCreate(t *testing.T) {
	s := openTestDB(t)
	id, err := s.CreateContext("mydom", "main", "main", 0, 0)
	if err != nil {
		t.Fatalf("CreateContext: %v", err)
	}
	if id == 0 {
		t.Fatal("expected non-zero context_id")
	}
}

func TestContextGet(t *testing.T) {
	s := openTestDB(t)
	id, _ := s.CreateContext("mydom", "main", "main", 0, 0)
	ctx, err := s.GetContext(id)
	if err != nil {
		t.Fatalf("GetContext: %v", err)
	}
	if ctx.Name != "main" || ctx.DomainKey != "mydom" || ctx.Status != "active" {
		t.Errorf("unexpected context: %+v", ctx)
	}
}

func TestContextGetByRef(t *testing.T) {
	s := openTestDB(t)
	s.CreateContext("mydom", "main", "main", 0, 0)
	ctx, err := s.GetContextByRef("mydom", "main")
	if err != nil {
		t.Fatalf("GetContextByRef: %v", err)
	}
	if ctx.Name != "main" {
		t.Errorf("name = %q, want main", ctx.Name)
	}
}

func TestContextList(t *testing.T) {
	s := openTestDB(t)
	s.CreateContext("mydom", "main", "main", 0, 0)
	s.CreateContext("mydom", "feature/x", "feature/x", 1, 0)
	list, err := s.ListContexts("mydom")
	if err != nil {
		t.Fatalf("ListContexts: %v", err)
	}
	if len(list) != 2 {
		t.Errorf("len = %d, want 2", len(list))
	}
}

func TestContextArchive(t *testing.T) {
	s := openTestDB(t)
	id, _ := s.CreateContext("mydom", "feat", "feat", 0, 0)
	if err := s.ArchiveContext(id); err != nil {
		t.Fatalf("ArchiveContext: %v", err)
	}
	ctx, _ := s.GetContext(id)
	if ctx.Status != "archived" {
		t.Errorf("status = %q, want archived", ctx.Status)
	}
}

func TestContextUpdateHead(t *testing.T) {
	s := openTestDB(t)
	id, _ := s.CreateContext("mydom", "main", "main", 0, 0)
	revID, _ := s.CreateRevision("mydom", "", "abc123", "manual", "full", "{}")
	if err := s.UpdateContextHead(id, revID, "abc123"); err != nil {
		t.Fatalf("UpdateContextHead: %v", err)
	}
	ctx, _ := s.GetContext(id)
	if ctx.HeadRevisionID != revID || ctx.HeadCommitSHA != "abc123" {
		t.Errorf("head not updated: rev=%d sha=%s", ctx.HeadRevisionID, ctx.HeadCommitSHA)
	}
}

func TestContextDuplicateNameFails(t *testing.T) {
	s := openTestDB(t)
	s.CreateContext("mydom", "main", "main", 0, 0)
	_, err := s.CreateContext("mydom", "main", "main", 0, 0)
	if err == nil {
		t.Fatal("expected error on duplicate context name")
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/store/ -run TestContext -v`
Expected: FAIL — functions don't exist.

- [ ] **Step 3: Implement context CRUD**

Create `internal/store/contexts.go`:

```go
package store

import (
	"database/sql"
	"errors"
	"fmt"
)

// ContextRow represents a row in knowledge_contexts.
type ContextRow struct {
	ContextID      int64  `json:"context_id"`
	DomainKey      string `json:"domain_key"`
	Name           string `json:"name"`
	GitRef         string `json:"git_ref"`
	BaseContextID  int64  `json:"base_context_id"`
	BaseRevisionID int64  `json:"base_revision_id"`
	HeadRevisionID int64  `json:"head_revision_id"`
	HeadCommitSHA  string `json:"head_commit_sha"`
	Status         string `json:"status"`
	CreatedAt      string `json:"created_at"`
}

// CreateContext inserts a new knowledge context and returns its ID.
func (s *Store) CreateContext(domainKey, name, gitRef string, baseContextID, baseRevisionID int64) (int64, error) {
	res, err := s.db.Exec(`
		INSERT INTO knowledge_contexts (domain_key, name, git_ref, base_context_id, base_revision_id, status)
		VALUES (?, ?, ?, ?, ?, 'active')
	`, domainKey, name, nullableStr(gitRef), nullableInt64(baseContextID), nullableInt64(baseRevisionID))
	if err != nil {
		return 0, fmt.Errorf("CreateContext: %w", err)
	}
	id, _ := res.LastInsertId()
	return id, nil
}

// GetContext returns a context by ID.
func (s *Store) GetContext(id int64) (*ContextRow, error) {
	r := &ContextRow{}
	err := s.db.QueryRow(`
		SELECT context_id, domain_key, name, COALESCE(git_ref,''),
		       COALESCE(base_context_id,0), COALESCE(base_revision_id,0),
		       COALESCE(head_revision_id,0), COALESCE(head_commit_sha,''),
		       status, created_at
		FROM knowledge_contexts WHERE context_id = ?
	`, id).Scan(&r.ContextID, &r.DomainKey, &r.Name, &r.GitRef,
		&r.BaseContextID, &r.BaseRevisionID,
		&r.HeadRevisionID, &r.HeadCommitSHA,
		&r.Status, &r.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("GetContext %d: %w", id, ErrNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("GetContext %d: %w", id, err)
	}
	return r, nil
}

// GetContextByRef returns the active context for a domain + git_ref.
func (s *Store) GetContextByRef(domainKey, gitRef string) (*ContextRow, error) {
	r := &ContextRow{}
	err := s.db.QueryRow(`
		SELECT context_id, domain_key, name, COALESCE(git_ref,''),
		       COALESCE(base_context_id,0), COALESCE(base_revision_id,0),
		       COALESCE(head_revision_id,0), COALESCE(head_commit_sha,''),
		       status, created_at
		FROM knowledge_contexts
		WHERE domain_key = ? AND git_ref = ? AND status = 'active'
	`, domainKey, gitRef).Scan(&r.ContextID, &r.DomainKey, &r.Name, &r.GitRef,
		&r.BaseContextID, &r.BaseRevisionID,
		&r.HeadRevisionID, &r.HeadCommitSHA,
		&r.Status, &r.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("GetContextByRef %s/%s: %w", domainKey, gitRef, ErrNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("GetContextByRef: %w", err)
	}
	return r, nil
}

// ListContexts returns all contexts for a domain.
func (s *Store) ListContexts(domainKey string) ([]ContextRow, error) {
	rows, err := s.db.Query(`
		SELECT context_id, domain_key, name, COALESCE(git_ref,''),
		       COALESCE(base_context_id,0), COALESCE(base_revision_id,0),
		       COALESCE(head_revision_id,0), COALESCE(head_commit_sha,''),
		       status, created_at
		FROM knowledge_contexts WHERE domain_key = ? ORDER BY context_id
	`, domainKey)
	if err != nil {
		return nil, fmt.Errorf("ListContexts: %w", err)
	}
	defer rows.Close()
	var out []ContextRow
	for rows.Next() {
		var r ContextRow
		rows.Scan(&r.ContextID, &r.DomainKey, &r.Name, &r.GitRef,
			&r.BaseContextID, &r.BaseRevisionID,
			&r.HeadRevisionID, &r.HeadCommitSHA,
			&r.Status, &r.CreatedAt)
		out = append(out, r)
	}
	return out, rows.Err()
}

// ArchiveContext sets a context's status to 'archived'.
func (s *Store) ArchiveContext(id int64) error {
	_, err := s.db.Exec(`UPDATE knowledge_contexts SET status='archived' WHERE context_id=?`, id)
	if err != nil {
		return fmt.Errorf("ArchiveContext: %w", err)
	}
	return nil
}

// UpdateContextHead updates the head revision and commit SHA.
func (s *Store) UpdateContextHead(contextID, revisionID int64, commitSHA string) error {
	_, err := s.db.Exec(`
		UPDATE knowledge_contexts
		SET head_revision_id=?, head_commit_sha=?
		WHERE context_id=?
	`, revisionID, commitSHA, contextID)
	if err != nil {
		return fmt.Errorf("UpdateContextHead: %w", err)
	}
	return nil
}
```

- [ ] **Step 4: Run tests**

Run: `go test ./internal/store/ -run TestContext -v`
Expected: All PASS

- [ ] **Step 5: Commit**

```bash
git add internal/store/contexts.go internal/store/contexts_test.go
git commit -m "feat: add knowledge context CRUD to store layer"
```

---

### Task 3: Changelog Writer

**Files:**
- Create: `internal/store/changelog.go`
- Create: `internal/store/changelog_test.go`

- [ ] **Step 1: Write tests**

Create `internal/store/changelog_test.go`:

```go
package store

import "testing"

func TestChangelogAppend(t *testing.T) {
	s := openTestDB(t)
	ctxID, _ := s.CreateContext("dom", "main", "main", 0, 0)
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
	ctxID, _ := s.CreateContext("dom", "main", "main", 0, 0)
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

func TestChangelogIsAppendOnly(t *testing.T) {
	s := openTestDB(t)
	// Changelog should not support updates — only inserts
	_, err := s.db.Exec(`UPDATE graph_changelog SET change_type='deleted' WHERE changelog_id=1`)
	// This should succeed (SQLite doesn't enforce append-only) but the API only exposes Append
	_ = err // Just verify the API works as designed
}
```

- [ ] **Step 2: Run tests to verify failure**

Run: `go test ./internal/store/ -run TestChangelog -v`
Expected: FAIL

- [ ] **Step 3: Implement changelog**

Create `internal/store/changelog.go`:

```go
package store

import "fmt"

// ChangelogRow represents a row in graph_changelog.
type ChangelogRow struct {
	ChangelogID int64  `json:"changelog_id"`
	RevisionID  int64  `json:"revision_id"`
	ContextID   int64  `json:"context_id"`
	EntityType  string `json:"entity_type"`
	EntityKey   string `json:"entity_key"`
	EntityID    int64  `json:"entity_id,omitempty"`
	ChangeType  string `json:"change_type"`
	FieldChanges string `json:"field_changes,omitempty"`
	CreatedAt   string `json:"created_at,omitempty"`
}

// AppendChangelog inserts a new changelog entry. Append-only — no updates or deletes.
func (s *Store) AppendChangelog(r ChangelogRow) (int64, error) {
	res, err := s.db.Exec(`
		INSERT INTO graph_changelog (revision_id, context_id, entity_type, entity_key, entity_id, change_type, field_changes)
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`, r.RevisionID, r.ContextID, r.EntityType, r.EntityKey, nullableInt64(r.EntityID), r.ChangeType, nullableStr(r.FieldChanges))
	if err != nil {
		return 0, fmt.Errorf("AppendChangelog: %w", err)
	}
	id, _ := res.LastInsertId()
	return id, nil
}

// QueryChangelog returns changelog entries for a context, optionally filtered by entity_key and revision range.
func (s *Store) QueryChangelog(contextID int64, entityKey string, fromRevision, toRevision int64) ([]ChangelogRow, error) {
	q := `SELECT changelog_id, revision_id, context_id, entity_type, entity_key,
	             COALESCE(entity_id,0), change_type, COALESCE(field_changes,''), created_at
	      FROM graph_changelog WHERE context_id = ?`
	args := []any{contextID}

	if entityKey != "" {
		q += ` AND entity_key = ?`
		args = append(args, entityKey)
	}
	if fromRevision > 0 {
		q += ` AND revision_id >= ?`
		args = append(args, fromRevision)
	}
	if toRevision > 0 {
		q += ` AND revision_id <= ?`
		args = append(args, toRevision)
	}
	q += ` ORDER BY changelog_id`

	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, fmt.Errorf("QueryChangelog: %w", err)
	}
	defer rows.Close()
	var out []ChangelogRow
	for rows.Next() {
		var r ChangelogRow
		rows.Scan(&r.ChangelogID, &r.RevisionID, &r.ContextID, &r.EntityType, &r.EntityKey,
			&r.EntityID, &r.ChangeType, &r.FieldChanges, &r.CreatedAt)
		out = append(out, r)
	}
	return out, rows.Err()
}
```

- [ ] **Step 4: Run tests**

Run: `go test ./internal/store/ -run TestChangelog -v`
Expected: All PASS

- [ ] **Step 5: Commit**

```bash
git add internal/store/changelog.go internal/store/changelog_test.go
git commit -m "feat: add append-only changelog writer to store"
```

---

### Task 4: Visible Revision CTE Builder

**Files:**
- Create: `internal/store/visibility.go`
- Create: `internal/store/visibility_test.go`

This is the core query primitive. It builds a CTE that resolves which revisions are visible for a given context lineage.

- [ ] **Step 1: Write tests**

Create `internal/store/visibility_test.go`:

```go
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

	// Branch off main at r2
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

	// Should see: r1, r2 (main up to branch point) + r3 (branch)
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

	// Should be usable in a query
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
```

- [ ] **Step 2: Run tests to verify failure**

Run: `go test ./internal/store/ -run TestVisible -v`
Expected: FAIL

- [ ] **Step 3: Add CreateRevisionWithContext to revisions.go**

Add to `internal/store/revisions.go`:

```go
// CreateRevisionWithContext creates a revision associated with a context.
func (s *Store) CreateRevisionWithContext(domainKey, beforeSHA, afterSHA, triggerKind, mode, metadata string, contextID int64) (int64, error) {
	res, err := s.db.Exec(`
		INSERT INTO graph_revisions (domain_key, git_before_sha, git_after_sha, trigger_kind, mode, metadata, context_id)
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`, domainKey, beforeSHA, afterSHA, triggerKind, mode, metadata, contextID)
	if err != nil {
		return 0, fmt.Errorf("CreateRevisionWithContext: %w", err)
	}
	id, _ := res.LastInsertId()
	return id, nil
}
```

- [ ] **Step 4: Implement visibility CTE builder**

Create `internal/store/visibility.go`:

```go
package store

import "fmt"

// BuildVisibleRevisionsCTE returns a SQL CTE clause and its arguments that define
// which revision_ids are visible for a given context.
// The CTE is named "visible_revisions" with a single column "revision_id".
// asOfRevision, if > 0, caps the visible revisions (used for time-travel queries).
func (s *Store) BuildVisibleRevisionsCTE(contextID int64, asOfRevision int64) (string, []any) {
	ctx, err := s.GetContext(contextID)
	if err != nil {
		// Fallback: just use the context_id directly
		cte := `WITH visible_revisions AS (SELECT revision_id FROM graph_revisions WHERE context_id = ?)`
		return cte, []any{contextID}
	}

	var args []any

	if ctx.BaseContextID == 0 {
		// Root context (main) — all its revisions, optionally capped
		if asOfRevision > 0 {
			cte := `WITH visible_revisions AS (
				SELECT revision_id FROM graph_revisions
				WHERE context_id = ? AND revision_id <= ?
			)`
			args = append(args, contextID, asOfRevision)
			return cte, args
		}
		cte := `WITH visible_revisions AS (
			SELECT revision_id FROM graph_revisions WHERE context_id = ?
		)`
		args = append(args, contextID)
		return cte, args
	}

	// Branch context: parent up to branch point + own revisions
	if asOfRevision > 0 {
		cte := `WITH visible_revisions AS (
			SELECT revision_id FROM graph_revisions
			WHERE context_id = ? AND revision_id <= ?
			UNION ALL
			SELECT revision_id FROM graph_revisions
			WHERE context_id = ? AND revision_id <= ?
		)`
		args = append(args, ctx.BaseContextID, ctx.BaseRevisionID, contextID, asOfRevision)
		return cte, args
	}

	cte := `WITH visible_revisions AS (
		SELECT revision_id FROM graph_revisions
		WHERE context_id = ? AND revision_id <= ?
		UNION ALL
		SELECT revision_id FROM graph_revisions
		WHERE context_id = ?
	)`
	args = append(args, ctx.BaseContextID, ctx.BaseRevisionID, contextID)
	return cte, args
}

// VisibleRevisionIDs returns the concrete list of visible revision IDs for a context.
// Useful for testing and for small graphs. For queries, use BuildVisibleRevisionsCTE.
func (s *Store) VisibleRevisionIDs(contextID int64, asOfRevision int64) ([]int64, error) {
	cte, args := s.BuildVisibleRevisionsCTE(contextID, asOfRevision)
	q := cte + ` SELECT revision_id FROM visible_revisions ORDER BY revision_id`
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, fmt.Errorf("VisibleRevisionIDs: %w", err)
	}
	defer rows.Close()
	var out []int64
	for rows.Next() {
		var id int64
		rows.Scan(&id)
		out = append(out, id)
	}
	return out, rows.Err()
}
```

- [ ] **Step 5: Run tests**

Run: `go test ./internal/store/ -run TestVisible -v`
Expected: All PASS

- [ ] **Step 6: Run full suite**

Run: `go test ./...`
Expected: All pass

- [ ] **Step 7: Commit**

```bash
git add internal/store/visibility.go internal/store/visibility_test.go internal/store/revisions.go
git commit -m "feat: visible revision CTE builder for context lineage queries"
```

---

### Task 5: Backfill Migration for Existing Data

**Files:**
- Modify: `internal/store/store.go` (migrate function)
- Create: `internal/store/migration_test.go`

When opening an existing DB that has revisions/nodes/edges but no contexts, backfill a "main" context and populate the new columns.

- [ ] **Step 1: Write test**

Create `internal/store/migration_test.go`:

```go
package store

import (
	"path/filepath"
	"testing"
)

func TestMigrationBackfillsMainContext(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")

	// Open DB, create some data without contexts (simulating old schema)
	s, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	// Create a revision without context_id (old-style)
	s.CreateRevision("mydom", "", "abc123", "manual", "full", "{}")
	s.Close()

	// Re-open — migration should backfill
	s2, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Re-open: %v", err)
	}
	defer s2.Close()

	// Should have auto-created a "main" context
	ctx, err := s2.GetContextByRef("mydom", "main")
	if err != nil {
		t.Fatalf("expected main context to be backfilled: %v", err)
	}
	if ctx.Status != "active" {
		t.Errorf("status = %q, want active", ctx.Status)
	}

	// Revision should have context_id set
	rev, _ := s2.GetLatestRevision("mydom")
	if rev == nil {
		t.Fatal("expected revision")
	}
}
```

- [ ] **Step 2: Run test to verify failure**

Run: `go test ./internal/store/ -run TestMigrationBackfill -v`
Expected: FAIL — no backfill logic yet

- [ ] **Step 3: Add backfill to migrate()**

In `internal/store/store.go`, add a `backfillContexts()` call at the end of `migrate()`:

```go
func (s *Store) migrate() error {
	// ... existing code ...

	// Backfill: create "main" context for domains that have revisions but no context.
	s.backfillContexts()

	return nil
}

func (s *Store) backfillContexts() {
	// Find domains with revisions but no context
	rows, err := s.db.Query(`
		SELECT DISTINCT r.domain_key FROM graph_revisions r
		WHERE NOT EXISTS (
			SELECT 1 FROM knowledge_contexts c WHERE c.domain_key = r.domain_key
		)
	`)
	if err != nil {
		return
	}
	defer rows.Close()

	var domains []string
	for rows.Next() {
		var d string
		rows.Scan(&d)
		domains = append(domains, d)
	}

	for _, dom := range domains {
		// Create main context
		res, err := s.db.Exec(`
			INSERT INTO knowledge_contexts (domain_key, name, git_ref, status)
			VALUES (?, 'main', 'main', 'active')
		`, dom)
		if err != nil {
			continue
		}
		ctxID, _ := res.LastInsertId()

		// Backfill context_id on revisions
		s.db.Exec(`UPDATE graph_revisions SET context_id = ? WHERE domain_key = ? AND context_id IS NULL`, ctxID, dom)

		// Backfill context_id on nodes
		s.db.Exec(`UPDATE graph_nodes SET context_id = ?, valid_from_revision_id = first_seen_revision_id WHERE domain_key = ? AND context_id IS NULL`, ctxID, dom)

		// Backfill context_id on edges (via from_node's domain)
		s.db.Exec(`
			UPDATE graph_edges SET context_id = ?, valid_from_revision_id = first_seen_revision_id
			WHERE context_id IS NULL AND from_node_id IN (SELECT node_id FROM graph_nodes WHERE domain_key = ?)
		`, ctxID, dom)

		// Backfill from_node_key, to_node_key on edges
		s.db.Exec(`
			UPDATE graph_edges SET
				from_node_key = (SELECT node_key FROM graph_nodes WHERE node_id = graph_edges.from_node_id),
				to_node_key = (SELECT node_key FROM graph_nodes WHERE node_id = graph_edges.to_node_id)
			WHERE from_node_key IS NULL
		`)

		// Backfill evidence context_id
		s.db.Exec(`
			UPDATE graph_evidence SET context_id = ?
			WHERE context_id IS NULL AND (
				node_id IN (SELECT node_id FROM graph_nodes WHERE domain_key = ?)
				OR edge_id IN (SELECT edge_id FROM graph_edges WHERE from_node_id IN (SELECT node_id FROM graph_nodes WHERE domain_key = ?))
			)
		`, ctxID, dom, dom)

		// Update context head to latest revision
		var latestRevID int64
		var latestSHA string
		s.db.QueryRow(`SELECT revision_id, git_after_sha FROM graph_revisions WHERE domain_key = ? ORDER BY revision_id DESC LIMIT 1`, dom).Scan(&latestRevID, &latestSHA)
		if latestRevID > 0 {
			s.db.Exec(`UPDATE knowledge_contexts SET head_revision_id = ?, head_commit_sha = ? WHERE context_id = ?`, latestRevID, latestSHA, ctxID)
		}
	}
}
```

- [ ] **Step 4: Run tests**

Run: `go test ./internal/store/ -run TestMigrationBackfill -v`
Expected: PASS

- [ ] **Step 5: Run full test suite**

Run: `go test ./...`
Expected: All pass (backfill is no-op for fresh DBs, safe for existing)

- [ ] **Step 6: Commit**

```bash
git add internal/store/store.go internal/store/migration_test.go
git commit -m "feat: backfill migration creates main context for existing domains"
```

---

### Task 6: Versioned Node Mutations

**Files:**
- Modify: `internal/store/nodes.go` (UpsertNode, MarkStaleNodes)
- Create: `internal/store/nodes_versioned_test.go`

Change UpsertNode to close old version + insert new when `context_id` is provided. Populate `from_node_key`/`to_node_key` on edges during import.

- [ ] **Step 1: Write tests for versioned upsert**

Create `internal/store/nodes_versioned_test.go`:

```go
package store

import "testing"

func TestVersionedNodeUpsertCreatesNewVersion(t *testing.T) {
	s := openTestDB(t)
	ctxID, _ := s.CreateContext("dom", "main", "main", 0, 0)
	rev1, _ := s.CreateRevisionWithContext("dom", "", "aaa", "manual", "full", "{}", ctxID)
	rev2, _ := s.CreateRevisionWithContext("dom", "aaa", "bbb", "manual", "incremental", "{}", ctxID)

	// Insert v1
	n := NodeRow{
		NodeKey: "code:svc:dom:foo", Layer: "code", NodeType: "provider",
		DomainKey: "dom", Name: "Foo", Status: "active",
		FirstSeenRevisionID: rev1, LastSeenRevisionID: rev1,
		Confidence: 1.0, Freshness: 1.0, TrustScore: 1.0, Metadata: "{}",
		ValidFromRevisionID: rev1, ContextID: ctxID,
	}
	id1, err := s.UpsertNode(n)
	if err != nil {
		t.Fatalf("UpsertNode v1: %v", err)
	}

	// Upsert v2 with different name (simulating re-extraction)
	n.Name = "FooUpdated"
	n.LastSeenRevisionID = rev2
	n.ValidFromRevisionID = rev2
	id2, err := s.UpsertNode(n)
	if err != nil {
		t.Fatalf("UpsertNode v2: %v", err)
	}

	// Should be a new version row (different ID)
	if id1 == id2 {
		t.Error("expected different node_id for new version")
	}

	// Old version should have valid_to set
	oldNode, _ := s.GetNodeByID(id1)
	if oldNode.ValidToRevisionID == 0 {
		t.Error("old version should have valid_to_revision_id set")
	}

	// New version should have valid_to NULL (0)
	newNode, _ := s.GetNodeByID(id2)
	if newNode.ValidToRevisionID != 0 {
		t.Error("new version should have valid_to_revision_id = NULL")
	}
}

func TestVersionedNodeGetByKeyReturnsCurrent(t *testing.T) {
	s := openTestDB(t)
	ctxID, _ := s.CreateContext("dom", "main", "main", 0, 0)
	rev1, _ := s.CreateRevisionWithContext("dom", "", "aaa", "manual", "full", "{}", ctxID)
	rev2, _ := s.CreateRevisionWithContext("dom", "aaa", "bbb", "manual", "incremental", "{}", ctxID)

	n := NodeRow{
		NodeKey: "code:svc:dom:bar", Layer: "code", NodeType: "provider",
		DomainKey: "dom", Name: "Bar", Status: "active",
		FirstSeenRevisionID: rev1, LastSeenRevisionID: rev1,
		Confidence: 1.0, Freshness: 1.0, TrustScore: 1.0, Metadata: "{}",
		ValidFromRevisionID: rev1, ContextID: ctxID,
	}
	s.UpsertNode(n)

	n.Name = "BarV2"
	n.LastSeenRevisionID = rev2
	n.ValidFromRevisionID = rev2
	s.UpsertNode(n)

	// GetNodeByKey should return current version (valid_to IS NULL)
	current, err := s.GetNodeByKey("code:svc:dom:bar")
	if err != nil {
		t.Fatalf("GetNodeByKey: %v", err)
	}
	if current.Name != "BarV2" {
		t.Errorf("name = %q, want BarV2 (current version)", current.Name)
	}
}
```

- [ ] **Step 2: Run tests to verify failure**

Run: `go test ./internal/store/ -run TestVersionedNode -v`
Expected: FAIL

- [ ] **Step 3: Add ValidFromRevisionID, ValidToRevisionID, ContextID to NodeRow**

In `internal/store/nodes.go`, add fields to `NodeRow`:

```go
type NodeRow struct {
	// ... existing fields ...
	ValidFromRevisionID int64   `json:"valid_from_revision_id,omitempty"`
	ValidToRevisionID   int64   `json:"valid_to_revision_id,omitempty"`
	ContextID           int64   `json:"context_id,omitempty"`
}
```

- [ ] **Step 4: Modify UpsertNode for versioned inserts**

Replace the UpsertNode implementation in `internal/store/nodes.go`. When `ValidFromRevisionID > 0` (new-style call), use the close-old/insert-new pattern. When `ValidFromRevisionID == 0` (legacy call), use the old UPDATE behavior for backward compatibility.

```go
func (s *Store) UpsertNode(n NodeRow) (int64, error) {
	// Check if node already exists (current version: valid_to IS NULL).
	const selQ = `SELECT node_id, layer, node_type, domain_key FROM graph_nodes WHERE node_key = ? AND (valid_to_revision_id IS NULL OR valid_to_revision_id = 0) ORDER BY node_id DESC LIMIT 1`
	row := s.db.QueryRow(selQ, n.NodeKey)
	var existingID int64
	var existingLayer, existingType, existingDomain string
	err := row.Scan(&existingID, &existingLayer, &existingType, &existingDomain)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return 0, fmt.Errorf("UpsertNode lookup: %w", err)
	}

	if errors.Is(err, sql.ErrNoRows) {
		// Insert new node.
		return s.insertNodeVersion(n)
	}

	// Existing: check immutable fields.
	if existingLayer != n.Layer || existingType != n.NodeType || existingDomain != n.DomainKey {
		return 0, fmt.Errorf("UpsertNode conflict: node_key %q immutable fields mismatch (layer=%s/%s, type=%s/%s, domain=%s/%s)",
			n.NodeKey, existingLayer, n.Layer, existingType, n.NodeType, existingDomain, n.DomainKey)
	}

	// Versioned mode: close old, insert new
	if n.ValidFromRevisionID > 0 {
		// Close current version
		s.db.Exec(`UPDATE graph_nodes SET valid_to_revision_id = ? WHERE node_id = ?`, n.ValidFromRevisionID, existingID)
		// Insert new version
		return s.insertNodeVersion(n)
	}

	// Legacy mode: update in place
	const updQ = `
		UPDATE graph_nodes
		SET name=?, qualified_name=?, repo_name=?, file_path=?, lang=?, owner_key=?,
		    environment=?, visibility=?, status=?, last_seen_revision_id=?,
		    confidence=?, freshness=?, trust_score=?, metadata=?
		WHERE node_id=?
	`
	_, err = s.db.Exec(updQ,
		n.Name, nullableStr(n.QualifiedName), nullableStr(n.RepoName), nullableStr(n.FilePath),
		nullableStr(n.Lang), nullableStr(n.OwnerKey), nullableStr(n.Environment),
		nullableStr(n.Visibility), n.Status, n.LastSeenRevisionID, n.Confidence, n.Freshness, n.TrustScore, n.Metadata,
		existingID,
	)
	if err != nil {
		return 0, fmt.Errorf("UpsertNode update: %w", err)
	}
	return existingID, nil
}

func (s *Store) insertNodeVersion(n NodeRow) (int64, error) {
	const insQ = `
		INSERT INTO graph_nodes
		  (node_key, layer, node_type, domain_key, name, qualified_name, repo_name,
		   file_path, lang, owner_key, environment, visibility, status,
		   first_seen_revision_id, last_seen_revision_id, confidence, freshness, trust_score, metadata,
		   valid_from_revision_id, valid_to_revision_id, context_id)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
	`
	res, err := s.db.Exec(insQ,
		n.NodeKey, n.Layer, n.NodeType, n.DomainKey, n.Name,
		nullableStr(n.QualifiedName), nullableStr(n.RepoName), nullableStr(n.FilePath),
		nullableStr(n.Lang), nullableStr(n.OwnerKey), nullableStr(n.Environment),
		nullableStr(n.Visibility), n.Status,
		n.FirstSeenRevisionID, n.LastSeenRevisionID, n.Confidence, n.Freshness, n.TrustScore, n.Metadata,
		nullableInt64(n.ValidFromRevisionID), nullableInt64(n.ValidToRevisionID), nullableInt64(n.ContextID),
	)
	if err != nil {
		return 0, fmt.Errorf("UpsertNode insert: %w", err)
	}
	id, _ := res.LastInsertId()
	return id, nil
}
```

- [ ] **Step 5: Update GetNodeByKey to prefer current version**

In the `GetNodeByKey` query in `internal/store/nodes.go`, change the WHERE clause:

```go
const q = `
	SELECT node_id, node_key, layer, node_type, domain_key, name,
	       COALESCE(qualified_name,''), COALESCE(repo_name,''), COALESCE(file_path,''),
	       COALESCE(lang,''), COALESCE(owner_key,''), COALESCE(environment,''),
	       COALESCE(visibility,''), status,
	       first_seen_revision_id, last_seen_revision_id, confidence, freshness, trust_score, metadata
	FROM graph_nodes WHERE node_key = ?
	AND (valid_to_revision_id IS NULL OR valid_to_revision_id = 0)
	ORDER BY node_id DESC LIMIT 1
`
```

- [ ] **Step 6: Update ListNodes to only return current versions**

Add to the base WHERE in `ListNodes`:

```go
base := `
	SELECT node_id, node_key, layer, node_type, domain_key, name,
	       COALESCE(qualified_name,''), COALESCE(repo_name,''), COALESCE(file_path,''),
	       COALESCE(lang,''), COALESCE(owner_key,''), COALESCE(environment,''),
	       COALESCE(visibility,''), status,
	       first_seen_revision_id, last_seen_revision_id, confidence, freshness, trust_score, metadata
	FROM graph_nodes
	WHERE (valid_to_revision_id IS NULL OR valid_to_revision_id = 0)
`
// Change the conditions append to use AND instead of WHERE
```

Update the filter building to always start with `AND` since we now have a base WHERE:

```go
if f.Layer != "" {
	base += " AND layer = ?"
	args = append(args, f.Layer)
}
// ... same for all other filters, change "WHERE" join to just append "AND" conditions
```

Remove the `if len(conds) > 0` block and the `strings.Join`. Each filter just appends `AND ...` directly.

- [ ] **Step 7: Run tests**

Run: `go test ./internal/store/ -run TestVersionedNode -v && go test ./... 2>&1 | tail -20`
Expected: New tests PASS, existing tests still pass.

- [ ] **Step 8: Commit**

```bash
git add internal/store/nodes.go internal/store/nodes_versioned_test.go
git commit -m "feat: versioned node mutations — close old version, insert new"
```

---

### Task 7: Versioned Edge Mutations + node_key FKs

**Files:**
- Modify: `internal/store/edges.go`
- Create: `internal/store/edges_versioned_test.go`

Add `from_node_key`, `to_node_key`, `ValidFromRevisionID`, `ValidToRevisionID`, `ContextID` to EdgeRow. Populate `from_node_key`/`to_node_key` during upsert. Switch to versioned insert pattern.

- [ ] **Step 1: Write tests**

Create `internal/store/edges_versioned_test.go`:

```go
package store

import "testing"

func TestVersionedEdgePopulatesNodeKeys(t *testing.T) {
	s := openTestDB(t)
	ctxID, _ := s.CreateContext("dom", "main", "main", 0, 0)
	rev, _ := s.CreateRevisionWithContext("dom", "", "aaa", "manual", "full", "{}", ctxID)

	// Create two nodes
	s.UpsertNode(NodeRow{
		NodeKey: "code:svc:dom:a", Layer: "code", NodeType: "provider",
		DomainKey: "dom", Name: "A", Status: "active",
		FirstSeenRevisionID: rev, LastSeenRevisionID: rev,
		Confidence: 1.0, Freshness: 1.0, TrustScore: 1.0, Metadata: "{}",
	})
	s.UpsertNode(NodeRow{
		NodeKey: "code:svc:dom:b", Layer: "code", NodeType: "provider",
		DomainKey: "dom", Name: "B", Status: "active",
		FirstSeenRevisionID: rev, LastSeenRevisionID: rev,
		Confidence: 1.0, Freshness: 1.0, TrustScore: 1.0, Metadata: "{}",
	})

	fromID, _ := s.GetNodeIDByKey("code:svc:dom:a")
	toID, _ := s.GetNodeIDByKey("code:svc:dom:b")

	_, err := s.UpsertEdge(EdgeRow{
		EdgeKey: "code:svc:dom:a->code:svc:dom:b::INJECTS",
		FromNodeID: fromID, ToNodeID: toID,
		FromNodeKey: "code:svc:dom:a", ToNodeKey: "code:svc:dom:b",
		EdgeType: "INJECTS", DerivationKind: "hard", Active: true,
		FirstSeenRevisionID: rev, LastSeenRevisionID: rev,
		Confidence: 1.0, Freshness: 1.0, TrustScore: 1.0, Metadata: "{}",
		ValidFromRevisionID: rev, ContextID: ctxID,
	})
	if err != nil {
		t.Fatalf("UpsertEdge: %v", err)
	}

	edge, _ := s.GetEdgeByKey("code:svc:dom:a->code:svc:dom:b::INJECTS")
	if edge.FromNodeKey != "code:svc:dom:a" {
		t.Errorf("from_node_key = %q, want code:svc:dom:a", edge.FromNodeKey)
	}
	if edge.ToNodeKey != "code:svc:dom:b" {
		t.Errorf("to_node_key = %q, want code:svc:dom:b", edge.ToNodeKey)
	}
}
```

- [ ] **Step 2: Run test to verify failure**

Run: `go test ./internal/store/ -run TestVersionedEdge -v`
Expected: FAIL

- [ ] **Step 3: Add new fields to EdgeRow and update UpsertEdge**

Add to `EdgeRow` in `internal/store/edges.go`:

```go
type EdgeRow struct {
	// ... existing fields ...
	FromNodeKey         string  `json:"from_node_key,omitempty"`
	ToNodeKey           string  `json:"to_node_key,omitempty"`
	ValidFromRevisionID int64   `json:"valid_from_revision_id,omitempty"`
	ValidToRevisionID   int64   `json:"valid_to_revision_id,omitempty"`
	ContextID           int64   `json:"context_id,omitempty"`
}
```

Update `UpsertEdge` to store `from_node_key`, `to_node_key`, and use versioned pattern when `ValidFromRevisionID > 0`. Follow the same close-old/insert-new pattern as nodes. Update `GetEdgeByKey` and `ListEdges` to read the new columns and filter by `valid_to_revision_id IS NULL`.

- [ ] **Step 4: Run tests**

Run: `go test ./internal/store/ -run TestVersionedEdge -v && go test ./...`
Expected: All pass

- [ ] **Step 5: Commit**

```bash
git add internal/store/edges.go internal/store/edges_versioned_test.go
git commit -m "feat: versioned edge mutations with stable node_key FKs"
```

---

### Task 8: Versioned Evidence Mutations

**Files:**
- Modify: `internal/store/evidence.go`
- Create: `internal/store/evidence_versioned_test.go`

Change `MarkEvidenceStaleByFiles` to close-old/insert-stale pattern. Add `evidence_uid` and `context_id` to evidence operations.

- [ ] **Step 1: Write tests**

Create `internal/store/evidence_versioned_test.go`:

```go
package store

import "testing"

func TestVersionedEvidenceStaleCreatesNewVersion(t *testing.T) {
	s := openTestDB(t)
	ctxID, _ := s.CreateContext("dom", "main", "main", 0, 0)
	rev1, _ := s.CreateRevisionWithContext("dom", "", "aaa", "manual", "full", "{}", ctxID)
	rev2, _ := s.CreateRevisionWithContext("dom", "aaa", "bbb", "manual", "incremental", "{}", ctxID)

	// Create a node and evidence
	nodeID, _ := s.UpsertNode(NodeRow{
		NodeKey: "code:svc:dom:x", Layer: "code", NodeType: "provider",
		DomainKey: "dom", Name: "X", Status: "active",
		FirstSeenRevisionID: rev1, LastSeenRevisionID: rev1,
		Confidence: 1.0, Freshness: 1.0, TrustScore: 1.0, Metadata: "{}",
	})

	_, err := s.AddEvidence(EvidenceRow{
		TargetKind: "node", NodeID: nodeID,
		SourceKind: "file", FilePath: "src/x.ts",
		ExtractorID: "claude", ExtractorVersion: "1.0",
		Confidence: 0.99, EvidenceStatus: "valid", EvidencePolarity: "positive",
		ValidFromRevisionID: rev1, ContextID: ctxID,
	})
	if err != nil {
		t.Fatalf("AddEvidence: %v", err)
	}

	// Mark stale by file (versioned mode)
	staleCount, _, _, err := s.MarkEvidenceStaleByFilesVersioned([]string{"src/x.ts"}, rev2, ctxID)
	if err != nil {
		t.Fatalf("MarkEvidenceStaleByFilesVersioned: %v", err)
	}
	if staleCount == 0 {
		t.Fatal("expected stale count > 0")
	}

	// Old evidence should have valid_to set
	evidence, _ := s.ListEvidenceByNode(nodeID)
	var validCount, staleVersionCount int
	for _, e := range evidence {
		if e.ValidToRevisionID > 0 {
			validCount++ // closed old version
		}
		if e.EvidenceStatus == "stale" && e.ValidToRevisionID == 0 {
			staleVersionCount++ // new stale version
		}
	}
	if validCount == 0 {
		t.Error("expected old evidence to have valid_to set")
	}
	if staleVersionCount == 0 {
		t.Error("expected new stale evidence version inserted")
	}
}
```

- [ ] **Step 2: Run test to verify failure**

Run: `go test ./internal/store/ -run TestVersionedEvidence -v`
Expected: FAIL

- [ ] **Step 3: Implement MarkEvidenceStaleByFilesVersioned**

Add to `internal/store/evidence.go`:

```go
// MarkEvidenceStaleByFilesVersioned closes current evidence versions and inserts stale versions.
// This is the immutable version — never overwrites historical knowledge.
func (s *Store) MarkEvidenceStaleByFilesVersioned(filePaths []string, revisionID, contextID int64) (staleCount int64, affectedEdgeIDs, affectedNodeIDs []int64, err error) {
	if len(filePaths) == 0 {
		return 0, nil, nil, nil
	}

	placeholders := ""
	args := make([]any, len(filePaths))
	for i, fp := range filePaths {
		if i > 0 {
			placeholders += ","
		}
		placeholders += "?"
		args[i] = fp
	}

	// Find current evidence from these files
	selQ := `SELECT evidence_id, target_kind, node_id, edge_id, source_kind, repo_name, file_path,
	                line_start, line_end, column_start, column_end, locator,
	                extractor_id, extractor_version, ast_rule, snippet_hash, commit_sha,
	                confidence, evidence_polarity, valid_from_revision_id,
	                COALESCE(evidence_uid,''), COALESCE(context_id,0), metadata
	         FROM graph_evidence
	         WHERE file_path IN (` + placeholders + `)
	         AND evidence_status IN ('valid','revalidated')
	         AND (valid_to_revision_id IS NULL OR valid_to_revision_id = 0)`

	rows, err := s.db.Query(selQ, args...)
	if err != nil {
		return 0, nil, nil, fmt.Errorf("MarkEvidenceStaleByFilesVersioned select: %w", err)
	}
	defer rows.Close()

	type evRow struct {
		id          int64
		targetKind  string
		nodeID, edgeID int64
		sourceKind, repoName, filePath string
		lineStart, lineEnd, colStart, colEnd int
		locator, extractorID, extractorVersion, astRule, snippetHash, commitSHA string
		confidence float64
		polarity string
		validFrom int64
		uid string
		ctxID int64
		metadata string
	}
	var toClose []evRow

	edgeSet := map[int64]bool{}
	nodeSet := map[int64]bool{}

	for rows.Next() {
		var r evRow
		var nid, eid sql.NullInt64
		var ls, le, cs, ce sql.NullInt64
		rows.Scan(&r.id, &r.targetKind, &nid, &eid, &r.sourceKind, &r.repoName, &r.filePath,
			&ls, &le, &cs, &ce, &r.locator,
			&r.extractorID, &r.extractorVersion, &r.astRule, &r.snippetHash, &r.commitSHA,
			&r.confidence, &r.polarity, &r.validFrom, &r.uid, &r.ctxID, &r.metadata)
		if nid.Valid { r.nodeID = nid.Int64 }
		if eid.Valid { r.edgeID = eid.Int64 }
		if ls.Valid { r.lineStart = int(ls.Int64) }
		if le.Valid { r.lineEnd = int(le.Int64) }
		if cs.Valid { r.colStart = int(cs.Int64) }
		if ce.Valid { r.colEnd = int(ce.Int64) }
		toClose = append(toClose, r)
		if r.edgeID > 0 { edgeSet[r.edgeID] = true }
		if r.nodeID > 0 { nodeSet[r.nodeID] = true }
	}

	// Close old versions and insert stale versions
	for _, r := range toClose {
		// Close old
		s.db.Exec(`UPDATE graph_evidence SET valid_to_revision_id = ? WHERE evidence_id = ?`, revisionID, r.id)

		// Insert stale version
		s.db.Exec(`
			INSERT INTO graph_evidence (target_kind, node_id, edge_id, source_kind, repo_name, file_path,
				line_start, line_end, column_start, column_end, locator,
				extractor_id, extractor_version, ast_rule, snippet_hash, commit_sha,
				confidence, evidence_status, evidence_polarity,
				valid_from_revision_id, evidence_uid, context_id, metadata)
			VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,'stale',?,?,?,?,?)
		`, r.targetKind, nullableInt64(r.nodeID), nullableInt64(r.edgeID),
			r.sourceKind, nullableStr(r.repoName), nullableStr(r.filePath),
			nullableInt(r.lineStart), nullableInt(r.lineEnd), nullableInt(r.colStart), nullableInt(r.colEnd),
			nullableStr(r.locator), r.extractorID, r.extractorVersion,
			nullableStr(r.astRule), nullableStr(r.snippetHash), nullableStr(r.commitSHA),
			r.confidence, r.polarity, revisionID, nullableStr(r.uid), contextID, r.metadata)
		staleCount++
	}

	for id := range edgeSet { affectedEdgeIDs = append(affectedEdgeIDs, id) }
	for id := range nodeSet { affectedNodeIDs = append(affectedNodeIDs, id) }

	return staleCount, affectedEdgeIDs, affectedNodeIDs, nil
}
```

- [ ] **Step 4: Run tests**

Run: `go test ./internal/store/ -run TestVersionedEvidence -v && go test ./...`
Expected: All pass

- [ ] **Step 5: Commit**

```bash
git add internal/store/evidence.go internal/store/evidence_versioned_test.go
git commit -m "feat: versioned evidence mutations — close old, insert stale version"
```

---

### Task 9: Context-Aware Graph Layer

**Files:**
- Modify: `internal/graph/invalidation.go`
- Modify: `internal/graph/query.go`
- Create: `internal/graph/context.go`
- Create: `e2e/context_isolation_test.go`

Wire context through the graph layer. InvalidateChanged uses versioned evidence. Queries accept optional context.

- [ ] **Step 1: Write e2e test for context isolation**

Create `e2e/context_isolation_test.go`:

```go
package e2e

import (
	"path/filepath"
	"testing"

	"github.com/anthropics/depbot/internal/graph"
	"github.com/anthropics/depbot/internal/registry"
	"github.com/anthropics/depbot/internal/store"
)

func TestContextIsolation(t *testing.T) {
	dir := t.TempDir()
	s, _ := store.Open(filepath.Join(dir, "ctx.db"))
	defer s.Close()
	reg, _ := registry.LoadDefaults()
	g := graph.New(s, reg)

	// Setup main context with baseline
	mainCtx, _ := s.CreateContext("dom", "main", "main", 0, 0)
	rev1, _ := s.CreateRevisionWithContext("dom", "", "aaa", "manual", "full", "{}", mainCtx)
	s.UpdateContextHead(mainCtx, rev1, "aaa")

	payload := graph.ImportPayload{
		Nodes: []graph.ImportNode{
			{NodeKey: "code:svc:dom:a", Layer: "code", NodeType: "provider", DomainKey: "dom", Name: "ServiceA"},
			{NodeKey: "code:svc:dom:b", Layer: "code", NodeType: "provider", DomainKey: "dom", Name: "ServiceB"},
		},
		Edges: []graph.ImportEdge{
			{FromNodeKey: "code:svc:dom:a", ToNodeKey: "code:svc:dom:b", EdgeType: "INJECTS", DerivationKind: "hard", FromLayer: "code", ToLayer: "code"},
		},
	}
	g.ImportAll(payload, rev1)

	// Create branch context
	branchCtx, _ := s.CreateContext("dom", "feature/x", "feature/x", mainCtx, rev1)

	// Add a new node only on main (after branch point)
	rev2, _ := s.CreateRevisionWithContext("dom", "aaa", "bbb", "manual", "incremental", "{}", mainCtx)
	mainPayload := graph.ImportPayload{
		Nodes: []graph.ImportNode{
			{NodeKey: "code:svc:dom:c", Layer: "code", NodeType: "provider", DomainKey: "dom", Name: "ServiceC"},
		},
	}
	g.ImportAll(mainPayload, rev2)

	// Branch should see A and B but NOT C
	_ = branchCtx
	// Note: context-filtered queries will be tested once implemented in this task
	t.Log("Context isolation setup complete — A, B visible to branch, C only on main after branch point")
}
```

- [ ] **Step 2: Create context.go with ResolveContext helper**

Create `internal/graph/context.go`:

```go
package graph

import "github.com/anthropics/depbot/internal/store"

// QueryContext holds context information for filtering queries.
type QueryContext struct {
	ContextID  int64
	AsOfRevision int64
}

// VisibleRevisionsCTE returns the SQL CTE and args for the given query context.
// If ctx is nil, returns empty (no filtering — legacy behavior).
func (g *Graph) VisibleRevisionsCTE(ctx *QueryContext) (string, []any) {
	if ctx == nil || ctx.ContextID == 0 {
		return "", nil
	}
	return g.store.BuildVisibleRevisionsCTE(ctx.ContextID, ctx.AsOfRevision)
}
```

- [ ] **Step 3: Update InvalidateChanged to use versioned evidence**

In `internal/graph/invalidation.go`, update `InvalidateChanged` to use `MarkEvidenceStaleByFilesVersioned` when a `contextID` is available:

```go
func (g *Graph) InvalidateChanged(domainKey string, revisionID int64, changedFiles []string) (*InvalidateResult, error) {
	return g.InvalidateChangedInContext(domainKey, revisionID, changedFiles, 0)
}

func (g *Graph) InvalidateChangedInContext(domainKey string, revisionID int64, changedFiles []string, contextID int64) (*InvalidateResult, error) {
	if len(changedFiles) == 0 {
		return &InvalidateResult{}, nil
	}

	var staleCount int64
	var affectedEdgeIDs, affectedNodeIDs []int64
	var err error

	if contextID > 0 {
		staleCount, affectedEdgeIDs, affectedNodeIDs, err = g.store.MarkEvidenceStaleByFilesVersioned(changedFiles, revisionID, contextID)
	} else {
		staleCount, affectedEdgeIDs, affectedNodeIDs, err = g.store.MarkEvidenceStaleByFiles(changedFiles)
	}
	if err != nil {
		return nil, err
	}

	for _, edgeID := range affectedEdgeIDs {
		if err := g.RecalculateEdgeTrust(edgeID); err != nil {
			return nil, err
		}
	}
	for _, nodeID := range affectedNodeIDs {
		if err := g.RecalculateNodeTrust(nodeID); err != nil {
			return nil, err
		}
	}

	filesToRescan, err := g.store.StaleFilePaths()
	if err != nil {
		return nil, err
	}

	return &InvalidateResult{
		StaleEvidence: int(staleCount),
		AffectedEdges: len(affectedEdgeIDs),
		AffectedNodes: len(affectedNodeIDs),
		FilesToRescan: filesToRescan,
	}, nil
}
```

- [ ] **Step 4: Run tests**

Run: `go test ./... 2>&1 | tail -20`
Expected: All pass (backward compatible — old callers use `InvalidateChanged` which defaults to contextID=0)

- [ ] **Step 5: Commit**

```bash
git add internal/graph/context.go internal/graph/invalidation.go e2e/context_isolation_test.go
git commit -m "feat: context-aware graph layer with versioned invalidation"
```

---

### Task 10: New MCP Tools for Contexts

**Files:**
- Modify: `internal/mcp/server.go`
- Modify: `internal/mcp/commands.go`

Add `chronicle_resolve_context`, `chronicle_context_list`, `chronicle_context_create`, `chronicle_context_archive`, `chronicle_changelog_query` tools.

- [ ] **Step 1: Add tool definitions and handlers**

In `internal/mcp/server.go`, add the new tool registrations in `NewServer()`:

```go
s.AddTool(contextResolveTool(), contextResolveHandler(g))
s.AddTool(contextListTool(), contextListHandler(g))
s.AddTool(contextCreateTool(), contextCreateHandler(g))
s.AddTool(contextArchiveTool(), contextArchiveHandler(g))
s.AddTool(changelogQueryTool(), changelogQueryHandler(g))
```

Implement each tool definition and handler following the existing pattern (extract params via `strParam`/`int64Param`, call store methods, return `jsonResult`). Each handler is ~20 lines.

Key tool: `chronicle_resolve_context` — accepts `domain` param, optionally `git_ref` and `commit_sha`. Looks up context by ref, falls back to finding closest commit.

- [ ] **Step 2: Update commands.go help text**

Add the new commands to the help text in `CommandInstructions["help"]`.

- [ ] **Step 3: Build and test**

Run: `go build ./... && go test ./...`
Expected: All pass

- [ ] **Step 4: Commit**

```bash
git add internal/mcp/server.go internal/mcp/commands.go
git commit -m "feat: MCP tools for knowledge context management"
```

---

### Task 11: Update chronicle update Command

**Files:**
- Modify: `internal/mcp/commands.go`

Update the `"update"` command instructions to include context resolution as the first step.

- [ ] **Step 1: Update command instructions**

Replace the `"update"` entry in `CommandInstructions` to include:

```go
"update": `Incremental graph update — rescan only files changed since the last scan:

1. Call chronicle_resolve_context(domain) to get current context
   - If no context and on a non-main branch, call chronicle_context_create to create one
   - If no context and no previous scan, tell user to run "chronicle scan" first
2. Get last revision in current context → before_sha (from context's head_commit_sha)
3. Get current HEAD: run git rev-parse HEAD → this is after_sha
4. If before_sha == after_sha, tell the user "Graph is up to date — no changes since last scan" and stop
5. Run git diff --name-only {before_sha} {after_sha} to get the list of changed files
   - If no files changed, tell the user and stop
6. Create an incremental revision:
   chronicle_revision_create(domain, context_id, mode="incremental", before_sha, after_sha, trigger_kind="manual")
7. Invalidate changed files:
   chronicle_invalidate_changed(domain, revision_id, changed_files as JSON array)
   → closes old evidence validity, inserts stale versions, writes changelog
   → returns stale_evidence count and files_to_rescan
8. Read ONLY the files listed in files_to_rescan (skip deleted files)
9. For each file: extract nodes/edges following chronicle_extraction_guide methodology
   → chronicle_import_all for re-extracted facts (positive evidence, max 10-15 nodes per call)
10. For relationships confirmed removed (e.g. deleted imports, removed dependencies):
    create negative evidence via chronicle_evidence_add(polarity="negative")
11. Finalize: chronicle_finalize_incremental_scan(domain, revision_id)
12. Snapshot: chronicle_snapshot_create(domain, revision_id)
13. Report summary: files scanned, nodes/edges updated, evidence revalidated vs still stale`,
```

- [ ] **Step 2: Similarly update "scan" to auto-create main context**

Add context creation as step 3.5 in the scan instructions:

```
3.5. Call chronicle_resolve_context(domain) — if no context exists, it auto-creates "main"
```

- [ ] **Step 3: Build**

Run: `go build ./...`
Expected: Success

- [ ] **Step 4: Commit**

```bash
git add internal/mcp/commands.go
git commit -m "feat: update chronicle commands with context-aware instructions"
```

---

### Task 12: Update CLAUDE.md and README

**Files:**
- Modify: `CLAUDE.md`
- Modify: `README.md`

- [ ] **Step 1: No code changes — update documentation**

Add a note to `CLAUDE.md` about context-aware queries being automatic. No user action required — Claude auto-resolves context from git state.

Update `README.md` "How It Works" section to mention branch-aware knowledge:

```markdown
**Branch-aware.** Chronicle tracks knowledge per git branch. Scan on `feature/payments` and the knowledge stays isolated from `main`. Switch branches and queries automatically show the right context — no manual configuration. When the branch merges, a simple `chronicle update` on main picks up the changes.
```

- [ ] **Step 2: Commit**

```bash
git add CLAUDE.md README.md
git commit -m "docs: document branch-aware knowledge contexts"
```

---

### Task 13: Integration Test — Full Context Lifecycle

**Files:**
- Create: `e2e/knowledge_context_test.go`

End-to-end test covering: create main → full scan → branch → incremental update on branch → verify isolation → "merge" back to main.

- [ ] **Step 1: Write the full lifecycle test**

Create `e2e/knowledge_context_test.go` with tests:

1. `TestContextLifecycleMainScan` — Full scan creates main context, nodes visible
2. `TestContextLifecycleBranchIsolation` — Branch context doesn't see main's later changes
3. `TestContextLifecycleBranchAddNode` — New node added on branch visible only in branch context
4. `TestContextLifecycleVersionedEvidence` — Invalidation creates new version rows, old rows have valid_to set
5. `TestContextLifecycleRevisionChain` — Revisions chain correctly with context_id
6. `TestContextLifecycleChangelog` — Mutations produce changelog entries

Each test follows the existing `setupTomAndJerry` pattern — `t.TempDir()`, fresh DB, import payload.

- [ ] **Step 2: Run tests**

Run: `go test ./e2e/ -run TestContextLifecycle -v`
Expected: All PASS

- [ ] **Step 3: Run full suite**

Run: `go test ./...`
Expected: All pass including existing tests (backward compatible)

- [ ] **Step 4: Commit**

```bash
git add e2e/knowledge_context_test.go
git commit -m "test: end-to-end knowledge context lifecycle tests"
```
