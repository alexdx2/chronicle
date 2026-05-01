# Chronicle MCP Benchmark — Results v2 (Post-Fix)

**Date:** 2026-05-01
**Model:** Sonnet
**Fixture:** tom-and-jerry (4 NestJS microservices), fresh scan (59 nodes, 71 edges)
**Runs:** 3x MCP (new), baseline reused from v1
**Fixes applied:** affected_surface expansion, pub/sub path traversal, tool description updates

## Correctness Scores (median of 3 runs)

| Task | MCP v1 | MCP v2 | Baseline | Max | v2 Winner |
|------|--------|--------|----------|-----|-----------|
| 1. Impact (Cat) | 5 | **10** | 9 | 12 | **MCP** |
| 2. Flow (attack) | 14 | **14** | 14 | 14 | Tie |
| 3. Reverse (kafka) | 10 | **10** | 10 | 10 | Tie |
| 4. Trap | 6 | **6** | 6 | 6 | Tie |
| 5. Path | 2 | **8** | 8 | 10 | Tie |
| **TOTAL** | **37 (71%)** | **48 (92%)** | **47 (90%)** | 52 | **MCP** |

## Efficiency Metrics (median of 3 runs)

| Task | MCP v2 Tokens | Baseline Tokens | MCP v2 Cost | Baseline Cost | MCP v2 Time | Baseline Time |
|------|--------------|----------------|------------|--------------|------------|--------------|
| 1. Impact | 182k | 132k | $0.21 | $0.19 | 78s | 94s |
| 2. Flow | 37k | 212k | $0.22 | $0.22 | 88s | 105s |
| 3. Reverse | 86k | 32k | $0.09 | $0.10 | 42s | 47s |
| 4. Trap | 47k | 45k | $0.05 | $0.02 | 12s | 10s |
| 5. Path | 214k | 111k | $0.19 | $0.22 | 64s | 114s |
| **Total** | **566k** | **533k** | **$0.76** | **$0.75** | **284s** | **372s** |

## Aggregate Comparison

| Metric | MCP v1 | MCP v2 | Baseline | v2 vs Baseline |
|--------|--------|--------|----------|----------------|
| Correctness | 37/52 (71%) | 48/52 (92%) | 47/52 (90%) | **MCP +2%** |
| Tokens | 398k | 566k | 533k | MCP +6% |
| Cost | $0.57 | $0.76 | $0.75 | Even |
| Duration | 237s | 284s | 372s | **MCP -24%** |
| Hallucinations | 4/15 runs | 0/15 runs | 1/15 runs | **MCP cleaner** |

## Detailed Scores Per Run

### Task 1 — Impact (Cat model) — max 12

| Run | MCP v1 | MCP v2 | Change |
|-----|--------|--------|--------|
| 1 | 5 | 10 | +5 |
| 2 | 5 | 11 | +6 |
| 3 | 9 | 10 | +1 |

**What fixed it:** `affected_surface` expansion. All 3 v2 runs found GET /tom/status, GET /tom/weapons, POST /tom/arm in the impact result. v1 missed all endpoints in 2/3 runs.

MCP v2 now **beats baseline** (10 vs 9) because it finds endpoints via graph traversal AND has cleaner direct dependency lists (fewer hallucinated extras than baseline's over-inclusive file tracing).

### Task 5 — Path (spectators-api → tom-api) — max 10

| Run | MCP v1 | MCP v2 | Change |
|-----|--------|--------|--------|
| 1 | 2 | 6 | +4 |
| 2 | 2 | 10 | +8 |
| 3 | 2 | 8 | +6 |

**What fixed it:** Pub/sub traversal. v2 runs find the Kafka indirect path (arena → battle-results → spectators) because `QueryPath` now crosses topic nodes in directed mode. v1 explicitly denied this path existed.

Run 2 scored a perfect 10/10 — found direct HTTP connection, Kafka indirect path, arena-api as intermediary, isolated databases, no hallucinations.

### Tasks 2, 3, 4 — Stable

All scored perfectly in both v1 and v2. No regressions.

- Task 2: 14/14 across all 3 runs, zero hallucinations
- Task 3: 10/10 across all 3 runs
- Task 4: 6/6 across all 3 runs, no confusion with CacheService

## Hallucination Analysis

| | MCP v1 | MCP v2 | Baseline |
|--|--------|--------|----------|
| Runs with hallucinations | 4/15 | **0/15** | 1/15 |
| Types | Invented TomEventHandler, denied Kafka path, fabricated endpoints | None | Overclaimed shared deps |

MCP v2 has **zero hallucinations** across all 15 runs. The graph now provides correct structural data, and the updated tool descriptions encourage verification.

## Cost of Improvement

| Investment | Cost |
|------------|------|
| Full rescan (one-time) | $2.83 |
| Benchmark v2 MCP runs (15 sessions) | $2.27 |
| Benchmark v1 all runs (30 sessions) | $6.60 |
| **Total benchmark cost** | **$11.70** |

## Key Takeaways

1. **Correctness is now MCP's advantage** — 92% vs 90%. The graph provides structured, verified answers that baseline file-reading can't match consistently.

2. **Speed advantage held** — MCP is 24% faster despite slightly more tokens. Graph queries replace multi-file exploration.

3. **Zero hallucinations** — The biggest win. Baseline occasionally over-includes (HealthCheckPipe as a direct dep, shared package claims). MCP v2 is precise because the graph encodes verified relationships.

4. **Token parity** — MCP uses ~6% more tokens than baseline. The graph data adds context but eliminates wasted file reads. Net neutral on cost.

5. **The fixes were surgical** — Two algorithm changes (40 lines of Go) + tool descriptions turned a 71% score into 92%. The graph engine was correct; it just needed to expose the right information.
