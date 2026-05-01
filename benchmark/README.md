# Chronicle MCP — Benchmark Suite

## What this tests

Does Chronicle MCP improve Claude Code's codebase reasoning compared to raw file reading (grep + Read)?

We run the same analysis tasks in two modes:
- **MCP mode** — Claude has Chronicle graph tools (impact, path, deps)
- **Baseline mode** — Claude has only file tools (Read, Grep, Glob, Bash)

Same prompts, same model, same codebase. Only tool availability differs.

## Results

### Microservices (tom-and-jerry — 4 NestJS services, Kafka, Prisma)

| Metric | MCP | Baseline |
|--------|:---:|:--------:|
| Correctness | **51/52 (98%)** | 47/52 (90%) |
| Speed | **259s** | 372s |
| Cost | $0.77 | $0.75 |
| Hallucinations | **0/15 runs** | 1/15 runs |

### Monolith (OtoPoint — 6000+ file NestJS, GraphQL, Bull, Stripe)

| Metric | MCP | Baseline |
|--------|:---:|:--------:|
| Correctness | **64/64 (100%)** | 63/64 (98%) |
| Speed | 596s | 557s |
| Cost | $2.09 | $1.65 |
| Hallucinations | **0/15 runs** | 1/15 runs |

### Where MCP wins

- **Cross-service dependencies** — Kafka producer→topic→consumer paths invisible to grep
- **Impact analysis** — graph traverses reverse deps + finds affected endpoints in one call
- **Consistency** — zero hallucinations, same quality across all runs
- **Speed on microservices** — 30% faster (graph replaces multi-file exploration)

### Where baseline is comparable

- Monoliths with flat structure (`grep` finds everything in one directory)
- Simple dependency lookups on well-structured NestJS code
- Trap questions (both correctly say "not found")

## Tasks

Each benchmark runs 5 tasks, 3 times per mode (30 sessions total per project):

| # | Task | What it tests |
|---|------|---------------|
| 1 | Impact Analysis | "What breaks if I change X?" — reverse deps + affected endpoints |
| 2 | Request Flow | Trace a request through all services, async effects |
| 3 | Reverse Dependencies | "Who depends on X?" — callers, gateways, consumers |
| 4 | Trap Question | Ask about something that doesn't exist — tests hallucination resistance |
| 5 | Path Finding | "How does A connect to B?" — direct + indirect paths |

## Methodology

### Prompt parity
Both modes receive identical system prompts and task prompts. The only difference is available tools.

### Scoring
- Each task has a **ground truth checklist** derived from the actual graph and source code
- Points awarded for specific facts found (e.g., "GET /tom/status endpoint found: +1")
- Points deducted for hallucinations (invented services, endpoints, or connections)
- Median of 3 runs used (avoids outlier bias)

### Ground truth source
- Graph queries against the Chronicle database (known-correct after scanning)
- Manual verification against source code for edge cases

### Rules
- Fresh Claude session per run (no context reuse)
- Ground truth never revealed to Claude
- Score facts only, not explanation quality

## Running the benchmark

```bash
# Tom-and-jerry (both modes)
cd fixtures/tom-and-jerry
bash ../../benchmark/run.sh

# MCP only (reuses baseline from previous run)
bash ../../benchmark/run.sh all mcp

# Single task
bash ../../benchmark/run.sh impact mcp
```

OtoPoint:
```bash
bash benchmark/otopoint/run.sh
```

## Claude Agent E2E tests

Separate from the benchmark — these test Claude's agent behavior with the graph:

```bash
bash benchmark/agent-e2e.sh
```

5 tests (8 assertions):
- High confidence → direct answer (no file reads needed)
- Empty graph → falls back to reading code
- Discovery → adds evidence to graph for future queries
- Wrong positive edge → code change detected → edge killed
- Partial coverage → Claude warns graph is incomplete

Cost: ~$3-4 per run.

## Files

```
benchmark/
├── README.md              # This file
├── BENCHMARK.md           # Task definitions + ground truth checklists
├── RESULTS-v1.md          # Pre-fix results (MCP lost on 2 tasks)
├── RESULTS-v2.md          # Post-fix results (MCP wins)
├── FORENSIC-ANALYSIS.md   # Deep-dive: tokens, tool calls, root causes
├── run.sh                 # Runner script (tom-and-jerry)
├── score.py               # Auto-scorer for metrics
├── agent-e2e.sh           # Claude agent behavior tests
├── trust-lifecycle-test.sh # Edge trust lifecycle test
├── empty-mcp.json         # Empty config for baseline mode
├── results/               # Answer texts + raw JSON
└── otopoint/              # OtoPoint benchmark (separate project)
```

## Cost

| Item | Cost |
|------|------|
| Full scan (tom-and-jerry, 73 nodes) | ~$1.70 |
| Full scan (otopoint, 264 nodes) | ~$5.50 |
| Benchmark run (30 sessions) | ~$3-4 |
| Agent E2E (5 tests) | ~$3-4 |
| **Total to reproduce everything** | **~$15** |
