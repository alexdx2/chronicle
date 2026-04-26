# Oracle MCP Server — Architecture Overview

## System Diagram

```
Claude (LLM)
  ↕ stdio (JSON-RPC 2.0)
Oracle MCP Server
  ├── 26 Tool Handlers (server.go)
  ├── Middleware: logging + auto-discovery (middleware.go)
  ├── Extraction Guide System (guide.go)
  ├── User-Facing Commands (commands.go)
  │
  ├── graph.Graph — validation + query engine
  │   ├── Import Pipeline (import.go)
  │   ├── Dependency Traversal — BFS (query.go)
  │   ├── Path Finding — scored BFS (path.go) [internal/graph/path.go not in dir listing but referenced]
  │   ├── Impact Analysis — reverse BFS with trust (impact.go)
  │   ├── Trust Computation — evidence-based (trust.go)
  │   └── Incremental Invalidation (invalidation.go)
  │
  ├── store.Store — SQLite persistence (9 tables)
  │   ├── Nodes, Edges, Evidence, Revisions
  │   ├── Snapshots, Discoveries, Glossary
  │   ├── Request Log, Project Settings
  │   └── Transactional bulk imports
  │
  ├── registry.Registry — type system from YAML
  │   ├── 8 layers, 30+ node types
  │   ├── 40+ edge types with layer constraints
  │   └── Traversal policy (structural vs. semantic edges)
  │
  └── admin.Server — HTTP dashboard (goroutine)
      ├── REST API (/api/graph, /api/stats, etc.)
      ├── WebSocket for live request log
      └── Workspace visualization (D3.js)
```

## Startup Sequence

**`internal/cli/mcp.go:41-93`**

1. `openGraph()` — opens `.depbot/oracle.db`, loads `registry/defaults.yaml`
2. `SetManifestPath()` — path to `oracle.domain.yaml`
3. `SetGuideStore()` — enables custom extraction prompts
4. `NewServerWithLogging(g, g.Store())` — creates server, wraps all 26 tools with logging middleware
5. Derive admin port: `4200 + fnv32a(abs_path) % 800` → deterministic port per directory
6. `admin.NewServer(...)` — start HTTP dashboard in goroutine
7. `server.ServeStdio(s)` — start MCP stdio loop (blocks)

## Request Flow

```
1. Claude sends JSON-RPC:  {"method":"tools/call","params":{"name":"oracle_import_all","arguments":{...}}}
2. mcp-go library routes to registered handler
3. loggingWrap middleware:
   ├── Records start time
   ├── Calls actual handler
   ├── Logs to mcp_request_log table (tool, params, result, duration)
   ├── Broadcasts to admin WebSocket
   └── If oracle_import_all: triggers async autoDiscover()
4. Handler: parse args → validate via registry → call graph methods → return JSON
5. Response sent via stdout
```

---

## Tool Catalog (26 Tools)

### Lifecycle (track scan passes)

| Tool | Required Params | Returns | Purpose |
|------|----------------|---------|---------|
| `oracle_revision_create` | domain, after_sha | `{revision_id}` | Start a scan pass |
| `oracle_snapshot_create` | domain, revision_id, node_count, edge_count | `{snapshot_id}` | Record point-in-time counts |
| `oracle_stale_mark` | domain, revision_id | `{stale_nodes, stale_edges}` | Mark unseen entities stale |

### Core Mutation (build graph)

| Tool | Required Params | Returns | Purpose |
|------|----------------|---------|---------|
| `oracle_node_upsert` | revision_id, name | `{node_id}` | Create/update node. Immutable: layer, type, domain |
| `oracle_edge_upsert` | revision_id, derivation_kind | `{edge_id}` | Create/update edge. Auto-maps confidence from derivation |
| `oracle_evidence_add` | extractor_id, extractor_version | `{evidence_id}` | Attach provenance. polarity=negative with conf≥0.8 contradicts |
| `oracle_import_all` | revision_id, payload (JSON) | `{nodes_created, edges_created, evidence_created}` | Bulk upsert in transaction. Warns if >15KB payload |

### Queries (analyze graph)

| Tool | Required Params | Returns | Purpose |
|------|----------------|---------|---------|
| `oracle_node_list` | — | `[Node]` | Filter by layer/type/domain/status |
| `oracle_node_get` | node_key | `{node, evidence}` | Single node + all evidence |
| `oracle_edge_list` | — | `[Edge]` | Filter by from/to/type |
| `oracle_query_deps` | node_key | `[DepNode]` | Forward BFS. depth default 3 |
| `oracle_query_reverse_deps` | node_key | `[DepNode]` | Reverse BFS |
| `oracle_query_path` | from_node_key, to_node_key | `{paths}` | Scored path finding. Cost=Σ(-ln(conf))+0.05×depth |
| `oracle_query_stats` | — | `{counts}` | Aggregate stats by layer/type/derivation |
| `oracle_impact` | node_key | `{impacts}` | Blast radius. Score=100×Π(trust)×0.95^(depth-1) |

### Incremental Scan

| Tool | Required Params | Returns | Purpose |
|------|----------------|---------|---------|
| `oracle_invalidate_changed` | domain, revision_id, changed_files | `{stale_evidence, files_to_rescan}` | Mark evidence from changed files stale |
| `oracle_finalize_incremental_scan` | domain, revision_id | `{revalidated, still_stale, contradicted}` | Complete incremental, recalc trust |

### Domain Language

| Tool | Required Params | Returns | Purpose |
|------|----------------|---------|---------|
| `oracle_define_term` | domain, term, description | `{term_id}` | Add/update glossary term |
| `oracle_get_glossary` | — | `[Term]` | List terms with aliases, anti-patterns |
| `oracle_check_language` | — | `{violations}` | Scan node names against anti-patterns |

### Metadata & Commands

| Tool | Required Params | Returns | Purpose |
|------|----------------|---------|---------|
| `oracle_extraction_guide` | — | JSON text | Scan methodology (compact or per-tech) |
| `oracle_scan_status` | — | `{status}` | Graph state + onboarding detection |
| `oracle_admin_url` | — | `{url}` | Dashboard URL |
| `oracle_command` | command | `{instructions}` | User-facing command instructions |
| `oracle_save_manifest` | content (YAML) | `{status}` | Save oracle.domain.yaml |
| `oracle_reset_db` | — | `{status}` | Drop & recreate all tables |
| `oracle_report_discovery` | domain, category, title, description | `{discovery_id}` | Record learning |
| `oracle_get_discoveries` | — | `[Discovery]` | Retrieve discoveries |

---

## Type System

**Source:** `internal/registry/defaults.yaml`

### Layers (8)

`code` · `service` · `contract` · `data` · `flow` · `ownership` · `infra` · `ci`

### Node Types by Layer

| Layer | Types |
|-------|-------|
| **code** | repository, package, module, file, symbol, controller, provider, resolver |
| **service** | service, worker, job, deployment, external_system |
| **contract** | http_api, endpoint, graphql_schema, graphql_operation, graphql_type, topic, async_channel, schema_subject, schema_version |
| **data** | model, entity, field, enum, relation, database, table |
| **flow** | flow, flow_step, use_case, usecase, state_transition, invariant, trigger, outcome |
| **ownership** | domain, team, owner |
| **infra** | terraform_module, k8s_object, k8s_service, k8s_deployment |
| **ci** | workflow, pipeline, release |

### Edge Types (40+)

#### Code → Code
`CONTAINS` · `IMPORTS` · `EXPORTS` · `DECLARES` · `INJECTS` · `CALLS_SYMBOL`

#### Code/Service → Contract
`EXPOSES_ENDPOINT` · `HANDLES_OPERATION` · `CALLS_ENDPOINT` · `PUBLISHES_TOPIC` · `CONSUMES_TOPIC`

#### Code/Service → Service
`CALLS_SERVICE`

#### Code → Data
`USES_MODEL` · `DEFINES_MODEL`

#### Data → Data
`HAS_FIELD` · `REFERENCES_MODEL` · `USES_ENUM` · `STORED_IN`

#### Contract → Contract
`RETURNS_TYPE` · `USES_SCHEMA` · `REGISTERS_SUBJECT`

#### Flow edges
`PART_OF_FLOW` · `PRECEDES` · `EMITS_AFTER` · `REQUIRES` · `TRIGGERS_FLOW` · `PRODUCES_OUTCOME` · `TRANSITIONS_TO` · `INVOKES` · `DEPENDS_ON`

#### Infra/CI
`DEPLOYS_AS` · `TARGETS_SERVICE` · `SELECTS_PODS` · `ROUTES_TO` · `READS_DB` · `WRITES_DB` · `USES_QUEUE` · `TRIGGERS_ANALYSIS` · `BUILDS_ARTIFACT` · `DEPLOYS_RESOURCE` · `READS_OUTPUT`

#### Ownership
`OWNED_BY` · `MAINTAINED_BY` · `DEPENDS_ON_DOMAIN`

### Structural vs Semantic Edges

**Structural** (excluded from dependency queries by default):
`CONTAINS`, `HAS_FIELD`, `DEPLOYS_AS`, `SELECTS_PODS`, `OWNED_BY`, `MAINTAINED_BY`, `DEPENDS_ON_DOMAIN`, `PART_OF_FLOW`, `BUILDS_ARTIFACT`, `DEPLOYS_RESOURCE`

**No Reverse Impact** (excluded from impact analysis):
`EXPOSES_ENDPOINT`, `PUBLISHES_TOPIC`, `TRIGGERS_ANALYSIS`, `EMITS_AFTER`

### Derivation Kinds → Confidence

| Kind | Confidence | Meaning |
|------|-----------|---------|
| `hard` | 0.95 | AST-level fact (import statement, decorator) |
| `linked` | 0.80 | Convention-based (file path pattern, naming) |
| `inferred` | 0.60 | Guessed relationship |
| `unknown` | 0.40 | Unvalidated |

---

## Trust & Evidence System

### Evidence Lifecycle

```
New evidence → status: valid, polarity: positive
  │
  ├─ File changes → status: stale (freshness drops to 0.5)
  │   ├─ Re-scanned, found again → status: revalidated
  │   └─ Not found → stays stale
  │
  ├─ Negative evidence added (polarity: negative, conf ≥ 0.8)
  │   └─ Edge status → contradicted (effective removal)
  │
  └─ Superseded by new evidence at different location → status: superseded
```

### Trust Computation

```
positive_confidence = 1 - Π(1 - cᵢ)    for positive valid/revalidated evidence
negative_confidence = 1 - Π(1 - cᵢ)    for negative valid/revalidated evidence
base_confidence     = positive × (1 - negative)

freshness = weighted_avg(evidence_freshness, weight=confidence)
  where valid/revalidated → 1.0, stale → 0.5, invalidated → 0.0
  capped at 0.6 if ANY evidence is stale

trust_score = base_confidence × freshness
```

### Edge Status (computed from evidence)

| Status | Condition |
|--------|-----------|
| `active` | Has valid/revalidated positive evidence, negative_conf < 0.8 |
| `stale` | Only stale positive evidence |
| `contradicted` | negative_confidence ≥ 0.8 |
| `removed` | All evidence invalidated |
| `unknown` | No evidence |

### Trust Thresholds for Claude

| Trust Score | Action |
|------------|--------|
| ≥ 0.8 | Use directly in answers |
| 0.4 - 0.8 | Mention uncertainty, suggest verification |
| < 0.4 | Read source file to verify before using |

---

## Query Algorithms

### Dependency Traversal (BFS)

**`internal/graph/query.go:43-130`**

- Forward (`QueryDeps`): follows outgoing edges
- Reverse (`QueryReverseDeps`): follows incoming edges
- Parameters: `node_key`, `max_depth` (default 3), `derivation_filter` (csv)
- Excludes structural edges by default (traversal policy)
- Returns: `[{node_key, name, layer, node_type, depth, trust_score, freshness, status}]`

### Path Finding (scored BFS)

**`internal/graph/path.go`**

- Modes: `directed` (forward only), `connected` (forward + reverse)
- Excludes CONTAINS edges unless opted
- **Scoring:**
  - Cost = Σ(-ln(edge_confidence)) + 0.05 × depth
  - Score = Π(edge_confidence) × 0.95^(depth-1)
- Returns top-k paths sorted by cost ASC
- Default: max_depth=6, top_k=3

### Impact Analysis (reverse BFS with trust)

**`internal/graph/impact.go:43-199`**

- Reverse BFS from changed node
- Respects traversal policy (skips structural, no_reverse_impact edges)
- **Impact Score** = 100 × Π(trust_scores along path) × 0.95^(depth-1)
- **Trust Chain** = product of edge trust scores
- Filters by `min_score` (default 0.1), returns `top_k` (default 50)
- Returns: `[{node_key, name, impact_score, trust_chain, path, edge_types, depth}]`

---

## SQLite Schema (9 Tables)

**`internal/store/store.go:114-299`**

| Table | Purpose | Key Columns |
|-------|---------|-------------|
| `graph_revisions` | Track scan passes | revision_id, domain_key, git_before_sha, git_after_sha, trigger_kind, mode |
| `graph_nodes` | Node storage | node_id, node_key (unique), layer, node_type, domain_key, name, confidence, freshness, trust_score, status |
| `graph_edges` | Edge storage | edge_id, edge_key (unique), from_node_id, to_node_id, edge_type, derivation_kind, confidence, freshness, trust_score |
| `graph_evidence` | Provenance | evidence_id, target_kind, node_id/edge_id, source_kind, file_path, line_start, extractor_id, evidence_status, evidence_polarity, confidence |
| `graph_snapshots` | Scan results | snapshot_id, revision_id, domain_key, node_count, edge_count |
| `mcp_request_log` | Audit trail | request_id, tool_name, params_json, result_json, duration_ms |
| `graph_discoveries` | Auto-detected patterns | discovery_id, category, title, description, related_nodes |
| `project_settings` | Key-value config | key, value |
| `domain_language` | Glossary | term_id, domain_key, term, aliases, anti_patterns, examples |

### Key Indexes

- `graph_nodes`: node_key (unique), layer+node_type, domain_key, repo_name+file_path
- `graph_edges`: edge_key (unique), from_node_id, to_node_id, edge_type
- `graph_evidence`: node_id, edge_id, source_kind+repo+file_path

### Evidence Deduplication Key

`(target_kind, node_id/edge_id, source_kind, repo_name, file_path, line_start, extractor_id, evidence_polarity)`

If duplicate: updates `observed_at`, `confidence`, `extractor_version`, status (stale→revalidated)

---

## Extraction Guide System

**`internal/mcp/guide.go`**

### Compact Guide (default, ~1,750 tokens)

Returned by `oracle_extraction_guide()` with no technology param. Contains:

1. **Workflow** (11 steps): discoveries → status → manifest → data scan → code scan → contracts → cross-service → flows → language → discoveries
2. **Key Rules**: node_key format (`layer:type:domain:name`), streaming import (1 file → import immediately, max 10 nodes/call), evidence requirements (file_path, line_start, extractor_id)
3. **Trust thresholds**: ≥0.8 use, 0.4-0.8 caveat, <0.4 verify
4. **User corrections**: negative evidence with source_kind='user_feedback', confidence=0.95
5. **Layer + edge type reference** (compact)

### Detailed Guides (per technology)

| Technology | Focus |
|------------|-------|
| `nestjs` | @Module→CONTAINS, @Injectable→INJECTS, @Controller+@Get/@Post→endpoints, Guards, WebSocket, Cron, Bull queues |
| `prisma` | model→data:model, enum→data:enum, @relation→REFERENCES_MODEL, prisma.model.*→USES_MODEL |
| `openapi` | method+path→contract:endpoint, info→contract:http_api |
| `flow` | Business use cases. Trace mutating endpoints→call chain→name after BUSINESS ACTION. use_case, flow_step, trigger, outcome nodes. TRIGGERS_FLOW, REQUIRES, PRODUCES_OUTCOME, TRANSITIONS_TO edges |

### Custom Instructions

Projects can store custom extraction prompts via `project_settings` table (key: `extraction_prompt`). Appended to default guide.

---

## Middleware: Auto-Discovery

**`internal/mcp/middleware.go:61-235`**

Runs async after each successful `oracle_import_all`. Detects:

| Pattern | Threshold | Category | Action |
|---------|-----------|----------|--------|
| Low-confidence edges | conf < 0.7 | missing_edge | Suggests adding stronger evidence |
| Nodes without evidence | 0 evidence entries | missing_edge | Suggests adding file_path + line_start |
| Orphan modules | Modules without CONTAINS | unknown_pattern | Suggests connecting providers |
| No EXPOSES_ENDPOINT | 0 edges, but controllers exist | scan gap (critical) | Flags incomplete scan |
| No CALLS_SERVICE | 0 edges, but >1 service | scan gap (warning) | Suggests cross-service scan |

Results stored as `graph_discoveries` entries, visible in admin dashboard.

---

## User-Facing Commands

**`internal/mcp/commands.go`**

| Command | Workflow |
|---------|----------|
| `scan` | Full: guide → manifest → import per file → snapshot. Incremental: git diff → invalidate → import changes → negative evidence → finalize |
| `data` | extraction_guide(prisma) → glob schema files → extract models/enums/relations → import |
| `language` | get_glossary → define terms → check_language → report violations |
| `impact` | Ask for node → node_list → oracle_impact depth=4 → explain blast radius |
| `deps` | Ask for node → query_deps forward → query_reverse_deps → explain both |
| `path` | Ask for start/end → query_path directed → fallback connected → explain |
| `flows` | extraction_guide(flow) → read service files → create use_case nodes → edges → report |
| `services` | node_list layer=service → per-service deps → edge_list CALLS → summarize |
| `status` | scan_status → query_stats → discoveries → glossary → admin_url |
| `help` | List all commands |

---

## Key Design Decisions

1. **Evidence-based trust, not binary facts.** Every relationship has provenance (file, line, extractor). Trust decays when evidence goes stale, recovers when revalidated.

2. **Negative evidence over deletion.** Removing a relationship requires explicit negative evidence (polarity=negative, conf≥0.8). This creates an audit trail and prevents accidental removal.

3. **Streaming imports.** Claude sends small batches (~10 nodes per `import_all` call) rather than one giant payload. Keeps token usage low and allows incremental progress.

4. **Registry validation.** Every node type and edge type is validated against `defaults.yaml` layer constraints. Prevents invalid graph structures at write time.

5. **Structural vs semantic edges.** CONTAINS, OWNS, HAS_FIELD etc. are "structural" — excluded from dependency queries and impact analysis by default. Keeps query results focused on meaningful relationships.

6. **Deterministic admin port.** FNV hash of project directory → port 4200-4999. Same project always gets same dashboard URL.

7. **Auto-discovery.** After each import, middleware automatically detects scan quality gaps and records them as discoveries. Claude sees these on next scan and can fill gaps.

8. **Incremental scans.** Git diff → mark evidence from changed files as stale → re-scan only those files → finalize. Avoids full re-scan on every change.
