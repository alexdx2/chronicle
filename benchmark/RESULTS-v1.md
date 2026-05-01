# Chronicle MCP Benchmark — Results v1 (Pre-Fix)

**Date:** 2026-04-30
**Model:** Sonnet
**Fixture:** tom-and-jerry (4 NestJS microservices)
**Runs:** 3 per task per mode (30 total sessions)

## Raw Metrics (median of 3 runs)

| Task | Mode | Tokens | Cost | Duration | Turns |
|------|------|--------|------|----------|-------|
| 1. Impact (Cat) | MCP | 160,252 | $0.17 | 74s | 17 |
| 1. Impact (Cat) | baseline | 132,309 | $0.19 | 94s | 13 |
| 2. Flow (attack) | MCP | 37,385 | $0.16 | 72s | 2 |
| 2. Flow (attack) | baseline | 212,449 | $0.22 | 105s | 20 |
| 3. Reverse (kafka) | MCP | 97,426 | $0.10 | 26s | 8 |
| 3. Reverse (kafka) | baseline | 32,363 | $0.10 | 47s | 2 |
| 4. Trap | MCP | 30,886 | $0.04 | 9s | 2 |
| 4. Trap | baseline | 44,952 | $0.02 | 10s | 3 |
| 5. Path | MCP | 71,978 | $0.11 | 56s | 6 |
| 5. Path | baseline | 111,382 | $0.22 | 114s | 12 |

## Aggregate

| Metric | MCP | Baseline | Delta |
|--------|-----|----------|-------|
| Tokens | 397,927 | 533,455 | -25.4% |
| Cost | $0.57 | $0.75 | -23.4% |
| Duration | 237s | 372s | -36.3% |

## Correctness Scores (median of 3 runs)

| Task | MCP | Baseline | Max | Winner |
|------|-----|----------|-----|--------|
| 1. Impact (Cat) | 5/12 | 9/12 | 12 | Baseline |
| 2. Flow (attack) | 14/14 | 14/14 | 14 | Tie |
| 3. Reverse (kafka) | 10/10 | 10/10 | 10 | Tie |
| 4. Trap | 6/6 | 6/6 | 6 | Tie |
| 5. Path | 2/10 | 8/10 | 10 | Baseline |
| **TOTAL** | **37/52 (71%)** | **47/52 (90%)** | 52 | **Baseline** |

## Root Cause Analysis

### Task 1 — Impact: MCP scored 5/12 vs baseline 9/12

**Problem:** `chronicle_impact` did reverse BFS but couldn't reach endpoints. EXPOSES_ENDPOINT goes forward (controller → endpoint), so reverse traversal correctly blocks it — but this means impact results never include affected API endpoints.

- MCP runs 1-2 trusted the tool output and reported 0 endpoints
- MCP run 3 inferred endpoints manually (scored 9/12)
- All baseline runs found endpoints by reading controller source code

**Fix applied:** Added `affected_surface` forward expansion — after reverse BFS, follows EXPOSES_ENDPOINT and PUBLISHES_TOPIC edges forward from impacted nodes. Returns endpoints and topics in a separate `affected_surface` section.

### Task 5 — Path: MCP scored 2/10 vs baseline 8/10

**Problem:** Both PUBLISHES_TOPIC and CONSUMES_TOPIC edges point TO the topic node. In directed mode, `producer → topic` works but `topic → consumer` has no outgoing edge. The arena → kafka → spectators path was invisible.

All 3 MCP runs:
- Missed the Kafka indirect path entirely
- 2 runs explicitly denied it: "There is no path through arena-api"
- Hallucinated that SpectatorService calls GET /tom/weapons and POST /tom/arm
- Hallucinated SpectatorService → jerry-api calls

Baseline found both paths by reading source code and tracing fetch() calls.

**Fix applied:** When BFS reaches a topic node (`contract:topic:*`) in directed mode, it now follows reverse CONSUMES_TOPIC edges — treating `consumer → topic` as `topic → consumer` for data flow semantics.

### Task 2 — Flow: Tie at 14/14, but MCP was faster

Both modes traced the full request flow correctly. MCP: 37k tokens, 72s. Baseline: 212k tokens, 105s. One MCP run hallucinated a `TomEventHandler` component (scored 13/14).

### Task 3 — Reverse deps: Tie at 10/10

Both modes identified producer, consumer, downstream flow perfectly.

### Task 4 — Trap: Tie at 6/6

Both modes correctly said CacheInvalidator doesn't exist. Neither hallucinated.

## Additional Fix: MCP Tool Descriptions

Updated `chronicle_impact` description to mention affected surface (endpoints, topics) and suggest verifying against source code.

Updated `chronicle_query_path` description to mention automatic Kafka/topic traversal.

## Scan Cost

Full rescan of tom-and-jerry after fixes:

| Metric | Value |
|--------|-------|
| Total tokens | 2,512,004 |
| Cost | $2.83 |
| Duration | 7.9 min |
| Result | 59 nodes, 71 edges, 5 topic edges |

## Conclusion (v1)

MCP was **faster and cheaper** (-25% tokens, -36% time) but **less correct** (71% vs 90%). The graph became a crutch — Claude trusted tool output instead of verifying, and when the graph had blind spots (endpoints not in impact, Kafka paths invisible), it propagated errors.

Two graph engine fixes applied. Benchmark v2 will measure whether these fixes close the correctness gap.
