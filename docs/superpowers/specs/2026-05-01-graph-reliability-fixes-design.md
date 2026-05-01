# Graph Reliability Fixes — Design Spec

**Date:** 2026-05-01
**Goal:** Make the graph a reliable source of truth for codebase reasoning. Fixes identified by A/B benchmark (MCP scored 37/52 vs baseline 47/52).

## Problem Summary

Three issues found:

1. **Impact misses endpoints** — Reverse BFS finds impacted components (Cat → TomService → TomController) but can't reach endpoints because EXPOSES_ENDPOINT goes forward (controller → endpoint), and the reverse traversal correctly blocks it. Result: MCP reports 0 endpoints affected.

2. **Path can't cross pub/sub** — Both PUBLISHES_TOPIC and CONSUMES_TOPIC point TO the topic node. In directed mode, `producer → topic` works but `topic → consumer` doesn't (no outgoing edge from topic). The arena → kafka → spectators path is invisible.

3. **MCP tool descriptions don't encourage hybrid reasoning** — Claude trusts graph output blindly instead of verifying against source code.

## Fix 1 — Impact: Affected Surface Expansion

After reverse BFS collects impacted nodes, add a forward expansion step that follows specific "surface" edge types from impacted nodes.

**Surface edge types:**
- `EXPOSES_ENDPOINT` — controller → endpoint
- `PUBLISHES_TOPIC` — producer → topic

**Output structure change:**

```go
type ImpactResult struct {
    ChangedNode      string            `json:"changed_node"`
    Impacts          []ImpactEntry     `json:"impacts"`
    AffectedSurface  AffectedSurface   `json:"affected_surface"`
    TotalImpacted    int               `json:"total_impacted"`
    MaxDepthReached  int               `json:"max_depth_reached"`
}

type AffectedSurface struct {
    Endpoints []SurfaceEntry `json:"endpoints"`
    Topics    []SurfaceEntry `json:"topics"`
}

type SurfaceEntry struct {
    NodeKey  string `json:"node_key"`
    Name     string `json:"name"`
    ExposedBy string `json:"exposed_by"` // the impacted node that exposes this
}
```

**Algorithm:**
1. Run existing reverse BFS → `impacts[]`
2. Collect all impacted node IDs (including the changed node itself)
3. For each impacted node, query forward edges filtered to EXPOSES_ENDPOINT and PUBLISHES_TOPIC
4. Deduplicate surface entries
5. Return in `affected_surface` section

**Not changed:** The reverse BFS logic, scoring, filtering — all stay the same.

## Fix 2 — Path: Pub/Sub Data Flow Traversal

When traversing in directed mode and the current node is a topic node (`node_type == "topic"`), also follow **reverse CONSUMES_TOPIC edges** — treating them as forward edges for data flow semantics.

**Semantic rule:** `producer → topic ← consumer` becomes `producer → topic → consumer` for path traversal.

**Implementation:** In `QueryPath`, after collecting forward edges from current node, check if current node is a topic. If yes, also collect reverse CONSUMES_TOPIC edges and add them as traversable neighbors (with the consumer as the neighbor, not the topic).

**This is automatic** — no flag needed. The `node_type` check is the condition.

**Edge representation in output:** The path edge will show:
```json
{"from": "contract:topic:...:battle-results", "to": "code:provider:...:battleresultconsumer", "type": "CONSUMES_TOPIC", "derivation": "hard"}
```
Note: the actual DB edge is `consumer → topic`, but we display it as `topic → consumer` because that's the data flow direction in the path context.

## Fix 3 — MCP Tool Description Updates

**chronicle_impact:**
```
"Analyze blast radius of a node change. Reverse dependency traversal with forward
expansion to find affected endpoints and topics. Use to answer 'what breaks if I
change X?' Results include impacted components and affected API surface. For
completeness, verify key findings against source code."
```

**chronicle_query_path:**
```
"Find paths between two nodes. Traverses through Kafka/message topics automatically
(producer → topic → consumer). Default mode 'directed' follows data flow. Use
'connected' for undirected exploration. Structural edges (CONTAINS) excluded by
default. Returns top-k paths ranked by path score."
```

## Fix 4 — Rescan Tom-and-Jerry

After fixes, delete and rescan the tom-and-jerry fixture DB to ensure clean data.

## Test Plan

### Impact tests (impact_test.go):
- `TestImpactAffectedEndpoints` — C changes, A is impacted, A EXPOSES_ENDPOINT POST /x → POST /x appears in affected_surface.endpoints
- `TestImpactAffectedTopics` — add a topic + PUBLISHES_TOPIC edge, verify it appears in affected_surface.topics
- `TestImpactSurfaceDedup` — same endpoint reachable from multiple impacted nodes → appears once

### Path tests (path_test.go):
- `TestQueryPathPubSub` — producer → topic ← consumer, directed mode finds path through topic
- `TestQueryPathPubSubMultiConsumer` — topic with 2 consumers, finds both paths
- `TestQueryPathNoPubSubForNonTopic` — reverse CONSUMES_TOPIC only activates for topic nodes, not arbitrary nodes

### E2E tests:
- Existing tom-and-jerry tests should still pass
- New test: impact on Cat model shows GET /tom/status, GET /tom/weapons, POST /tom/arm in affected_surface
- New test: path from arena-api producer to spectators-api consumer traverses battle-results topic
