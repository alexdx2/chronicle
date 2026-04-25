# Domain Oracle CLI — Sub-project 1: Foundation

## Summary

Go CLI tool (`oracle`) that serves as a **validated graph storage and query API** for a multi-layered, evidence-backed knowledge graph. Claude Code (with skills and rules) is the intelligence layer — it reads code, extracts entities/relationships, and calls the CLI to store and query them. The CLI is a strict, deterministic CRUD + query layer over SQLite with built-in validation, type enforcement, and idempotency guarantees.

This is sub-project 1 of 3:
1. **Foundation** (this spec) — Go CLI with SQLite schema, type registry, validation layer, graph CRUD API, bulk import, basic queries, MCP server, domain manifest
2. **Graph Engine** — deps, reverse-deps, path traversal, diff pipeline, impact analysis queries
3. **Claude Skills** — extraction skills, explain, verify, scan orchestration rules

## Core Principle

**Claude = brain, CLI = strict storage API with guardrails.**

Claude Code reads repos and extracts entities. The CLI validates, normalizes, deduplicates, and persists. The CLI never trusts input blindly — it enforces a type registry, key format rules, and idempotency semantics. This prevents graph corruption from inconsistent extraction runs.

## Decisions

| Decision | Choice | Rationale |
|---|---|---|
| Language | Go | Fast CLI, single binary |
| Storage | SQLite (modernc.org/sqlite, pure Go) | Zero-dependency, local-first |
| CLI framework | Cobra | Standard Go CLI |
| Intelligence | Claude Code (skills + rules) | Already understands code, no need for custom extractors |
| Interface | CLI commands + MCP server | CLI for direct use, MCP for Claude Code integration |
| Validation | CLI-side validation layer | Guardrail against LLM extraction instability |
| Type system | YAML type registry file | Strict allowed node_types and edge_types |
| Idempotency | Upsert by stable keys, evidence dedup | Safe for repeated scans |
| Test strategy | Go unit/integration tests | Deterministic, reproducible |

## Architecture

```
cmd/oracle/main.go              # Cobra CLI entrypoint
internal/
  manifest/
    manifest.go                  # oracle.domain.yaml types + loader
  registry/
    registry.go                  # Type registry loader + validator
  validate/
    validate.go                  # Key normalization, field checks, enum validation
    keys.go                      # node_key / edge_key format rules + normalizer
  store/
    store.go                     # DB init, migrations, transaction wrapper
    nodes.go                     # Node CRUD + queries
    edges.go                     # Edge CRUD + queries
    evidence.go                  # Evidence CRUD + queries (with dedup)
    revisions.go                 # Revision tracking
    snapshots.go                 # Snapshot recording
  graph/
    graph.go                     # High-level graph operations (validate -> store)
    query.go                     # deps, reverse-deps, path, impact (sub-project 2)
  mcp/
    server.go                    # MCP server exposing CLI commands as tools
    tools.go                     # Tool definitions with strict input schemas
```

No extractors, no scanner, no Node.js tooling. The Go binary is purely validation + storage + query + MCP.

## Type Registry

A YAML file (`oracle.types.yaml`) that defines all allowed node types, edge types, layers, and valid edge combinations. The CLI refuses any upsert that doesn't conform.

```yaml
version: "1"

layers:
  - code
  - service
  - contract
  - flow
  - ownership
  - infra
  - ci

node_types:
  code:
    - repository
    - package
    - module
    - file
    - symbol
    - controller
    - provider
    - resolver
  service:
    - service
    - worker
    - job
    - deployment
    - external_system
  contract:
    - http_api
    - endpoint
    - graphql_schema
    - graphql_operation
    - graphql_type
    - topic
    - async_channel
    - schema_subject
    - schema_version
  flow:
    - flow
    - flow_step
    - state_transition
    - invariant
  ownership:
    - domain
    - team
    - owner
  infra:
    - terraform_module
    - k8s_object
    - k8s_service
    - k8s_deployment
  ci:
    - workflow
    - pipeline
    - release

edge_types:
  CONTAINS:
    from_layers: [code, service, flow]
    to_layers: [code, service, contract, flow]
  IMPORTS:
    from_layers: [code]
    to_layers: [code]
  EXPORTS:
    from_layers: [code]
    to_layers: [code]
  DECLARES:
    from_layers: [code]
    to_layers: [code]
  INJECTS:
    from_layers: [code]
    to_layers: [code]
  CALLS_SYMBOL:
    from_layers: [code]
    to_layers: [code]
  EXPOSES_ENDPOINT:
    from_layers: [code, service]
    to_layers: [contract]
  HANDLES_OPERATION:
    from_layers: [code]
    to_layers: [contract]
  RETURNS_TYPE:
    from_layers: [contract]
    to_layers: [contract]
  CALLS_ENDPOINT:
    from_layers: [code, service]
    to_layers: [contract]
  CALLS_SERVICE:
    from_layers: [service]
    to_layers: [service]
  PUBLISHES_TOPIC:
    from_layers: [code, service]
    to_layers: [contract]
  CONSUMES_TOPIC:
    from_layers: [code, service]
    to_layers: [contract]
  USES_SCHEMA:
    from_layers: [contract]
    to_layers: [contract]
  REGISTERS_SUBJECT:
    from_layers: [contract]
    to_layers: [contract]
  DEPLOYS_AS:
    from_layers: [service]
    to_layers: [infra]
  TARGETS_SERVICE:
    from_layers: [infra, ci]
    to_layers: [service]
  SELECTS_PODS:
    from_layers: [infra]
    to_layers: [infra]
  ROUTES_TO:
    from_layers: [infra]
    to_layers: [infra, service]
  READS_DB:
    from_layers: [service]
    to_layers: [service, infra]
  WRITES_DB:
    from_layers: [service]
    to_layers: [service, infra]
  USES_QUEUE:
    from_layers: [service]
    to_layers: [service, contract]
  OWNED_BY:
    from_layers: [code, service, contract, flow]
    to_layers: [ownership]
  MAINTAINED_BY:
    from_layers: [code, service]
    to_layers: [ownership]
  DEPENDS_ON_DOMAIN:
    from_layers: [ownership]
    to_layers: [ownership]
  PART_OF_FLOW:
    from_layers: [flow]
    to_layers: [flow]
  PRECEDES:
    from_layers: [flow]
    to_layers: [flow]
  EMITS_AFTER:
    from_layers: [flow]
    to_layers: [contract]
  REQUIRES:
    from_layers: [flow]
    to_layers: [code, service, contract]
  TRIGGERS_ANALYSIS:
    from_layers: [ci]
    to_layers: [service, contract]
  BUILDS_ARTIFACT:
    from_layers: [ci]
    to_layers: [service]
  DEPLOYS_RESOURCE:
    from_layers: [infra]
    to_layers: [infra, service]
  READS_OUTPUT:
    from_layers: [infra]
    to_layers: [infra]

derivation_kinds:
  - hard
  - linked
  - inferred
  - unknown

source_kinds:
  - file
  - openapi
  - graphql
  - asyncapi
  - avro
  - proto
  - schema_registry
  - terraform
  - k8s
  - git
  - ci
  - webhook
  - runtime

node_statuses:
  - active
  - stale
  - deleted
  - unknown

trigger_kinds:
  - full_scan
  - manual
  - git_hook
  - push_webhook
  - release_webhook
  - ci_pipeline
```

## Validation Layer

Every mutation passes through validation before reaching the store. Validation rules:

### Key Normalization
- `node_key` format enforced: `{layer}:{type}:{domain}:{qualified_name}`
- All keys lowercased and trimmed
- Slashes normalized, trailing/leading stripped
- `edge_key` auto-generated if not provided: `{from_node_key}->{to_node_key}:{edge_type}`

### Field Validation
- `layer` must be in registry layers
- `node_type` must be in registry under the given layer
- `edge_type` must be in registry
- Edge from/to layers must match registry's allowed combinations
- `derivation_kind` must be in registry
- `source_kind` must be in registry
- `confidence` must be in range [0.0, 1.0]
- `domain_key` must match manifest domain
- Required fields: `node_key`, `layer`, `node_type`, `domain_key`, `name` (for nodes); `from`, `to`, `edge_type`, `derivation_kind` (for edges)

### Rejection Behavior
- Invalid input returns a structured error with field-level details
- No partial writes — either the full mutation succeeds or nothing changes

## Idempotency Strategy

### Nodes
- Upsert by `node_key`: if exists, update mutable fields (`name`, `file_path`, `confidence`, `metadata`, `status`, `last_seen_revision_id`). Immutable fields (`layer`, `node_type`, `domain_key`) must match or the upsert is rejected with a conflict error.

### Edges
- Upsert by `edge_key`: if exists, update `derivation_kind`, `confidence`, `metadata`, `active`, `last_seen_revision_id`. From/to nodes and `edge_type` are immutable after creation.

### Evidence
- Dedup rule: same `(target_kind, node_id|edge_id, source_kind, repo_name, file_path, line_start, extractor_id)` → update existing evidence (refresh `observed_at`, `confidence`, `commit_sha`). Different line or different extractor → new evidence entry.

### Revision Lifecycle
- A revision represents one scan pass. All upserts within a scan reference the same `revision_id`.
- After a full scan completes: nodes/edges NOT seen in this revision (i.e., `last_seen_revision_id < current`) are marked `status = stale` (not deleted). They remain queryable but flagged.
- Explicit `oracle node delete` / `oracle edge delete` sets `status = deleted` and `active = false`. Records are kept for history.
- A node rename = new node. The old node becomes stale. If Claude detects a rename, it should create the new node and explicitly mark the old one stale.
- A contract version bump = new `schema_version` node linked to the same `schema_subject`. The subject node stays, a new version node is added.

### Transaction Boundary
- `oracle revision create` starts a logical transaction scope
- All upserts reference this revision
- `oracle snapshot create` closes the scope and records final counts
- If the CLI is used via MCP, the batch import command wraps everything in a single SQLite transaction

## Historical Model

| Event | What happens |
|---|---|
| Edge not seen in new full scan | `active = false`, `status = stale`, `last_seen_revision_id` stays at last seen |
| Node not seen in new full scan | `status = stale`, `last_seen_revision_id` stays |
| Node renamed | New node created, old node marked `stale` by caller |
| Contract version bump | New `schema_version` node, linked to existing `schema_subject` |
| Explicit delete | `status = deleted`, `active = false`, record preserved |
| Edge contradicted by stronger source | `active = false`, new edge created with updated derivation |
| Snapshot | Records counts at point in time: total nodes, edges, changed counts. Full graph state is the live tables, not the snapshot. |

## Domain Manifest

`oracle.domain.yaml` — declares domain scope. Claude Code reads this to know what repos to analyze. `oracle init` creates a **skeleton** that must be edited by a human.

```yaml
domain: orders
description: Order processing and payment domain
repositories:
  - name: orders-api
    path: ./repos/orders-api
    tags: [nestjs, graphql, kafka-producer]
  - name: payments-api
    path: ./repos/payments-api
    tags: [nestjs, rest, kafka-consumer]
  - name: platform-manifests
    path: ./repos/platform-manifests
    tags: [kubernetes, terraform]
owner: checkout-team
```

## SQLite Schema

Adapted from the PostgreSQL schema in the master design doc:
- `bigserial` -> `INTEGER PRIMARY KEY AUTOINCREMENT`
- `jsonb` -> `TEXT` (JSON strings, queried via JSON1 extension)
- `timestamptz` -> `TEXT` (ISO 8601)
- `numeric(4,3)` -> `REAL` with CHECK constraints
- No GIN indexes
- `direction` field removed from edges (use two directed edges instead of bidirectional)
- `target_kind` in evidence: only `node` and `edge` (no `answer` for foundation)

Tables: `graph_revisions`, `graph_nodes`, `graph_edges`, `graph_evidence`, `graph_snapshots` — same logical structure as master spec with simplifications noted above.

## CLI Commands

All commands output JSON by default, human-readable with `--format text`.

### Init & DB

```bash
oracle init [--path ./oracle.domain.yaml]  # Create skeleton manifest + types file + init DB
oracle db migrate                           # Run schema migrations
oracle db reset                             # Drop and recreate (with confirmation)
```

`oracle init` creates:
- `oracle.domain.yaml` skeleton (must be edited by human)
- `oracle.types.yaml` with default type registry
- `oracle.db` SQLite database with schema

### Graph Mutation (called by Claude Code via MCP)

```bash
# Revisions
oracle revision create --domain orders --after-sha abc123 --trigger manual --mode full
oracle revision get <revision_id>

# Nodes
oracle node upsert --node-key "code:controller:orders:OrdersController" \
  --layer code --type controller --domain orders --name OrdersController \
  --repo orders-api --file src/orders/orders.controller.ts \
  --revision <id> --confidence 0.99 --metadata '{"route_prefix":"/orders"}'
oracle node get <node_key>
oracle node list [--layer code] [--type controller] [--domain orders] [--repo orders-api] [--status active]
oracle node delete <node_key>

# Edges
oracle edge upsert --edge-key "code:OrdersController->OrdersService:INJECTS" \
  --from "code:controller:orders:OrdersController" \
  --to "code:provider:orders:OrdersService" \
  --type INJECTS --derivation hard \
  --revision <id> --confidence 0.99
oracle edge get <edge_key>
oracle edge list [--from <node_key>] [--to <node_key>] [--type INJECTS] [--derivation hard]
oracle edge delete <edge_key>

# Evidence
oracle evidence add --target-kind edge \
  --edge-key "code:OrdersController->OrdersService:INJECTS" \
  --source-kind file --repo orders-api \
  --file src/orders/orders.controller.ts \
  --line-start 12 --line-end 12 \
  --extractor-id claude-code --extractor-version 1.0 \
  --commit-sha abc123 --confidence 0.99
oracle evidence list [--node-key <key>] [--edge-key <key>] [--source-kind file]

# Snapshots
oracle snapshot create --revision <id> --domain orders --kind full \
  --node-count 42 --edge-count 87
oracle snapshot list [--domain orders]
```

### Bulk Import

For batch operations (typical Claude extraction produces many entities at once):

```bash
oracle import nodes --file nodes.json --revision <id>
oracle import edges --file edges.json --revision <id>
oracle import evidence --file evidence.json
oracle import all --file graph.json --revision <id>   # nodes + edges + evidence in one file
```

All bulk imports run in a single SQLite transaction. The `all` format:

```json
{
  "nodes": [...],
  "edges": [...],
  "evidence": [...]
}
```

Each entry is validated against the type registry before any writes. If any entry fails validation, the entire import is rejected with detailed errors.

### Graph Query

```bash
oracle query node <node_key>              # Node details + all evidence
oracle query edges <node_key>             # All edges from/to a node
oracle query evidence <node_key>          # All evidence for a node
oracle query evidence --edge-key <key>    # All evidence for an edge (trace provenance)
oracle query deps <node_key> [--depth 2] [--derivation hard,linked]
oracle query reverse-deps <node_key> [--depth 2]
oracle query stats [--domain orders]      # Graph summary stats
```

### Validation Commands

```bash
oracle validate graph       # Full graph integrity check
oracle validate keys        # Check all node_key/edge_key formats
oracle validate evidence    # Find orphan evidence, missing targets
oracle validate types       # Verify all nodes/edges conform to type registry
```

Validation outputs:
- Orphan edges (from/to node doesn't exist)
- Orphan evidence (target node/edge doesn't exist)
- Illegal edge types (not in registry)
- Illegal node types (not in registry)
- Malformed keys (wrong format)
- Confidence out of range
- Stale nodes/edges not refreshed in N revisions

### Manifest & Registry

```bash
oracle manifest show [--path oracle.domain.yaml]
oracle manifest validate [--path oracle.domain.yaml]
oracle registry show [--path oracle.types.yaml]
oracle registry validate [--path oracle.types.yaml]
```

## MCP Server

The CLI doubles as an MCP server via `oracle mcp serve`. MCP tool schemas are strict — they enforce required fields and allowed enums matching the type registry.

| MCP Tool | CLI equivalent |
|---|---|
| `oracle_revision_create` | `oracle revision create` |
| `oracle_node_upsert` | `oracle node upsert` |
| `oracle_node_list` | `oracle node list` |
| `oracle_node_get` | `oracle node get` |
| `oracle_edge_upsert` | `oracle edge upsert` |
| `oracle_edge_list` | `oracle edge list` |
| `oracle_evidence_add` | `oracle evidence add` |
| `oracle_import_all` | `oracle import all` |
| `oracle_query_node` | `oracle query node` |
| `oracle_query_edges` | `oracle query edges` |
| `oracle_query_evidence` | `oracle query evidence` |
| `oracle_query_deps` | `oracle query deps` |
| `oracle_query_reverse_deps` | `oracle query reverse-deps` |
| `oracle_query_stats` | `oracle query stats` |
| `oracle_snapshot_create` | `oracle snapshot create` |
| `oracle_validate_graph` | `oracle validate graph` |
| `oracle_stale_mark` | Mark unseen nodes/edges as stale after full scan |

MCP transport: stdio.

## Data Flow

```
Developer asks Claude Code: "scan the orders-api repo"
  -> Claude Code reads oracle.domain.yaml (via Read tool)
  -> Claude Code reads repo files (via Read/Glob/Grep tools)
  -> Claude Code understands the code (NestJS controllers, services, etc.)
  -> Claude Code calls oracle_revision_create
  -> Claude Code builds a graph.json with nodes, edges, evidence
  -> Claude Code calls oracle_import_all (single transaction)
  -> Oracle CLI validates every entry against type registry
  -> Oracle CLI persists to SQLite (with dedup/upsert)
  -> Claude Code calls oracle_snapshot_create
  -> Oracle CLI calls oracle_stale_mark for unseen entities

Developer asks Claude Code: "what depends on OrdersService?"
  -> Claude Code calls oracle_query_reverse_deps tool
  -> Oracle CLI traverses graph, returns JSON
  -> Claude Code formats and explains the results with evidence refs
```

## Success Criteria

1. `oracle init` creates skeleton manifest, type registry, and SQLite database
2. Type registry is loaded and enforced on every mutation
3. Key normalization produces stable, predictable keys
4. `oracle node upsert` / `oracle edge upsert` are idempotent (same key = update, not duplicate)
5. `oracle import all` processes batch JSON in a single transaction with full validation
6. Evidence dedup works: same location + extractor = update, not duplicate
7. `oracle validate graph` detects orphans, type violations, malformed keys
8. `oracle query deps` / `oracle query reverse-deps` traverse edges correctly
9. `oracle mcp serve` starts an MCP server with strict tool schemas
10. All commands produce valid JSON output
11. Go tests cover: validation rules, key normalization, store CRUD, idempotency, evidence dedup, graph queries, bulk import, type registry enforcement
