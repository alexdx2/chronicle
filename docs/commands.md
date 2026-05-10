# Command Reference

All commands work by saying them in a session with Chronicle MCP enabled.

## Scanning

| Command | Description |
|---------|-------------|
| `chronicle scan` | Full project scan — data models, code structure, endpoints, cross-service deps |
| `chronicle update` | Incremental update — rescan only files changed since last scan (via git diff) |
| `chronicle data` | Scan data models only (Prisma, TypeORM schemas) |
| `chronicle verify` | Verify low-confidence edges by reading source code |

## Querying

| Command | Description |
|---------|-------------|
| `chronicle impact X` | What breaks if X changes (blast radius) |
| `chronicle deps X` | Forward + reverse dependencies of X |
| `chronicle path A B` | Find paths between two nodes |
| `chronicle services` | Service architecture overview |
| `chronicle status` | Graph stats, freshness, dashboard URL |

## Domain language

| Command | Description |
|---------|-------------|
| `chronicle language` | Define domain glossary terms, check for violations |

## Visualization

| Command | Description |
|---------|-------------|
| `chronicle diagram` | Live architecture diagram in the browser |

Diagrams are built from real graph entities — nodes are validated against the database, edges auto-discovered. Virtual nodes (e.g. "User", "External API") can be added for actors not in the graph. Virtual elements render with dashed borders.

## Business flows

| Command | Description |
|---------|-------------|
| `chronicle flows` | Discover and map end-to-end use cases |

## How commands work

Each command maps to MCP tool calls under the hood. You say `chronicle impact OrderService` — the agent resolves the name, queries the graph, and returns impacted nodes with evidence. You don't need to call tools directly.
