# Scan v2 — Stateful Workflow with Two-Phase Extraction

**Date:** 2026-05-04
**Status:** Draft v2

## Problem

4 scan attempts on otopoint all failed:
1. Claude can't write good glob patterns alone (produced 9K-27K files instead of ~150)
2. Pipeline breaks mid-scan — extracted facts never resolved into graph
3. Claude can forget any step (resolve, finalize) — no enforcement
4. No flows extracted — only surface-level imports/deps

## Core Design Change

**Scan is a stateful workflow, not instructions Claude might follow.**

MCP manages a `scan_run` object. Claude only does extraction work. MCP controls phase transitions and blocks invalid operations.

---

## 1. Scan Run State Machine

```sql
CREATE TABLE scan_runs (
    run_id          INTEGER PRIMARY KEY AUTOINCREMENT,
    revision_id     INTEGER NOT NULL,
    domain_key      TEXT NOT NULL,
    phase           TEXT NOT NULL DEFAULT 'setup',
    status          TEXT NOT NULL DEFAULT 'running',
    total_files     INTEGER DEFAULT 0,
    extracted_files INTEGER DEFAULT 0,
    resolved        INTEGER DEFAULT 0,
    created_at      TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now')),
    updated_at      TEXT
);
```

### Phases

```
setup → phase1_extract → phase1_resolve → phase2_select → phase2_extract → phase2_resolve → finalized
```

### Transitions (MCP enforces)

| From | To | Trigger |
|------|-----|---------|
| `setup` | `phase1_extract` | `discover_files` called successfully |
| `phase1_extract` | `phase1_resolve` | All scan_file obligations satisfied |
| `phase1_resolve` | `phase2_select` | `resolve_extractions` completed |
| `phase2_select` | `phase2_extract` | Trigger candidates selected |
| `phase2_extract` | `phase2_resolve` | All flow extractions done |
| `phase2_resolve` | `finalized` | `finalize` returns clean |

### Status

| Status | Meaning |
|--------|---------|
| `running` | Active, can accept next action |
| `paused` | Rate limited or interrupted, resume with same action |
| `blocked` | Requires specific action before continuing |
| `completed` | Phase done, ready for next |
| `failed` | Error, needs manual fix |

---

## 2. `chronicle_scan_next_file` — Now Phase-Aware

Returns different actions based on scan run phase:

```json
// During phase1_extract:
{
  "scan_run_id": 1,
  "phase": "phase1_extract",
  "action": "extract_files",
  "files": ["api/src/services/order.service.ts", ...],
  "progress": {"extracted": 45, "total": 139, "remaining": 94}
}

// When all files extracted but not resolved:
{
  "scan_run_id": 1,
  "phase": "phase1_extract",
  "action": "call_resolve_extractions",
  "blocked": true,
  "reason": "139 files extracted, 139 unresolved. Call chronicle_resolve_extractions to build the graph."
}

// During phase2_extract:
{
  "scan_run_id": 1,
  "phase": "phase2_extract",
  "action": "trace_flow",
  "trigger": {"type": "graphql_mutation", "name": "createOrder", "file": "api/src/graphql/resolvers/order.resolver.ts"},
  "context": {"service": "OrderService", "injects": ["PaymentService", "VoucherService", "NotificationService"]}
}

// When everything done:
{
  "scan_run_id": 1,
  "phase": "finalized",
  "action": "none",
  "done": true,
  "summary": {"nodes": 256, "edges": 312, "flows": 15, "evidence": 200}
}
```

### Hard blocks

If Claude calls the wrong tool for the current phase:

```json
{
  "blocked": true,
  "required_action": "chronicle_resolve_extractions",
  "reason": "All Phase 1 obligations are done. Resolve is required before Phase 2."
}
```

`finalize` is impossible if unresolved > 0:
```json
{
  "error": "Cannot finalize: 104 unresolved extractions. Call chronicle_resolve_extractions first."
}
```

---

## 3. `setup` Command — Interactive Manifest Builder

### Flow

```
1. Claude reads top-level dirs, package.json workspaces
2. Groups git-tracked files by directory
3. Shows user file counts per directory grouped by role:

   "Backend (include by default):
     api/src/services/        — 47 files
     api/src/controllers/     — 14 files
     api/src/graphql/resolvers/ — 33 files
     api/prisma/              — 1 file

   Frontend (exclude by default):
     crm/src/components/      — 892 files
     crm/src/hooks/           — 67 files
     mobile/src/screens/      — 156 files

   Config/schemas:
     docker-compose*.yml      — 4 files
     */package.json           — 12 files

   Total with current selection: ~125 files

   Would you like to adjust? (add/remove directories)"

4. User confirms or adjusts
5. Claude saves manifest → calls discover_files → shows final count
6. If count > 500, suggests more excludes
```

### Key: shows COUNTS per DIRECTORY, not individual files

User makes directory-level decisions: "exclude components and screens" → removes 1000+ files in one choice.

---

## 4. File Processing Statuses

Not just "extracted" or "skip". Richer classification:

| Status | Meaning | Creates facts? |
|--------|---------|---------------|
| `extracted` | Architecture found, facts produced | Yes |
| `no_runtime_architecture` | No services/deps/endpoints, but file is legitimate code | No |
| `config_only` | Contains config values (topics, routes, env names) — may be useful | Possible |
| `type_only` | Only type definitions, interfaces, DTOs | No |
| `generated` | Auto-generated file (codegen, compiled) | No |
| `skipped` | Intentionally not read (test, story) | No |
| `failed` | Error reading or processing | No |

Important: `config_only` files MAY produce facts. A file with Kafka topic names or Redis channel names is config but architecturally relevant.

---

## 5. Phase 1 — Breadth Extraction

### Subagent prompt (schema-first)

MCP gives each subagent the EXACT allowed fact schema, not free-text instructions:

```json
{
  "file": "api/src/services/order.service.ts",
  "revision_id": 1,
  "domain": "otopoint",
  "extraction_schema": {
    "allowed_facts": [
      {"kind": "import", "fields": ["to", "symbols"]},
      {"kind": "injection", "fields": ["service_name", "param_name"]},
      {"kind": "endpoint", "fields": ["method", "path", "handler"]},
      {"kind": "method_call", "fields": ["object", "method", "context"]},
      {"kind": "message_producer", "fields": ["topic", "payload_hint"]},
      {"kind": "message_consumer", "fields": ["topic", "handler"]},
      {"kind": "external_call", "fields": ["url", "method", "service_name"]},
      {"kind": "delegation", "fields": ["to_file", "method", "reason"]},
      {"kind": "model_usage", "fields": ["model", "operation"]},
      {"kind": "decorator", "fields": ["name", "target"]},
      {"kind": "config_value", "fields": ["key", "value", "usage"]}
    ]
  },
  "instructions": "Read this file. Extract ONLY facts matching the schema above. Output as JSON array."
}
```

Subagent outputs ONLY structured facts. No prose, no interpretation, no guessing about other files.

### Resumability

- Each `file_extracted` call satisfies the scan_file obligation
- If scan breaks: next `scan_next_file` returns remaining files
- `resolve_extractions` works on whatever exists — partial is fine
- No data lost on interruption

---

## 6. Phase 2 — Flow Extraction from Triggers

### Starts from triggers, NOT from hubs

A flow is: trigger → handler → orchestration → side effects.

### Step 1: MCP identifies candidate triggers

After Phase 1 resolve, the graph has:
- Contract nodes (endpoints, mutations, socket events, consumers, crons)
- Code nodes (services, resolvers, controllers, gateways)
- Edges (EXPOSES_ENDPOINT, INJECTS, CONSUMES_TOPIC)

MCP queries for trigger candidates:
```sql
SELECT node_key, name FROM graph_nodes
WHERE layer = 'contract'
  AND node_type IN ('endpoint', 'mutation', 'query', 'ws_event', 'topic', 'cron')
  AND active = 1
```

### Step 2: Score and rank triggers

```
trigger_score =
  + downstream_dependency_count (how many services involved)
  + write_operations (how many models written)
  + external_calls (how many 3rd party systems)
  + event_production (does it trigger async downstream)
  + name_keywords (order, payment, checkout, voucher, create, update, delete)
```

Top N triggers selected for flow tracing.

### Step 3: Claude traces each trigger

For each selected trigger, Claude receives:

```json
{
  "action": "trace_flow",
  "trigger": {
    "type": "graphql_mutation",
    "name": "createOrder",
    "file": "api/src/graphql/resolvers/order.resolver.ts",
    "method": "OrderResolver.createOrder"
  },
  "graph_context": {
    "handler_service": "OrderService",
    "service_injects": ["PaymentService", "VoucherService", "NotificationService", "PrismaService"],
    "service_file": "api/src/services/order.service.ts"
  }
}
```

Claude reads the service file and traces the flow:

```json
{
  "kind": "flow",
  "flow_name": "Create Order",
  "trigger": {
    "type": "graphql_mutation",
    "name": "createOrder",
    "file": "api/src/graphql/resolvers/order.resolver.ts"
  },
  "steps": [
    {"service": "OrderService", "method": "createOrder", "calls": ["VoucherService.lock", "PaymentService.charge"]},
    {"service": "PaymentService", "method": "createPaymentIntent", "external": "Stripe"},
    {"service": "NotificationService", "method": "notifyOrderCreated", "produces": "order.created"}
  ],
  "requires": ["OrderService", "PaymentService", "VoucherService", "NotificationService"],
  "models_written": ["Order", "Payment", "Transaction"],
  "confidence": 0.85
}
```

### Phase 2 is optional but automatic

Phase 2 runs as part of the same scan unless user interrupts. MCP transitions automatically after Phase 1 resolve completes.

---

## 7. Scan Command (simplified)

With stateful workflow, the scan command becomes trivial:

```
scan command:
1. Call chronicle_scan_next_file in a loop
2. Do whatever it tells you (extract files, resolve, trace flows)
3. Stop when it says done=true
4. For file extraction: spawn parallel subagents
5. For flow tracing: read files sequentially (needs graph context)

That's it. MCP handles all state transitions and phase logic.
```

The intelligence is in the SERVER, not the instructions.

---

## 8. Implementation Plan

| Priority | Component | What |
|----------|-----------|------|
| 1 | `store/scan_runs.go` | New table + CRUD |
| 2 | `graph/scan_workflow.go` | State machine logic, phase transitions |
| 3 | `internal/mcp/server.go` | Rewrite `scan_next_file` to be phase-aware |
| 4 | `internal/mcp/server.go` | Add hard blocks to `finalize` and `resolve` |
| 5 | `internal/mcp/commands.go` | Simplify scan command to "call scan_next_file in loop" |
| 6 | `internal/mcp/commands.go` | Add `setup` command with interactive manifest flow |
| 7 | `graph/discover.go` | Add `GroupFilesByDirectory` for setup display |
| 8 | `graph/resolve_extractions.go` | Handle `delegates` and `config_value` fact kinds |
| 9 | `graph/scan_workflow.go` | Phase 2 trigger detection + scoring |
| 10 | Tests | tom-and-jerry full scan test with stateful workflow |

---

## 9. Testing

### tom-and-jerry (controlled fixture):
- Phase 1: 42 files → all extracted, resolved, 0 rejected
- Phase 2: "Tom attacks Jerry" flow traced from POST /arena/attack trigger
- Delegation: BattleHandlerFactory found via `delegates` fact
- Scan run transitions through all phases to `finalized`

### otopoint (real project):
- Setup: ~139 files discovered after user confirms excludes
- Phase 1: completes in 1-2 sessions (resumable)
- Phase 2: top 15 triggers traced
- Final: more nodes/edges than old scan, all with verified evidence
