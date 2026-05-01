<p align="center">
  <img src="assets/logo.png" alt="Chronicle MCP" width="180">
</p>

# Chronicle MCP

Architecture memory for AI coding agents.

Chronicle builds a persistent knowledge graph of your codebase — models, services, endpoints, dependencies — so your AI agent queries what it already knows instead of re-reading files every session.

## Quick Start

```bash
npm install -g @alexdx/chronicle-mcp
claude mcp add chronicle -- chronicle mcp serve --open
```

Then in Claude Code, say `chronicle scan`. Chronicle reads your codebase, builds the graph, opens a dashboard. Every session after this, your agent has instant access to the full architecture.

## What you can ask

```
"What breaks if I change the Order model?"
"How does POST /orders flow to the payment service?"
"What depends on SocketService?"
"Show me a diagram of the checkout flow"
```

## Core commands

| Command | What it does |
|---------|-------------|
| `chronicle scan` | Full project scan — builds the graph from scratch |
| `chronicle update` | Incremental update — rescans only files changed since last scan |
| `chronicle impact X` | What breaks if X changes |
| `chronicle deps X` | What does X depend on / who depends on X |
| `chronicle path A B` | How does A connect to B |
| `chronicle diagram` | Live architecture diagram in the browser |
| `chronicle status` | Graph health + freshness check |

## Keeping the graph fresh

Chronicle tracks how far behind the graph is. Say `chronicle status`:

```
status: stale
commits_behind: 4
files_changed: 17
suggestion: "Run chronicle update to rescan 4 commits"
```

Run `chronicle update` when the graph falls behind. It only re-scans changed files — fast and incremental.

## Benchmark

We ran 5 architecture analysis tasks (impact analysis, flow tracing, dependency lookup, path finding, hallucination trap) on a 4-service NestJS codebase — with and without Chronicle. Each task scored against a ground-truth checklist. [Full methodology and raw data](benchmark/)

**Result:** Chronicle found cross-service Kafka paths and affected endpoints that baseline grep missed. Zero hallucinations across all runs (baseline hallucinated once). Both tied on simple single-service lookups.

## Docs

- [How it works](docs/how-it-works.md) — layers, evidence, trust scores
- [All commands](docs/commands.md) — full command reference
- [Benchmark details](benchmark/README.md) — methodology and raw data

## Links

- **npm**: [@alexdx/chronicle-mcp](https://www.npmjs.com/package/@alexdx/chronicle-mcp)

## License

MIT
