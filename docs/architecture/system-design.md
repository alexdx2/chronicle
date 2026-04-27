# Oracle: System Design Document

**Version**: 1.0
**Date**: 2026-04-26
**Purpose**: External architecture review — algorithms, storage, consistency model

---

## 1. System Overview

Oracle is a knowledge graph engine for codebase analysis. It operates as an MCP (Model Context Protocol) server that pairs with an LLM (Claude Code) to extract, store, and query structural relationships in software projects.

**Key architectural decision**: The LLM handles understanding (reading code, inferring relationships). Oracle handles persistence, validation, querying, and trust computation. This separation means Oracle never parses source code — it only receives structured facts with provenance metadata.

```
┌──────────────────┐         stdio (MCP)         ┌──────────────────────────┐
│   Claude Code    │ ◄─────────────────────────► │         Oracle           │
│                  │                              │                          │
│  Reads files     │  oracle_import_all           │  Validates input         │
│  Infers facts    │  oracle_query_path           │  Stores in SQLite (WAL)  │
│  Reports issues  │  oracle_impact               │  Traverses graph         │
│                  │  oracle_define_term           │  Computes trust          │
└──────────────────┘                              │  Detects quality gaps    │
                                                  │                          │
                                                  │  HTTP + WebSocket        │
                                                  │  Admin dashboard         │
                                                  └──────────────────────────┘
```

---

## 2. Storage Design

### 2.1 Database: SQLite with WAL

**Choice rationale**: Single-file embedded database. No deployment infrastructure. WAL mode enables concurrent reads during writes. Foreign keys enforced at the engine level.

**Connection string**: `?_journal_mode=WAL&_foreign_keys=on`

### 2.2 Schema (9 tables)

| Table | Purpose | Key Constraint |
|-------|---------|---------------|
| `graph_revisions` | Scan history (git SHA, mode, trigger) | UNIQUE(domain_key, git_after_sha) |
| `graph_nodes` | Entities (models, services, endpoints) | UNIQUE(node_key) |
| `graph_edges` | Typed relationships between nodes | UNIQUE(edge_key), FK to both nodes |
| `graph_evidence` | Provenance records (file + line for each fact) | Exclusive-arc: node_id XOR edge_id |
| `graph_snapshots` | Point-in-time counts | UNIQUE(revision_id, domain_key) |
| `graph_discoveries` | Self-learning findings | — |
| `domain_language` | Ubiquitous language glossary | UNIQUE(domain_key, term) |
| `mcp_request_log` | MCP call audit trail | — |
| `project_settings` | Key-value config | PK on key |

### 2.3 Node Key Format

```
layer:node_type:domain_key:name
```

All keys are lowercased on ingestion. The key is immutable — once created, a node's layer, type, and domain cannot change. Only mutable fields (name, file_path, confidence, metadata) update on re-import.

### 2.4 Edge Key Format

```
from_node_key→to_node_key:EDGE_TYPE
```

Auto-generated from component parts. Ensures a single directed relationship of a given type between two nodes.

### 2.5 Evidence Deduplication

Evidence is deduplicated by the composite key:

```
(target_kind, node_id|edge_id, source_kind, repo_name, file_path, line_start, extractor_id, polarity)
```

On match: updates `observed_at` and `confidence`. On miss: creates new evidence row. This means the same extraction from the same location is treated as a re-observation, not a duplicate.

### 2.6 Indexing Strategy

```sql
-- Node lookup paths
idx_graph_nodes_layer_type     (layer, node_type)
idx_graph_nodes_domain         (domain_key)
idx_graph_nodes_repo_path      (repo_name, file_path)

-- Edge traversal (critical for BFS)
idx_graph_edges_from           (from_node_id)
idx_graph_edges_to             (to_node_id)
idx_graph_edges_type           (edge_type)

-- Evidence queries
idx_graph_evidence_node        (node_id)
idx_graph_evidence_edge        (edge_id)
idx_graph_evidence_source      (source_kind, repo_name, file_path)
idx_graph_evidence_status      (evidence_status)
idx_graph_evidence_file_status (file_path, evidence_status)
```

The edge indexes on `from_node_id` and `to_node_id` are the most performance-critical — they serve every BFS traversal step.

---

## 3. Graph Model

### 3.1 Layered Architecture

Nodes belong to one of 8 layers that map to software architecture concerns:

```
┌─────────────────────────────────────────────┐
│  ownership   Teams, owners                  │
├─────────────────────────────────────────────┤
│  ci          Pipelines, releases            │
├─────────────────────────────────────────────┤
│  infra       Terraform, Kubernetes          │
├─────────────────────────────────────────────┤
│  service     Deployable microservices       │
├─────────────────────────────────────────────┤
│  contract    Endpoints, topics, operations  │
├─────────────────────────────────────────────┤
│  flow        Business processes             │
├─────────────────────────────────────────────┤
│  code        Modules, controllers, services │
├─────────────────────────────────────────────┤
│  data        Models, enums, fields          │
└─────────────────────────────────────────────┘
```

### 3.2 Edge Types and Constraints

Each edge type has a constrained `(from_layer → to_layer)` mapping validated by a type registry:

| Edge Type | From → To | Meaning |
|-----------|-----------|---------|
| INJECTS | code → code | Constructor/DI injection |
| CONTAINS | any → any | Structural containment |
| EXPOSES_ENDPOINT | code → contract | Controller exposes HTTP/GraphQL |
| USES_MODEL | code → data | Service reads/writes a model |
| REFERENCES_MODEL | data → data | FK or relation between models |
| CALLS_SERVICE | code → service | Cross-service HTTP/gRPC call |
| PUBLISHES_TOPIC | code → contract | Produces to message topic |
| CONSUMES_TOPIC | code → contract | Subscribes to message topic |
| HAS_FIELD | data → data | Model contains field |

### 3.3 Derivation Kinds

Every edge carries a `derivation_kind` indicating how the relationship was determined:

| Kind | Confidence | Meaning |
|------|-----------|---------|
| hard | 0.95 | Directly observed in code (import statement, decorator) |
| linked | 0.80 | Inferred from configuration (env vars, URLs) |
| inferred | 0.60 | Heuristic (naming convention, file proximity) |
| unknown | 0.40 | Cannot determine extraction method |

### 3.4 Traversal Policy

The type registry defines a **traversal policy** that governs which edges participate in graph queries:

**Structural edges** (`CONTAINS`, `HAS_FIELD`): Excluded from path and impact queries by default. These represent containment hierarchy, not dependency flow.

**No-reverse-impact edges** (`EXPOSES_ENDPOINT`, `PUBLISHES_TOPIC`): Participate in forward path queries but are excluded from reverse impact analysis. Rationale: an endpoint breaking doesn't "impact" its controller — the controller owns the endpoint.

---

## 4. Algorithms

### 4.1 Dependency Query (Forward/Reverse BFS)

**Purpose**: "What does X depend on?" / "What depends on X?"

```
Algorithm: BFS
Input: start_node_key, max_depth, derivation_filter[], reverse (bool)
Output: [{node_key, name, layer, node_type, depth, trust_score}]

1. Resolve start_node_key → node_id
2. Initialize queue = [(start_node_id, depth=0)]
3. Initialize visited = {start_node_id}
4. While queue is not empty:
   a. Dequeue (current_id, current_depth)
   b. If current_depth >= max_depth: skip
   c. Query edges:
      - forward: WHERE from_node_id = current_id AND active = 1
      - reverse: WHERE to_node_id = current_id AND active = 1
   d. For each edge:
      - If derivation_filter set and edge.derivation_kind ∉ filter: skip
      - next_id = edge.to_node_id (forward) or edge.from_node_id (reverse)
      - If next_id ∈ visited: skip
      - Add next_id to visited
      - Fetch node metadata, emit result entry
      - Enqueue (next_id, current_depth + 1)
```

**Complexity**: O(V + E) where V = reachable nodes, E = edges traversed. Per-step cost dominated by SQLite index lookups on `from_node_id` / `to_node_id`.

### 4.2 Path Query (BFS with Path Tracking)

**Purpose**: "How does A connect to B?"

```
Algorithm: BFS with full-path state
Input: from_key, to_key, mode ("directed" | "connected"), max_depth
Output: [{path: [node_keys], edges: [types], path_score, path_cost}]

Modes:
  - "directed": follow edges in their natural direction only
  - "connected": follow edges in both directions (needed for pub/sub flows)

Scoring (per path):
  path_score = Π(edge.confidence) × 0.95^(depth - 1)
  path_cost  = Σ(-ln(edge.confidence)) + 0.05 × depth

Tie-breaking: lower cost → lower depth → lexicographic on node keys

Filtering:
  - Structural edges excluded unless --include-structural
  - Derivation filter applied if specified
```

The path score uses a decay factor (0.95 per hop) to penalize longer paths, reflecting that longer dependency chains are less certain to represent real coupling.

### 4.3 Impact Analysis (Reverse BFS with Policy)

**Purpose**: "What breaks if X changes?"

```
Algorithm: Reverse BFS with traversal policy filtering
Input: changed_node_key, max_depth (default 10), min_score, top_k (default 50)
Output: [{node_key, name, layer, depth, impact_score, trust_chain, path, edge_types}]

1. Resolve changed_node_key → node_id
2. BFS following INCOMING edges (who depends on this node)
3. Per edge, apply policy:
   - Skip if policy.IsStructural(edge_type) AND !include_structural
   - Skip if !policy.AllowsReverseImpact(edge_type)
   - Skip if derivation_filter set and not matching
4. Score computation:
   trust_chain = Π(edge.trust_score) along path
   impact_score = 100 × trust_chain × 0.95^(depth - 1)
5. Filter: impact_score >= min_score
6. Sort: descending score, ascending depth
7. Return top_k results
```

**Key insight**: Impact score uses `trust_score` (which incorporates evidence freshness), not raw `confidence`. This means stale edges contribute less impact, reflecting uncertainty about whether the relationship still holds.

### 4.4 Trust Computation

**Purpose**: Compute multi-dimensional trust for nodes and edges from their evidence.

```
Algorithm: Evidence-based trust scoring
Input: evidence[] for a given node or edge
Output: (confidence, freshness, trust_score, status)

Step 1 — Partition evidence:
  positive_valid = evidence WHERE polarity='positive' AND status IN ('valid','revalidated')
  negative_valid = evidence WHERE polarity='negative' AND status IN ('valid','revalidated')

Step 2 — Combine confidence (independent OR):
  positive_confidence = 1 - Π(1 - e.confidence) for e in positive_valid
  negative_confidence = 1 - Π(1 - e.confidence) for e in negative_valid

Step 3 — Base confidence:
  base_confidence = positive_confidence × (1 - negative_confidence)

Step 4 — Freshness (confidence-weighted average):
  For each positive evidence e:
    freshness(e) = 1.0 if status ∈ {valid, revalidated}
                 = 0.5 if status = stale
                 = 0.0 if status ∈ {invalidated, superseded}
  weighted_freshness = Σ(freshness(e) × e.confidence) / Σ(e.confidence)
  If any positive evidence is stale: cap freshness at 0.6
  If negative_confidence >= 0.8: force freshness = 0.0

Step 5 — Trust score:
  trust_score = base_confidence × freshness

Step 6 — Status determination:
  if negative_confidence >= 0.8 → "contradicted"
  else if has_valid_positive    → "active"
  else if has_stale_positive    → "stale"
  else if all_invalidated       → "removed"
  else                          → "unknown"
```

**Design rationale**:

- **OR combination** for positive evidence means multiple independent observations strengthen confidence (seeing a dependency in 3 different files is stronger than 1).
- **Negative evidence** actively reduces confidence rather than merely lowering it — contradicting evidence at 0.8+ confidence triggers "contradicted" status.
- **Freshness** is separate from confidence because a fact can be highly confident (hard derivation from AST) but stale (the file was modified since extraction).
- **0.6 freshness cap** when any evidence is stale ensures that even one stale source noticeably degrades trust, preventing false confidence from a mix of stale and valid evidence.

### 4.5 Incremental Invalidation

**Purpose**: Efficiently update the graph when files change without re-scanning everything.

```
Algorithm: File-based invalidation + targeted recalculation
Input: changed_files[], revision_id
Output: {stale_evidence, affected_edges, affected_nodes, files_to_rescan}

1. For each changed file:
   UPDATE graph_evidence SET evidence_status = 'stale'
   WHERE file_path = changed_file AND evidence_status IN ('valid', 'revalidated')

2. Collect distinct edge_ids and node_ids from affected evidence

3. For each affected edge:
   - Fetch all evidence for this edge
   - Recompute trust (Section 4.4)
   - Update edge.confidence, edge.freshness, edge.trust_score, edge.status

4. For each affected node:
   - Same trust recomputation

5. Return list of file paths with stale evidence (files_to_rescan)
```

**Key invariant**: Stale evidence is never auto-deleted. It persists until:
- Re-validated (same fact extracted again in a new scan → status = 'revalidated')
- Explicitly invalidated (negative evidence submitted)
- Superseded (new evidence at a different location for the same fact)

This prevents information loss from incomplete incremental scans.

---

## 5. Consistency Model

### 5.1 Transaction Boundaries

**Import atomicity**: The entire `ImportAll` operation (nodes + edges + evidence) runs in a single SQLite transaction. If any node or edge fails validation, the entire batch rolls back. Evidence is non-fatal — missing targets are silently skipped within the same transaction.

```go
store.WithTx(func(tx *Store) error {
    // All nodes must succeed
    // All edges must succeed
    // Evidence: skip on missing target (non-fatal)
    return nil // commit
})
```

### 5.2 Idempotency Guarantees

- **Nodes**: Upsert by `node_key`. Immutable fields (layer, node_type, domain_key) must match or the operation is rejected. Mutable fields are overwritten.
- **Edges**: Upsert by `edge_key`. Same immutability rules apply to from/to nodes and edge_type.
- **Evidence**: Deduplicated by composite key. Matching evidence updates `observed_at` and `confidence`. Non-matching creates a new row.

This means repeated scans of the same codebase produce the same graph state (modulo `last_seen_revision_id` timestamps).

### 5.3 Stale Management

Entities follow a lifecycle:

```
                ┌─────── new evidence ──────┐
                ▼                            │
active ──► stale ──► revalidated ──► active (via re-extraction)
              │
              ▼
         contradicted (via negative evidence with confidence >= 0.8)
```

**Mark-stale**: At scan finalization, nodes not seen in the current revision are marked stale. Edges not seen become `active = false`.

**No auto-deletion**: Stale entities persist indefinitely. This is deliberate — incomplete scans (Claude skipping a file) should not destroy prior knowledge.

### 5.4 Concurrent Access

SQLite WAL mode allows:
- Multiple concurrent readers (admin dashboard, queries)
- Single writer at a time (import operations)
- Readers don't block writers

The admin dashboard's WebSocket polling reads from the same database without interfering with active MCP import operations.

---

## 6. Type Registry

### 6.1 Purpose

The registry is a YAML-defined schema that constrains what can be stored in the graph. It prevents invalid combinations like a `data → data` edge with type `INJECTS` (which is only valid for `code → code`).

### 6.2 Validation Rules

On every import:

1. `node.layer` must exist in registry
2. `node.node_type` must be defined under that layer
3. `edge.edge_type` must be a registered edge type
4. `edge.from_layer → edge.to_layer` must be in the allowed pairs for that edge type
5. `edge.derivation_kind` must be one of: hard, linked, inferred, unknown
6. `evidence.source_kind` must be one of: ast, pattern, convention, manual, comment

### 6.3 Extensibility

The registry is embedded at build time (`defaults.yaml`) but can be overridden per-project via `.depbot/oracle.types.yaml`. This allows projects to define custom layers, node types, or edge types.

---

## 7. Extraction Model

### 7.1 Streaming Architecture

Oracle never sees source code. It receives structured facts. The extraction architecture is designed to avoid context window exhaustion:

```
┌────────────────────────────────────────────────┐
│  Per-file extraction loop (inside Claude):     │
│                                                │
│  for each file:                                │
│    1. Read file into context                   │
│    2. Extract nodes, edges, evidence           │
│    3. Call oracle_import_all (send to Oracle)   │
│    4. Discard file from context                │
│                                                │
│  Never accumulates multiple files.             │
└────────────────────────────────────────────────┘
```

For large codebases (≥5 modules), Claude spawns parallel agents — one per service/repository — each with its own context window.

### 7.2 Evidence Provenance

Every fact in the graph traces back to source:

```
Evidence record:
  - file_path: "src/users/users.controller.ts"
  - line_start: 12, line_end: 14
  - source_kind: "ast" | "pattern" | "convention" | "manual"
  - extractor_id: "claude"
  - extractor_version: "0.1.0"
  - commit_sha: "abc123..."
  - confidence: 0.95
  - polarity: "positive" | "negative"
```

This means any fact can be audited: "Why does the graph say X depends on Y?" → "Because file Z, lines 12-14, observed in commit abc123."

### 7.3 Self-Learning

After each import, an auto-discovery system detects quality gaps:

- Nodes without any evidence (unsubstantiated claims)
- Controllers without endpoint edges (likely missing extraction)
- Multiple services without cross-service edges (likely missing pass)
- Modules without containment edges

These discoveries are stored and fed back to Claude on the next scan, enabling iterative improvement without code changes.

---

## 8. Query Interface

### 8.1 MCP Tools (25+)

The system exposes all functionality via MCP tools, making it accessible to any MCP-compatible client:

| Category | Tools |
|----------|-------|
| Mutation | `revision_create`, `import_all`, `node_upsert`, `edge_upsert`, `evidence_add`, `snapshot_create`, `stale_mark` |
| Query | `query_deps`, `query_reverse_deps`, `query_path`, `query_stats`, `impact` |
| Discovery | `report_discovery`, `get_discoveries`, `scan_status` |
| Language | `define_term`, `get_glossary`, `check_language` |
| Admin | `extraction_guide`, `save_manifest`, `admin_url`, `reset_db` |

### 8.2 Admin Dashboard

An embedded HTTP server + SPA provides visual exploration:

- **Port assignment**: `4200 + FNV32(abs_project_path) % 800` — deterministic, allows multiple projects simultaneously
- **Real-time**: WebSocket broadcasts MCP requests as they arrive
- **Views**: Tree hierarchy, force-directed graph (D3.js), filterable by layer/type
- **Audit**: Full MCP request log with params, results, duration

### 8.3 CLI

Direct command-line access to all graph operations for scripting and debugging:

```
oracle mcp serve       # Start MCP server + admin dashboard
oracle query deps X    # Forward BFS
oracle query path A B  # Find connections
oracle impact X        # Blast radius
oracle validate        # Check graph integrity
```

---

## 9. Design Trade-offs

| Decision | Trade-off | Rationale |
|----------|-----------|-----------|
| SQLite (not Postgres/Neo4j) | No multi-node scaling, no native graph traversal | Zero-config, single file, fast for medium graphs (<100k nodes), portable |
| BFS in application layer | O(V+E) with per-hop DB queries | Simpler than maintaining an in-memory graph replica; SQLite is fast enough for sub-second on typical codebases |
| LLM as extractor | Non-deterministic, slower | Far more capable than AST-only tools for cross-service inference and heuristic relationships |
| Evidence over assertions | Storage overhead per fact | Enables trust computation, audit trails, contradiction detection |
| Stale-not-deleted | Graph may contain outdated information | Prevents information loss from incomplete scans; degraded freshness signals uncertainty |
| Single-process | No horizontal scaling | Designed for developer workstation, not cloud deployment |
| WAL mode | Slightly more disk usage | Concurrent readers during writes (dashboard + imports simultaneously) |

---

## 10. Failure Modes and Mitigations

| Failure | Impact | Mitigation |
|---------|--------|------------|
| Claude extracts wrong relationship | Bad edge in graph | Evidence system enables contradiction; low derivation_kind → low confidence; next scan may correct |
| Incomplete scan (Claude stops mid-file) | Missing nodes/edges | Stale-not-deleted preserves prior state; incremental scan fills gaps |
| SQLite corruption | Total data loss | WAL mode is crash-safe; rebuild from re-scan (graph is reproducible from source code) |
| Type registry mismatch | Import rejection | Immediate error with context; registry is extensible |
| Concurrent MCP calls | Race condition on writes | SQLite WAL serializes writes; reads are non-blocking |

---

## 11. Performance Characteristics

**Import throughput**: ~1000 nodes/sec on typical hardware (validated, transactional SQLite writes with index maintenance).

**Query latency**:
- Stats: O(N) scan of nodes table (one query)
- Deps BFS (depth 3): Sub-millisecond for typical codebases (<1000 nodes)
- Path query: Proportional to graph connectivity; bounded by max_depth
- Impact analysis: Same as reverse BFS, plus scoring computation

**Storage footprint**: ~100 bytes per node, ~80 bytes per edge, ~200 bytes per evidence record. A 1000-node graph with full evidence occupies ~1MB.

**Memory**: Oracle holds no in-memory graph replica. All traversal is via SQLite queries. Memory footprint is proportional to BFS queue depth, not graph size.

---

## 12. Security Considerations

- Database is local-only (`.depbot/oracle.db`), no network exposure
- Admin dashboard binds to `localhost` only
- No authentication on admin API (local development tool)
- MCP communication via stdio (no network)
- Evidence stores file paths and line numbers, not code content
- `snippet_hash` allows verification without storing code
