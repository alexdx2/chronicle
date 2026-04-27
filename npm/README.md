<p align="center">
  <img src="https://raw.githubusercontent.com/alexdx2/chronicle/main/assets/logo.png" alt="Chronicle MCP" width="180">
</p>

# Chronicle MCP — Knowledge Graph

Self-learning code analysis tool for Claude Code. Builds a knowledge graph of your codebase — data models, services, endpoints, dependencies — and answers questions like "what breaks if I change X?"

## Install

```bash
npm install -g @alexdx/chronicle-mcp
```

## Setup with Claude Code

Add to your Claude Code MCP config (`~/.claude.json`):

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

## Links

- [Repository](https://github.com/alexdx2/chronicle)
