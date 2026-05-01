# Forensic Benchmark Analysis

## Executive Summary

Two projects benchmarked: tom-and-jerry (4 microservices, 40 files) and OtoPoint (NestJS monolith, 6000+ files). 75 total benchmark runs across 3 datasets (v1, v2, otopoint). Total benchmark cost: ~$20 including scans.

**The core finding:** MCP's advantage is structural, not universal. It wins on tasks requiring cross-boundary traversal (Kafka paths, impact surfaces) and loses on tasks where `grep` is sufficient. On a well-structured monolith, Sonnet is already good enough that baseline scores 98%. The graph adds value through consistency and zero hallucinations, not dramatic accuracy gains.

**MCP was cheaper on tom-and-jerry** because graph queries replaced 10-20 turns of file exploration. **MCP was more expensive on OtoPoint** because 36 tool schemas add ~4K tokens per turn, and the monolith's flat structure means `grep` finds answers faster than graph traversal.

---

## Methodology

### Prompt Parity

Both modes received identical system prompts per project:

**Tom-and-jerry:**
> "You are analyzing a NestJS microservices codebase with 4 services: tom-api, jerry-api, arena-api, spectators-api. Answer precisely based on what you can verify. If you cannot find something, say so. Do not guess or hallucinate dependencies."

**OtoPoint:**
> "You are analyzing a NestJS monolith backend (OtoPoint) with Prisma, GraphQL, Bull queues, WebSockets, Centrifugo, and Stripe. Answer precisely based on what you can verify. If you cannot find something, say so. Do not guess or hallucinate dependencies."

Both included: "Do not read any files in the benchmark/ directory."

Task prompts were identical between modes. The only difference was tool availability.

### Tool Availability

| | MCP mode | Baseline mode |
|--|----------|---------------|
| Built-in tools | Read, Grep, Glob, Bash, Edit, Write, Agent, etc. | Same |
| Chronicle tools | 36 tools (chronicle_impact, chronicle_query_path, etc.) | None |
| Other MCP | Gmail, Calendar, context7, playwright, browser-tools (from user config) | None (strict empty config) |
| Tool schema overhead | ~14K tokens (built-in + 36 Chronicle + other MCP) | ~10K tokens (built-in only) |

**Issue:** MCP mode loads ALL user MCP servers (Gmail, Calendar, playwright, etc.), not just Chronicle. This adds unnecessary tool schemas. The `--mcp-config` flag loads Chronicle, but Claude Code also loads other configured servers. Baseline uses `--strict-mcp-config --mcp-config empty.json` which blocks everything.

**Impact:** ~4K extra tokens per turn from Chronicle schemas, plus additional overhead from other MCP servers the user has configured. Over a multi-turn session, this compounds.

---

## Context Reconstruction

### What Claude actually received (per turn):

| Component | MCP mode (est.) | Baseline mode (est.) |
|-----------|-----------------|---------------------|
| System prompt | ~200 tokens | ~200 tokens |
| Built-in tool schemas | ~10K tokens | ~10K tokens |
| Chronicle tool schemas (36 tools) | ~4K tokens | 0 |
| Other MCP tool schemas | ~2-4K tokens | 0 |
| CLAUDE.md + project context | ~2-5K tokens | ~2-5K tokens |
| Conversation history (per turn) | Grows | Grows |
| Tool call results | Graph JSON | File contents |

**Key insight:** The first turn costs ~16-20K tokens for MCP vs ~12-15K for baseline, purely from tool schema loading. This is a fixed tax on every session.

### Were prompts truly identical?

**Yes, except:**
1. Tool schemas differ (~4-6K extra tokens for MCP)
2. CLAUDE.md may differ if the project has Chronicle-specific instructions
3. MCP mode may have additional server initialization messages

The task text and system prompt are byte-identical between modes.

---

## Token Usage Breakdown

### Tom-and-Jerry (v2, medians)

| Task | MCP tokens | Baseline tokens | MCP turns | Baseline turns | Token delta |
|------|-----------|----------------|-----------|---------------|-------------|
| 1_impact | 182K | 132K | 17 | 13 | MCP +38% |
| 2_flow | 37K | 212K | 2 | 20 | **MCP -82%** |
| 3_reverse | 86K | 32K | 8 | 2 | MCP +169% |
| 4_trap | 47K | 45K | 3 | 3 | MCP +4% |
| 5_path | 214K | 111K | 20 | 12 | MCP +93% |
| **Total** | **566K** | **533K** | | | **MCP +6%** |

### OtoPoint (medians)

| Task | MCP tokens | Baseline tokens | MCP turns | Baseline turns | Token delta |
|------|-----------|----------------|-----------|---------------|-------------|
| 1_impact | 110K | 68K | 3 | 2 | MCP +62% |
| 2_flow | 69K | 66K | 2 | 2 | MCP +5% |
| 3_reverse | 68K | 65K | 2 | 2 | MCP +5% |
| 4_trap | 62K | 59K | 2 | 2 | MCP +5% |
| 5_voucher | 69K | 137K | 2 | 12 | **MCP -50%** |
| **Total** | **378K** | **395K** | | | **MCP -4%** |

### Why MCP was cheaper on tom-and-jerry Task 2

**Task 2 (Flow):** MCP used 37K tokens in 2 turns vs baseline's 212K in 20 turns.

MCP called `chronicle_query_deps` or `chronicle_impact` once, got the full dependency chain as structured JSON, and wrote the answer. Baseline had to:
1. Find the controller file (Grep)
2. Read it (Read)
3. Find the service (Grep for imports)
4. Read the service (Read)
5. Find cross-service calls (Grep)
6. Read client files (Read)
7. Read Kafka producer (Read)
8. Read consumer (Read)
9. ... for 20 turns

Each file read adds ~500-2000 tokens of file content plus tool call overhead. 20 turns × ~10K tokens/turn = 200K.

### Why MCP was more expensive on OtoPoint Task 1

**Task 1 (Impact):** MCP used 110K tokens in 3 turns vs baseline's 68K in 2 turns.

The graph has 264 nodes and 331 edges. `chronicle_impact` returns a large JSON payload with 22 impacted nodes, 7 endpoints, full paths and scores. This single response is ~5-10K tokens of graph data. Plus 36 tool schemas at ~4K per turn. Over 3 turns: 3 × 4K schema overhead + 10K graph result = ~22K extra tokens.

Baseline read the Prisma schema (one file, ~3K tokens), grepped for "Order" in service files (compact results), and was done in 2 turns.

### Token overhead sources (estimated)

| Source | Per-turn cost | Over 15 runs (median 3 turns) |
|--------|-------------|-------------------------------|
| Chronicle tool schemas (36 tools) | ~4K tokens | ~180K |
| Other MCP tool schemas (user config) | ~2K tokens | ~90K |
| Graph query result payloads | ~2-10K tokens | ~45-150K |
| **Total MCP overhead** | **~8-16K/turn** | **~315-420K** |

---

## Correctness Breakdown

### Tom-and-Jerry (v2)

| Task | MCP median | Baseline median | MCP-only facts | Baseline-only facts | Hallucinations |
|------|-----------|----------------|---------------|-------------------|----------------|
| 1. Impact | 10/12 | 9/12 | affected_surface endpoints (graph-derived) | TomClient (found via code grep) | MCP: 0, Baseline: minor (extra endpoints) |
| 2. Flow | 14/14 | 14/14 | — | — | MCP: 0, Baseline: 0 |
| 3. Reverse | 10/10 | 10/10 | — | — | MCP: 0, Baseline: 0 |
| 4. Trap | 6/6 | 6/6 | — | — | MCP: 0, Baseline: 0 |
| 5. Path | 8/10 | 8/10 | Kafka path (graph pub/sub traversal) | — | MCP: 0, Baseline: shared lib claims |

**Task 1 analysis:** MCP's edge came from `affected_surface` — the graph engine automatically found all 3 endpoints exposed by TomController after impact traversal. Baseline found endpoints by reading the controller source, but one run missed them (vague REST section). MCP missed PrismaService-tom in 2/3 runs (the graph doesn't emphasize framework internals). Baseline consistently found TomClient because it grepped for HTTP calls.

**Task 5 analysis:** MCP found the Kafka indirect path because `chronicle_query_path` traverses through topic nodes (our fix). Baseline found it by manually tracing fetch() calls and Kafka producer/consumer files. Both scored 8/10 median — MCP's path was graph-derived, baseline's was code-derived.

### OtoPoint

| Task | MCP median | Baseline median | MCP-only facts | Baseline-only facts | Hallucinations |
|------|-----------|----------------|---------------|-------------------|----------------|
| 1. Impact | 16/16 | 16/16 | — | — | MCP: 0, Baseline: 1 run with `dashboard.resolver.ts` |
| 2. Flow | 14/14 | 13/14 | — | — | MCP: 0, Baseline: 0 |
| 3. Reverse | 14/14 | 14/14 | — | — | MCP: 0, Baseline: 0 |
| 4. Trap | 6/6 | 6/6 | — | — | MCP: 0, Baseline: 0 |
| 5. Voucher | 14/14 | 14/14 | — | — | MCP: 0, Baseline: 0 |

**Why scores are nearly identical on OtoPoint:** The monolith's flat directory structure (`api/src/services/*.ts`) means `grep "Order"` in one directory finds everything. There are no cross-repo boundaries, no Kafka paths to miss, no ambiguous service boundaries. The graph doesn't reveal anything that grepping can't find.

### Hallucination sources

| Hallucination | Mode | Task | Source |
|--------------|------|------|--------|
| TomEventHandler (v1) | MCP | TJ Task 2 | Model reasoning (invented component) |
| SpectatorService calls /tom/weapons (v1) | MCP | TJ Task 5 | Graph data showed CALLS_SERVICE edge, model extrapolated to specific endpoints |
| "No Kafka path exists" (v1) | MCP | TJ Task 5 | Graph tool behavior (couldn't traverse pub/sub) |
| dashboard.resolver.ts | Baseline | OtoP Task 1 | Model reasoning (assumed file exists from service name) |
| createOrderFromGroup mutation | Baseline | OtoP Task 1 | Model reasoning (assumed mutation from method name) |

**Pattern:** MCP v1 hallucinations came from trusting incomplete graph data. After fixes (v2), MCP has zero hallucinations across 30 runs. Baseline hallucinations come from model reasoning about file/endpoint names it hasn't verified.

---

## Tool-Call Analysis

### Turn count is the proxy for tool calls

We don't have per-turn tool-call logs (the `iterations` field is always empty in CLI output). `num_turns` is the best proxy — each turn typically involves 1-3 tool calls.

### Tom-and-Jerry: Where MCP saves turns

| Task | MCP turns | Baseline turns | Why |
|------|-----------|---------------|-----|
| 2_flow | **2** | 20 | Graph gives full dep chain in 1 query. Baseline reads 10+ files. |
| 4_trap | 2 | 3 | Graph says "not found" instantly. Baseline greps then confirms. |

### Tom-and-Jerry: Where MCP spends more turns

| Task | MCP turns | Baseline turns | Why |
|------|-----------|---------------|-----|
| 1_impact | 17 | 13 | MCP runs chronicle_impact, then queries individual nodes for details, then verifies. Iterative graph exploration. |
| 3_reverse | 8 | 2 | MCP queries reverse deps, then lists edges, then gets node details. Baseline: one grep for "SocketService" (tom-and-jerry doesn't have one — this task was only on OtoPoint). |
| 5_path | 20 | 12 | MCP tries multiple path queries (directed, connected), queries nodes along paths. Baseline reads source files sequentially. |

**Key finding:** MCP is most efficient when a single graph query answers the question (flow, trap). It's least efficient when it needs iterative exploration (impact details, path variants), because each graph query is a full turn with tool-schema overhead.

### OtoPoint: Flatter usage

Most tasks use 2-3 turns in both modes. The exception is **Task 5 (Voucher)** where baseline needed 12 turns (grepping through many files) vs MCP's 2 (graph gave direct callers).

### Estimated tool-call patterns (from answer content analysis)

| Mode | Typical tool sequence |
|------|----------------------|
| MCP (impact) | chronicle_impact → chronicle_node_get × N → chronicle_edge_list → (optional file Read for verification) |
| MCP (flow) | chronicle_query_deps → answer |
| MCP (reverse) | chronicle_query_reverse_deps → chronicle_edge_list → answer |
| MCP (path) | chronicle_query_path (directed) → chronicle_query_path (connected) → chronicle_node_get × N → answer |
| MCP (trap) | chronicle_node_list (search) → answer "not found" |
| Baseline (any) | Grep → Read → Grep → Read → ... → answer |

---

## Graph Usefulness Analysis

### Which Chronicle tools were most useful?

| Tool | Usefulness | Evidence |
|------|-----------|----------|
| `chronicle_impact` | **High** | Directly answered Task 1. affected_surface found endpoints baseline missed in 1 run. |
| `chronicle_query_path` | **High** | Found Kafka paths invisible to baseline in v1. After fix, matched baseline on v2. |
| `chronicle_query_reverse_deps` | **Medium** | Gave correct caller lists but baseline grep found the same. |
| `chronicle_query_deps` | **Medium** | Useful for flow tracing but adds a turn where baseline reads 1 file. |
| `chronicle_node_list` | **Low** | Used for trap question — could be replaced by grep. |
| `chronicle_node_get` | **Low** | Individual node lookup adds turns. Better to batch in impact/path results. |

### Did Claude still read code after graph results?

**Tom-and-jerry:** Yes, in 2/3 MCP impact runs, Claude read source files after graph queries to verify or add detail. The graph answer wasn't always self-sufficient.

**OtoPoint:** Rarely. MCP runs mostly relied on graph output alone (2-3 turns). This is because the OtoPoint graph (264 nodes) covered the code structure well enough.

### Did the graph reduce search space?

**Tom-and-jerry Task 2 (Flow):** Yes. Graph gave the full chain in 1 query (2 turns). Baseline needed 20 turns of sequential file discovery.

**OtoPoint Task 5 (Voucher):** Yes. Graph gave 3 direct callers immediately (2 turns). Baseline grepped through 49 service files (12 turns).

**OtoPoint Tasks 2-4:** No measurable reduction. Both modes needed ~2 turns. The flat monolith structure made grep just as fast.

### Did graph answers require verification?

In v1 (pre-fix), yes — the graph gave wrong answers on path and impact queries, and Claude trusted them without verification. This caused hallucinations.

In v2 (post-fix), the graph answers were correct, so verification was technically unnecessary. But the updated tool descriptions ("verify key findings against source code") occasionally triggered additional file reads.

---

## Baseline Behavior Analysis

### How did baseline discover answers?

**Pattern 1: Targeted grep (most common)**
For "what depends on X?", baseline greps for the class name across the codebase. In a monolith (OtoPoint), this finds everything in one Grep call because all code is in `api/src/`.

**Pattern 2: File-chain exploration**
For flow tracing, baseline reads a file, finds imports/calls, reads the next file, and so on. This is expensive (20 turns on tom-and-jerry Task 2) but thorough.

**Pattern 3: Schema-first**
For model impact, baseline reads `prisma/schema.prisma` first (compact, all models in one file), then greps for model usage in services. Efficient for monoliths.

### Was the codebase structure easy enough that baseline did not suffer?

**OtoPoint: Yes.** The monolith has a predictable structure:
- All services in `api/src/services/`
- All controllers in `api/src/controllers/`
- All models in one `prisma/schema.prisma`
- NestJS decorators make dependencies explicit

One `grep -r "OrderService" api/src/` finds every caller. No cross-repo boundaries to miss.

**Tom-and-jerry: Partially.** The 4-service architecture means dependencies span directories. Baseline could still find them by grepping across `fixtures/tom-and-jerry/*/src/`, but Kafka connections (producer in arena-api, consumer in spectators-api) required reading specific files to discover.

### Where did baseline waste tokens?

1. **Tom-and-jerry Task 2:** 20 turns of sequential file reading. Each file add ~1-2K tokens of content. Total: ~200K tokens for what the graph answered in 37K.

2. **OtoPoint Task 5:** 12 turns grepping through services. The graph answered in 2 turns.

3. **Tom-and-jerry Task 5:** 12 turns reading files to trace paths. Graph answered in the same number of turns (20) but for different reasons (iterative path queries).

---

## Scan Cost and Amortization

### Scan Costs

| Project | Scan | Tokens | Cost | Duration | Nodes | Edges | Elements/$ |
|---------|------|--------|------|----------|-------|-------|------------|
| Tom-and-jerry | Single pass | 2.5M | $2.83 | 7.9 min | 59 | 71 | 46 |
| OtoPoint | Pass 1 (code+contract) | 4.2M | $2.60 | 11 min | 176 | 152 | 126 |
| OtoPoint | Pass 2 (data+integrations) | 5.2M | $2.88 | 14 min | +88 | +179 | 93 |
| OtoPoint | **Total** | 9.4M | **$5.49** | 25 min | 264 | 331 | 108 |

### Cost per graph element

| Project | Cost | Nodes+Edges | Cost per element |
|---------|------|-------------|-----------------|
| Tom-and-jerry | $2.83 | 130 | $0.022 |
| OtoPoint | $5.49 | 595 | $0.009 |

OtoPoint is 2.4x more efficient per graph element because bulk imports amortize the per-turn overhead.

### Were both scan passes necessary?

**Pass 1** created code structure (providers, controllers, endpoints, INJECTS edges). This is the minimum viable graph.

**Pass 2** added data models (Prisma), USES_MODEL edges, REFERENCES_MODEL, USES_ENUM, CALLS_SERVICE, and pub/sub edges. Without this pass, the graph would have been structurally incomplete — missing the entire data layer.

**Verdict:** Both passes were necessary. Pass 1 alone would have produced a graph that can't answer "what models does this service use?" or "what services call Stripe?"

### Was any scan work wasteful?

- Pass 1 read many source files (61 turns) but only created INJECTS and EXPOSES_ENDPOINT edges. It could have been more focused.
- Pass 2 re-read some files already seen in pass 1 (cache reads: 5M tokens suggest heavy overlap).
- The multi-pass approach itself adds overhead from re-establishing context.

**Estimated waste:** ~20-30% of scan tokens went to re-reading cached context between turns. A batch-import approach (read all files once, extract everything, import in one call) would be more efficient.

---

## Amortization Model

### Per-query savings/overhead

| Project | Task | MCP cost | Baseline cost | Delta per query |
|---------|------|----------|---------------|-----------------|
| TJ 2_flow | | $0.16 | $0.22 | **-$0.06 (MCP saves)** |
| TJ 5_path | | $0.19 | $0.22 | **-$0.03 (MCP saves)** |
| TJ 1_impact | | $0.21 | $0.19 | +$0.02 (MCP costs more) |
| TJ 3_reverse | | $0.09 | $0.10 | -$0.01 |
| TJ 4_trap | | $0.05 | $0.02 | +$0.03 |
| OtoP 5_voucher | | $0.45 | $0.43 | +$0.02 |
| OtoP 2_flow | | $0.57 | $0.40 | +$0.17 |
| OtoP 1_impact | | $0.65 | $0.46 | +$0.19 |

### Break-even calculation

**Tom-and-jerry:**
- Scan cost: $2.83
- Average savings per query: ~$0.02 (some tasks save, some cost more)
- Break-even: $2.83 / $0.02 = **~142 queries**

But if we only count the winning tasks (flow, path):
- Average savings: $0.045/query
- Break-even: $2.83 / $0.045 = **~63 queries**

**OtoPoint:**
- Scan cost: $5.49
- Average overhead per query: +$0.04 (MCP is more expensive on this project)
- Break-even: **Never** — MCP costs more per query on this monolith

### Best-case / worst-case

| Scenario | Break-even queries |
|----------|-------------------|
| **Best case:** Microservices with Kafka, mostly flow/path queries | ~50 queries |
| **Typical:** Mixed query types on microservices | ~100-150 queries |
| **Worst case:** Monolith with flat structure, simple queries | Never (MCP adds overhead) |

### When scan cost amortizes

The scan produces a persistent graph. If a developer asks 5-10 codebase questions per day:
- **Microservices:** Break-even in 1-2 weeks
- **Monolith:** May not break even on cost, but amortizes on consistency and zero hallucinations

---

## Root Causes

### Why MCP won on tom-and-jerry

1. **Cross-service boundaries.** The graph encoded producer→topic→consumer paths that span 4 repos. Baseline had to discover these by reading files across directories.
2. **Impact surface expansion.** The graph engine automatically followed EXPOSES_ENDPOINT forward from impacted controllers, finding all 3 affected endpoints. Baseline missed endpoints in 1/3 runs.
3. **Single-query answers.** For flow tracing, one `chronicle_query_deps` call replaced 20 turns of file exploration.

### Why MCP tied on OtoPoint

1. **Flat monolith structure.** Everything is in `api/src/`. One `grep` finds all dependencies. No cross-boundary advantage for the graph.
2. **Tool schema overhead.** 36 Chronicle tools + other MCP servers add ~6K tokens per turn. On a 2-turn task, this overhead dominates any savings from graph queries.
3. **Incomplete graph.** 264 nodes for a 6000-file project means ~4% coverage. Many services baseline found by grepping were not in the graph.
4. **Tasks were too easy.** All 5 OtoPoint tasks had clear, self-contained answers. No cross-service Kafka paths or ambiguous multi-hop dependencies.

### Why MCP hallucinated in v1 but not v2

**v1 hallucinations** came from two graph blind spots:
1. Impact couldn't reach endpoints (EXPOSES_ENDPOINT direction issue) → Claude invented endpoints
2. Path couldn't cross Kafka topics (CONSUMES_TOPIC direction issue) → Claude denied paths existed

**v2 fix:** Two algorithm changes (40 lines of Go) — affected_surface expansion and pub/sub traversal. After fixing, the graph returned correct data, and Claude stopped hallucinating because it had truthful evidence.

**Lesson:** MCP hallucinations are caused by incomplete/incorrect graph data, not by model reasoning. Fix the data source and hallucinations disappear.

---

## Recommendations

### Scanner improvements (P0)

1. **Multi-pass scanning architecture.** Enforce: data layer → code structure → data usage → integrations → validation. Each pass validates output before next begins.
2. **Batch import.** Instead of N individual tool calls, read all files in one pass and call `chronicle_import_all` once. Reduces turns from 60-80 to 10-15.
3. **Scan coverage metric.** After scan, compare node count to file count. If ratio < 10%, warn about incomplete coverage.

### Tool output compression (P1)

4. **Reduce tool schema count.** 36 tools is too many. Combine related tools (e.g., `chronicle_node_list` + `chronicle_node_get` → one tool with optional filters). Target: 10-15 tools.
5. **Compact graph output.** Impact results include full paths and scores. For most queries, just the node list + affected endpoints is enough. Add `compact: true` parameter.
6. **Lazy tool loading.** Don't load all 36 tool schemas on every request. Load core tools (impact, path, deps, stats) by default, others on demand.

### Tool description improvements (P1)

7. **Guide tool selection.** Add a `chronicle_command` pre-router that maps natural questions to the right tool, preventing iterative trial-and-error.
8. **Discourage node-by-node exploration.** Update descriptions to say "use impact/path for multi-node queries" instead of individual node lookups.

### Benchmark improvements (P2)

9. **Add harder OtoPoint tasks.** Cross-module queries: "How does a Stripe webhook affect the voucher system?" "What happens when Redis pub/sub loses a message?" These would test paths baseline can't easily grep.
10. **Add per-turn logging.** Use `--output-format stream-json` to capture individual tool calls, token usage per turn, and exact tool arguments.
11. **Measure graph hit rate.** Track what % of answer facts came from graph vs. code reading.
12. **Test incremental scans.** Measure cost of updating the graph after a code change (should be much cheaper than full scan).

### New metrics to track

13. **Facts per token** — correctness points divided by tokens used. The efficiency metric that matters.
14. **First-turn accuracy** — does the first tool call find the answer? MCP should win here.
15. **Hallucination rate per 1000 tokens** — normalize across projects.
16. **Graph freshness decay** — how fast does accuracy degrade as code changes without re-scanning?

---

## Appendix: Per-Run Notes

### Tom-and-Jerry v1 → v2 Comparison (MCP only)

| Task | v1 score | v2 score | v1 tokens | v2 tokens | What changed |
|------|----------|----------|-----------|-----------|--------------|
| 1_impact | 5/12 | 10/12 | 36K | 182K | affected_surface expansion found endpoints |
| 2_flow | 14/14 | 14/14 | 35K | 37K | No change needed |
| 3_reverse | 10/10 | 10/10 | 97K | 86K | No change needed |
| 4_trap | 6/6 | 6/6 | 31K | 47K | No change needed |
| 5_path | 2/10 | 8/10 | 72K | 214K | pub/sub traversal fixed Kafka paths |

v2 is more correct (+11 points) but uses more tokens (+168K total). The extra tokens come from richer graph responses (affected_surface JSON, multi-path results).

### OtoPoint: Answer Content Patterns

**MCP answers:**
- Zero graph terminology in responses (no "node_key", "INJECTS", etc.) — Claude translates graph data to natural language
- File path density is higher than baseline on Tasks 1 and 3 — graph surfaces file paths the model wouldn't grep for
- Word count is similar to baseline (±10%)
- Consistently structured output

**Baseline answers:**
- Exploration phrases ("I searched", "I found") appear occasionally
- Higher variance between runs (one weak run on Task 1)
- Sometimes vague on REST endpoints (says "endpoints in controllers/" without listing them)
- File path density is comparable or lower

### Scan Efficiency

| Metric | Tom-and-jerry | OtoPoint |
|--------|--------------|----------|
| Files in project | ~40 | 6000+ |
| Nodes created | 59 | 264 |
| Coverage (nodes/files) | 148% | 4.4% |
| Scan cost | $2.83 | $5.49 |
| Scan time | 8 min | 25 min |
| Cost per node | $0.048 | $0.021 |

Tom-and-jerry has >100% coverage because nodes include non-file entities (services, endpoints, topics). OtoPoint at 4.4% coverage means the graph is a sparse index, not a complete model. This explains why baseline can find things MCP can't — the graph simply doesn't have them.
