<p align="center">
  <img src="assets/logo.png" alt="Chronicle MCP" width="180">
</p>

# Chronicle MCP

A knowledge graph for your codebase. Chronicle maps the invisible structure — how models connect to services, services to endpoints, endpoints to the outside world — and gives Claude Code the ability to reason about it, query it, and explain it visually.

```
You: "What breaks if I change the User model?"

Chronicle:
  → UserService (depth 1) — USES_MODEL
  → AuthController (depth 2) — INJECTS
  → GET /auth/profile (depth 3) — EXPOSES_ENDPOINT

  3 services affected, 1 Kafka consumer downstream.
```

## Why

AI coding assistants are stateless. Every conversation starts from zero — Claude re-reads files, re-discovers architecture, re-learns what it already understood yesterday. Context windows are finite and expensive. Your codebase isn't.

**Chronicle is long-lived memory for Claude Code.** It persists a knowledge graph of your codebase in SQLite — data models, services, endpoints, dependencies, cross-service calls — and survives between sessions. Claude doesn't re-read your entire codebase every time. It queries what Chronicle already knows.

**Incremental updates via git diff.** After the first full scan, Chronicle tracks what changed. Say `chronicle scan` again and it runs an incremental update: `git diff` to find changed files, invalidate stale evidence, re-scan only what moved. The graph stays fresh with minimal work — every commit makes it smarter, not slower.

**Branch-aware.** Chronicle tracks knowledge per git branch. Scan on `feature/payments` and the knowledge stays isolated from `main`. Switch branches and queries automatically show the right context — no manual configuration. When the branch merges, a simple `chronicle update` on main picks up the changes.

**Evidence-backed trust.** Every fact in the graph has provenance — file path, line number, confidence score, derivation kind (hard from AST, linked by convention, inferred). When code changes, evidence gets re-evaluated. Stale facts lose trust. Fresh scans add new evidence. The graph doesn't just remember — it knows how much to trust what it remembers.

## How It Works

**Claude reads. Chronicle remembers.**

When you say `chronicle scan`, Claude reads your code file by file — Prisma schemas, NestJS modules, controllers, services — and extracts structured facts: "UserService injects PrismaService", "OrderController exposes POST /orders", "api-service calls payments-service via HTTP". Chronicle validates each fact, normalizes keys, and stores it in SQLite.

**Incremental scan flow:**
```
git diff → changed files → invalidate stale evidence → re-scan only affected files
  → new evidence added → trust scores recalculated → graph updated
```

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

This means you can ask questions that cross layers: "how does the User model connect to the payments-service?" Chronicle traces the path through code and contract layers to give you a real answer.

**Self-learning.** After each import, Chronicle auto-discovers gaps — missing endpoint extractions, nodes without evidence, orphan providers. Claude discovers too: uncertain relationships, unknown patterns, naming inconsistencies. All discoveries are stored and fed into the next scan. The graph gets more complete and more confident over time.

**Domain language.** Chronicle tracks your project's vocabulary — define terms, aliases, anti-patterns. If someone names a service "PurchaseService" in a project where the correct term is "Order", Chronicle flags it. The glossary lives in the dashboard and feeds into scan analysis.

## Live Diagrams

Chronicle doesn't just answer questions — it can show you.

Claude creates a live diagram session, pushes nodes and edges to your browser in real-time, and annotates what it's explaining. You open a URL, and as Claude talks through the architecture, the diagram updates in front of you — nodes light up, annotations appear, the view evolves step by step.

```
You: "Show me how the order flow works"

Claude:
  1. Creates a diagram session → opens in your browser
  2. Queries the graph for order-related nodes
  3. Pushes them to the canvas — you see OrderController, OrderService, Kafka topics
  4. Highlights the flow path, adds annotations at each step
  5. Walks you through it: "Step 1: POST /orders hits OrderController..."
```

Diagrams support step-through presentations — Claude builds a guided walkthrough with numbered steps, each highlighting different parts of the system. You navigate with Previous/Next. It's a live architecture tour, not a static PNG.

## Dashboard

The admin dashboard starts automatically with the MCP server. It's an embedded SPA — zero infrastructure, single binary.

- **Overview** — graph stats, real-time MCP request log via WebSocket, discoveries feed
- **Graph** — multiple exploration modes:
  - **Tree** — hierarchical drill-down by layer
  - **Explore** — breadcrumb navigation, layer by layer
  - **Workspace** — drag entities from a search palette onto a canvas; drop two nodes and Chronicle auto-finds the shortest path between them; expand neighbors incrementally
- **Language** — domain glossary editor + violation checker
- **Diagrams** — live sessions pushed by Claude, with annotations and step-through navigation

Filter by node type, by repo. Hide a node type and Chronicle preserves logical connections through it — transitive edges show "via POST /api" so you don't lose the story.

## Quick Start

```bash
npm install -g @alexdx/chronicle-mcp
claude mcp add chronicle -- chronicle mcp serve --open
```

That's it. Open Claude Code in any project, say `chronicle scan`. The dashboard opens in your browser, Chronicle discovers your project structure and starts building the graph.

### Try It

The repo includes a 4-service demo project (Tom & Jerry) you can scan immediately:

```bash
cd fixtures/tom-and-jerry
claude   # opens Claude Code in the fixture directory
# say: "chronicle scan"
```

Four NestJS microservices — tom-api, jerry-api, arena-api, spectators-api — with Prisma models, HTTP cross-service calls, Kafka topics, guards, interceptors, gateways. A small but real graph that shows every layer Chronicle can capture.

Or if you already have the dashboard running — paste the path to `fixtures/tom-and-jerry` into the project switcher (top of the dashboard) and it loads the pre-built graph instantly. No restart needed.

## Commands

| Say this | What happens |
|---|---|
| `chronicle scan` | Full project scan — models, code, endpoints, services |
| `chronicle update` | Incremental update — rescan only files changed since last scan via git diff |
| `chronicle verify` | Verify low-confidence edges — find code evidence to confirm or reject |
| `chronicle impact X` | What breaks if I change X? |
| `chronicle deps X` | What depends on X? |
| `chronicle path A B` | How does A connect to B? |
| `chronicle data` | Analyze data models |
| `chronicle flows` | Business use case analysis — end-to-end processes |
| `chronicle services` | Service architecture map |
| `chronicle language` | Domain glossary + violations |
| `chronicle diagram` | Live architecture diagram in the browser |
| `chronicle status` | Dashboard URL + graph stats |

Full CLI reference with all subcommands, flags, and data model: **[docs/cli.md](docs/cli.md)**

## How Claude Uses Chronicle (MCP Tool Flow)

When you ask Claude to analyze or change something, it calls Chronicle MCP tools in a specific sequence. Here's what happens under the hood:

### "What breaks if I change tom-api?"

```
Claude                              Chronicle MCP
  │                                      │
  ├─ chronicle_impact(node_key,depth=4) ─→  BFS reverse traversal
  │                                      │  Returns: impacted nodes + scores + paths
  │                                      │
  │  "TomClient is at risk (score 95)"   │
  │  "ArenaService transitively (45)"    │
  │                                      │
  ├─ chronicle_node_get("tomclient") ───→  Returns node + evidence[]
  │                                      │  file_path, line_start, confidence
  │                                      │
  ├─ chronicle_query_deps(node_key) ────→  What tom-api depends on
  ├─ chronicle_query_reverse_deps() ────→  Who depends on tom-api
  │                                      │
  └─ "3 services affected, here's why…" │
```

### Key design decisions

- **Impact/deps responses don't include evidence.** Blast radius can touch 50 nodes — fetching evidence for all would be too heavy. Claude requests evidence per node when it needs to explain *why*.
- **Evidence is the source of trust.** Every node and edge has provenance: file path, line number, extractor, confidence. When code changes, evidence gets invalidated. Trust scores recalculate automatically.
- **Claude follows instructions in `CommandInstructions`.** Each command (`scan`, `impact`, `deps`, etc.) has a step-by-step prompt telling Claude which tools to call and in what order. You can customize these in the dashboard Settings tab.

### Tool categories

| Category | Tools | Purpose |
|----------|-------|---------|
| **Read** | `chronicle_impact`, `query_deps`, `query_reverse_deps`, `query_path`, `query_stats`, `node_get`, `edge_list` | Query the graph |
| **Write** | `revision_create`, `import_all`, `node_upsert`, `edge_upsert`, `evidence_add` | Build/update the graph |
| **Lifecycle** | `invalidate_changed`, `finalize_incremental_scan`, `snapshot_create`, `stale_mark` | Manage scan lifecycle |
| **Meta** | `extraction_guide`, `scan_status`, `command`, `define_term`, `check_language` | Guidance and domain language |
| **Visual** | `diagram_create`, `diagram_update`, `diagram_annotate` | Live diagrams |

## Development

```bash
air    # hot-reload: rebuilds Go + restarts dashboard on file changes
```

The dashboard serves static files from disk in dev mode (`--dev` flag), so you can edit HTML/JS and refresh without rebuilding.

## Links

- **npm**: [@alexdx/chronicle-mcp](https://www.npmjs.com/package/@alexdx/chronicle-mcp)

## License

MIT
