<p align="center">
  <img src="assets/logo.png" alt="Chronicle MCP" width="180">
</p>

# Chronicle MCP

Persistent architecture memory for Claude Code.

Every new Claude Code session starts from scratch. Chronicle fixes that — it builds a local knowledge graph of your codebase that persists across conversations.

```
You: "What breaks if I change the Order model?"

Claude (with Chronicle):
  → OrderService (direct dependency)
  → PaymentService (2 hops)
  → POST /orders (exposed endpoint)
  3 services affected, 1 Kafka topic downstream.
  Evidence: src/orders/order.service.ts:14, src/payments/payment.service.ts:8
```

Without Chronicle, Claude would need to re-read dozens of files to answer this. With Chronicle, the answer comes from a graph grounded in source-code evidence.

## Quick start

```bash
npm install -g @alexdx/chronicle-mcp
claude mcp add chronicle -- chronicle mcp serve --open
```

Then in Claude Code:

```
chronicle scan
```

Chronicle will ask you to confirm the scope before scanning anything.

## How the brain works

Chronicle stores architecture as a graph of relationships with evidence:

```
OrderController → exposes → POST /orders     (src/orders/order.controller.ts:12, confidence: high)
OrderController → injects → OrderService     (src/orders/order.controller.ts:8, confidence: high)
OrderService → uses_model → Order            (src/orders/order.service.ts:14, confidence: high)
arena-api → calls_service → tom-api          (src/arena/tom.client.ts:6, derivation: linked)
```

This is not a vector index or chat memory. It is a structured graph where every fact traces back to a file and line number. Claude queries it to answer architecture questions without re-reading your entire codebase.

## Keeping it fresh

| When | Run | What happens |
|------|-----|--------------|
| First time | `chronicle scan` | Builds the full graph (interactive — you confirm scope) |
| After code changes | `chronicle update` | Rescans only changed files, refreshes affected edges |
| Unsure if graph is current | `chronicle status` | Reports freshness, staleness, missing evidence |

Run `chronicle update` after changing service boundaries, models, endpoints, event topics, or pulling a large branch.

## What you can ask

Natural language — Claude uses the graph automatically:

- What breaks if I change OrderService?
- How does POST /orders reach the payment service?
- What depends on User model?
- Show me the checkout flow as a diagram
- Is the graph up to date?

Or use commands directly:

| Command | What it does |
|---------|-------------|
| `chronicle scan` | Build the project graph |
| `chronicle update` | Refresh after code changes |
| `chronicle status` | Check graph health |
| `chronicle impact X` | What may break if X changes |
| `chronicle deps X` | What X depends on |
| `chronicle path A B` | How A connects to B |
| `chronicle diagram` | Live architecture diagram in browser |

## The first scan

When you run `chronicle scan`, Claude will:

1. Inspect your repository structure
2. Suggest scan scope (backend services, data models, APIs)
3. Ask you to confirm or adjust
4. Extract architecture facts from code
5. Store them in `.depbot/chronicle.db`
6. Open the dashboard with a visual graph

You control what gets scanned. Chronicle shows what's included, what's excluded, and what questions the graph will answer.

## Dashboard

Chronicle includes a live dashboard that starts automatically:

- Graph explorer — navigate services, models, endpoints
- Impact analysis — visual blast radius
- Diagrams — entity-first architecture diagrams (built from real graph data, not invented)
- Growth chart — track how knowledge accumulates over scans
- Request log — see what tools Claude called

Get the URL with `chronicle status`.

## How it works with Claude Code

Chronicle runs as an MCP server. Claude Code calls its tools automatically during conversations — you don't need to invoke low-level graph operations manually.

Under the hood, scanning uses a hybrid AST + LLM pipeline: tree-sitter handles deterministic patterns (imports, decorators, DI), and LLM agents classify ambiguous patterns (cross-service calls, event emits). See [docs/scanning.md](docs/scanning.md) for the full breakdown.

## What Chronicle is good at

Architecture that is visible in code:

- imports and dependency injection
- controllers, services, modules
- models and schemas (Prisma, TypeORM)
- event topics (Kafka, RabbitMQ)
- API definitions (REST endpoints, GraphQL)
- cross-service HTTP calls
- environment-based service discovery

## Limitations

Chronicle is not a compiler. It may need help when:

- behavior is fully dynamic (reflection, eval)
- relationships are hidden in generated code
- the repository is very large (>5000 files — narrow the scope)
- naming is inconsistent across services

When in doubt: `chronicle status`, or ask Claude to show the evidence behind an answer.

## Docs

- [How it works](docs/how-it-works.md)
- [Scan pipeline](docs/scanning.md)
- [Commands reference](docs/commands.md)

## Links

- **npm**: [@alexdx/chronicle-mcp](https://www.npmjs.com/package/@alexdx/chronicle-mcp)
- **GitHub**: [alexdx2/chronicle](https://github.com/alexdx2/chronicle)

## License

MIT
