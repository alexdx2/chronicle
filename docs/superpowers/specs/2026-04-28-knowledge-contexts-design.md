# Knowledge Contexts + Versioned Queries

**Date:** 2026-04-28
**Status:** Approved

## Problem

Chronicle's knowledge graph is stateless with respect to branches and time. All queries return the latest mutated state. When two developers scan on different branches, their knowledge pollutes each other. When you want to know "what did the graph look like before the refactor?", you can't — because mutations are in-place.

## Core Invariant

**Never overwrite historical knowledge.** Chronicle does not mutate knowledge in place. It creates a new temporal version of knowledge inside a context, and queries resolve visibility from the caller's git-derived context lineage.

No `UPDATE ... SET status='stale'`. Instead: set `valid_to_revision_id` on the old row, insert a new row with the new status.

## Architecture Model

This is **temporal versioning + context lineage + changelog**. Not event sourcing.

**Source of truth hierarchy:**

| Layer | Role |
|-------|------|
| `graph_nodes` / `graph_edges` / `graph_evidence` | **Versioned temporal rows — source of truth.** Each row has `entity_uid` (stable identity), `valid_from_revision_id`, `valid_to_revision_id` (visibility window). Multiple versions of the same entity coexist. |
| `graph_changelog` | **Explanation trail — audit/debug/diff.** Append-only. Records what changed, when, and why. Not a replay source. |
| `current_*` views (future) | **Optional fast projections — optimization.** Materialized views over temporal rows filtered to `valid_to_revision_id IS NULL`. Not required for correctness. |

Versioned rows are the source of truth. If the changelog and the temporal rows disagree, the temporal rows win.

## Design

### 1. Knowledge Contexts

A context is a named knowledge timeline — typically mapping to a git branch.

```sql
CREATE TABLE knowledge_contexts (
    context_id        INTEGER PRIMARY KEY AUTOINCREMENT,
    domain_key        TEXT NOT NULL,
    name              TEXT NOT NULL,         -- "main", "feature/payments", "release/1.2"
    git_ref           TEXT,                  -- branch name or tag
    base_context_id   INTEGER,              -- FK: parent context (NULL for main)
    base_revision_id  INTEGER,              -- revision where this context diverged
    head_revision_id  INTEGER,              -- latest revision in this context
    head_commit_sha   TEXT,                 -- latest scanned commit SHA
    status            TEXT NOT NULL DEFAULT 'active'
                      CHECK (status IN ('active', 'merged', 'archived')),
    created_at        TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (base_context_id) REFERENCES knowledge_contexts(context_id),
    FOREIGN KEY (base_revision_id) REFERENCES graph_revisions(revision_id),
    UNIQUE (domain_key, name)
);
```

### 2. Revisions Get Context

```sql
ALTER TABLE graph_revisions ADD COLUMN context_id INTEGER
    REFERENCES knowledge_contexts(context_id);
```

Every revision belongs to a context. Existing revisions are migrated to an auto-created "main" context.

### 3. Graph Changelog (append-only event log)

```sql
CREATE TABLE graph_changelog (
    changelog_id      INTEGER PRIMARY KEY AUTOINCREMENT,
    revision_id       INTEGER NOT NULL,
    context_id        INTEGER NOT NULL,
    entity_type       TEXT NOT NULL CHECK (entity_type IN ('node', 'edge', 'evidence')),
    entity_key        TEXT NOT NULL,        -- node_key, edge composite key, or evidence identifier
    entity_id         INTEGER,              -- FK to the actual row (node_id, edge_id, evidence_id)
    change_type       TEXT NOT NULL
                      CHECK (change_type IN ('created', 'updated', 'stale', 'invalidated', 'revalidated', 'deleted')),
    field_changes     TEXT,                 -- JSON: {"status": ["active","stale"], "trust_score": [0.95, 0.5]}
    created_at        TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (revision_id) REFERENCES graph_revisions(revision_id),
    FOREIGN KEY (context_id) REFERENCES knowledge_contexts(context_id)
);

CREATE INDEX idx_changelog_revision ON graph_changelog(revision_id);
CREATE INDEX idx_changelog_context ON graph_changelog(context_id);
CREATE INDEX idx_changelog_entity ON graph_changelog(entity_type, entity_key);
```

This table is **append-only**. No updates, no deletes. Every state transition in the graph produces a changelog row. The changelog is an **explanation trail** for audit, debugging, and diffing — not a replay source. The versioned temporal rows in `graph_nodes`, `graph_edges`, `graph_evidence` are the source of truth.

### 4. Immutable Evidence Mutations

Current behavior (WRONG — overwrites history):
```sql
UPDATE graph_evidence SET evidence_status='stale'
WHERE file_path IN (...) AND evidence_status IN ('valid','revalidated')
```

New behavior (closes validity, inserts new version):
```sql
-- Close old evidence validity window
UPDATE graph_evidence SET valid_to_revision_id = ?
WHERE file_path IN (...) AND evidence_status IN ('valid','revalidated')
AND valid_to_revision_id IS NULL

-- Insert new evidence row with stale status
INSERT INTO graph_evidence (
    ..., evidence_status, valid_from_revision_id, valid_to_revision_id
) VALUES (
    ..., 'stale', ?, NULL
)

-- Append changelog entry
INSERT INTO graph_changelog (revision_id, context_id, entity_type, entity_key, change_type, field_changes)
VALUES (?, ?, 'evidence', ?, 'stale', '{"status":["valid","stale"]}')
```

Same pattern for every state transition:
- `valid → stale`: close old, insert stale version
- `stale → revalidated`: close stale, insert revalidated version
- `valid → invalidated`: close old, insert invalidated version with reason

### 5. Entity Identity Model

Every versioned entity has two identities:

| Field | Role | Example |
|-------|------|---------|
| `entity_uid` | **Stable logical identity.** Never changes across versions. Used for relationships, queries, user-facing references. | `node_key = "code:provider:tomandjerry:tomservice"` |
| `version_id` | **Concrete version row.** Auto-increment PK. Each mutation creates a new version_id for the same entity_uid. | `node_version_id = 47` |

This distinction is critical: when an update inserts a new row, `node_id` (the PK) changes but `node_key` (the identity) stays the same. Edges and evidence reference `entity_uid`, not version PKs, so relationships survive version transitions.

For **nodes**:
```sql
graph_nodes:
  node_version_id         INTEGER PRIMARY KEY   -- version identity (was node_id)
  node_key                TEXT NOT NULL          -- stable logical identity (entity_uid)
  valid_from_revision_id  INTEGER NOT NULL       -- visibility window start
  valid_to_revision_id    INTEGER                -- visibility window end (NULL = open)
  context_id              INTEGER NOT NULL       -- which context owns this version
  status, layer, node_type, name, ...            -- version payload
```

For **edges**:
```sql
graph_edges:
  edge_version_id         INTEGER PRIMARY KEY   -- version identity (was edge_id)
  from_node_key           TEXT NOT NULL          -- stable FK (NOT from_node_id)
  to_node_key             TEXT NOT NULL          -- stable FK (NOT to_node_id)
  edge_type               TEXT NOT NULL          -- together these three form the edge identity
  valid_from_revision_id  INTEGER NOT NULL
  valid_to_revision_id    INTEGER
  context_id              INTEGER NOT NULL
  derivation_kind, active, ...                   -- version payload
```

For **evidence**:
```sql
graph_evidence:
  evidence_version_id     INTEGER PRIMARY KEY   -- version identity
  evidence_uid            TEXT NOT NULL          -- stable identity (derived from target+source+location)
  target_node_key         TEXT                   -- stable FK (NOT node_id)
  target_edge_key         TEXT                   -- stable FK (NOT edge_id) — composite of from_key:to_key:type
  valid_from_revision_id  INTEGER NOT NULL
  valid_to_revision_id    INTEGER
  context_id              INTEGER NOT NULL
  evidence_status, confidence, file_path, ...    -- version payload
```

### 6. Immutable Node/Edge Mutations

Same principle as evidence. Instead of:
```sql
UPDATE graph_nodes SET status='stale' WHERE last_seen_revision_id < ?
```

Do:
```sql
-- Close current version
UPDATE graph_nodes SET valid_to_revision_id = ?
WHERE node_key = ? AND valid_to_revision_id IS NULL AND context_id = ?

-- Insert new version with stale status
INSERT INTO graph_nodes (node_key, status, valid_from_revision_id, context_id, ...)
VALUES (?, 'stale', ?, ?, ...)

-- Record in changelog
INSERT INTO graph_changelog (revision_id, context_id, entity_type, entity_key, change_type, field_changes)
VALUES (?, ?, 'node', ?, 'stale', '{"status":["active","stale"]}')
```

A node version is "current" when `valid_to_revision_id IS NULL`. Multiple versions of the same `node_key` coexist — one per context, or multiple within a context as the entity evolves over time.

### 7. Query Resolution: Visible Revision Set via Context Lineage

When Claude queries, the system resolves which revisions are visible based on **context lineage** — not just `revision_id <= N`.

The lineage is always: parent context up to the branch point + current context's own revisions. This prevents mixing revisions from sibling branches that happen to have overlapping revision IDs.

```
context = "feature/payments"
  context_id = 3
  base_context_id → 1 (main)
  base_revision_id → 18

visible = revisions WHERE
  (context_id = 1 AND revision_id <= 18)     -- parent up to branch point
  UNION
  (context_id = 3)                            -- current context, all revisions
```

Implemented as a CTE for performance (avoids `IN (...)` with large lists):

```sql
WITH visible_revisions AS (
    -- Parent context up to branch point
    SELECT revision_id FROM graph_revisions
    WHERE context_id = :base_context_id
      AND revision_id <= :base_revision_id
    UNION ALL
    -- Current context's own revisions
    SELECT revision_id FROM graph_revisions
    WHERE context_id = :context_id
)
SELECT n.* FROM graph_nodes n
WHERE n.valid_from_revision_id IN (SELECT revision_id FROM visible_revisions)
  AND (
    n.valid_to_revision_id IS NULL
    OR n.valid_to_revision_id NOT IN (SELECT revision_id FROM visible_revisions)
  )
  AND n.node_key = :node_key  -- or other filters
```

For the main context (no parent), the CTE simplifies to:

```sql
WITH visible_revisions AS (
    SELECT revision_id FROM graph_revisions
    WHERE context_id = :main_context_id
      AND revision_id <= :as_of_revision  -- optional ceiling
)
```

A **node version** is visible if:
- `valid_from_revision_id` is in the visible set (it was created within the lineage)
- AND `valid_to_revision_id IS NULL` OR `valid_to_revision_id` is NOT in the visible set (it wasn't superseded within the lineage)

Same rule for edges and evidence. The CTE is built once per query call and reused across all joins.

### 8. Auto-Detection Flow

Every MCP query auto-resolves context. No user action required.

```
1. git rev-parse HEAD → current_sha
2. git branch --show-current → current_branch

3. SELECT * FROM knowledge_contexts
   WHERE domain_key = ? AND git_ref = current_branch AND status = 'active'
   → found? Use it.

4. Not found? Walk git log --format='%H' to find closest ancestor commit
   matching any context's head_commit_sha
   → found on main at revision 15?
     Return context=main, as_of_revision=15

5. Nothing found?
   → Return context=main, as_of_revision=latest
     (best effort — same as current behavior)
```

New MCP tool: `chronicle_resolve_context(domain)` — returns `{context_id, context_name, as_of_revision, visible_revision_ids}`. Claude calls this once at conversation start.

### 9. Auto-Create Context on Scan

When `chronicle update` or `chronicle scan` runs on a non-main branch:

```
1. Detect branch via git branch --show-current
2. If branch != "main" and no active context for this branch:
   a. Find the merge-base: git merge-base HEAD main
   b. Find the revision on main closest to that merge-base commit
   c. CREATE context:
      name = branch_name
      git_ref = branch_name
      base_context_id = main.context_id
      base_revision_id = that revision
3. Create revision with context_id = new context
4. Proceed with normal scan/update
```

### 10. Modified Query Tools

All query tools get an optional `context` parameter (auto-filled by Claude via `chronicle_resolve_context`):

- `chronicle_query_deps(node_key, context=...)`
- `chronicle_query_reverse_deps(node_key, context=...)`
- `chronicle_impact(node_key, context=...)`
- `chronicle_query_path(from, to, context=...)`
- `chronicle_node_list(context=...)`
- `chronicle_edge_list(context=...)`
- `chronicle_query_stats(context=...)`

When `context` is omitted, falls back to current behavior (main, latest state).

When `context` is provided, queries filter nodes/edges/evidence to those visible in the context's revision set.

### 11. New MCP Tools

| Tool | Purpose |
|------|---------|
| `chronicle_resolve_context(domain)` | Auto-detect context from git state |
| `chronicle_context_list(domain)` | List all contexts for a domain |
| `chronicle_context_create(domain, name, git_ref, base_context)` | Explicit context creation |
| `chronicle_context_archive(context_id)` | Mark context as archived |
| `chronicle_changelog_query(context, entity_key, from_revision, to_revision)` | Query change history |

### 12. Updated `chronicle update` Command

```
1. Call chronicle_resolve_context(domain) to get current context
   - If no context and on a non-main branch, auto-create context
   - If no context and no previous scan, tell user to run "chronicle scan" first
2. Get last revision in current context → before_sha
3. git rev-parse HEAD → after_sha
4. If before_sha == after_sha → "Graph is up to date"
5. git diff --name-only {before_sha} {after_sha} → changed files
6. chronicle_revision_create(domain, context_id, mode="incremental", ...)
7. chronicle_invalidate_changed(domain, revision_id, changed_files)
   → closes old evidence validity, inserts stale versions, writes changelog
8. Read files_to_rescan, re-extract
9. chronicle_import_all → inserts new evidence versions, writes changelog
10. chronicle_finalize_incremental_scan
11. chronicle_snapshot_create
12. Report summary
```

### 13. Context Lifecycle

```
1. First scan on main → auto-creates "main" context (context_id=1)
2. Developer: git checkout -b feature/payments
3. Developer: "chronicle update"
   → auto-creates context: name="feature/payments",
     base_context=main, base_revision_id=main.head_revision_id
4. Subsequent scans on feature/payments → revisions in that context
5. Developer: git checkout main && git merge feature/payments
6. Developer: "chronicle update" on main
   → git diff picks up merged changes, scans fresh
   → main context advances, new revisions on main
7. feature/payments context → status='merged' (manual or auto-detect)
   → stays forever as historical record
```

### 14. Migration

Existing databases need a migration:

1. Create `knowledge_contexts` table
2. Create `graph_changelog` table
3. Insert "main" context for each existing domain
4. Backfill `context_id` on existing revisions → point to main
5. Rename identity columns:
   - `graph_nodes.node_id` → `node_version_id` (PK stays), `node_key` remains as stable identity
   - `graph_edges.edge_id` → `edge_version_id`, add composite stable identity (`from_node_key`, `to_node_key`, `edge_type`)
   - `graph_evidence.evidence_id` → `evidence_version_id`, add `evidence_uid`
6. Switch FKs in edges/evidence from `node_id`/`edge_id` to `node_key`/`edge_key` references
7. Add `valid_from_revision_id`, `valid_to_revision_id`, `context_id` to nodes/edges/evidence
8. Backfill: `valid_from_revision_id = first_seen_revision_id`, `valid_to_revision_id = NULL`, `context_id = main.context_id`

No data loss. Existing behavior preserved — all current data becomes main context history, single open version per entity.

### 15. What This Does NOT Do

- No merge logic between contexts (lazy merge via rescan)
- No conflict resolution between overlapping scans
- No automatic context cleanup (explicit archive only)
- No cross-domain contexts
- Not event sourcing — no replay from changelog. Versioned temporal rows are the source of truth
- Changelog is explanation trail: audit, debug, diff. Not a replay source
- No branch deletion detection (contexts stay until explicitly archived)

### 16. Testing Strategy

Add to `e2e/incremental_update_test.go`:

1. **Context auto-creation**: Full scan creates "main" context
2. **Branch context**: Create branch context with base_revision, verify visible revision set
3. **Query isolation**: Two contexts see different graph states for same node
4. **Immutable evidence**: After invalidation, old evidence row has valid_to set, new stale row exists
5. **Changelog append**: Every mutation produces changelog entries
6. **Context resolution**: Given a commit SHA, resolves to correct context + revision
7. **Merged context**: After marking merged, queries on main don't include branch revisions
8. **Entity identity**: Same `node_key` has multiple `node_version_id` rows after mutations, edges reference `node_key` not version ID
9. **Migration**: Existing DB without contexts gets "main" backfilled correctly, identity columns renamed
