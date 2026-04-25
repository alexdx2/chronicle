# Oracle — Architecture & Algorithm

## What is Oracle

Oracle is an MCP server that maintains a **knowledge graph** of a codebase in SQLite. Claude Code is the intelligence — it reads and understands code. Oracle is the memory — it stores, validates, and queries the structured result.

```
┌─────────────────────┐     stdio (MCP protocol)     ┌─────────────────────┐
│     Claude Code     │ ◄──────────────────────────► │    Oracle CLI       │
│                     │                               │                     │
│  • Reads files      │   oracle_import_all           │  • Validates input  │
│  • Understands code │   oracle_query_deps           │  • Stores in SQLite │
│  • Extracts graph   │   oracle_impact               │  • Traverses graph  │
│  • Reports findings │   oracle_define_term           │  • Checks language  │
│                     │   oracle_report_discovery      │  • Auto-discovers   │
└─────────────────────┘                               │    quality gaps     │
                                                      │                     │
                                                      │  HTTP :4200+        │
                                                      │  • Admin dashboard  │
                                                      │  • WebSocket live   │
                                                      └─────────────────────┘
```

## Data Model

### SQLite Schema (8 tables)

```
.depbot/oracle.db
├── graph_nodes           — entities (models, controllers, endpoints, services)
├── graph_edges           — relationships (INJECTS, CALLS_SERVICE, REFERENCES_MODEL)
├── graph_evidence        — proof (file_path + line_number for each fact)
├── graph_revisions       — scan history (git SHA, timestamp, mode)
├── graph_snapshots       — point-in-time counts
├── graph_discoveries     — self-learning findings (system + Claude + user)
├── domain_language       — ubiquitous language glossary
└── mcp_request_log       — MCP call log (tool, params, result, duration)
```

### Node Structure

```
node_key:    "code:controller:myapp:userscontroller"  (layer:type:domain:name)
layer:       code | data | service | contract | flow | ownership | infra | ci
node_type:   controller | provider | model | endpoint | topic | service | ...
domain_key:  "myapp"
name:        "UsersController"
file_path:   "src/users/users.controller.ts"
status:      active | stale | deleted
confidence:  0.0 - 1.0
```

### Edge Structure

```
edge_key:         "from_key->to_key:EDGE_TYPE"  (auto-generated)
edge_type:        INJECTS | CONTAINS | EXPOSES_ENDPOINT | CALLS_SERVICE | ...
derivation_kind:  hard | linked | inferred | unknown
from_layer/to_layer:  validated against type registry
confidence:       0.0 - 1.0
active:           true/false (stale edges become inactive)
```

### Graph Layers

```
DATA         Prisma models, entities, enums, fields
  │            User, Order, Merchant, Product
  │ USES_MODEL, REFERENCES_MODEL
  │
CODE         Modules, controllers, providers, resolvers, guards
  │            UserController, OrderService, AuthGuard
  │ INJECTS, CONTAINS
  │
CONTRACT     HTTP endpoints, GraphQL operations, Kafka topics
  │            POST /orders, Query getUser, topic: order-created
  │ EXPOSES_ENDPOINT, PUBLISHES_TOPIC, CONSUMES_TOPIC
  │
SERVICE      Deployable services
               api-service, payments-service, notifications
               CALLS_SERVICE, CALLS_ENDPOINT (linked, via env URLs)
```

### Edge Types & Traversal Policy

```
Edge Type          │ From → To          │ Derivation │ Forward Path │ Reverse Impact
───────────────────┼────────────────────┼────────────┼──────────────┼───────────────
INJECTS            │ code → code        │ hard       │ yes          │ yes
CONTAINS           │ any → any          │ hard       │ no (struct)  │ no
EXPOSES_ENDPOINT   │ code → contract    │ hard       │ yes          │ no
CALLS_SERVICE      │ code → service     │ linked     │ yes          │ yes
CALLS_ENDPOINT     │ code → contract    │ linked     │ yes          │ yes
PUBLISHES_TOPIC    │ code → contract    │ hard       │ yes          │ no
CONSUMES_TOPIC     │ code → contract    │ hard       │ yes          │ yes
USES_MODEL         │ code → data        │ hard       │ yes          │ yes
REFERENCES_MODEL   │ data → data        │ hard       │ yes          │ yes
DEFINES_MODEL      │ code → data        │ hard       │ yes          │ yes
HAS_FIELD          │ data → data        │ hard       │ no (struct)  │ no
```

**Structural edges** (CONTAINS, HAS_FIELD) are excluded from path/impact queries by default.
**No-reverse-impact edges** (EXPOSES_ENDPOINT, PUBLISHES_TOPIC) don't propagate impact backward.

## Algorithm: Scan

### Phase 0: Startup

```
1. Claude Code starts: oracle mcp serve
2. Oracle:
   - Creates .depbot/ if not exists
   - Opens SQLite DB (auto-migrates schema)
   - Starts MCP server (stdio)
   - Starts admin dashboard (HTTP, auto-derived port from project path)
```

### Phase 1: Onboarding Detection

```
Claude calls: oracle_scan_status

If graph is empty (first run):
  → Returns { onboarding: { is_first_run: true, ask_user: "..." } }
  → Claude asks user: "Want me to scan this project?"

If graph exists:
  → Returns node/edge counts, last revision, discoveries count
  → Claude decides: full rescan or incremental
```

### Phase 2: Discovery & Manifest

```
Claude calls: oracle_get_discoveries
  → Reads findings from previous scans (corrections, patterns, insights)
  → Applies learned knowledge to this scan

Claude auto-discovers project:
  → Glob for package.json, go.mod, prisma/schema.prisma, tsconfig.json, Dockerfile
  → Identifies domain name, repositories, tech stack

Claude calls: oracle_save_manifest
  → Writes .depbot/oracle.domain.yaml
```

### Phase 3: Extraction (the main loop)

```
Claude calls: oracle_revision_create
  → Creates revision record with git SHA

For each repository:
  If large project (>= 5 modules):
    Claude spawns Agent per service/repo
    Each agent works independently with fresh context

  Per-file extraction (streaming — never accumulate):
    ┌─────────────────────────────────────────┐
    │ 1. Read ONE file                        │
    │ 2. Extract nodes + edges + evidence     │
    │ 3. Call oracle_import_all IMMEDIATELY    │
    │ 4. Forget file contents, move to next   │
    └─────────────────────────────────────────┘

  Extraction order:
    Pass 1: Data models (prisma/schema.prisma → models, enums, relations)
    Pass 2: Code structure (*.module.ts, *.controller.ts, *.service.ts → CONTAINS, INJECTS)
    Pass 3: Contracts (route decorators → endpoints, Kafka → topics)
    Pass 4: Cross-service (HTTP clients with env URLs → CALLS_SERVICE, CALLS_ENDPOINT)
```

### Phase 4: Validation (inside Oracle)

```
Every oracle_import_all call goes through:

1. Key normalization
   "Code:Controller:MyApp:UsersController" → "code:controller:myapp:userscontroller"

2. Type registry check
   - layer must be valid (code, data, service, contract, ...)
   - node_type must exist under that layer
   - edge_type must be valid
   - from_layer → to_layer must be allowed for that edge type

3. Idempotent upsert
   - Same node_key → update mutable fields (name, file_path, confidence)
   - Immutable fields (layer, node_type, domain) must match or reject
   - Same edge_key → update derivation, confidence, metadata

4. Evidence dedup
   - Same (target, source_kind, file_path, line_start, extractor_id) → update
   - Different line → new evidence entry

5. Transaction
   - Entire import is atomic — one bad entry rolls back everything
   - Evidence is non-fatal — skips missing nodes/edges silently
```

### Phase 5: Auto-Discovery (system)

```
After each oracle_import_all, middleware automatically:

1. Checks for nodes without evidence
   → Discovery: "49 nodes without evidence"

2. Checks for controllers without EXPOSES_ENDPOINT edges
   → Discovery: "4 controllers but 0 EXPOSES_ENDPOINT edges"

3. Checks for multiple services without cross-service edges
   → Discovery: "4 services but no cross-service edges"

4. Checks for modules without CONTAINS edges
   → Discovery: "1 module without CONTAINS edges"

5. Reports overall scan quality
   → Discovery: "Scan quality: good — 518 nodes, 853 edges"
```

### Phase 6: Claude Discoveries

```
After scanning, Claude calls oracle_report_discovery for:

- unknown_pattern: code patterns it couldn't classify
  "TomEventHandler uses @OnEvent but is not in module providers"

- missing_edge: relationships it suspects but can't confirm
  "BattleEvent likely references Cat and Mouse via tomId/jerryId"

- pattern: overall assessment
  "Monorepo with single NestJS API backend"
```

### Phase 7: Domain Language

```
Claude calls: oracle_get_glossary
  → Reads existing terms

Claude calls: oracle_define_term (for each key concept)
  → Term: "Order", Context: "ordering"
  → Aliases: ["Purchase Order"]
  → Anti-patterns: ["Purchase", "Booking"]
  → Examples: ["OrderService", "OrderResolver"]

Claude calls: oracle_check_language
  → Scans all node names against anti-patterns
  → Returns violations: "PurchaseService uses 'Purchase' → should use 'Order'"
```

### Phase 8: Finalize

```
Claude calls: oracle_snapshot_create
  → Records point-in-time node/edge counts

Claude calls: oracle_stale_mark
  → Nodes not seen in this revision → status = "stale"
  → Edges not seen → active = false
```

## Algorithm: Query

### Dependency Query (oracle_query_deps)

```
BFS from start node, following outgoing edges.
- Skips inactive edges
- Applies derivation filter (hard, linked, inferred)
- Returns: [{node_key, name, layer, node_type, depth}]
```

### Reverse Dependency Query (oracle_query_reverse_deps)

```
Same BFS but following incoming edges.
"Who depends on X?"
```

### Path Query (oracle_query_path)

```
BFS with full path tracking.

Mode "directed": follows edges in natural direction only
  → Produces meaningful dependency chains
  → A INJECTS→ B INJECTS→ C CALLS_SERVICE→ D

Mode "connected": follows edges in both directions
  → Finds any topological relationship
  → Useful for: Producer → topic ← Consumer

Scoring:
  path_score = Π(edge_confidence) × 0.95^(depth-1)
  path_cost = Σ(-ln(edge_confidence)) + 0.05 × depth

Filtering:
  - Structural edges (CONTAINS) excluded by default
  - Derivation filter: hard, linked, inferred
  - --include-structural to override

Tie-breaking: lower cost → lower depth → lexicographic
```

### Impact Query (oracle_impact)

```
Reverse BFS with traversal policy:
  - Structural edges → EXCLUDED
  - No-reverse-impact edges (EXPOSES_ENDPOINT, PUBLISHES_TOPIC) → EXCLUDED
  - Only edges where policy.AllowsReverseImpact() = true

Scoring:
  impact_score = 100 × Π(edge_confidence) × 0.95^(depth-1)

Result sorted by score descending.

Example: "What breaks if User model changes?"
  → UserService (USES_MODEL, depth 1, score 100)
  → UserResolver (INJECTS, depth 2, score 95)
  → Query getUser (EXPOSES_ENDPOINT → excluded from reverse)
```

## Algorithm: Admin Dashboard

```
oracle mcp serve
  ├── MCP server (stdio) — Claude communicates here
  ├── HTTP server (localhost:4200+) — browser dashboard
  │   ├── GET /api/stats         → graph statistics
  │   ├── GET /api/requests      → MCP request log
  │   ├── GET /api/graph         → full graph data for visualization
  │   ├── GET /api/low-confidence → edges below threshold
  │   ├── GET /api/discoveries   → self-learning findings
  │   ├── GET /api/glossary      → domain language terms
  │   ├── GET /api/language-check → naming violations
  │   ├── GET /api/metrics       → efficiency metrics
  │   ├── POST /api/validate     → graph integrity check
  │   ├── GET/PUT /api/manifest  → domain manifest editor
  │   ├── POST /api/glossary/save → add/edit term
  │   ├── POST /api/glossary/delete → delete term
  │   ├── POST /api/glossary/dismiss → remove anti-pattern
  │   └── WS /ws                 → real-time MCP request stream
  └── Polling goroutine
      → Every 1s: checks mcp_request_log for new entries
      → Broadcasts to WebSocket clients
```

### Port Assignment

```
Each project gets a stable port derived from its directory path:
  port = 4200 + FNV32(abs_path) % 800

/home/alex/personal/depbot    → :4778
/home/alex/personal/otopoint  → :4970
/home/alex/personal/other     → :4593

Multiple dashboards can run simultaneously.
```

## Algorithm: Self-Learning Loop

```
Scan 1:
  Claude extracts graph → system detects gaps → discoveries stored

Scan 2:
  Claude reads discoveries → applies corrections → better extraction
  → System detects fewer gaps → new discoveries

Scan N:
  System knowledge accumulates:
  - Which patterns Claude misses (system discoveries)
  - Which patterns are unusual (Claude discoveries)
  - What the user told us (user insights)
  - What the correct domain language is (glossary)

  Each scan starts with oracle_get_discoveries
  → Claude adjusts its extraction based on past findings
```

## File Layout

```
.depbot/                          Created automatically per-project
├── oracle.db                     SQLite database (all data)
├── oracle.domain.yaml            Domain manifest (auto-discovered)
└── oracle.types.yaml             Type registry (defaults embedded)

cmd/oracle/main.go                CLI entrypoint

internal/
├── admin/                        Admin dashboard
│   ├── server.go                 HTTP server + API handlers
│   ├── websocket.go              WebSocket hub
│   └── static/index.html         Embedded SPA (Alpine.js + D3.js)
│
├── cli/                          Cobra commands
│   ├── root.go                   Global flags, openGraph(), ensureDepbotDir()
│   ├── mcp.go                    oracle mcp serve (+ admin + browser open)
│   ├── admin.go                  oracle admin (standalone dashboard)
│   ├── query.go                  oracle query deps/reverse-deps/path/stats
│   ├── impact.go                 oracle impact
│   ├── node.go                   oracle node upsert/get/list/delete
│   ├── edge.go                   oracle edge upsert/get/list/delete
│   ├── evidence.go               oracle evidence add/list
│   ├── init.go                   oracle init
│   └── ...
│
├── graph/                        Graph operations
│   ├── graph.go                  Facade: validate → store
│   ├── import.go                 Bulk import (transactional, streaming)
│   ├── path.go                   BFS path query (directed/connected)
│   ├── impact.go                 Reverse BFS impact analysis
│   └── query.go                  Deps, reverse-deps, stats
│
├── mcp/                          MCP server
│   ├── server.go                 25+ tool definitions + handlers
│   ├── middleware.go             Request logging + auto-discovery
│   ├── guide.go                  Extraction guide (compact + detailed)
│   └── commands.go               User command definitions
│
├── registry/                     Type system
│   ├── registry.go               Loader + validators
│   ├── defaults.go               Embedded default YAML
│   └── defaults.yaml             8 layers, 50+ node types, 40+ edge types
│
├── store/                        SQLite persistence
│   ├── store.go                  Schema + migrations + DBTX interface
│   ├── nodes.go                  Node CRUD + upsert + stale marking
│   ├── edges.go                  Edge CRUD + upsert + stale marking
│   ├── evidence.go               Evidence CRUD + dedup
│   ├── revisions.go              Revision tracking
│   ├── snapshots.go              Snapshot recording
│   ├── discoveries.go            Self-learning storage
│   ├── language.go               Domain language glossary + violations
│   ├── requestlog.go             MCP request logging
│   └── metrics.go                Scan efficiency metrics
│
├── validate/                     Input validation
│   ├── keys.go                   Key normalization (layer:type:domain:name)
│   └── validate.go               Field validation for nodes/edges/evidence
│
└── manifest/                     Domain manifest parser
    └── manifest.go               YAML loader + validation
```

## MCP Tools (25+)

```
Discovery & Setup:
  oracle_extraction_guide    — methodology (compact ~760 tokens, detailed per tech)
  oracle_scan_status         — graph state + onboarding detection
  oracle_save_manifest       — auto-save project config
  oracle_command             — execute named commands (scan, data, language, ...)
  oracle_admin_url           — dashboard URL
  oracle_reset_db            — fresh start

Graph Mutation:
  oracle_revision_create     — start a scan
  oracle_import_all          — bulk import (validated, transactional)
  oracle_node_upsert         — single node
  oracle_edge_upsert         — single edge
  oracle_evidence_add        — add provenance
  oracle_snapshot_create     — record scan result
  oracle_stale_mark          — flag removed entities
  oracle_node_list/get       — read nodes
  oracle_edge_list           — read edges

Graph Query:
  oracle_query_deps          — forward dependencies (BFS)
  oracle_query_reverse_deps  — reverse dependencies (BFS)
  oracle_query_path          — path between nodes (directed/connected)
  oracle_query_stats         — aggregate statistics
  oracle_impact              — blast radius analysis

Self-Learning:
  oracle_report_discovery    — Claude reports findings
  oracle_get_discoveries     — read previous findings

Domain Language:
  oracle_define_term         — add/update glossary term
  oracle_get_glossary        — read glossary
  oracle_check_language      — find naming violations
```
