# Oracle MCP — Domain Knowledge Graph

Self-learning code analysis tool for Claude Code. Builds a knowledge graph of your codebase — data models, services, endpoints, dependencies — and answers questions like "what breaks if I change X?"

## Install

```bash
npm install -g @alexdx/depbot-oracle
```

## Setup with Claude Code

Add to your Claude Code MCP config (`~/.claude.json`):

```json
{
  "mcpServers": {
    "oracle": {
      "command": "oracle",
      "args": ["mcp", "serve"]
    }
  }
}
```

## Usage

Open Claude Code in any project and say:

```
oracle scan          — Full project scan
oracle data          — Analyze data models
oracle language      — Domain language glossary
oracle impact X      — What breaks if I change X?
oracle deps X        — What depends on X?
oracle services      — Service architecture
oracle status        — Dashboard URL + graph stats
```

## What it does

- Scans your codebase (NestJS, Prisma, GraphQL, Kafka, etc.)
- Builds a multi-layered knowledge graph (data → code → contracts → services)
- Tracks dependencies, impact paths, domain language
- Self-learns: discovers patterns, reports gaps, improves with each scan
- Admin dashboard with visual graph at `http://localhost:<auto-port>`

## Links

- [Repository](https://gitlab.com/Alex_dx3/depbot)
- [Full documentation](https://gitlab.com/Alex_dx3/depbot/-/blob/main/README.md)
