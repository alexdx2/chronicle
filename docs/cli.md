# Chronicle CLI Reference

## Global Flags

All commands accept these flags:

| Flag | Default | Description |
|------|---------|-------------|
| `--project` | `.` (current dir) | Path to the project root; the chronicle dir resolves against it |
| `--chronicle-dir` | `.depbot` | Artifacts directory (db, manifest, registry, journal, scan work). Relative → under project root; absolute → used as-is |

Most of the time you only need `--project`:

```bash
chronicle query stats --project /path/to/my-app
```

---

## Core Commands

### `chronicle init`

Initialize `.depbot/` directory with manifest, type registry, and database. Run this once in a new project.

```bash
chronicle init
```

Creates:
- `.depbot/chronicle.db` — SQLite database
- `.depbot/chronicle.domain.yaml` — domain manifest (edit to describe your project)
- `.depbot/chronicle.types.yaml` — type registry (defaults are fine)
- `CLAUDE.md` — enables Chronicle slash commands in Claude Code

### `chronicle mcp serve`

Start the MCP server (stdio transport). This is what Claude Code connects to.

```bash
chronicle mcp serve [flags]
```

| Flag | Default | Description |
|------|---------|-------------|
| `--admin-port` | `4200` | Admin dashboard HTTP port |
| `--no-admin` | `false` | Disable admin dashboard |
| `--open` | `false` | Auto-open dashboard in browser |

**Typical setup** — add to Claude Code MCP config:

```json
{
  "mcpServers": {
    "chronicle": {
      "command": "chronicle",
      "args": ["mcp", "serve", "--open"]
    }
  }
}
```

### `chronicle admin`

Start the admin dashboard as a standalone server (without MCP).

```bash
chronicle admin [flags]
```

| Flag | Default | Description |
|------|---------|-------------|
| `--port` | `4200` | HTTP port |
| `--dev` | `false` | Serve static files from disk (live reload for development) |

### `chronicle version`

Print the current version.

```bash
chronicle version
# chronicle v0.1.0
```

---

## Graph Queries

### `chronicle query deps`

Forward dependency traversal — what does this node depend on?

```bash
chronicle query deps <node_key> [flags]
```

| Flag | Default | Description |
|------|---------|-------------|
| `--depth` | `3` | Max BFS depth |
| `--derivation` | | Comma-separated derivation filter |

```bash
chronicle query deps code:provider:orders:ordersservice --depth 2
```

### `chronicle query reverse-deps`

Reverse dependency traversal — who depends on this node?

```bash
chronicle query reverse-deps <node_key> [flags]
```

| Flag | Default | Description |
|------|---------|-------------|
| `--depth` | `3` | Max BFS depth |
| `--derivation` | | Comma-separated derivation filter |

### `chronicle query path`

Find paths between two nodes.

```bash
chronicle query path <from_key> <to_key> [flags]
```

| Flag | Default | Description |
|------|---------|-------------|
| `--max-depth` | `6` | Max traversal depth |
| `--top-k` | `3` | Max paths to return |
| `--mode` | `directed` | `directed` or `connected` |
| `--derivation` | | Comma-separated derivation filter |

```bash
chronicle query path code:controller:orders:orderscontroller service:service:orders:payments-api
```

### `chronicle query stats`

Aggregate graph statistics for a domain.

```bash
chronicle query stats [flags]
```

| Flag | Default | Description |
|------|---------|-------------|
| `--domain` | | Domain key (empty = all) |

### `chronicle query node`

Get a single node with its evidence.

```bash
chronicle query node <node_key>
```

### `chronicle query edges`

Get outgoing and incoming edges for a node.

```bash
chronicle query edges <node_key>
```

### `chronicle query evidence`

Query evidence for a node or edge.

```bash
chronicle query evidence --node <node_key>
chronicle query evidence --edge <edge_key>
```

### `chronicle impact`

Blast radius analysis — what breaks if I change this node?

```bash
chronicle impact <node_key> [flags]
```

| Flag | Default | Description |
|------|---------|-------------|
| `--depth` | `4` | Max traversal depth |
| `--top-k` | `50` | Max results |
| `--min-score` | `0.1` | Minimum impact score |
| `--derivation` | | Comma-separated derivation filter |
| `--include-structural` | `false` | Include structural (CONTAINS) edges |

```bash
chronicle impact code:provider:orders:paymentsservice --depth 3
```

---

## Graph Mutations

### `chronicle node upsert`

Create or update a node.

```bash
chronicle node upsert --revision-id <id> --name <name> [flags]
```

Key format: `layer:type:domain:qualified_name` (all lowercase).

### `chronicle node get`

Get a node by key.

```bash
chronicle node get <node_key>
```

### `chronicle node list`

List nodes with optional filters.

```bash
chronicle node list [flags]
```

| Flag | Description |
|------|-------------|
| `--layer` | Filter by layer (code, service, contract, data, flow) |
| `--node-type` | Filter by node type |
| `--domain` | Filter by domain |
| `--status` | Filter by status (active, stale, deleted) |

### `chronicle node delete`

Soft-delete a node by key.

```bash
chronicle node delete <node_key>
```

### `chronicle edge upsert`

Create or update an edge.

```bash
chronicle edge upsert --revision-id <id> --from <key> --to <key> --type <edge_type> --derivation <kind>
```

Derivation kinds: `hard` (AST-level), `linked` (convention), `inferred` (guessed), `unknown`.

### `chronicle edge get / list / delete`

Same pattern as node commands.

### `chronicle evidence add`

Add provenance evidence for a node or edge.

```bash
chronicle evidence add --target-kind node --node-key <key> \
  --source-kind file --file-path src/user.service.ts --line-start 42 \
  --extractor-id claude-code --extractor-version 1.0
```

| Flag | Description |
|------|-------------|
| `--target-kind` | `node` or `edge` |
| `--source-kind` | `file` or `user_feedback` |
| `--polarity` | `positive` (default) or `negative` (contradicts the fact) |
| `--confidence` | 0-1 confidence score |

### `chronicle evidence list`

List evidence for a node or edge.

```bash
chronicle evidence list --node <node_key>
chronicle evidence list --edge <edge_key>
```

---

## Scan Lifecycle

### `chronicle revision create`

Create a new revision to track a scan pass.

```bash
chronicle revision create --domain <key> --after-sha <sha> [--before-sha <sha>] [--mode full|incremental]
```

### `chronicle revision get`

Get revision details.

```bash
chronicle revision get <revision_id>
```

### `chronicle import all`

Bulk import nodes, edges, and evidence from a JSON file.

```bash
chronicle import all --revision-id <id> --file payload.json
```

### `chronicle snapshot create`

Record a point-in-time snapshot after a scan.

```bash
chronicle snapshot create --domain <key> --revision-id <id> --node-count <n> --edge-count <n>
```

### `chronicle snapshot list`

List snapshots for a domain.

```bash
chronicle snapshot list --domain <key>
```

### `chronicle validate graph`

Run integrity checks: malformed keys, confidence out of range, orphan edges.

```bash
chronicle validate graph
```

---

## Data Model

### Node Key Format

```
layer:type:domain:qualified_name
```

All lowercase. Examples:
- `data:model:orders:user`
- `code:provider:orders:userservice`
- `contract:endpoint:orders:get_/users/:id`
- `service:service:orders:payments-api`

### Layers

| Layer | What it contains |
|-------|-----------------|
| `data` | Models, enums, relations (Prisma, TypeORM) |
| `code` | Modules, controllers, providers, guards, interceptors |
| `contract` | Endpoints, Kafka topics, GraphQL operations |
| `service` | Deployable services |
| `flow` | Business use cases, processes |
| `ownership` | Teams, owners |
| `infra` | Infrastructure components |

### Edge Types

| Edge Type | From → To | Description |
|-----------|-----------|-------------|
| `USES_MODEL` | code → data | Service uses a data model |
| `REFERENCES_MODEL` | data → data | Model references another model |
| `INJECTS` | code → code | Dependency injection |
| `CONTAINS` | code → code | Module contains provider |
| `EXPOSES_ENDPOINT` | code → contract | Controller exposes endpoint |
| `CALLS_SERVICE` | service → service | Cross-service call |
| `CALLS_ENDPOINT` | code → contract | Code calls an endpoint |
| `PUBLISHES` | code → contract | Publishes to Kafka topic |
| `SUBSCRIBES` | code → contract | Subscribes to Kafka topic |
| `TRIGGERS_FLOW` | contract → flow | Endpoint triggers a use case |
| `REQUIRES` | flow → code/data | Flow requires a service or model |

### Derivation Kinds

| Kind | Confidence | How it was found |
|------|-----------|-----------------|
| `hard` | High | AST-level: decorators, imports, type annotations |
| `linked` | Medium | Convention: naming patterns, file structure |
| `inferred` | Low | Guessed from context |
| `unknown` | Lowest | Unclassified |
