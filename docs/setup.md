# Domain Oracle — Setup Guide

## Install

Build the Oracle CLI from source:

```bash
cd /path/to/depbot
go build -o oracle ./cmd/oracle
```

Move the binary to your PATH or use the full path in the MCP config below.

## Initialize a Project

In any project directory you want to analyze:

```bash
oracle init
```

This creates:
- `oracle.domain.yaml` — domain manifest (edit this)
- `oracle.types.yaml` — type registry (defaults are fine)
- `oracle.db` — SQLite database

Edit `oracle.domain.yaml` to describe your domain:

```yaml
domain: my-domain
description: My project domain
repositories:
  - name: my-api
    path: .
    tags: [nestjs, rest]
owner: my-team
```

## Configure Claude Code

Add the Oracle MCP server to your Claude Code settings. In your project's `.claude/settings.json` or global settings:

```json
{
  "mcpServers": {
    "oracle": {
      "command": "/absolute/path/to/oracle",
      "args": ["mcp", "serve", "--db", "/absolute/path/to/oracle.db"]
    }
  }
}
```

**Important:** Use absolute paths for both the binary and the database file.

## First Scan

Open Claude Code in your project and say:

> "Scan this project and build a knowledge graph"

Claude will:
1. Call `oracle_extraction_guide` to learn the extraction methodology
2. Call `oracle_scan_status` to check if a graph already exists
3. Read your `oracle.domain.yaml` to know the domain and repos
4. Read your source files, identify entities and relationships
5. Import everything via `oracle_import_all`
6. Create a snapshot and mark stale entities

## Querying

Once scanned, ask questions like:

- "What depends on OrdersService?"
- "Show me the path from OrdersController to payments-api"
- "What would be impacted if I change PaymentsService?"
- "What are the stats for the orders domain?"
- "List all endpoints in the graph"

Claude uses the Oracle MCP tools to query the graph and provide evidence-backed answers.

## CLI Usage

You can also use the CLI directly:

```bash
# List nodes
oracle node list --layer code --domain my-domain

# Query dependencies
oracle query deps code:provider:orders:ordersservice --depth 2

# Find paths
oracle query path code:controller:orders:orderscontroller service:service:orders:payments-api

# Analyze impact
oracle impact code:provider:orders:paymentsservice --depth 3

# Check graph stats
oracle query stats --domain orders

# Validate graph integrity
oracle validate graph
```

## Re-scanning

To update the graph after code changes, just tell Claude:

> "Re-scan the orders-api repo"

The Oracle handles idempotent upserts — existing entities are updated, new ones are added, removed ones are marked stale.
