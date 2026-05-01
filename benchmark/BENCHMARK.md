# Chronicle MCP — Manual A/B Benchmark

## Goal

This benchmark compares Claude Code performance **with** and **without** Chronicle MCP on the same codebase analysis tasks.

**What we measure:**
1. **Correctness** — does the answer match ground truth? (primary)
2. **Hallucination reduction** — does MCP prevent invented dependencies?
3. **Completeness** — how many expected items are found?
4. **Token/tool-call efficiency** — rediscovery cost reduction (secondary)

**Framing:** Chronicle MCP improves codebase reasoning accuracy while reducing rediscovery cost.

## Rules

- Each task is run **3 times per mode** (3x MCP, 3x baseline = 6 runs per task).
- Each run **must start from a fresh Claude session**. Do not reuse conversation context.
- Do **not** reveal ground truth to Claude.
- Do **not** prime Claude with hints about expected answers.
- Score only **facts that match ground truth**. Do not score "nice explanation."
- Use the **median** of 3 runs for final comparison (avoids outlier bias).

## Scoring Scale

```
10 = all expected items found, zero hallucinations
7-9 = most expected items found, minor misses
4-6 = partial answer, important misses
1-3 = mostly wrong or mostly hallucinated
 0 = complete hallucination or total failure
```

## Setup

**Target codebase:** `fixtures/tom-and-jerry/` — 4 NestJS microservices (tom-api, jerry-api, arena-api, spectators-api) with Prisma, HTTP cross-service calls, Kafka.

**Pre-built graph:** `.depbot/chronicle.db` — 47 nodes, 68 edges, 24 evidence entries.

### Run A — With MCP (Chronicle)

```bash
cd fixtures/tom-and-jerry
claude
# Chronicle MCP tools are available via CLAUDE.md
# Paste the task prompt, then /exit when done
```

### Run B — Without MCP (Baseline)

```bash
cd fixtures/tom-and-jerry
claude --no-mcp
# Only file reading tools available
# Paste the same task prompt, then /exit when done
```

### What to record after each run

| Metric | How to get it |
|--------|--------------|
| Total tokens | Shown in session summary on /exit |
| Tool calls | Count in conversation |
| Duration | Wall clock (start → final answer) |
| Files read | Count Read/Glob/Grep tool calls |
| MCP calls | Count chronicle_* tool calls |

---

## Tasks

### TASK 1 — Impact Analysis (Cat model)

**Prompt (paste as-is):**

```
What breaks if I change the Cat data model? List all directly affected components,
transitive dependencies (up to depth 3), affected API endpoints, and external systems.
Provide file references where possible.

Format your answer as:
## Direct Dependencies
## Transitive Dependencies
## Affected Endpoints
## External Systems
## Confidence (0-100)
```

**Ground truth (do not show to Claude):**

Direct dependencies:
- CatWeapon model (REFERENCES_MODEL, tom-api/prisma/schema.prisma)
- TomService (USES_MODEL Cat, tom-api/src/tom/tom.service.ts)

Transitive (depth 2-3):
- TomController (INJECTS TomService, tom-api/src/tom/tom.controller.ts)
- PrismaService-tom (INJECTS by TomService)
- TomModule (CONTAINS TomService, TomController)
- TomClient in arena-api (CALLS_SERVICE tom-api)

Affected endpoints:
- GET /tom/status (exposed by TomController)
- GET /tom/weapons (exposed by TomController)
- POST /tom/arm (exposed by TomController)

External systems:
- None directly. Cat model is internal to tom-api.
- Indirect: arena-api calls tom-api via TomClient (HTTP)

**Checklist (max 12 points):**
- [ ] CatWeapon found (+1)
- [ ] TomService found (+1)
- [ ] TomController found (+1)
- [ ] PrismaService-tom found (+1)
- [ ] TomClient / arena-api cross-service found (+1)
- [ ] GET /tom/status (+1)
- [ ] GET /tom/weapons (+1)
- [ ] POST /tom/arm (+1)
- [ ] No hallucinated direct deps (+2)
- [ ] No hallucinated endpoints (+2)

---

### TASK 2 — Request Flow (POST /arena/attack)

**Prompt:**

```
Trace the full request flow for POST /arena/attack. Show every component involved
from the HTTP request to any async side effects (Kafka, webhooks, etc).
Include cross-service calls.

Format:
## Request Chain (ordered)
## Cross-Service Calls
## Async Side Effects
## Data Models Touched
```

**Ground truth:**

Request chain:
1. POST /arena/attack → ArenaController (arena.controller.ts)
2. ArenaController → ArenaService (INJECTS)
3. ArenaService → TomClient (INJECTS) → calls GET /tom/status on tom-api
4. ArenaService → JerryClient (INJECTS) → calls GET /jerry/status on jerry-api
5. ArenaService → JerryClient → calls GET /jerry/traps on jerry-api
6. ArenaService → PrismaService-arena (INJECTS) → persists to BattleEvent model
7. ArenaService → BattleResultProducer (INJECTS) → publishes to battle-results topic

Cross-service calls:
- arena-api → tom-api (via TomClient, HTTP)
- arena-api → jerry-api (via JerryClient, HTTP)

Async side effects:
- BattleResultProducer → battle-results Kafka topic
- BattleResultConsumer (spectators-api) consumes battle-results
- SpectatorService processes the event

Data models touched:
- BattleEvent (arena-api/prisma/schema.prisma)
- Cat (read via tom-api, indirectly)
- Mouse (read via jerry-api, indirectly)

**Checklist (max 14 points):**
- [ ] ArenaController entry point (+1)
- [ ] ArenaService (+1)
- [ ] TomClient → tom-api call (+1)
- [ ] JerryClient → jerry-api call (+1)
- [ ] PrismaService / BattleEvent persistence (+1)
- [ ] BattleResultProducer → Kafka (+1)
- [ ] battle-results topic named (+1)
- [ ] BattleResultConsumer on spectators-api side (+1)
- [ ] SpectatorService downstream (+1)
- [ ] Cross-service: arena→tom (+1)
- [ ] Cross-service: arena→jerry (+1)
- [ ] BattleEvent model (+1)
- [ ] No hallucinated steps (+2)

---

### TASK 3 — Reverse Dependencies (battle-results topic)

**Prompt:**

```
What components depend on the "battle-results" Kafka topic?
List producers, consumers, and the services they belong to.
What happens downstream after a message is published?

Format:
## Producers
## Consumers
## Downstream Flow
## Services Involved
```

**Ground truth:**

Producers:
- BattleResultProducer (arena-api/src/arena/battle-result.producer.ts)

Consumers:
- BattleResultConsumer (spectators-api/src/spectators/battle-result.consumer.ts)

Downstream flow:
- BattleResultConsumer → SpectatorService (INJECTS)
- SpectatorService → NotificationService (pushes notifications)
- StatsController (exposes GET /stats/leaderboard, GET /stats/recent)

Services involved:
- arena-api (produces)
- spectators-api (consumes + processes)

**Checklist (max 10 points):**
- [ ] BattleResultProducer identified (+1)
- [ ] Correct file path for producer (+1)
- [ ] BattleResultConsumer identified (+1)
- [ ] Correct file path for consumer (+1)
- [ ] SpectatorService downstream (+1)
- [ ] StatsController / endpoints downstream (+1)
- [ ] arena-api as producer service (+1)
- [ ] spectators-api as consumer service (+1)
- [ ] No hallucinated topics or services (+2)

---

### TASK 4 — Trap Question (non-existent component)

**Prompt:**

```
What components depend on CacheInvalidator? List all dependencies and affected endpoints.
```

**Ground truth:**

CacheInvalidator does not exist in this codebase. The correct answer is "not found" or "does not exist."

Note: There IS a CacheService in arena-api (src/arena/cache.service.ts), but NO CacheInvalidator.

**Checklist (max 6 points):**
- [ ] Says "not found" / "doesn't exist" (+2)
- [ ] Does not hallucinate dependencies (+2)
- [ ] Does not confuse with CacheService (+1)
- [ ] Does not invent endpoints (+1)

---

### TASK 5 — Path Finding (spectators-api to tom-api)

**Prompt:**

```
How is spectators-api connected to tom-api? Find all paths between these two services.
Show both direct and indirect connections.

Format:
## Direct Connections
## Indirect Connections (via other services)
## Shared Dependencies
```

**Ground truth:**

Direct connections:
- SpectatorService (spectators-api) → CALLS_SERVICE → tom-api (HTTP call)

Indirect connections (via arena-api):
- spectators-api ← battle-results topic ← BattleResultProducer (arena-api)
- arena-api → TomClient → CALLS_SERVICE → tom-api
- So: spectators-api <-[kafka]- arena-api -[http]-> tom-api

Shared dependencies:
- None at the data model level (different Prisma schemas)
- Both are called by arena-api

**Checklist (max 10 points):**
- [ ] Direct: SpectatorService → tom-api HTTP call (+2)
- [ ] Indirect: Kafka path via arena-api (+2)
- [ ] arena-api as intermediary (+2)
- [ ] Both called by arena-api (+1)
- [ ] No shared data models (correctly noted) (+1)
- [ ] No hallucinated connections (+2)

---

## Results Template

Copy and fill for **each of the 3 runs** per task per mode:

```
### Run: [task_N] / [mcp|baseline] / run [1|2|3]

Duration: ____

Tokens (input / output / total): ____ / ____ / ____
Tool calls total: ____
  - File reads (Read/Glob/Grep): ____
  - Chronicle MCP calls: ____

Checklist score: ____ / [max]
Hallucinations: none | [describe]

Notes:
```

---

## Summary Table (use median of 3 runs)

| Task | Mode | Tokens (median) | Tool Calls (median) | Score (median) | Max | Hallucinations |
|------|------|-----------------|---------------------|----------------|-----|----------------|
| 1. Impact (Cat) | MCP | | | | /12 | |
| 1. Impact (Cat) | baseline | | | | /12 | |
| 2. Flow (attack) | MCP | | | | /14 | |
| 2. Flow (attack) | baseline | | | | /14 | |
| 3. Reverse (kafka) | MCP | | | | /10 | |
| 3. Reverse (kafka) | baseline | | | | /10 | |
| 4. Trap | MCP | | | | /6 | |
| 4. Trap | baseline | | | | /6 | |
| 5. Path | MCP | | | | /10 | |
| 5. Path | baseline | | | | /10 | |

---

## Aggregate Metrics

Calculate after all runs:

```
Token saving %     = (baseline_median - mcp_median) / baseline_median * 100
Correctness delta  = mcp_total_score - baseline_total_score  (out of 52)
Hallucination diff = baseline_hallucination_count - mcp_hallucination_count
```

### Final Summary

| Metric | Baseline | MCP | Delta |
|--------|----------|-----|-------|
| Total score (out of 52) | | | |
| Total tokens (sum of medians) | | | % saved |
| Total tool calls | | | |
| Tasks with hallucinations | /5 | /5 | |
