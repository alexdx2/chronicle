<p align="center">
  <img src="assets/logo.png" alt="Chronicle MCP" width="180">
</p>

# Chronicle MCP

Persistent architecture memory for Claude Code.

Chronicle scans your codebase, builds a knowledge graph of models, services, endpoints, and dependencies — then lets Claude query that graph instead of rediscovering your architecture from scratch every session.

**Claude reads your code. Chronicle remembers what it learned.**

```
You: "What breaks if I change the User model?"

Chronicle:
  → UserService (depth 1, score 95) — USES_MODEL
  → AuthController (depth 2, score 45) — INJECTS
  → GET /auth/profile (depth 3, score 21) — EXPOSES_ENDPOINT

  3 services affected, 1 Kafka consumer downstream.
```

## Quick Start

```bash
npm install -g @alexdx/chronicle-mcp
claude mcp add chronicle -- chronicle mcp serve --open
```

Open Claude Code in any project. Say `chronicle scan`. Done.

The dashboard opens in your browser. Chronicle discovers your project structure and starts building the graph. Every session after this, Claude queries what it already knows instead of re-reading everything.

## What you can ask

- What breaks if I change `User`?
- Why does `OrderService` depend on `PaymentService`?
- Show the request flow for `POST /orders`
- Which endpoints touch this model?
- Find low-confidence or stale dependencies
- Show me a live diagram of the checkout flow

| Ask Claude | Chronicle does |
|---|---|
| `chronicle scan` | Full project scan — models, code, endpoints, services |
| `chronicle update` | Incremental update — rescan only changed files via git diff |
| `chronicle verify` | Find code evidence to confirm or reject low-confidence edges |
| `chronicle impact X` | Blast radius — what breaks if X changes |
| `chronicle deps X` | What does X depend on |
| `chronicle path A B` | How does A connect to B |
| `chronicle data` | Analyze data models (Prisma, TypeORM) |
| `chronicle flows` | End-to-end business use cases |
| `chronicle services` | Service architecture map |
| `chronicle language` | Domain glossary + violations |
| `chronicle diagram` | Live architecture diagram in the browser |
| `chronicle status` | Dashboard URL + graph stats |

## Why Chronicle exists

AI coding assistants are powerful, but they are stateless.

Every new session starts with the same wasteful ritual: Claude re-reads files, rebuilds mental models, guesses dependencies, and loses that knowledge when the chat ends. Context windows are finite. Your codebase isn't.

Chronicle turns that temporary understanding into durable infrastructure.

- **Persistent.** The graph survives between sessions. Claude picks up where it left off.
- **Incremental.** After the first scan, `chronicle update` runs a git diff and re-scans only what changed. Every commit makes the graph smarter, not slower.
- **Branch-aware.** Scan on `feature/payments` and the knowledge stays isolated from `main`. Switch branches, queries show the right context automatically.
- **Evidence-backed.** Every fact has provenance — file path, line number, confidence, derivation kind. When code changes, evidence gets invalidated. Trust scores recalculate. The graph knows how much to trust what it remembers.

## How it works

Chronicle is not a static code index. It is a living, evidence-backed architecture graph that Claude can query, update, and visualize.

When you say `chronicle scan`, Claude reads your code file by file and extracts structured facts: "UserService injects PrismaService", "OrderController exposes POST /orders", "api-service calls payments-service via HTTP". Chronicle validates, normalizes, and stores each fact in SQLite.

The graph is layered:

```
DATA         User ──→ Order ──→ OrderItem       (models, enums, relations)
               ↑
CODE         UserService ──→ PrismaService       (modules, controllers, providers)
               ↓
CONTRACT     GET /users/:id, order-created topic (endpoints, Kafka topics)
               ↓
SERVICE      api-service ──→ payments-service    (deployable services)
```

Questions that cross layers — "how does the User model connect to the payments-service?" — get real answers. Chronicle traces the path through code and contract layers.

**Incremental updates:**
```
git diff → changed files → invalidate stale evidence → re-scan only affected files
  → new evidence added → trust scores recalculated → graph updated
```

**Self-learning.** After each scan, Chronicle auto-discovers gaps — missing extractions, nodes without evidence, orphan providers. Discoveries feed into the next scan. The graph gets more complete and more confident over time.

**Domain language.** Define terms, aliases, anti-patterns for your project's vocabulary. If someone names a service "PurchaseService" where the correct term is "Order", Chronicle flags it.

## Live diagrams

Claude creates a diagram session, pushes nodes and edges to your browser in real-time, and annotates what it's explaining. The diagram updates as Claude talks — nodes light up, annotations appear, the view evolves step by step.

```
You: "Show me how the order flow works"

Claude opens a diagram in your browser:
  Step 1: POST /orders → OrderController (entry point)
  Step 2: OrderController → OrderService → PaymentsService (cross-service call)
  Step 3: OrderService → order-created topic (async event)
```

Navigate with Previous/Next. It's a live architecture tour, not a static PNG.

## Dashboard

Starts automatically with the MCP server. Embedded SPA — zero infrastructure, single binary.

- **Overview** — graph stats, real-time MCP request log, discoveries feed, growth chart
- **Graph** — three exploration modes:
  - **Tree** — hierarchical drill-down by layer
  - **Explore** — breadcrumb navigation
  - **Workspace** — drag entities onto a canvas, auto-find paths between them, expand neighbors
- **Language** — domain glossary editor + violation checker
- **Diagrams** — live sessions pushed by Claude, with step-through navigation
- **Settings** — manifest editor, MCP prompt customization, edge category config

Filter by node type, repo, confidence threshold. Hide a node type and Chronicle preserves logical connections through it — transitive edges show "via POST /api" so you don't lose the story.

## Try it

The repo includes a 4-service demo (Tom & Jerry) with a pre-built graph:

```bash
cd fixtures/tom-and-jerry
claude   # try: "chronicle impact Cat" or "chronicle diagram"
```

Four NestJS microservices with Prisma models, HTTP cross-service calls, Kafka topics, guards, interceptors, gateways. The graph is already scanned — just query it.

## Under the hood: MCP tool flow

When Claude analyzes something, it calls Chronicle MCP tools in sequence:

```
Claude                              Chronicle MCP
  │                                      │
  ├─ chronicle_impact(node_key,depth=4) ─→  BFS reverse traversal
  │                                      │  Returns: impacted nodes + scores + paths
  │                                      │
  ├─ chronicle_node_get("tomclient") ───→  Returns node + evidence[]
  │                                      │  file_path, line_start, confidence
  │                                      │
  ├─ chronicle_query_deps(node_key) ────→  Forward dependencies
  ├─ chronicle_query_reverse_deps() ────→  Reverse dependencies
  │                                      │
  └─ "3 services affected, here's why…" │
```

**Impact/deps don't include evidence.** Blast radius can touch 50 nodes — fetching evidence for all would be too heavy. Claude requests evidence per node when it needs to explain *why*.

**Evidence is the source of trust.** Every node and edge has provenance. When code changes, evidence gets invalidated, trust scores recalculate automatically.

**Command prompts are customizable.** Each command has step-by-step instructions telling Claude which tools to call. Edit them in the dashboard Settings tab.

| Category | Tools |
|----------|-------|
| **Read** | `impact`, `query_deps`, `query_reverse_deps`, `query_path`, `query_stats`, `node_get`, `edge_list` |
| **Write** | `revision_create`, `import_all`, `node_upsert`, `edge_upsert`, `evidence_add` |
| **Lifecycle** | `invalidate_changed`, `finalize_incremental_scan`, `snapshot_create`, `stale_mark` |
| **Meta** | `extraction_guide`, `scan_status`, `command`, `define_term`, `check_language` |
| **Visual** | `diagram_create`, `diagram_update`, `diagram_annotate` |

## Benchmark: MCP vs raw code reading

We tested Chronicle against Claude Code without MCP (baseline grep + file reads) on the same codebase analysis tasks. The graph consistently outperforms on correctness and speed.

**4-service microservices (tom-and-jerry fixture):**

| Task | Chronicle MCP | Baseline (grep) | Winner |
|------|:---:|:---:|:---:|
| Impact analysis | 11/12 | 9/12 | MCP |
| Request flow tracing | 14/14 | 14/14 | Tie |
| Reverse dependencies | 10/10 | 10/10 | Tie |
| Trap question (hallucination test) | 6/6 | 6/6 | Tie |
| Cross-service path finding | 10/10 | 8/10 | MCP |
| **Total correctness** | **51/52 (98%)** | **47/52 (90%)** | **MCP +8%** |
| **Speed** | **259s** | 372s | **MCP 30% faster** |
| **Hallucinations** | 0 | 1 run | **MCP cleaner** |

Chronicle wins on tasks that require cross-boundary reasoning — Kafka paths, impact surfaces, multi-service dependencies. Both tie on tasks where `grep` is sufficient.

Full methodology and raw data: [`benchmark/`](benchmark/)

## Multi-repo

Need to connect graphs across repositories? Chronicle Pro adds federation — cross-repo impact analysis, external node resolution, and a combined dashboard. Contact for access.

## Development

```bash
air    # hot-reload: rebuilds Go + restarts dashboard on file changes
```

Dashboard serves static files from disk in dev mode (`--dev`), so you can edit HTML/JS and refresh without rebuilding.

## Links

- **npm**: [@alexdx/chronicle-mcp](https://www.npmjs.com/package/@alexdx/chronicle-mcp)

## License

MIT
