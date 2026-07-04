<p align="center">
  <img src="https://raw.githubusercontent.com/alexdx2/chronicle/main/assets/logo.png" alt="Chronicle MCP" width="180">
</p>

# Chronicle MCP — Knowledge Graph

Self-learning code analysis tool for coding agents (Claude Code, Codex CLI, ...). Builds a knowledge graph of your codebase — data models, services, endpoints, dependencies — and answers questions like "what breaks if I change X?"

## Install

```bash
npm install -g @alexdx/chronicle-mcp
```

## Setup with Claude Code

```bash
claude mcp add chronicle -- chronicle mcp serve
```

Or add to your Claude Code MCP config (`~/.claude.json`):

```json
{
  "mcpServers": {
    "chronicle": {
      "command": "chronicle",
      "args": ["mcp", "serve"]
    }
  }
}
```

## Setup with Codex CLI

```bash
chronicle setup codex
```

This registers the server in `~/.codex/config.toml` (plus a SessionStart hook that
re-primes the Chronicle entry point after `/clear` or compaction), writes usage
guidance to `~/.codex/AGENTS.md` (and to the project's `AGENTS.md` when run inside
a project), and installs `/chronicle-scan`, `/chronicle-impact`, ... custom prompts.
Equivalent manual config:

```toml
[mcp_servers.chronicle]
command = "chronicle"
args = ["mcp", "serve"]
startup_timeout_sec = 30   # journal sync on open can exceed Codex's 10s default
tool_timeout_sec = 180     # resolve/commit on large graphs can exceed the 60s default
```

Chronicle detects the connected client: Claude Code gets the parallel (subagent)
scan workflow; clients without subagents (Codex, Cursor) automatically get a
single-agent workflow. Query tools work identically everywhere.

## Usage

Open Claude Code in any project and say:

```
chronicle scan          — Full project scan
chronicle data          — Analyze data models
chronicle language      — Domain language glossary
chronicle impact X      — What breaks if I change X?
chronicle deps X        — What depends on X?
chronicle services      — Service architecture
chronicle status        — Dashboard URL + graph stats
```

## What it does

- Scans your codebase (NestJS, Prisma, GraphQL, Kafka, etc.)
- Builds a multi-layered knowledge graph (data → code → contracts → services)
- Tracks dependencies, impact paths, domain language
- Self-learns: discovers patterns, reports gaps, improves with each scan
- Admin dashboard with visual graph at `http://localhost:<auto-port>`

The graph is git-native: every mutation is recorded in an append-only event journal (`.depbot/events/<domain>.jsonl`) that you can track in git, and the SQLite db is a derived cache rebuilt from it anytime (`chronicle journal rebuild`). Two teammates' scans merge through a normal git merge — after a pull, the graph syncs automatically on the next command. See the [repository README](https://github.com/alexdx2/chronicle) for the full journal workflow.

## Links

- [Repository](https://github.com/alexdx2/chronicle)
