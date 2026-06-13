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

### Live check

```bash
claude mcp add chronicle -- chronicle mcp serve --live-check
```

With `--live-check`, Chronicle verifies evidence against source files at query time. When you inspect a node, each evidence assertion is re-checked against the current file on disk using mechanical verifiers (AST parsing, not LLM).

- Import moved to a different line? Still valid — no flag.
- Import removed or dependency deleted from `package.json`? Flagged as `_changed` in the response.
- File deleted entirely? All its evidence flagged as `missing`.

This is read-only — the graph is not modified, just annotated with what's drifted. Off by default to avoid filesystem overhead on every query.

### Event journal

Every graph mutation is recorded as a semantic event in
`.depbot/events/<domain>.jsonl` (append-only, git-friendly, `merge=union`).
The journal is the durable source of truth — the SQLite db is a queryable
materialization of it:

- `chronicle journal init` bootstraps the journal for an existing db
  (exports the current graph as genesis events).
- `chronicle journal rebuild` reconstructs `chronicle.db` from the journal
  (replay + carry-over of non-journaled local state + verified atomic swap).
- Sync on open: every `chronicle` command applies journal events the db has
  not seen yet — after a `git pull` that merges a teammate's events, the
  graph materializes automatically.
- `chronicle journal verify` replays the journal into a temp db and diffs it
  against the live graph (shadow validation).

Recommended git setup: track `.depbot/events/` and `.depbot/chronicle.domain.yaml`,
gitignore `.depbot/chronicle.db*` — a fresh clone rebuilds the db from events on
first command. (This repo's tom-and-jerry fixture db intentionally stays tracked
for the instant demo.)

## Commands

| Command | What it does |
|---------|-------------|
| `chronicle scan` | Build the project graph |
| `chronicle search X` | Find a node by name (deterministic lexical match) |
| `chronicle refresh` | Re-verify evidence on git-changed files since the last scan — zero-token, no LLM |
| `chronicle status` | Check graph health |
| `chronicle impact X` | What may break if X changes |
| `chronicle deps X` | What X depends on |
| `chronicle subgraph X` | The neighborhood around X in one call (trust-truncated) |
| `chronicle path A B` | How A connects to B |
| `chronicle diagram` | Live architecture diagram in browser |
| `chronicle hook install` | Nudge your AI agent to query the graph before grepping |

New MCP tools for agents: `chronicle_node_search` (resolve a name to a node key), `chronicle_subgraph` (one-call neighborhood), and `chronicle_insights` (hubs, low-trust edges to verify, structural gaps).

## Dashboard

The MCP server runs a web dashboard on localhost. It opens automatically on first scan, or get the URL with `chronicle status`.

![Dashboard](assets/dashboard.png)

- **Overview** — graph stats, low-confidence edges, request log, growth chart
- **Graph** — interactive graph explorer with tree, explore, and workspace modes
- **Language** — domain glossary and naming violation checker
- **Diagrams** — live architecture diagrams built from graph data
- **Settings** — scan manifest, instruction packs, edge type config

## Data and privacy

All data stays on your machine. Nothing is sent externally.

- Graph stored in `.depbot/chronicle.db` (SQLite)
- Dashboard runs on localhost only
- To remove: delete `.depbot/`

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
