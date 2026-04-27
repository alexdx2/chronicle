# Evidence Trust System Design

## Context

Oracle's knowledge graph stores facts about code (nodes, edges) with evidence provenance. The primary consumer is Claude, who both extracts facts (during scans) and queries them (when answering user questions).

**Problems with current model:**
1. **Stale data lies** — after code changes, the graph shows outdated relationships as confident (confidence=1.0). Claude makes decisions based on dead facts.
2. **Impact is flat** — confidence defaults to 1.0 everywhere, so impact scoring can't differentiate strong vs weak relationships.
3. **No incrementality** — every scan is a full rescan because there's no way to know what specifically became stale.

**Key constraint:** The extractor is Claude, not an AST parser. Claude is non-deterministic — same file scanned twice may produce slightly different results. The system must not rely on Claude generating stable hashes or identifiers.

**Design principle:** The system generates hashes, computes scores, and manages lifecycle. Claude provides `derivation_kind` and file locations. Everything else is computed.

---

## 1. Evidence Lifecycle

### New fields on `graph_evidence`

```sql
evidence_status TEXT NOT NULL DEFAULT 'valid'
    CHECK (evidence_status IN ('valid','stale','revalidated','invalidated','superseded')),
evidence_polarity TEXT NOT NULL DEFAULT 'positive'
    CHECK (evidence_polarity IN ('positive','negative')),
valid_from_revision_id INTEGER REFERENCES graph_revisions(revision_id),
valid_to_revision_id INTEGER REFERENCES graph_revisions(revision_id),
last_verified_revision_id INTEGER REFERENCES graph_revisions(revision_id),
invalidated_by_revision_id INTEGER REFERENCES graph_revisions(revision_id),
invalidated_reason TEXT
```

### Status definitions

| Status | Meaning | Transition |
|--------|---------|-----------|
| `valid` | Evidence confirmed in latest scan or file unchanged | Created or re-verified |
| `stale` | Source file changed since last observation | System marks via file-based invalidation |
| `revalidated` | Was stale, re-checked and confirmed | Claude re-scanned file, found the fact again |
| `invalidated` | Contradicted by negative evidence | Negative evidence explicitly disproves this fact |
| `superseded` | Replaced by newer evidence for same fact | New evidence created at different location |

### Evidence polarity

| Polarity | Meaning | Example |
|----------|---------|---------|
| `positive` | Evidence supports the fact | "OrdersService injects PaymentsService" — found in constructor |
| `negative` | Evidence contradicts the fact | "OrdersService constructor no longer references PaymentsService" |

Negative evidence is created by Claude during re-scan when a previously observed relationship is confirmed to be removed. The absence of new positive evidence does NOT create negative evidence — only explicit confirmation of removal does.

### Lifecycle flow

1. Claude scans file, creates positive evidence → `status=valid`, `valid_from=current_revision`
2. Next scan, file unchanged → evidence stays `valid`, `last_verified` updated
3. File changed → system marks evidence `stale` (via `oracle_invalidate_changed`)
4. Claude re-reads file:
   - Found again → `status=revalidated`, update `observed_at`, `last_verified`
   - Confirmed removed → Claude creates **negative evidence** with `polarity=negative`, old evidence stays `stale`
   - Not checked (Claude didn't get to it) → evidence stays `stale`
5. Found at different location → old evidence `superseded`, new evidence created
6. Stale evidence older than N revisions → remains `stale` (never auto-invalidated)

### Key rule: stale stays stale

`FinalizeIncrementalScan` does NOT auto-invalidate stale evidence. Evidence transitions to `invalidated` ONLY when negative evidence explicitly contradicts it. This prevents killing valid relationships just because Claude didn't rescan a file.

### Confidence mapping

Claude provides `derivation_kind` on edges. The system maps to confidence automatically:

| derivation_kind | confidence |
|----------------|-----------|
| hard | 0.95 |
| linked | 0.80 |
| inferred | 0.60 |
| unknown | 0.40 |

Claude does NOT set confidence numbers directly. The mapping is deterministic.

---

## 2. Freshness and Trust Score

### Separation of concerns

| Metric | What it measures | Who sets it |
|--------|-----------------|------------|
| **confidence** | How strong is the evidence (derivation quality) | System, from derivation_kind |
| **freshness** | How current is the evidence (has source changed) | System, from file/revision state |
| **trust_score** | Combined reliability for ranking | System, computed |

### Freshness per evidence

| Evidence status | Freshness contribution |
|-----------------|----------------------|
| `valid` or `revalidated` | 1.0 |
| `stale` | 0.5 |
| `invalidated` | 0.0 |
| `superseded` | 0.0 |

### Edge/Node freshness — weighted average, not max

Freshness for an edge/node is the **weighted average** of its evidence freshness values, weighted by evidence confidence:

```
freshness = Σ(evidence_freshness_i × evidence_confidence_i) / Σ(evidence_confidence_i)
```

This prevents one old valid evidence from masking stale evidence. Additional caps:
- If **any** evidence from a changed file is `stale` → cap freshness at 0.6
- If **strong negative evidence** exists (negative with confidence >= 0.8) → trust near 0

### Formulas

**Positive confidence** (from valid/revalidated positive evidence):
```
positive_confidence = 1 - Π(1 - positive_evidence_confidence_i)
```

**Negative confidence** (from valid negative evidence):
```
negative_confidence = 1 - Π(1 - negative_evidence_confidence_i)
```

**Base confidence:**
```
base_confidence = positive_confidence × (1 - negative_confidence)
```

Example: positive=0.95, negative=0.92 → 0.95 × 0.08 = 0.076 (edge effectively dead).

**Trust score:**
```
trust_score = base_confidence × freshness
```

**Impact scoring** (updated):
```
impact_score = 100 × Π(trust_score_i along path) × 0.95^(depth-1)
```

### Trust after full scan

With derivation_kind=hard, trust_score is 0.95, NOT 1.0. This is correct — it reflects extraction confidence, not certainty.

### Trust thresholds for Claude

| Trust Score | Claude behavior |
|-------------|----------------|
| >= 0.8 | Use directly, no verification needed |
| 0.4 - 0.8 | Use with caveat, verify if critical |
| < 0.4 | Read source file to verify before using |

---

## 3. File-Based Invalidation

### Mechanism

Evidence is already indexed by `(source_kind, repo_name, file_path)`. When files change:

1. Claude gets changed files from `git diff`
2. Calls `oracle_invalidate_changed(domain, revision_id, changed_files)`
3. System finds all `valid` evidence with `file_path` in changed_files
4. Marks them `stale`
5. Finds all edges/nodes linked to stale evidence
6. Recalculates freshness (drops to 0.5)
7. Returns list of what needs re-scanning

### Key rule

Incremental scan must NOT invalidate evidence from untouched files. Only evidence whose source file appears in the changed_files list gets marked stale.

---

## 4. Computed Edge/Node Status

### Edge status (computed from evidence)

| Condition | Status |
|-----------|--------|
| Has valid/revalidated positive evidence, no strong negative | `active` |
| Only stale positive evidence, no negative | `stale` |
| Strong negative evidence exists (negative confidence >= 0.8) | `contradicted` |
| All evidence invalidated | `removed` |
| No evidence at all | `unknown` |

### Node status — same logic, but nodes can also exist without evidence (manually created).

### Recalculation trigger

Whenever evidence status changes (via scan, invalidation, or finalization), the system recalculates:
1. Gather all evidence for the edge/node
2. Compute confidence from valid evidence
3. Compute freshness from evidence status distribution
4. trust_score = confidence × freshness
5. Update edge/node status

---

## 5. Incremental Scan Workflow

### Full scan (unchanged)
```
1. oracle_revision_create(mode="full")
2. oracle_extraction_guide
3. Read files → oracle_import_all
4. oracle_stale_mark(domain, revision_id)
5. oracle_snapshot_create
```

### Incremental scan (new)
```
1. git diff → changed_files
2. oracle_revision_create(mode="incremental", before_sha, after_sha)
3. oracle_invalidate_changed(domain, revision_id, changed_files)
   → { stale_evidence, affected_edges, affected_nodes, files_to_rescan }
4. Read ONLY changed files → oracle_import_all
   - For facts still present: re-import as positive evidence → status becomes revalidated
   - For facts confirmed removed: create negative evidence (polarity=negative)
   - For facts not checked: do nothing, evidence stays stale
5. oracle_finalize_incremental_scan_scan(domain, revision_id)
   → { revalidated, still_stale, contradicted, edges_status_changed }
```

### Finalization logic

`oracle_finalize_incremental_scan_scan` does NOT auto-invalidate anything. It:
1. Counts revalidated evidence (stale → revalidated during step 4)
2. Counts still-stale evidence (not re-checked)
3. Counts contradicted (negative evidence added)
4. Recalculates trust_score for all affected edges/nodes
5. Updates edge/node status based on evidence state

**Key rule:** Stale evidence stays stale. Invalidation happens ONLY through negative evidence.
Stale evidence older than N revisions can optionally become `expired` in the future, but never `invalidated` without explicit negative evidence.

---

## 6. MCP Tool Changes

### Modified tools (response format changes)

**`oracle_query_deps` / `oracle_query_reverse_deps`** — add to each result:
- `trust_score` (float)
- `freshness` (float)
- `status` (computed: active/stale/removed/unknown)

**`oracle_impact`** — change impact formula to use trust_score:
- Add `trust_chain` (float): product of trust_scores along path

**`oracle_edge_list`** — add `trust_score`, `freshness` to each edge.

**`oracle_node_get`** — add `evidence_status` to each evidence entry, add computed `trust_score` to node.

**`oracle_node_list`** — add `trust_score` and computed `status` to each node.

### New tools

**`oracle_invalidate_changed`**
```
Input:  { domain: string, revision_id: int, changed_files: string[] }
Output: { stale_evidence: int, affected_edges: int, affected_nodes: int, files_to_rescan: string[] }
```
Marks evidence from changed files as stale. Returns what needs re-scanning.

**`oracle_finalize_incremental_scan_scan`**
```
Input:  { domain: string, revision_id: int }
Output: { revalidated: int, still_stale: int, contradicted: int, edges_status_changed: int }
```
Completes an incremental scan — recalculates trust scores for all affected entities. Does NOT auto-invalidate stale evidence.

### Updated extraction guide

Add to `oracle_extraction_guide`:
```
## Trust-Aware Queries
When using graph data to answer user questions:
- Check trust_score in query results
- trust >= 0.8: use directly in your answer
- trust 0.4-0.8: mention uncertainty ("based on last scan, but file has changed...")
- trust < 0.4: read the source file to verify before answering
- When running impact analysis, note if trust_chain < 0.7 — the path may be broken
```

### Updated scan command

`oracle_command(command='scan')` adds incremental variant:
```
If user says "update the graph" or "rescan changes":
1. git diff to find changed files
2. oracle_revision_create(mode="incremental")
3. oracle_invalidate_changed(changed_files)
4. Read and re-extract only changed files
5. oracle_finalize_incremental_scan
```

---

## 7. Schema Changes Summary

### graph_evidence — new columns
```sql
ALTER TABLE graph_evidence ADD COLUMN evidence_status TEXT NOT NULL DEFAULT 'valid'
    CHECK (evidence_status IN ('valid','stale','revalidated','invalidated','superseded'));
ALTER TABLE graph_evidence ADD COLUMN evidence_polarity TEXT NOT NULL DEFAULT 'positive'
    CHECK (evidence_polarity IN ('positive','negative'));
ALTER TABLE graph_evidence ADD COLUMN valid_from_revision_id INTEGER
    REFERENCES graph_revisions(revision_id);
ALTER TABLE graph_evidence ADD COLUMN valid_to_revision_id INTEGER
    REFERENCES graph_revisions(revision_id);
ALTER TABLE graph_evidence ADD COLUMN last_verified_revision_id INTEGER
    REFERENCES graph_revisions(revision_id);
ALTER TABLE graph_evidence ADD COLUMN invalidated_by_revision_id INTEGER
    REFERENCES graph_revisions(revision_id);
ALTER TABLE graph_evidence ADD COLUMN invalidated_reason TEXT;
```

### graph_edges — add trust fields
```sql
ALTER TABLE graph_edges ADD COLUMN freshness REAL NOT NULL DEFAULT 1.0
    CHECK (freshness >= 0 AND freshness <= 1);
ALTER TABLE graph_edges ADD COLUMN trust_score REAL NOT NULL DEFAULT 1.0
    CHECK (trust_score >= 0 AND trust_score <= 1);
```

### graph_nodes — add trust fields
```sql
ALTER TABLE graph_nodes ADD COLUMN freshness REAL NOT NULL DEFAULT 1.0
    CHECK (freshness >= 0 AND freshness <= 1);
ALTER TABLE graph_nodes ADD COLUMN trust_score REAL NOT NULL DEFAULT 1.0
    CHECK (trust_score >= 0 AND trust_score <= 1);
```

### New index
```sql
CREATE INDEX idx_graph_evidence_status ON graph_evidence(evidence_status);
CREATE INDEX idx_graph_evidence_file_status ON graph_evidence(file_path, evidence_status);
```

---

## 8. Files to Modify

| File | Changes |
|------|---------|
| `internal/store/store.go` | Schema migration: new columns on evidence, edges, nodes |
| `internal/store/evidence.go` | EvidenceRow struct, AddEvidence (set valid_from), new methods: MarkStaleByFiles, FinalizeIncremental |
| `internal/store/edges.go` | EdgeRow struct (add freshness, trust_score), RecalculateTrust |
| `internal/store/nodes.go` | NodeRow struct (add freshness, trust_score), RecalculateTrust |
| `internal/mcp/server.go` | New tools: oracle_invalidate_changed, oracle_finalize_incremental_scan. Update responses for deps/impact/edge_list/node_get |
| `internal/graph/graph.go` | Trust computation logic, confidence mapping from derivation_kind |
| `internal/graph/impact.go` | Update impact formula to use trust_score |
| `internal/mcp/commands.go` | Update scan command instructions for incremental workflow |
| `internal/mcp/middleware.go` | Update auto-discovery to use trust_score thresholds |
| `internal/admin/templates/` | Dashboard: show trust_score, freshness, evidence status |

## 9. Verification

1. **Unit tests**: Test confidence mapping, combine formula, freshness calculation, trust computation
2. **Integration test**: Full scan → modify file → incremental scan → verify trust_scores updated
3. **MCP test**: Call oracle_invalidate_changed, verify stale evidence count, call oracle_finalize_incremental_scan, verify invalidated count
4. **Query test**: oracle_query_deps returns trust_score, oracle_impact uses trust in formula
5. **E2E**: Scan project → change a file → incremental rescan → verify stale edges show lower trust → verify impact scores reflect staleness
