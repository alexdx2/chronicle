<p align="center">
  <img src="assets/logo.png" alt="Chronicle MCP" width="180">
</p>

# Chronicle MCP

Persistent architecture memory for AI coding agents.

Every new session starts from scratch. Chronicle fixes that — it builds a local knowledge graph of your codebase that persists across conversations.

```
You: "What breaks if I change the Order model?"

Chronicle:
  → OrderService (direct dependency)
  → PaymentService (2 hops)
  → POST /orders (exposed endpoint)
  3 services affected, 1 Kafka topic downstream.
```

The answer comes from a graph where every fact traces back to a file and line number.

## Quick start

```bash
npm install -g @alexdx/chronicle-mcp
claude mcp add chronicle -- chronicle mcp serve --open
```

Then:

```
chronicle scan
```

Chronicle will ask you to confirm the scope before scanning anything.

## How the brain works

Chronicle stores architecture as a graph of relationships:

```
OrderController → exposes → POST /orders
OrderController → injects → OrderService
OrderService → uses_model → Order
arena-api → calls_service → tom-api
```

Each fact links back to source code: file, line number, confidence score.

This is not a vector index or chat memory. It is a structured graph with traceable evidence.

## Keeping it fresh

| When | Run | What happens |
|------|-----|--------------|
| First time | `chronicle scan` | Builds the full graph (you confirm scope first) |
| After code changes | `chronicle update` | Updates the parts that changed |
| Unsure if current | `chronicle status` | Reports what's fresh, stale, or missing |

## Commands

| Command | What it does |
|---------|-------------|
| `chronicle scan` | Build the project graph |
| `chronicle update` | Refresh after code changes |
| `chronicle status` | Check graph health |
| `chronicle impact X` | What may break if X changes |
| `chronicle deps X` | What X depends on |
| `chronicle path A B` | How A connects to B |
| `chronicle diagram` | Live architecture diagram in browser |

## Dashboard

Chronicle includes a live dashboard that starts automatically.

![Dashboard](assets/dashboard.png)

- Graph explorer — navigate services, models, endpoints
- Impact analysis — visual blast radius
- Diagrams — architecture diagrams built from real graph data
- Growth chart — knowledge accumulation over scans

Get the URL with `chronicle status`.

## Data and privacy

Graph data stays on your machine in `.depbot/chronicle.db` (SQLite). The dashboard runs on localhost.

Note: the dashboard loads fonts and D3.js from CDN (fonts.googleapis.com, d3js.org).

To remove all Chronicle data: delete `.depbot/`.

## How it works

Scanning uses a hybrid AST + LLM pipeline: tree-sitter handles deterministic patterns (imports, decorators, DI), LLM agents classify ambiguous patterns (cross-service calls, event emits).

See [docs/scanning.md](docs/scanning.md) for the full breakdown.

## Docs

- [How it works](docs/how-it-works.md)
- [Scan pipeline](docs/scanning.md)
- [Commands reference](docs/commands.md)

## Links

- **npm**: [@alexdx/chronicle-mcp](https://www.npmjs.com/package/@alexdx/chronicle-mcp)
- **GitHub**: [alexdx2/chronicle](https://github.com/alexdx2/chronicle)

## License

MIT
