# Scan v2 — Resumable Two-Phase Scan with Interactive Setup

**Date:** 2026-05-04
**Status:** Draft

## Problem

4 scan attempts on otopoint all failed:
1. Claude can't write good glob patterns alone (produced 9K-27K files instead of ~150)
2. Pipeline breaks mid-scan (rate limits, context overflow) — extracted facts never resolved into graph
3. No flows extracted — only surface-level imports/deps
4. `resolve_extractions` never called — 104 files extracted, 0 graph nodes

## Design

### Three components:

1. **`setup` command** — interactive manifest builder, runs before first scan
2. **Phase 1 scan** — breadth: parallel agents extract surface facts from every file
3. **Phase 2 scan** — depth: sequential flow tracing across the graph built by phase 1

---

## 1. `setup` Command — Interactive Manifest Builder

### What it does

Claude analyzes the repo structure and proposes a manifest WITH scan config. User confirms/adjusts. Not a one-shot guess — a conversation.

### Flow

```
1. Claude reads top-level dirs, package.json workspaces, Dockerfiles
2. Groups git-tracked files by directory path
3. Shows user:

   "Here's what I found:

   Backend:
     api/src/services/        — 47 .ts files
     api/src/controllers/     — 14 .ts files
     api/src/graphql/resolvers/ — 33 .ts files
     api/src/gateways/        — 3 .ts files
     api/prisma/              — 1 .prisma file

   Shared:
     packages/pricing-engine/ — 8 .ts files
     packages/shared-core/    — 4 .ts files

   Frontend (presentation layer — usually excluded):
     crm/src/components/      — 892 .tsx files
     crm/src/hooks/           — 67 .ts files
     web/src/components/      — 234 .tsx files
     mobile/src/screens/      — 156 .tsx files

   Config:
     docker-compose*.yml      — 4 files
     */package.json           — 12 files

   Total if we include everything: 1,534 files
   Recommended scan (backend + shared + config): ~125 files

   What would you like to include? Any dirs to add or remove?"

4. User confirms or adjusts
5. Claude writes manifest with scan.include/exclude
6. Claude calls chronicle_discover_files to validate count
7. Shows final count: "139 files will be scanned. Ready to scan?"
```

### Key principles

- Shows FILE COUNTS per directory, not individual files
- Groups by role (backend/shared/frontend/config), not by file extension
- Proposes a default that EXCLUDES presentation layer
- User can add/remove entire directories
- Validates count before saving — if > 500, warns and suggests more excludes

### Command instructions

```
setup command:
1. Analyze project structure (git ls-files, group by top-level dirs)
2. Show file counts per directory grouped by role
3. Propose include/exclude — default excludes presentation layer
4. Ask user to confirm or adjust
5. Save manifest with scan config
6. Call chronicle_discover_files to validate total count
7. Show final count and confirm
```

---

## 2. Phase 1 — Breadth Scan (Parallel Extraction)

### What it does

Every file from discovery gets read by a subagent. Surface-level extraction only: imports, exports, injections, endpoints, decorators. No cross-file analysis.

### Flow

```
scan command (after setup is done):

SETUP:
1. chronicle_extraction_guide — read rules
2. chronicle_discover_files — get file list (already filtered by setup)
3. Create revision

SCAN LOOP:
4. chronicle_scan_next_file — returns batch of 10 files
5. If done → go to RESOLVE
6. Spawn parallel subagents (one per file). Each subagent:
   - Reads ONE file
   - Extracts: imports, deps, endpoints, injections, decorators
   - Calls chronicle_file_extracted(file, "extracted", facts_json)
   - Does NOT trace cross-file flows (that's phase 2)
7. Wait for all agents → go to step 4

RESOLVE:
8. chronicle_resolve_extractions — builds graph from all facts
9. chronicle_finalize_incremental_scan — check status
   If rejected evidence → fix
   If uncovered files → resume loop
   If clean → proceed to Phase 2
```

### Subagent prompt (per file)

```
Read this file and extract architectural facts.

For each fact, output JSON:
  {"kind": "import", "to": "./x", "symbols": ["X"]}
  {"kind": "endpoint", "method": "POST", "to": "/orders"}
  {"kind": "call", "object": "stripeService", "method": "createPaymentIntent"}
  {"kind": "decorator", "decorator": "Injectable"}
  {"kind": "http_call", "target": "http://notifications:3005/push"}
  {"kind": "produces", "to": "battle-results"}
  {"kind": "consumes", "to": "battle-results"}

Rules:
- Only extract what you SEE in this file
- Don't guess about other files
- Mark as "no_architecture" if file is just types/constants/helpers
- If you see delegation (factory.register(), handler.process()), note it as:
  {"kind": "delegates", "to": "./factory.ts", "method": "registerHandlers"}
```

### Resumability

If scan breaks mid-way (rate limit, crash):
- Extracted files are already stored in `scan_extractions`
- Obligations track which files are done
- Next `scan_next_file` call skips already-satisfied obligations
- `resolve_extractions` works on whatever facts exist
- No data lost — scan picks up where it left off

---

## 3. Phase 2 — Depth Scan (Flow Extraction)

### What it does

After Phase 1 builds the graph, Phase 2 traces business flows ACROSS services. It reads the graph to understand what connects, then reads specific service files to trace orchestration.

### Flow

```
PHASE 2 (after Phase 1 resolve completes):

1. MCP identifies "hub nodes" — services with most INJECTS edges
   (OrderService with 10 deps = orchestrator = likely has flows)

2. For each hub (top 10-20 by edge count):
   a. Claude reads the service file
   b. Claude reads the graph (what does this service inject?)
   c. Claude traces: "what business process does this method implement?"
   d. Creates flow facts:
      {"kind": "flow", "flow_name": "Create Order", "method": "createOrder",
       "trigger": "POST /orders",
       "requires": ["PaymentService", "VoucherService", "NotificationService"]}
   e. Calls chronicle_file_extracted with flow facts

3. chronicle_resolve_extractions (second time) — adds flow nodes/edges

4. chronicle_finalize_incremental_scan — final check
```

### Why phase 2 needs phase 1

A flow like "Create Order" involves:
- OrderResolver → OrderService → PaymentService → VoucherService → NotificationService

Without the graph from Phase 1, Claude doesn't KNOW these connections. Phase 1 gives it the DI graph, then Phase 2 traces what those connections DO.

### Phase 2 is optional

If user just wants structure (deps, endpoints), Phase 1 is enough. Phase 2 adds business intelligence — "what breaks if I change PaymentService" includes flow impact.

---

## 4. `delegates` Fact Kind

New fact kind for Phase 1 to capture delegation without tracing it:

```json
{"kind": "delegates", "to": "./battle-handler.factory.ts", "method": "registerBattleHandlers"}
```

During `resolve_extractions`, if a file has a `delegates` fact:
1. Check if the delegated file was also extracted
2. If yes — merge their edges (the factory's handlers belong to the gateway)
3. If no — create an obligation to scan that file, warn in finalize

This solves the delegation problem WITHOUT requiring each subagent to read multiple files.

---

## 5. Pipeline Resilience

### Problem: scan breaks, facts orphaned

### Solution: `resolve_extractions` is idempotent and works on partial data

```
- Can be called multiple times (marks resolved facts, doesn't duplicate)
- Works with whatever extractions exist (partial is fine)
- Subsequent calls add new facts to existing graph (doesn't rebuild)
- scan_next_file resumes from unsatisfied obligations (not from scratch)
```

### Problem: rate limits kill the scan

### Solution: scan is designed to be run multiple times

```
First run: processes 60/139 files, hits limit
User: "chronicle scan" again
MCP: "79 files remaining from previous scan. Continuing..."
Picks up at file 61, no wasted work.
```

### Problem: resolve never called

### Solution: if scan_next_file returns done=true, the INSTRUCTIONS say call resolve. But also: `finalize` should warn if there are unresolved extractions.

Add to finalize output:
```json
{
  "unresolved_extractions": 104,
  "warning": "104 file extractions were never resolved. Call chronicle_resolve_extractions first."
}
```

---

## 6. Changes to Existing Code

| Component | Change |
|-----------|--------|
| `commands.go` | Add `setup` command with interactive manifest instructions |
| `commands.go` | Update `scan` command — assume manifest exists, simpler instructions |
| `graph/discover.go` | Add `GroupFilesByDirectory` for setup display |
| `graph/resolve_extractions.go` | Handle `delegates` fact kind |
| `graph/invalidation.go` | Finalize warns about unresolved extractions |
| `internal/mcp/server.go` | Update `scan_next_file` to indicate phase |
| `internal/mcp/server.go` | Add phase 2 hub detection tool |

---

## 7. Testing on tom-and-jerry

### Pass criteria for Phase 1:
- All 42 files processed (0 uncovered)
- All socket.on() handlers found (including delegated ones via `delegates` fact)
- External HTTP calls captured
- 0 rejected evidence (no hallucinations)
- Graph matches expected: ~78 nodes, ~57 edges

### Pass criteria for Phase 2:
- "Tom attacks Jerry" flow traced with all 5 services
- "WebSocket spectating" flow traced with factory delegation
- Flows connect to trigger endpoints AND required services

---

## 8. Testing on otopoint

### Pass criteria:
- ~139 files scanned (backend + shared + config)
- Scan completes in one session OR resumes cleanly
- `resolve_extractions` always called
- Evidence has assertions (>80% verified at creation)
- Flows extracted for top 10 hub services
- No SQLite locks (busy_timeout handles contention)
