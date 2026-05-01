# Chronicle MCP Benchmark — OtoPoint Results

**Date:** 2026-05-01
**Model:** Sonnet
**Project:** OtoPoint — NestJS monolith, 6000+ TS files, Prisma, GraphQL, Bull, WebSocket, Centrifugo, Stripe
**Graph:** 264 nodes, 331 edges (2-pass scan, $5.49)
**Runs:** 3 per task per mode (30 total)

## Correctness Scores (median of 3 runs)

| Task | MCP | Baseline | Max | Winner |
|------|-----|----------|-----|--------|
| 1. Impact (Order) | **16** | 16 | 16 | Tie (MCP more consistent) |
| 2. Flow (create order) | **14** | 13 | 14 | **MCP** |
| 3. Reverse (SocketService) | **14** | 14 | 14 | Tie |
| 4. Trap (InventoryService) | **6** | 6 | 6 | Tie |
| 5. Voucher refactor | **14** | 14 | 14 | Tie |
| **TOTAL** | **64/64 (100%)** | **63/64 (98%)** | 64 | **MCP** |

## Consistency (variance across 3 runs)

| Task | MCP scores | Baseline scores |
|------|-----------|----------------|
| 1. Impact | 16, 16, 16 | 12, 16, 16 |
| 2. Flow | 13, 14, 14 | 13, 13, 14 |
| 3. Reverse | 14, 14, 14 | 14, 14, 14 |
| 4. Trap | 6, 6, 6 | 6, 6, 6 |
| 5. Voucher | 14, 14, 14 | 14, 14, 14 |

MCP: 0 weak runs out of 15
Baseline: 1 weak run out of 15 (Task 1 run 1 scored 12/16 — vague REST endpoints, hallucinated `dashboard.resolver.ts`)

## Efficiency Metrics (median of 3 runs)

| Task | MCP Tokens | Baseline Tokens | MCP Cost | Baseline Cost | MCP Time | Baseline Time |
|------|-----------|----------------|----------|--------------|----------|--------------|
| 1. Impact | 110k | 68k | $0.65 | $0.46 | 170s | 147s |
| 2. Flow | 69k | 66k | $0.57 | $0.40 | 188s | 148s |
| 3. Reverse | 68k | 65k | $0.32 | $0.27 | 88s | 94s |
| 4. Trap | 62k | 59k | $0.10 | $0.09 | 11s | 11s |
| 5. Voucher | 69k | 137k | $0.45 | $0.43 | 139s | 157s |
| **Total** | **378k** | **395k** | **$2.09** | **$1.65** | **596s** | **557s** |

## Aggregate

| Metric | MCP | Baseline | Delta |
|--------|-----|----------|-------|
| Correctness (median) | 64/64 (100%) | 63/64 (98%) | MCP +1 |
| Consistency (min run) | 63/64 | 61/64 | MCP more stable |
| Hallucinations | 0/15 runs | 1/15 runs | MCP cleaner |
| Tokens | 378k | 395k | MCP -4% |
| Cost | $2.09 | $1.65 | Baseline -21% cheaper |
| Duration | 596s | 557s | Baseline -7% faster |

## Analysis

### On a real production codebase, MCP and baseline are nearly tied on correctness.

Both score 98-100% on the checklist. The gap is much smaller than on tom-and-jerry because:

1. **Sonnet is good at reading NestJS code.** The codebase follows standard patterns (decorators, injection, Prisma). Baseline doesn't need graph help to understand it.

2. **The graph is incomplete.** 2-pass scan captured 264 nodes, but a 6000-file monolith has far more. Services that baseline finds by grepping imports, MCP might miss if they weren't scanned.

3. **Tasks were "too easy."** All 5 tasks have clear, self-contained answers. No cross-service Kafka paths or ambiguous dependencies that would expose baseline's weaknesses.

### Where MCP does win:

- **Consistency** — all 15 MCP runs scored at or near maximum. Baseline had 1 weak run (Task 1 run 1 at 12/16).
- **Zero hallucinations** — MCP never invented services, endpoints, or file paths.
- **Task 5 (Voucher)** — MCP used fewer tokens (69k vs 137k) because the graph provided the dependency tree directly instead of grepping through files.

### Where baseline wins:

- **Cost** — 21% cheaper overall. MCP has overhead from loading graph context into every response.
- **Speed** — 7% faster on average. Graph queries add round-trips.

### What would show a bigger MCP advantage:

- **Larger blast radius queries** spanning multiple services (not a monolith)
- **Event-driven flows** where Kafka/pub-sub connections are non-obvious
- **Multi-repo projects** where baseline can't grep across repos
- **Stale-code detection** where the graph tracks freshness

## Total Benchmark Cost

| Item | Cost |
|------|------|
| Scan pass 1 (code + contract) | $2.60 |
| Scan pass 2 (data + integrations) | $2.88 |
| Benchmark runs (30 sessions) | $3.74 |
| **Total** | **$9.22** |

## Comparison with Tom-and-Jerry

| | Tom-and-Jerry (v2) | OtoPoint |
|--|-------------------|----------|
| Codebase | 4 microservices, 40 files | 1 monolith, 6000+ files |
| Graph | 59 nodes, 71 edges | 264 nodes, 331 edges |
| MCP correctness | 48/52 (92%) | 64/64 (100%) |
| Baseline correctness | 47/52 (90%) | 63/64 (98%) |
| MCP advantage | +2% correctness, -24% time | +2% correctness, -4% tokens |
| Key finding | Kafka paths + impact surface | Consistency + zero hallucinations |
