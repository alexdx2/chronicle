# Sub-project 4: Admin Dashboard

## Summary

A localhost-only admin dashboard embedded in the Oracle binary (`oracle admin`). Serves a single-page web app on `http://localhost:4200` with real-time WebSocket updates. Two views: **Overview** (stats, MCP request log, graph breakdown, low confidence items, scan history, validation) and **Graph** (layered/force-directed diagram with click-to-inspect). Light theme. No auth.

## Decisions

| Decision | Choice | Rationale |
|---|---|---|
| Serving | Embedded in Go binary via `//go:embed` | Single binary, zero deps |
| Updates | WebSocket | Real-time MCP request streaming |
| Frontend | Alpine.js + htmx | Reactive without build step, small enough to embed |
| Graph rendering | D3.js (force) + custom SVG (layered) | D3 for interactive, custom for structured view |
| Theme | Light | User preference |
| Auth | None | Localhost only |

## Architecture

```
oracle admin [--port 4200] [--db oracle.db]
  ├── HTTP server on localhost:4200
  │   ├── GET /              → serves index.html (embedded SPA)
  │   ├── GET /api/stats     → graph stats JSON
  │   ├── GET /api/requests  → recent MCP request log
  │   ├── GET /api/low-conf  → low confidence edges
  │   ├── GET /api/scans     → scan/revision history
  │   ├── GET /api/validate  → run validation, return issues
  │   ├── GET /api/graph     → full graph (nodes + edges) for diagram
  │   └── WS  /ws            → WebSocket for real-time MCP request events
  └── MCP middleware
      └── Wraps the MCP server to intercept all tool calls and broadcast to WS
```

### MCP Request Logging

The key integration point: when `oracle mcp serve` handles tool calls, each request/response is logged to an in-memory ring buffer AND broadcast via WebSocket to connected admin clients.

This means `oracle admin` needs to run alongside `oracle mcp serve`. Two approaches:

**Chosen: Combined mode.** `oracle admin --db oracle.db` starts BOTH the HTTP admin server AND reads from the same SQLite DB. MCP requests are logged to a `mcp_request_log` SQLite table by a middleware wrapper around the MCP server. The admin server reads from this table and polls for new entries.

The MCP server writes request logs to SQLite. The admin server reads them and pushes to WebSocket clients.

### New SQLite Table

```sql
CREATE TABLE IF NOT EXISTS mcp_request_log (
  request_id     INTEGER PRIMARY KEY AUTOINCREMENT,
  timestamp      TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%f', 'now')),
  tool_name      TEXT NOT NULL,
  params_json    TEXT NOT NULL DEFAULT '{}',
  result_json    TEXT,
  error_message  TEXT,
  duration_ms    INTEGER NOT NULL DEFAULT 0,
  summary        TEXT    -- human-readable one-liner: "18n 17e 7ev", "rev #5", "0 paths"
);

CREATE INDEX IF NOT EXISTS idx_mcp_request_log_timestamp
  ON mcp_request_log(timestamp DESC);
```

### MCP Middleware

A wrapper around the MCP server handler that:
1. Records timestamp before calling the real handler
2. Calls the real handler
3. Computes duration
4. Generates a human-readable summary from the result
5. Inserts a row into `mcp_request_log`

This is transparent — the MCP protocol behavior is unchanged.

## Views

### Overview Tab

**Header bar:** Domain name, current revision, last scan time.

**Stat cards (6):**
- Nodes (total)
- Edges (total)
- Active nodes
- Stale nodes
- Validation issues
- Low confidence edges (below threshold)

**MCP Request Log panel** (left, tall):
- Header: "MCP Requests" + summary stats (total, errors, avg latency)
- Each entry: timestamp, tool name, status (ok/error/empty), duration, human-readable summary
- Error entries: red left border, expanded error message
- Empty results (0 paths, node not found): amber left border
- New entries stream in via WebSocket, prepended at top
- Expandable: click to see full params/response JSON

**Graph Breakdown panel** (right top):
- Nodes by layer (code: 9, contract: 7, service: 2)
- Edges by derivation (hard: 14, linked: 3)
- Edge type badges

**Low Confidence Edges panel** (right middle):
- Edges below configurable threshold (default < 0.80)
- Shows: from → to, edge type, derivation, confidence score

**Scan History panel** (right bottom):
- Recent revisions: mode (full/incr), SHA, time ago, counts (+18n +17e, 3 stale)

**Validation bar** (bottom):
- Current validation status: "0 issues — 18 nodes, 17 edges, 7 evidence checked"
- "Re-check" button triggers validation API call

### Graph Tab

**Toolbar:**
- View toggle: Layered / Force
- Layer filter chips: code, contract, service (toggle on/off)
- "Hide structural" toggle (default: on — CONTAINS edges hidden)
- "Highlight low confidence" toggle

**Layered View:**
- Nodes organized into horizontal bands by layer (Code → Contract → Service)
- Dashed lines separate layers
- Dependency arrows between nodes with edge type labels
- Low-confidence nodes/edges highlighted in amber with score shown
- Nodes colored by layer (blue = code, green = contract, red = service)
- Click a node to show detail panel

**Force-Directed View:**
- D3.js force simulation
- Nodes as circles, colored by layer
- Edges as lines, styled by derivation (solid = hard, dashed = linked)
- Drag nodes, zoom, pan
- Click for details
- Low-confidence edges in amber

**Node Detail Panel** (bottom, shown on click):
- Node key, name, file path + line range
- Outgoing edges with type, derivation, score
- Incoming edges with type, derivation, score
- Evidence entries

## Color Scheme (Light Theme)

| Element | Color |
|---|---|
| Primary / code layer | #4361ee (blue) |
| Success / healthy | #2ecc71 (green) |
| Warning / low conf | #f39c12 (amber) |
| Error | #e74c3c (red) |
| Contract layer | #27ae60 (green) |
| Service layer | #e74c3c (red) |
| Topic | #1abc9c (teal) |
| Background | #f0f1f3 |
| Cards | #ffffff with box-shadow |
| Text primary | #333 |
| Text secondary | #999 |

## Files

```
internal/admin/
  server.go          # HTTP server, WebSocket hub, API handlers
  middleware.go      # MCP request logging middleware
  handlers.go        # API endpoint handlers (/api/stats, /api/requests, etc.)
  websocket.go       # WebSocket hub + client management
internal/admin/static/
  index.html         # Main SPA (Alpine.js + htmx + D3.js)
  style.css          # Light theme styles
  app.js             # Alpine.js app logic, WebSocket client
  graph.js           # D3.js force graph + layered SVG renderer
internal/cli/admin.go  # "oracle admin" command
internal/store/
  store.go           # Modified — add mcp_request_log table to schema
  requestlog.go      # MCP request log CRUD
```

All static files embedded via `//go:embed internal/admin/static/*`.

## API Endpoints

| Endpoint | Method | Response |
|---|---|---|
| `/` | GET | Embedded SPA HTML |
| `/api/stats` | GET | Graph stats (nodes, edges, by layer, by derivation, active, stale) |
| `/api/requests` | GET | Recent MCP request log (default last 100) |
| `/api/requests?since=<id>` | GET | Requests since a given ID (for polling fallback) |
| `/api/low-confidence?threshold=0.8` | GET | Edges below threshold |
| `/api/scans` | GET | Recent revisions with snapshot data |
| `/api/validate` | POST | Run validation, return issues |
| `/api/graph` | GET | Full graph data (nodes + edges + evidence counts) for diagram |
| `/ws` | WS | Real-time MCP request events |

## WebSocket Protocol

Server → Client messages:

```json
{"type": "mcp_request", "data": {
  "request_id": 47,
  "timestamp": "2026-04-23T14:23:05.123",
  "tool_name": "oracle_import_all",
  "duration_ms": 142,
  "status": "ok",
  "summary": "18n 17e 7ev"
}}
```

```json
{"type": "stats_update", "data": {
  "node_count": 18, "edge_count": 17,
  "active_nodes": 18, "stale_nodes": 0
}}
```

Stats update is sent after any mutation request (import, upsert, delete, stale_mark).

## CLI Command

```bash
oracle admin [--port 4200] [--db oracle.db]
```

Opens the admin dashboard on localhost. Uses the same DB as the MCP server.

## Summary Generation

Each MCP tool response gets a human-readable summary for the request log:

| Tool | Summary format |
|---|---|
| oracle_import_all | "18n 17e 7ev" |
| oracle_revision_create | "rev #5" |
| oracle_node_upsert | "node_id: 42" |
| oracle_edge_upsert | "edge_id: 17" |
| oracle_query_deps | "3 deps, depth 2" |
| oracle_query_reverse_deps | "2 reverse deps" |
| oracle_query_path | "1 path (score 0.79)" or "0 paths" |
| oracle_impact | "5 impacted (max score 94)" |
| oracle_query_stats | "18n 17e" |
| oracle_extraction_guide | "full guide" or "nestjs guide" |
| oracle_scan_status | "domain: orders, rev #5" |
| oracle_stale_mark | "3 nodes, 1 edge stale" |
| oracle_snapshot_create | "snapshot #3" |
| oracle_node_list | "12 nodes" |
| oracle_edge_list | "8 edges" |
| oracle_evidence_add | "evidence #42" |
| oracle_node_get | "OrdersController + 2 evidence" |
| oracle_validate_graph | "0 issues" or "3 issues" |
| (error) | error message truncated to 80 chars |

## Success Criteria

1. `oracle admin` starts HTTP server on localhost:4200
2. Overview tab shows all 6 panels with live data from SQLite
3. MCP requests stream in real-time via WebSocket
4. Graph tab renders layered diagram from graph data
5. Graph tab renders force-directed diagram with D3.js
6. Click-to-inspect shows node details in both views
7. Low confidence edges highlighted in amber
8. Validation runs on demand and shows issues
9. Light theme matches the approved mockup
10. All static assets embedded in Go binary (zero external deps at runtime)
11. All existing tests continue to pass
