# Sub-project 2: E2E Fixtures + Graph Engine

## Summary

Add a synthetic fixture project (two NestJS services) with golden extraction data, path traversal queries, node-based impact analysis, and a comprehensive E2E test. Extends the existing Go CLI with new query commands and MCP tools.

Key design principles from review:
- Path traversal is **directed by default** (undirected only via `--mode connected`)
- Scores are called "scores" not "confidence" — they are heuristic, not probabilistic
- Edge traversal is governed by a **traversal policy** per edge type
- CONTAINS edges are **structural only** — excluded from dependency path/impact by default
- Fixture graph is **symmetric** — both services get service nodes + repo nodes

## Edge Traversal Policy

Not all edge types behave the same in path/impact traversal. A policy registry defines which edges participate in which operations.

| edge_type | forward_path | reverse_impact | notes |
|---|---|---|---|
| CONTAINS | no (structural) | no | Browsing only. Use `--include-structural` to include. |
| IMPORTS | yes | yes | Code dependency |
| INJECTS | yes | yes | DI dependency |
| CALLS_SYMBOL | yes | yes | Code-level call |
| EXPOSES_ENDPOINT | yes | no | Controller → endpoint. Endpoint change doesn't impact controller. |
| CALLS_ENDPOINT | yes | yes | Consumer → endpoint. Endpoint change impacts consumer. |
| CALLS_SERVICE | yes | yes | Service dependency |
| PUBLISHES_TOPIC | yes | no | Producer → topic. Topic change doesn't reverse-impact producer. |
| CONSUMES_TOPIC | yes | yes | Consumer ← topic. Topic change impacts consumer. |
| USES_SCHEMA | yes | yes | Schema dependency |
| DEPLOYS_AS | no (structural) | no | Infra mapping |
| TARGETS_SERVICE | yes | yes | Infra → service |
| SELECTS_PODS | no (structural) | no | K8s internal |
| ROUTES_TO | yes | yes | Traffic routing |
| OWNED_BY | no (structural) | no | Ownership |
| MAINTAINED_BY | no (structural) | no | Ownership |
| PART_OF_FLOW | no (structural) | no | Flow structure |
| PRECEDES | yes | yes | Flow ordering |
| TRIGGERS_ANALYSIS | yes | no | CI trigger |
| BUILDS_ARTIFACT | no (structural) | no | CI structure |

**Implementation:** A `TraversalPolicy` struct loaded alongside the type registry. The path and impact algorithms consult this policy before traversing each edge.

```yaml
# Added to oracle.types.yaml under a new section
traversal_policy:
  structural_edge_types:
    - CONTAINS
    - DEPLOYS_AS
    - SELECTS_PODS
    - OWNED_BY
    - MAINTAINED_BY
    - PART_OF_FLOW
    - BUILDS_ARTIFACT
  no_reverse_impact:
    - EXPOSES_ENDPOINT
    - PUBLISHES_TOPIC
    - TRIGGERS_ANALYSIS
```

Everything not listed in `structural_edge_types` participates in forward path by default. Everything not listed in `structural_edge_types` or `no_reverse_impact` participates in reverse impact.

## Fixture Project

### Structure

```
fixtures/orders-domain/
  oracle.domain.yaml              # Domain manifest
  expected-graph.json             # Golden ImportPayload
  orders-api/
    src/
      orders/
        orders.module.ts
        orders.controller.ts
        orders.service.ts
      payments/
        payments.service.ts       # HTTP client calling POST /payments/charge
      events/
        order-created.producer.ts # Kafka producer
    openapi.yaml
    package.json
    tsconfig.json
  payments-api/
    src/
      payments/
        payments.controller.ts
        payments.service.ts
    openapi.yaml
    package.json
    tsconfig.json
```

### Golden Graph (expected-graph.json)

An `ImportPayload` JSON file representing perfect extraction.

**Nodes (~18):**

| node_key | layer | type | name |
|---|---|---|---|
| `code:repository:orders:orders-api` | code | repository | orders-api |
| `code:repository:orders:payments-api` | code | repository | payments-api |
| `code:module:orders:ordersmodule` | code | module | OrdersModule |
| `code:controller:orders:orderscontroller` | code | controller | OrdersController |
| `code:provider:orders:ordersservice` | code | provider | OrdersService |
| `code:provider:orders:paymentsservice` | code | provider | PaymentsService |
| `code:provider:orders:ordercreatedproducer` | code | provider | OrderCreatedProducer |
| `code:controller:orders:paymentscontroller` | code | controller | PaymentsController |
| `code:provider:orders:paymentsapiservice` | code | provider | PaymentsApiService |
| `service:service:orders:orders-api` | service | service | orders-api |
| `service:service:orders:payments-api` | service | service | payments-api |
| `contract:endpoint:orders:get:/orders` | contract | endpoint | GET /orders |
| `contract:endpoint:orders:get:/orders/:id` | contract | endpoint | GET /orders/:id |
| `contract:endpoint:orders:post:/orders` | contract | endpoint | POST /orders |
| `contract:endpoint:orders:post:/orders/:id/capture` | contract | endpoint | POST /orders/:id/capture |
| `contract:endpoint:orders:post:/payments/charge` | contract | endpoint | POST /payments/charge |
| `contract:endpoint:orders:post:/payments/refund` | contract | endpoint | POST /payments/refund |
| `contract:topic:orders:order-created` | contract | topic | order-created |

**Edges (~16):**

| from | to | type | derivation | notes |
|---|---|---|---|---|
| orders-api (repo) → OrdersModule | CONTAINS | hard | structural |
| OrdersModule → OrdersController | CONTAINS | hard | structural |
| OrdersModule → OrdersService | CONTAINS | hard | structural |
| OrdersModule → PaymentsService | CONTAINS | hard | structural |
| OrdersModule → OrderCreatedProducer | CONTAINS | hard | structural |
| payments-api (repo) → PaymentsController | CONTAINS | hard | structural |
| payments-api (repo) → PaymentsApiService | CONTAINS | hard | structural |
| OrdersController → OrdersService | INJECTS | hard | dependency |
| OrdersService → PaymentsService | INJECTS | hard | dependency |
| OrdersService → OrderCreatedProducer | INJECTS | hard | dependency |
| OrdersController → POST /orders | EXPOSES_ENDPOINT | hard | no reverse impact |
| OrdersController → POST /orders/:id/capture | EXPOSES_ENDPOINT | hard | no reverse impact |
| PaymentsController → POST /payments/charge | EXPOSES_ENDPOINT | hard | no reverse impact |
| PaymentsService → POST /payments/charge | CALLS_ENDPOINT | linked | dependency |
| PaymentsService → payments-api (service) | CALLS_SERVICE | linked | dependency |
| OrderCreatedProducer → order-created | PUBLISHES_TOPIC | hard | no reverse impact |

**Evidence:** Each non-structural edge gets at least one evidence entry with file path + line range pointing to the fixture source file.

### Symmetry Notes

- Both services have repo nodes (`code:repository`) and service nodes (`service:service`)
- `PaymentsService` in orders-api calls `POST /payments/charge` (CALLS_ENDPOINT) AND `payments-api` service (CALLS_SERVICE) — the CALLS_ENDPOINT is the precise edge, CALLS_SERVICE is the coarse edge. Both are useful at different query levels.
- CONTAINS edges exist for structural browsing but don't participate in dependency path/impact.

## Graph Engine Additions

### Path Query

**Command:** `oracle query path <from> <to> [--max-depth N] [--top-k N] [--derivation hard,linked] [--mode directed|connected] [--include-structural]`

**Modes:**
- `directed` (default) — follows edges in their natural direction only. Produces meaningful dependency/call chains.
- `connected` — follows edges in both directions. Exploratory, shows any topological relationship.

**Algorithm:** BFS with path tracking. Finds up to `top-k` (default 3) paths. Paths ranked by path score (lowest cost first).

**Traversal policy:** Skips structural edge types (CONTAINS, OWNED_BY, etc.) unless `--include-structural` is set. Derivation filter applied.

**Path score (heuristic, not probabilistic confidence):**
```
path_cost = Σ(-ln(edge_confidence)) + 0.05 * depth
path_score = Π(edge_confidence) * 0.95^(depth-1)
```

Lower cost = better path. `path_score` is a quality heuristic for display, not a probability.

**Tie-breaking:** When two paths have equal cost:
1. Lower depth first
2. Lexicographic ordering of path node keys
3. Stable insertion order

**Output:**
```json
{
  "from": "code:controller:orders:orderscontroller",
  "to": "service:service:orders:payments-api",
  "mode": "directed",
  "paths": [
    {
      "nodes": ["code:controller:orders:orderscontroller", "code:provider:orders:ordersservice", "code:provider:orders:paymentsservice", "service:service:orders:payments-api"],
      "edges": [
        {"from": "...", "to": "...", "type": "INJECTS", "derivation": "hard"},
        {"from": "...", "to": "...", "type": "INJECTS", "derivation": "hard"},
        {"from": "...", "to": "...", "type": "CALLS_SERVICE", "derivation": "linked"}
      ],
      "depth": 3,
      "path_score": 0.79,
      "path_cost": 0.38
    }
  ],
  "total_paths_found": 1
}
```

**Max depth:** default 6, configurable.

### Impact Query

**Command:** `oracle impact <node_key> [--depth N] [--derivation hard,linked] [--min-score 0.1] [--include-structural]`

**Algorithm:** Reverse-dependency BFS from the changed node. Only traverses edges where the traversal policy allows `reverse_impact`. CONTAINS and other structural edges are excluded by default. EXPOSES_ENDPOINT and PUBLISHES_TOPIC are excluded from reverse impact (endpoint change doesn't impact the controller that exposes it; topic change doesn't impact the producer).

**Impact score (heuristic):**
```
path_score = Π(edge_confidence) * 0.95^(depth-1)
impact_score = 100 * path_score
```

This is a reachability-weighted ranking score, not a true impact probability.

**Output:**
```json
{
  "changed_node": "code:provider:orders:paymentsservice",
  "impacts": [
    {
      "node_key": "code:provider:orders:ordersservice",
      "name": "OrdersService",
      "layer": "code",
      "depth": 1,
      "impact_score": 94.05,
      "path": ["code:provider:orders:paymentsservice", "code:provider:orders:ordersservice"],
      "edge_types": ["INJECTS"]
    },
    {
      "node_key": "code:controller:orders:orderscontroller",
      "name": "OrdersController",
      "layer": "code",
      "depth": 2,
      "impact_score": 89.30,
      "path": ["code:provider:orders:paymentsservice", "code:provider:orders:ordersservice", "code:controller:orders:orderscontroller"],
      "edge_types": ["INJECTS", "INJECTS"]
    }
  ],
  "total_impacted": 2,
  "max_depth_reached": 2
}
```

**Min-score filter:** default 0.1. `--top-k` (default 50) limits results for large graphs.

### New Files

```
internal/graph/policy.go           # TraversalPolicy — which edges participate in which operations
internal/graph/policy_test.go
internal/graph/path.go             # Path traversal (directed + connected modes)
internal/graph/path_test.go
internal/graph/impact.go           # Impact analysis (reverse BFS with policy)
internal/graph/impact_test.go
```

### Registry Extension

Add `traversal_policy` section to `oracle.types.yaml` and `defaults.yaml`:

```yaml
traversal_policy:
  structural_edge_types:
    - CONTAINS
    - DEPLOYS_AS
    - SELECTS_PODS
    - OWNED_BY
    - MAINTAINED_BY
    - DEPENDS_ON_DOMAIN
    - PART_OF_FLOW
    - BUILDS_ARTIFACT
    - DEPLOYS_RESOURCE
  no_reverse_impact:
    - EXPOSES_ENDPOINT
    - PUBLISHES_TOPIC
    - TRIGGERS_ANALYSIS
    - EMITS_AFTER
```

Update `Registry` struct to parse and expose `TraversalPolicy`.

### CLI Commands

Add to `internal/cli/query.go`:
- `oracle query path <from> <to>` with flags: `--max-depth` (6), `--top-k` (3), `--derivation`, `--mode` (directed), `--include-structural`

Add new `internal/cli/impact.go`:
- `oracle impact <node_key>` with flags: `--depth` (4), `--derivation`, `--min-score` (0.1), `--top-k` (50), `--include-structural`

### MCP Tools

Add to `internal/mcp/server.go`:
- `oracle_query_path` — path between two nodes
- `oracle_impact` — impact analysis for a changed node

### Stats Enhancement

Extend `QueryStats` to include:
- Nodes by layer
- Edges by type
- Edges by derivation kind
- Active vs stale breakdown

(Already partially implemented in Sub-project 1, enhance with derivation breakdown.)

## E2E Test

**File:** `e2e/graph_e2e_test.go`

### Phase 1: Golden Payload Validation

Before any graph operations, validate the golden payload itself:
- All `from`/`to` node keys in edges reference existing nodes in the payload
- All `edge_type` values are valid per registry
- All `node_key` values are unique
- All `edge_key` values are unique (or auto-generated uniquely)
- All evidence references point to valid node/edge targets
- All from/to layer combinations are valid per registry

This catches broken fixtures early.

### Phase 2: Import + Basic Queries

1. **Setup:** Open temp DB, load default registry, read `fixtures/orders-domain/expected-graph.json`
2. **Import:** Create revision, call `ImportAll`
3. **Verify counts:** 18 nodes, 16 edges, evidence entries
4. **Query deps:** OrdersController direct deps (excluding CONTAINS) = OrdersService (via INJECTS)
5. **Query reverse-deps:** payments-api service reverse-deps depth 3 = PaymentsService → OrdersService → OrdersController

### Phase 3: Path Queries

6. **Directed path exists:** OrdersController → payments-api (service) should find a path via INJECTS → INJECTS → CALLS_SERVICE
7. **No directed dependency path:** OrderCreatedProducer → PaymentsController should find no directed dependency path (they are not connected via dependency edges)
8. **Derivation filter:** path with `--derivation hard` only should NOT find OrdersController → payments-api (because CALLS_SERVICE is `linked`)
9. **Derivation filter:** path with `--derivation hard,linked` SHOULD find it

### Phase 4: Impact

10. **Impact of PaymentsService change:** OrdersService (depth 1), OrdersController (depth 2)
11. **Impact scoring:** OrdersService has higher score than OrdersController (closer = higher)
12. **CONTAINS excluded:** OrdersModule should NOT appear in impact results (CONTAINS is structural)
13. **EXPOSES_ENDPOINT excluded:** endpoints should NOT appear in impact when a controller changes (no reverse impact on EXPOSES_ENDPOINT)

### Phase 5: Lifecycle

14. **Idempotent re-import:** same payload, no duplicates
15. **Stale marking:** new revision with partial import, correct nodes marked stale
16. **Stats:** verify node/edge counts, layer distribution, derivation breakdown

## Success Criteria

1. Fixture project exists with realistic NestJS code + OpenAPI specs
2. `expected-graph.json` passes self-validation (unique keys, valid types, valid edge layers)
3. Edge traversal policy correctly excludes structural edges from path/impact
4. `oracle query path` finds correct directed paths with score (not "confidence")
5. `oracle query path --mode connected` finds undirected paths
6. `oracle impact` computes correct reverse impact respecting traversal policy
7. CONTAINS edges don't pollute dependency paths or impact results
8. Derivation filter tests prove filters actually work
9. E2E test passes all phases
10. MCP tools work via stdio
11. All existing tests continue to pass
