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

You can grep a codebase. You can ask an AI to read files. But neither gives you the answer to "what happens downstream if I change this?" — because that answer lives in the connections between things, not in any single file.

Chronicle extracts those connections into a persistent graph: data models, services, controllers, endpoints, Kafka topics, cross-service calls. Every relationship has a derivation (hard from AST, or linked by convention) and evidence (file + line number). The graph survives between sessions. Claude doesn't re-read your entire codebase every time — it queries what Chronicle already knows.

## How It Works

**Claude reads. Chronicle remembers.**

When you say `chronicle scan`, Claude reads your code file by file — Prisma schemas, NestJS modules, controllers, services — and extracts structured facts: "UserService injects PrismaService", "OrderController exposes POST /orders", "api-service calls payments-service via HTTP". Chronicle validates each fact, normalizes keys, and stores it in SQLite.

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
| `chronicle impact X` | What breaks if I change X? |
| `chronicle deps X` | What depends on X? |
| `chronicle path A B` | How does A connect to B? |
| `chronicle data` | Analyze data models |
| `chronicle services` | Service architecture map |
| `chronicle language` | Domain glossary + violations |
| `chronicle status` | Dashboard URL + graph stats |

## Development

```bash
air    # hot-reload: rebuilds Go + restarts dashboard on file changes
```

The dashboard serves static files from disk in dev mode (`--dev` flag), so you can edit HTML/JS and refresh without rebuilding.

## Links

- **npm**: [@alexdx/chronicle-mcp](https://www.npmjs.com/package/@alexdx/chronicle-mcp)

## License

MIT
