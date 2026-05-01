# Command Reference

All commands are triggered by saying them in a Claude Code session with Chronicle MCP enabled.

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

## Business flows

| Command | Description |
|---------|-------------|
| `chronicle flows` | Discover and map end-to-end use cases |

## How commands work

Each command maps to a set of MCP tool calls. When you say `chronicle impact OrderService`, Claude:

1. Calls `chronicle_command(command='impact')` to get step-by-step instructions
2. Calls `chronicle_impact(node_key="OrderService")` — name resolves automatically
3. Returns impacted nodes, affected endpoints, and trust scores

## MCP tools (for advanced use)

| Category | Tools |
|----------|-------|
| **Read** | `impact`, `query_deps`, `query_reverse_deps`, `query_path`, `query_stats`, `node_get`, `edge_list` |
| **Write** | `revision_create`, `import_all`, `node_upsert`, `edge_upsert`, `evidence_add` |
| **Lifecycle** | `invalidate_changed`, `finalize_incremental_scan`, `snapshot_create`, `stale_mark` |
| **Meta** | `extraction_guide`, `scan_status`, `command`, `define_term`, `check_language` |
| **Visual** | `diagram_create`, `diagram_update`, `diagram_annotate` |
