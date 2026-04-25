# Domain Oracle

A self-learning code analysis tool that builds a multi-layered knowledge graph of any codebase. Claude Code reads and understands your code, Oracle validates, stores, and queries the structured result. The system improves with every scan — tracking what it knows, what it doesn't, and what changed.

```
You: "What breaks if I change the User model?"

Oracle:
  → UserService (depth 1, score 100) — USES_MODEL
  → AuthController (depth 2, score 95) — INJECTS
  → GET /auth/profile endpoint (depth 3, score 90) — EXPOSES_ENDPOINT

  3 services affected, 1 Kafka consumer downstream.
  Evidence: user.service.ts:12, auth.controller.ts:8
```

## Install

```bash
npm install -g @alexdx/depbot-oracle
```

Or build from source:
```bash
git clone https://gitlab.com/Alex_dx3/depbot.git
cd depbot && go build -o oracle ./cmd/oracle
```

## Setup

Add to Claude Code MCP config (`~/.claude.json` → project → mcpServers):

```json
{
  "oracle": {
    "command": "oracle",
    "args": ["mcp", "serve", "--open"]
  }
}
```

That's it. Open Claude Code in any project — Oracle auto-creates `.depbot/`, auto-discovers your project structure, and the admin dashboard opens in your browser.

## Commands

Say these in Claude Code:

| Command | What it does |
|---|---|
| `oracle scan` | Full project scan — data models, code, endpoints, services |
| `oracle data` | Analyze data models (Prisma, TypeORM, entities) |
| `oracle language` | Define domain language glossary, check violations |
| `oracle impact X` | "What breaks if I change X?" |
| `oracle deps X` | "What depends on X?" |
| `oracle path A B` | "How does A connect to B?" |
| `oracle services` | Service architecture overview |
| `oracle status` | Dashboard URL + graph stats |
| `oracle help` | Show all commands |

On first run, Claude automatically detects it's a new project and offers to run the scan.

## How It Works

### The scan loop

```
┌───────────────────────────────────────────────────────────┐
│  1. Claude calls oracle_scan_status                       │
│     → Detects first run → asks "Want me to scan?"         │
│                                                            │
│  2. Claude calls oracle_extraction_guide                   │
│     → Gets methodology (compact ~760 tokens)               │
│                                                            │
│  3. Claude reads your code file by file                    │
│     → READ file → extract → oracle_import_all → forget     │
│     → Never accumulates in context (streaming)             │
│                                                            │
│  4. System auto-discovers quality gaps                     │
│     → Missing endpoints? Missing evidence? Low confidence? │
│                                                            │
│  5. Claude reports discoveries                             │
│     → Unusual patterns, uncertain relationships            │
│     → Defines domain language terms                        │
│                                                            │
│  Next scan reads previous discoveries and improves         │
└───────────────────────────────────────────────────────────┘
```

### What the graph captures

```
DATA LAYER          User ──REFERENCES──→ Order ──REFERENCES──→ OrderItem
(Prisma models,     Merchant ──REFERENCES──→ Product
 entities, enums)

        ↑ USES_MODEL
        │
CODE LAYER          UserController ──INJECTS──→ UserService ──INJECTS──→ PrismaService
(modules,           OrderController ──INJECTS──→ OrderService
 controllers,                                        │
 providers)                               CALLS_SERVICE (linked)
        │                                            │
        │ EXPOSES_ENDPOINT                           ↓
        ↓                                 SERVICE LAYER
CONTRACT LAYER      GET /users/:id        api-service
(endpoints,         POST /orders          payments-service
 topics)            order-created topic   notifications-service
```

Every relationship has:
- **Derivation**: `hard` (visible in AST) or `linked` (convention-based)
- **Evidence**: file path + line number
- **Traversal policy**: structural edges excluded from dependency analysis

### Self-learning

**System auto-discovers** after each import:
- Missing endpoint extractions
- Missing cross-service edges
- Nodes without evidence
- Structural gaps

**Claude discovers** during analysis:
- Unknown code patterns
- Relationships it can't confirm
- Orphan providers, unused decorators

**Users can teach it** — discoveries stored for future scans.

### Domain Language

Oracle tracks your project's ubiquitous language:
- Define terms with aliases and anti-patterns
- Automatic violation checking against the knowledge graph
- Edit glossary in the admin dashboard

```
Term: "Order"
Context: "ordering"
Anti-patterns: ["Purchase", "Booking"]
→ If a node is named "PurchaseService" → violation warning
```

### Admin Dashboard

Starts automatically with MCP server. Use `--open` flag to auto-open browser.

- **Overview** — stats, MCP request log (real-time WebSocket), discoveries
- **Graph** — Tree / Explore (drill-down) / Force (D3.js) views with filters
- **Language** — Domain glossary editor + violation checker
- **Settings** — Manifest editor

Filter presets: All | Data Models | API Surface | Services. Filter by repo.

## Graph Model

### Layers

| Layer | Purpose |
|---|---|
| `data` | Prisma models, entities, enums, relations |
| `code` | Modules, controllers, providers, resolvers, guards |
| `service` | Deployable services |
| `contract` | HTTP endpoints, Kafka topics, GraphQL operations |
| `flow` | Business process flows |
| `ownership` | Teams, owners |
| `infra` | Terraform, K8s |
| `ci` | Pipelines, releases |

### Key edge types

| Edge | Meaning | Derivation |
|---|---|---|
| `INJECTS` | Constructor DI, @UseGuards, @UseInterceptors | hard |
| `EXPOSES_ENDPOINT` | Controller → HTTP route | hard |
| `CALLS_SERVICE` | HTTP client → service via env URL | linked |
| `USES_MODEL` | Service → Prisma model | hard |
| `REFERENCES_MODEL` | Model → model via @relation | hard |
| `PUBLISHES_TOPIC` | Producer → Kafka topic | hard |
| `CONSUMES_TOPIC` | Consumer ← Kafka topic | hard |
| `CONTAINS` | Module → providers (structural) | hard |

## MCP Tools

25+ tools including:

| Tool | Purpose |
|---|---|
| `oracle_command` | Execute commands (scan, data, language, impact, etc.) |
| `oracle_extraction_guide` | Get extraction methodology |
| `oracle_scan_status` | Graph state + onboarding detection |
| `oracle_import_all` | Bulk import with validation |
| `oracle_query_path` | Path between nodes |
| `oracle_impact` | Blast radius analysis |
| `oracle_define_term` | Domain language glossary |
| `oracle_check_language` | Naming violation checker |
| `oracle_report_discovery` | Self-learning — report findings |
| `oracle_get_discoveries` | Read previous findings |
| `oracle_admin_url` | Dashboard URL |

## Testing

```bash
# Unit + integration
go test ./... -count=1

# E2E with golden graph (Tom & Jerry 4-service fixture)
go test ./e2e/ -v -run TestTJ

# E2E with real Claude (requires claude CLI)
./e2e/claude_agent_test.sh
```

### Test evolution (9 iterations)

```
Run 1:  49n  62e    —  baseline
Run 5:  56n  75e    —  auto-discoveries working
Run 7:  62n  76e    —  efficiency metrics, 147KB payload
Run 9:  62n  76e    —  streaming 2.5KB avg, domain language, 7 terms defined
```

## Project structure

```
cmd/oracle/                     CLI entrypoint
internal/
  admin/                        HTTP server + WebSocket + embedded SPA
  cli/                          Cobra commands
  graph/                        Path, impact, queries
  mcp/                          MCP server, extraction guide, commands
  registry/                     Type registry + traversal policy
  store/                        SQLite (nodes, edges, evidence, discoveries, language)
  validate/                     Key normalization + field validation
fixtures/                       Test fixtures (orders-domain, tom-and-jerry)
e2e/                            E2E tests + Claude agent test
npm/                            npm package wrapper
```

## Links

- **npm**: [@alexdx/depbot-oracle](https://www.npmjs.com/package/@alexdx/depbot-oracle)
- **GitLab**: [Alex_dx3/depbot](https://gitlab.com/Alex_dx3/depbot)

## License

MIT
