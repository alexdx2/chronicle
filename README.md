<p align="center">
  <img src="assets/logo.png" alt="Chronicle MCP" width="180">
</p>

# Chronicle MCP

Architecture memory for AI coding agents.

**Stop re-explaining your codebase every session.**

```
"What breaks if I change the Order model?"

→ OrderService (depth 1)
→ PaymentService (depth 2)
→ POST /orders (depth 3)

3 services affected, 1 Kafka topic downstream.
```

## Quick Start

```bash
npm install -g @alexdx/chronicle-mcp
claude mcp add chronicle -- chronicle mcp serve --open
```

Then in Claude Code:

```
chronicle scan
```

## What you can ask

- What breaks if I change the Order model?
- How does POST /orders reach the payment service?
- What depends on SocketService?
- Show checkout flow diagram

## Commands

| Command | What it does |
|---------|-------------|
| `chronicle scan` | Full project scan — builds the graph |
| `chronicle update` | Incremental — rescans only changed files |
| `chronicle impact X` | What breaks if X changes |
| `chronicle deps X` | What does X depend on |
| `chronicle path A B` | How does A connect to B |
| `chronicle diagram` | Live architecture diagram in browser |
| `chronicle status` | Graph health + freshness check |

## Keeping the graph fresh

```
chronicle status

→ stale, 4 commits behind, 17 files changed
→ suggestion: "Run chronicle update"
```

Run `chronicle update` when the graph falls behind. It only re-scans changed files.

## Benchmark

Chronicle vs raw code reading (grep + file reads):

- Finds cross-service dependencies grep misses
- Zero hallucinations (baseline hallucinates)
- Same performance on simple lookups

Chronicle wins on real architecture reasoning. [Full methodology and data →](benchmark/)

## Docs

- [How it works](docs/how-it-works.md) — layers, evidence, trust scores
- [Commands](docs/commands.md) — full reference
- [Benchmark](benchmark/README.md) — methodology and raw data

## Links

- **npm**: [@alexdx/chronicle-mcp](https://www.npmjs.com/package/@alexdx/chronicle-mcp)

## License

MIT
