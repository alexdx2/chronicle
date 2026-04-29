# Multi-Repo Federation Design

**Date:** 2026-04-29
**Status:** Draft v3 — incorporating review feedback

## Problem

Chronicle currently operates on a single project directory with one `.depbot/` directory, one `chronicle.db`, and one `chronicle.domain.yaml` manifest. In multi-repo architectures (e.g., microservices), the knowledge graph of one repo often references services, models, or endpoints defined in another repo. Today these references are dead ends — the graph stops at the repo boundary.

## Goal

Enable Chronicle to federate queries across multiple repos' databases when run from a parent directory, without copying data or requiring additional configuration. Each repo remains the single authority for its own graph. Cross-repo visibility is determined by the directory you run Chronicle from.

## Distribution Model: OSS Core + Enterprise Federation

Chronicle is split across two repositories:

**Open-source core** (`github.com/anthropics/depbot`) — the single-repo product:
- Full graph engine: scanning, nodes, edges, evidence, queries, impact analysis
- External nodes (`status: external`) as boundary markers
- Node aliases table and `chronicle alias` command
- Deterministic alias extraction during scan (package names, OpenAPI titles, Kafka topic strings, explicit config values)
- Extension interfaces: `GraphQuerier`, `GraphDiscoverer` (see section 4.2)
- Dashboard, MCP server, CLI — all single-repo features

**Enterprise extension** (separate repo) — multi-repo federation only:
- Imports `github.com/anthropics/depbot` as a Go module dependency
- Provides implementations of `GraphQuerier` and `GraphDiscoverer` for multi-repo
- Owns: discovery logic, `FederatedGraph`, cross-repo resolution, federated MCP behavior, multi-repo dashboard views
- Does NOT fork — tracks upstream, extends via interfaces

**Boundary rule:** if it works with one `.depbot/` directory, it belongs in OSS. If it requires coordinating across multiple `.depbot/` directories, it belongs in enterprise.

### Extension Interfaces (OSS defines, enterprise implements)

```go
// GraphQuerier is the primary query interface. OSS provides a single-repo
// implementation. Enterprise provides a federated implementation.
type GraphQuerier interface {
    QueryDeps(nodeKey string, maxDepth int, filters []string) ([]DepNode, error)
    QueryReverseDeps(nodeKey string, maxDepth int, filters []string) ([]DepNode, error)
    QueryPath(fromKey, toKey string, maxDepth int) ([]PathStep, error)
    Impact(nodeKey string, maxDepth int) (*ImpactResult, error)
    QueryStats() (*Stats, error)
}

// GraphDiscoverer finds .depbot/ directories and returns openable graph targets.
// OSS provides a single-directory implementation. Enterprise scans children.
type GraphDiscoverer interface {
    Discover(rootDir string) ([]GraphTarget, error)
}

// GraphTarget represents a discovered repo with a .depbot/ directory.
type GraphTarget struct {
    RepoName string // directory name
    Path     string // absolute path to .depbot/
    Domain   string // from manifest, if available
}
```

The CLI and MCP server use `GraphQuerier` — they don't know whether they're talking to a single `Graph` or a `FederatedGraph`. The enterprise binary wires in its own implementations at startup.

## Design

### 1. Discovery (Enterprise)

When the enterprise binary executes any command from a directory, it checks for `.depbot/chronicle.db` files in **immediate child directories** (one level deep, not recursive).

```
workspace/
  repo-a/
    .depbot/
      chronicle.db
      chronicle.domain.yaml
  repo-b/
    .depbot/
      chronicle.db
      chronicle.domain.yaml
```

Running `chronicle query deps X` from `workspace/` discovers both databases. Running from `repo-a/` sees only repo-a's database.

**Discovery rules:**
- Scan immediate child directories for `.depbot/chronicle.db`
- **Sort discovered repos by directory name** for deterministic ordering — results must not depend on filesystem enumeration order
- Skip directories without `.depbot/` silently
- **If current directory has `.depbot/` AND children also have `.depbot/`, include all** — the current dir's graph participates in federation alongside its children
- Use `--no-federation` to force current-dir-only mode
- Each discovered DB is opened read-only for queries
- If no `.depbot/` is found anywhere (not in current dir, not in children), error as today

**Federation scope reporting:** Every federated command logs its scope for debuggability:

```yaml
federation_scope:
  root: workspace/
  repos:
    - name: notifications-api
      path: ./notifications-api
      domain: notifications
    - name: orders-api
      path: ./orders-api
      domain: orders
    - name: payments-api
      path: ./payments-api
      domain: payments
```

This is included in `QueryStats` output and logged at the start of any federated query. Without this, "why did it find X yesterday but not today" is impossible to debug.

### 2. External Nodes (OSS)

When a repo is scanned and detects a reference to something not defined locally (e.g., an HTTP call to `orders-service`, a Kafka topic `order.placed` consumed but not produced locally), it creates the node with a new status: **`external`**.

**Addition to `node_statuses` in registry:**
```yaml
node_statuses:
  - active
  - stale
  - deleted
  - unknown
  - external    # new: referenced but not defined in this repo
```

**External node properties:**
- Created with `status: external`
- Has edges connecting it to local nodes (e.g., `CALLS_SERVICE`, `CONSUMES_TOPIC`)
- Has evidence pointing to the local code that references it (the import, the HTTP client call, the consumer config)
- Has a valid `node_key` following the standard format: `layer:type:domain:name`
- Can have aliases attached (stored in `node_aliases`, same as any other node) — these are used by the enterprise resolver
- Has `confidence` reflecting how certain we are about the reference (typically lower than locally-defined nodes)
- Does NOT have `file_path`, `lang`, or other source-location fields (those belong to the defining repo)

**Single-repo behavior (OSS):** External nodes appear as boundary markers. Queries that reach an external node stop traversal there. Impact analysis reports them as "external dependency — graph boundary." This is useful on its own — it shows the repo's external surface area.

### 3. Node Aliases (OSS)

A `node_key` like `service:service:orders:orders-service` is the canonical identifier, but cross-repo references rarely use it. Code says `orders-api`, DNS says `orders.internal`, the OpenAPI spec says `Orders API`, the Kafka topic is `order.placed`. Without aliases, federation only works on demos.

Aliases live in OSS because they're useful in single-repo mode too — they help identify what an external node actually refers to, even without federation.

**New table: `node_aliases`**

```sql
CREATE TABLE node_aliases (
    alias_id         INTEGER PRIMARY KEY,
    node_id          INTEGER NOT NULL REFERENCES graph_nodes(node_id),
    alias            TEXT    NOT NULL,       -- original form, for display
    normalized_alias TEXT    NOT NULL,       -- lowercase, trimmed, for matching
    alias_kind       TEXT    NOT NULL,
    confidence       REAL    NOT NULL DEFAULT 0.8,
    UNIQUE(node_id, normalized_alias, alias_kind)
);

CREATE INDEX idx_node_aliases_normalized ON node_aliases(normalized_alias, alias_kind);
CREATE INDEX idx_node_aliases_node_id ON node_aliases(node_id);
```

**Normalization:** `normalized_alias = strings.ToLower(strings.TrimSpace(alias))`. The raw `alias` field is preserved for display. All lookups use `normalized_alias`. This prevents `Orders API`, `orders api`, and `orders-api` from being separate worlds.

**Alias kinds:**
- `dns` — hostname or DNS entry (e.g., `orders.internal`, `orders-api.prod.svc.cluster.local`)
- `package` — package/module name (e.g., `@acme/orders-client`, `orders_pb2`)
- `http_base_url` — base URL pattern (e.g., `/api/orders`, `http://orders.internal`)
- `kafka_topic` — topic name used to identify the owning service (e.g., `order.placed`, `order.updated`)
- `openapi_title` — title from OpenAPI/Swagger spec
- `manual` — explicitly declared by the user

**Alias population during scan (OSS — deterministic extraction only):**
- Package names from import declarations
- OpenAPI/Swagger titles from spec files
- Kafka topic strings from producer/consumer config
- Explicit config values (e.g., `SERVICE_URL=http://orders.internal`)

This is deterministic extraction from obvious sources — not fuzzy/ML inference.

**Where external node aliases are stored:** External nodes are regular `graph_nodes` rows with `status: external`. Their aliases live in the same `node_aliases` table. During resolution (enterprise), the resolver collects:
1. The external node's `name` field
2. All aliases linked to the external node via `node_aliases.node_id`
3. The external node's `node_key`

These are matched against non-external nodes' keys and aliases in other repos.

### 4. Federated Query Engine (Enterprise)

When multiple databases are discovered, the enterprise query engine builds a unified view by resolving external nodes across databases.

#### 4.1 Resolution

Resolution uses a tiered strategy — exact key match first, then alias match scoped by kind:

1. Load all databases (read-only)
2. For each database, index all non-external nodes by `node_key` and by `(alias_kind, normalized_alias)` pairs
3. When a query traverses into an external node:
   - **Tier 1:** exact `node_key` match in another repo → resolve if exactly one active candidate
   - **Tier 2:** alias match, scoped by `alias_kind` — the external node's aliases are looked up against other repos' aliases, matching only within the same `alias_kind`. A `kafka_topic` alias cannot accidentally match a `package` alias.
   - **Tier 3:** no match → remains unresolved
4. If multiple candidates at any tier → mark as `ambiguous`, do not pick one silently

**Resolution is read-only and ephemeral.** No data is written to any database. The cross-DB index exists only in memory for the duration of the query.

#### 4.2 Multi-DB Graph Wrapper (Enterprise)

A new `FederatedGraph` struct implements the `GraphQuerier` interface by wrapping multiple `Graph` instances. Keyed by **`repo_name`** (directory name), not `domain_key` — because multiple repos can share a domain (e.g., `orders-api` and `orders-worker` are both `domain_key=orders`).

```go
// FederatedGraph implements GraphQuerier across multiple repos.
type FederatedGraph struct {
    graphs  map[string]*Graph          // repo_name -> Graph
    index   map[string][]NodeRef       // node_key -> candidates (non-external nodes)
    aliases map[AliasKey][]NodeRef     // (kind, normalized_alias) -> candidates
}

// AliasKey scopes alias lookups to prevent cross-kind false matches.
type AliasKey struct {
    Kind  string // alias_kind
    Value string // normalized_alias
}

// NodeRef identifies a node across repos.
type NodeRef struct {
    RepoName   string
    NodeID     int64
    NodeKey    string
    Status     string
    TrustScore float64
}
```

**Key behaviors:**
- `QueryDeps` / `QueryReverseDeps`: BFS traversal that crosses DB boundaries when an external node resolves
- `QueryPath`: pathfinding across federated graphs
- `Impact`: blast radius analysis that follows edges into other repos
- `QueryStats`: aggregated stats across all repos, plus per-repo breakdown, plus federation scope

When a single `.depbot/` is found (current directory only), the OSS `Graph` is used directly via the `GraphQuerier` interface — no `FederatedGraph` wrapper needed.

#### 4.3 Resolution Status

Query results use a `ResolutionStatus` instead of a simple boolean, because an external node being resolved is fundamentally different from one that isn't:

```go
type DepNode struct {
    NodeKey             string          `json:"node_key"`
    Name                string          `json:"name"`
    Layer               string          `json:"layer"`
    NodeType            string          `json:"node_type"`
    Depth               int             `json:"depth"`
    TrustScore          float64         `json:"trust_score"`
    Freshness           float64         `json:"freshness"`
    Status              string          `json:"status"`
    SourceRepo          string          `json:"source_repo"`                     // repo where the node is defined (or referenced)
    ResolvedRepo        string          `json:"resolved_repo,omitempty"`         // repo where resolved to (empty if local or unresolved)
    ResolutionStatus    string          `json:"resolution_status"`               // local | external_resolved | external_unresolved | ambiguous
    ResolutionMethod    string          `json:"resolution_method,omitempty"`     // exact | alias | none
    ResolutionAliasKind string          `json:"resolution_alias_kind,omitempty"` // e.g., "kafka_topic" — only set when method=alias
    ResolutionAlias     string          `json:"resolution_alias,omitempty"`      // the alias value that matched
    AmbiguousCandidates []AmbiguousRef  `json:"ambiguous_candidates,omitempty"`  // only set when status=ambiguous
}
```

**Resolution statuses:**
- `local` — node is defined in the repo being queried, normal traversal
- `external_resolved` — node was external in `SourceRepo`, successfully resolved to `ResolvedRepo`
- `external_unresolved` — node was external, no match found in any federated repo
- `ambiguous` — node was external, multiple candidate matches found, none selected

**In OSS (single-repo mode):** `ResolutionStatus` is always `local` for non-external nodes, `external_unresolved` for external nodes. The `ResolvedRepo`, `ResolutionMethod`, `ResolutionAlias*`, and `AmbiguousCandidates` fields are always empty. The struct is defined in OSS so both implementations return the same type.

### 5. CLI Changes

#### 5.1 Discovery in Root Command

`resolveDefaults()` in `cli/root.go` uses the `GraphDiscoverer` interface:

**OSS behavior:**
```
1. Check current dir for .depbot/ (existing behavior)
2. If found -> single-repo mode
3. If not found -> error
```

**Enterprise behavior:**
```
1. Check current dir for .depbot/
2. Scan immediate children for .depbot/ directories
3. Sort all discovered repos by directory name (deterministic)
4. If current dir has .depbot/ + children have .depbot/ -> federated (all included)
5. If only children have .depbot/ -> federated (children only)
6. If only current dir has .depbot/ -> single-repo mode
7. If none found -> error
```

The `--project` flag continues to work — it pins to a specific project directory and disables federation.

#### 5.2 New Flags (Enterprise)

- `--no-federation` — forces single-repo mode even when child `.depbot/` directories exist

#### 5.3 New Command: `chronicle alias` (OSS)

```
chronicle alias add <node_key> <alias> --kind <alias_kind> [--confidence 0.9]
chronicle alias list <node_key>
chronicle alias remove <alias_id>
```

### 6. MCP Server Changes

The MCP server (`mcp serve`) uses `GraphQuerier` for all read operations. The wiring depends on the binary:

**OSS binary:** always single-repo `Graph` as the `GraphQuerier`.

**Enterprise binary:** uses discovery logic, may wire in `FederatedGraph`:

- **Read operations** (queries, deps, impact, path) use `GraphQuerier` — federated transparently
- **Write operations** (node_upsert, edge_upsert, evidence_add, alias_add) **require an explicit `project` parameter** specifying the target repo directory. This is **mandatory** in federation mode — if omitted, the tool returns an error. Writes never fan out across repos.

In single-repo mode (either binary), the `project` parameter is optional (defaults to current `.depbot/`).

### 7. Admin Dashboard

**OSS dashboard:** single-repo view as today, plus:
- External nodes shown with dashed border, muted color
- Alias information visible on node detail panel

**Enterprise dashboard additions:**
- Combined graph view with repos color-coded
- Repo filter/selector to scope the view
- External nodes show resolution status visually:
  - `external_unresolved`: dashed border, muted color
  - `external_resolved`: dashed border with solid fill, link indicator to resolved repo
  - `ambiguous`: warning indicator with candidate list on hover
- Stats page shows per-repo breakdown, aggregate numbers, and `federation_scope`
- Resolution method shown on edge tooltips (exact vs alias, which alias)

### 8. Conflict Handling (Enterprise)

**Exact `node_key` duplicate** (two repos both define the same `node_key` with `status: active`):

This is a **modeling conflict**. It cannot be resolved by aliases — the `node_key` itself is duplicated.

- Resolution status is set to `ambiguous`
- All candidates are preserved:

```go
type AmbiguousRef struct {
    RepoName   string  `json:"repo_name"`
    NodeKey    string  `json:"node_key"`
    TrustScore float64 `json:"trust_score"`
    Status     string  `json:"status"`
}
```

- `QueryStats` includes a `conflicts` section listing all ambiguous node keys and their candidates
- Traversal **stops** at ambiguous nodes — no silent pick, no hidden behavior
- Resolution requires fixing the source data:
  - Rename the node in one repo (different `domain` or `name` → different `node_key`)
  - Mark one as `stale` or `deleted`

**Alias ambiguity** (an alias matches nodes in multiple repos):

- Same `ambiguous` status, but this CAN be resolved by adding a `manual` alias to clarify which candidate is correct, or by removing the conflicting alias from one repo.

**The only case where federation resolves automatically:** exactly one candidate with `status: active` at that tier (exact key or alias). Everything else is surfaced.

### 9. Constraints and Non-Goals

**Constraints:**
- Discovery is one level deep only (no recursive scanning)
- Discovered repos are sorted by directory name for deterministic results
- Node key must be globally unique across repos for exact-match resolution — alias-based resolution handles the rest
- Alias matching is scoped by `alias_kind` to prevent false cross-kind matches
- Databases are opened read-only in federation — no cross-DB writes
- Federation adds latency proportional to the number of repos (one DB open per repo)

**In scope for OSS:**
- External nodes as boundary markers
- Node aliases table with deterministic extraction during scan
- `chronicle alias` CLI command
- Extension interfaces (`GraphQuerier`, `GraphDiscoverer`)
- `DepNode` struct with resolution fields (unused in OSS, populated in enterprise)

**In scope for Enterprise:**
- Multi-repo discovery and `FederatedGraph`
- Cross-repo resolution (exact key + alias tiers)
- Federated MCP read behavior
- Mandatory `project` parameter for writes in federation mode
- Multi-repo dashboard views
- Federation scope reporting
- Conflict detection and `ambiguous` status

**Non-goals for this iteration:**
- Remote repos (all repos must be on local disk)
- Cross-repo evidence aggregation (evidence stays in its source repo's DB)
- Parent-level manifest or configuration file
- Recursive directory scanning
- Fuzzy/ML-based alias inference (deterministic extraction only)

### 10. Backward Compatibility

- **Single-repo usage is unchanged.** The OSS binary works exactly as today.
- **No new config files required.** Federation is purely discovery-based.
- **External node status is additive.** Existing graphs continue to work — they just don't have external nodes until the next scan.
- **The `external` status needs to be added to the registry.** Non-breaking addition to `node_statuses`.
- **The `node_aliases` table is created by migration.** Empty by default, no impact on existing graphs.
- **`DepNode` gains new fields.** All new fields are `omitempty` — existing consumers see no change in single-repo mode.

### 11. Example Walkthrough

```
workspace/
  .depbot/           # platform-level graph (optional)
  orders-api/        # defines: service:service:orders:orders-service
    .depbot/         # aliases: dns=orders.internal, kafka_topic=order.placed
  orders-worker/     # defines: service:worker:orders:orders-worker (same domain!)
    .depbot/
  payments-api/      # calls orders-api via HTTP
    .depbot/
  notifications-api/ # consumes order.placed topic
    .depbot/
```

**From `payments-api/` (single repo, OSS):**
```
$ chronicle query deps service:service:payments:payments-service
  -> CALLS_SERVICE -> service:service:orders:orders-service
       [external_unresolved, source_repo=payments-api]
```
External node as boundary marker. Graph stops here. No federation needed.

**From `workspace/` (federated, Enterprise):**
```
$ chronicle query deps service:service:payments:payments-service
  federation_scope: [workspace, notifications-api, orders-api, orders-worker, payments-api]

  -> CALLS_SERVICE -> service:service:orders:orders-service
       [external_resolved, source=payments-api, resolved=orders-api, method=exact]
    -> USES_MODEL -> data:model:orders:Order [local, orders-api]
    -> EXPOSES_ENDPOINT -> contract:http_api:orders:POST /orders [local, orders-api]
```
Parent dir's `.depbot/` is included in scope alongside children.

**Alias resolution — notifications-api references `order.placed` topic:**
```
$ chronicle query deps service:service:notifications:event-processor
  -> CONSUMES_TOPIC -> contract:kafka_topic:orders:order.placed
       [external_resolved, source=notifications-api, resolved=orders-api,
        method=alias, alias_kind=kafka_topic, alias=order.placed]
```
No exact `node_key` match. Alias lookup scoped to `kafka_topic` kind finds it in orders-api.

**Ambiguous example — duplicate node_key:**
```
$ chronicle query deps some-consumer
  -> CALLS_SERVICE -> service:service:orders:orders-service
       [ambiguous, candidates: [orders-api(trust=0.9), legacy-orders-api(trust=0.7)]]
```
Traversal stops. This is a modeling conflict — must be fixed in the source repos, not with aliases.

**Impact analysis from `workspace/`:**
```
$ chronicle impact data:model:orders:Order
  Direct: orders-api (USES_MODEL, EXPOSES_ENDPOINT)
  Cross-repo: payments-api (CALLS_SERVICE -> orders-service) [resolved, method=exact]
  Cross-repo: notifications-api (CONSUMES_TOPIC -> order.placed) [resolved, method=alias(kafka_topic)]
  Unresolved: analytics-pipeline (no matching repo in scope)
```
