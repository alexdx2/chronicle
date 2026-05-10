# How Chronicle Works

## The graph

Chronicle stores your architecture as a layered knowledge graph:

```
DATA         User ──→ Order ──→ OrderItem       (models, enums, relations)
               ↑
CODE         UserService ──→ PrismaService       (modules, controllers, providers)
               ↓
CONTRACT     GET /users/:id, order-created topic (endpoints, Kafka topics)
               ↓
SERVICE      api-service ──→ payments-service    (deployable services)
```

Questions that cross layers — "how does the User model connect to the payments-service?" — get real answers because the graph traces paths through all layers.

## Scanning

Chronicle uses a hybrid AST + LLM pipeline. Tree-sitter parses deterministic patterns (imports, decorators, DI), a rules engine maps framework syntax to semantic meaning, and LLM agents classify ambiguous patterns (HTTP calls, event emits) that require context.

Extraction runs in two phases: haiku agents scan all files in parallel for breadth, then sonnet agents trace complex flows on trigger files for depth. Instruction packs tell agents what framework patterns to look for.

Every extracted fact carries evidence — file, line, confidence, derivation kind — enabling incremental re-scans and trust scoring.

See [docs/scanning.md](scanning.md) for the full pipeline breakdown.

## Evidence and trust

Every fact in the graph has provenance — file path, line number, confidence score, derivation kind. When code changes:

1. Changed files are invalidated
2. Evidence for affected nodes/edges becomes stale
3. Trust scores recalculate automatically
4. Re-scanning restores trust with fresh evidence

If an agent verifies a dependency exists (positive evidence) or doesn't exist (negative evidence), that feeds back into the graph. Trust scores adjust. The graph learns.

## Incremental updates

```
git diff → changed files → invalidate stale evidence → re-scan affected files
  → new evidence → trust recalculated → graph updated
```

Only changed files get re-scanned. A 6000-file project with 3 changed files takes seconds, not minutes.

## Pub/sub traversal

Chronicle automatically traverses Kafka/message topics in directed mode:

```
Producer → topic → Consumer
```

This means path queries find event-driven connections that grep can't.

## Impact analysis

`chronicle impact X` does reverse BFS + forward surface expansion:

1. Reverse traversal: finds all nodes that depend on X (transitively)
2. Forward expansion: from impacted nodes, finds affected endpoints and topics

Result: "If you change X, these 5 services, 3 endpoints, and 1 Kafka topic are affected."

## Name resolution

Graph queries accept names, not just internal keys:

```
chronicle impact "OrderService"    →  resolves to code:provider:myapp:orderservice
chronicle path "Producer" "Consumer"  →  finds both, traces path
```

## Branch awareness

Scan on `feature/payments` and the knowledge stays isolated from `main`. Switch branches, queries show the right context.

## Dashboard

Starts automatically with the MCP server. Embedded SPA — zero infrastructure.

- **Overview** — graph stats, request log, growth chart
- **Graph** — tree, explore, and workspace modes
- **Language** — domain glossary + violation checker
- **Diagrams** — entity-first live diagrams (validated from graph, virtual nodes for actors)
- **Settings** — manifest, prompts, edge config

## Multi-repo

Chronicle Pro adds federation — cross-repo impact analysis, external node resolution, combined dashboard. Contact for access.
