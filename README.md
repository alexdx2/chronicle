<p align="center">
  <img src="assets/logo.png" alt="Chronicle MCP" width="180">
</p>

# Chronicle MCP

Persistent architecture memory for Claude Code.

Chronicle builds a local knowledge graph of your codebase so Claude can understand your project across sessions. Instead of re-reading the same files again and again, Claude can ask Chronicle:

- what depends on this service?
- what breaks if this model changes?
- how does this endpoint reach that system?
- is the project graph still fresh?

Chronicle stores the graph locally in `.depbot/chronicle.db` and keeps it up to date with `chronicle scan`, `chronicle update`, and `chronicle status`.

## Quick start

Install Chronicle:

```bash
npm install -g @alexdx/chronicle-mcp
```

Add it to Claude Code:

```bash
claude mcp add chronicle -- chronicle mcp serve --open
```

Open your project in Claude Code and run:

```
chronicle scan
```

## What happens during the first scan

When you run `chronicle scan`, Claude will:

1. Inspect your repository structure.
2. Suggest what should be scanned.
3. Ask you to confirm or adjust the scope.
4. Extract architecture facts from code.
5. Store them in `.depbot/chronicle.db`.
6. Show a summary of the graph.

Chronicle does not blindly scan everything. You see what source areas are included, what is excluded, and what questions the graph will be able to answer.

## The project brain

Chronicle is not a chat memory and not a vector search index. It is a local architecture graph.

Each fact is stored as a relationship:

```
OrderController → uses → OrderService
OrderService → uses_model → Order
OrderController → exposes → POST /orders
```

Each relationship includes evidence:

```
src/orders/order.service.ts:14
confidence: high
status: fresh
```

This lets Claude answer architecture questions with traceable evidence instead of guessing.

## Keeping the brain up to date

Your code changes. Chronicle tracks that.

**Full scan** — use when setting up Chronicle for the first time:

```
chronicle scan
```

**Incremental update** — use after changing code:

```
chronicle update
```

Chronicle checks what changed and refreshes the affected parts of the graph.

**Status check** — use when unsure whether the graph is still reliable:

```
chronicle status
```

Chronicle reports whether the graph is fresh, stale, incomplete, or missing evidence.

**Best practice** — run `chronicle update` when:

- you change service boundaries
- you add or remove endpoints
- you change models or schemas
- you change event topics
- you pull a large branch
- Claude gives an answer that feels outdated

## What you can ask Claude

```
chronicle scan
What breaks if I change OrderService?
How does POST /orders reach the payment service?
What depends on User model?
Show me the checkout flow
Is the graph up to date?
Update Chronicle after my latest changes
```

## Common commands

| Command | What it does |
|---------|-------------|
| `chronicle scan` | Build the first project graph |
| `chronicle update` | Refresh the graph after code changes |
| `chronicle status` | Check freshness and graph health |
| `chronicle impact X` | Show what may break if X changes |
| `chronicle deps X` | Show what X depends on |
| `chronicle path A B` | Explain how A connects to B |
| `chronicle diagram` | Open an architecture diagram |

## How Chronicle works with Claude Code

Chronicle runs as an MCP server. That means Claude Code can call Chronicle tools during a conversation:

- scan the project
- update the graph
- ask what depends on something
- find paths between services
- show impact of a change
- open the dashboard

You do not need to call low-level graph tools manually. Claude uses them when needed.

## What Chronicle is good at

Chronicle works best when architecture is visible in code:

- imports and dependency injection
- controllers and services
- models and schemas
- config files and environment variables
- event topics (Kafka, RabbitMQ)
- API definitions (REST, GraphQL)

## Limitations

Chronicle is not a compiler and does not guarantee perfect program analysis. It may need help when:

- behavior is fully dynamic
- important relationships are hidden in generated code
- naming is inconsistent
- the repository is very large
- the scan scope was too narrow

When in doubt, run:

```
chronicle status
```

or ask Claude to show the evidence behind an answer.

## Why Chronicle

Without Chronicle, Claude often has to rediscover your architecture from scratch. With Chronicle, Claude can use a persistent graph grounded in source-code evidence.

That means:

- less repeated explanation
- faster architecture questions
- better impact analysis
- clearer dependency paths
- answers backed by evidence

## Docs

- [How it works](docs/how-it-works.md)
- [Scan pipeline](docs/scanning.md)
- [Commands](docs/commands.md)

## Links

- **npm**: [@alexdx/chronicle-mcp](https://www.npmjs.com/package/@alexdx/chronicle-mcp)

## License

MIT
