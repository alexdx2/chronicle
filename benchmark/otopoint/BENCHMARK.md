# Chronicle MCP Benchmark — OtoPoint

## Goal

Same methodology as tom-and-jerry benchmark, applied to a real-world NestJS monolith (6000+ TS files, 6 repos).

## Rules

- 3 runs per task per mode (3x MCP, baseline reused from tom-and-jerry methodology)
- Fresh Claude session per run
- Do not reveal ground truth
- Score facts only, not explanations
- Use median of 3 runs

## Setup

**Target:** `/home/alex/personal/otopoint/` — NestJS monolith with Prisma, GraphQL, Bull queues, WebSockets, Centrifugo, Stripe

**Working directory:** `/home/alex/personal/otopoint/`

### MCP mode
```bash
cd /home/alex/personal/otopoint
claude  # chronicle MCP tools available
```

### Baseline mode
```bash
cd /home/alex/personal/otopoint
claude --strict-mcp-config --mcp-config /home/alex/personal/chronicle/depbot/benchmark/empty-mcp.json
```

## Tasks

### TASK 1 — Impact Analysis (Order model)

**Prompt:**
```
What breaks if I change the Order Prisma model? List all directly affected services,
transitive dependencies, affected API endpoints (REST and GraphQL), and external
systems (Redis, Stripe, WebSocket, etc). Provide file references.

Format:
## Direct Dependencies
## Transitive Dependencies
## Affected Endpoints
## External Systems
## Confidence (0-100)
```

### TASK 2 — Request Flow (Order creation)

**Prompt:**
```
Trace the full flow when a customer creates an order. Start from the GraphQL mutation
or REST endpoint, through all services, side effects (voucher application, points,
socket events, notifications), to database persistence.

Format:
## Request Chain (ordered)
## Cross-Service Calls
## Async Side Effects (queues, WebSocket, push)
## Data Models Touched
```

### TASK 3 — Reverse Dependencies (SocketService)

**Prompt:**
```
What components depend on SocketService? List all services that inject it,
what events they emit, and through which gateways (web, mobile, CRM).

Format:
## Services that inject SocketService
## Events emitted
## WebSocket Gateways
## Downstream consumers
```

### TASK 4 — Trap Question

**Prompt:**
```
What components depend on InventoryService? List all dependencies and affected endpoints.
```

### TASK 5 — Cross-cutting concern (VoucherService impact)

**Prompt:**
```
If I refactor VoucherApplicationService, what needs to change? Trace all callers,
the data models involved, any queue processors, and API endpoints that expose
voucher functionality.

Format:
## Direct Callers
## Data Models
## Queue Processors
## API Endpoints (REST + GraphQL)
## External Integrations
```

## Scoring

Ground truth will be derived from the Chronicle graph after scan completes.
Each task scored with specific checklists like the tom-and-jerry benchmark.
