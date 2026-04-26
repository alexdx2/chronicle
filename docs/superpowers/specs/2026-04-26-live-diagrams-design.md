# Live Diagrams — Claude Shows Architecture via Dashboard

**Date:** 2026-04-26
**Status:** Draft

## Problem

When Claude explains architecture, dependencies, or impact to a user, it can only describe things in text. Users would understand better with a visual diagram. The dashboard already has a workspace canvas that renders nodes/edges — we need a way for Claude to push diagram content to it in real-time.

## Solution Overview

Three new MCP tools let Claude create live diagram sessions. Claude calls `oracle_diagram_create()` to get a URL, shares it with the user, then pushes graph data via `oracle_diagram_update()` and highlights/annotates via `oracle_diagram_annotate()`. The admin server stores sessions in memory and broadcasts updates via WebSocket. The dashboard renders diagrams using the existing workspace canvas.

## MCP Tools (3 new)

### `oracle_diagram_create(title?)`

Creates a new diagram session.

- **Parameters:**
  - `title` (optional string): Human-readable name, e.g. "Auth Flow"
- **Returns:** `{session_id: "a1b2c3d4", url: "http://localhost:{port}/diagram/a1b2c3d4"}`
- **Behavior:** Generates 8-char random session ID. Calls admin server `POST /api/diagram` to register the session. Returns URL for the user.

### `oracle_diagram_update(session_id, payload)`

Pushes graph data to a diagram session. Can be called repeatedly to evolve the diagram.

- **Parameters:**
  - `session_id` (required string): Session from `oracle_diagram_create`
  - `payload` (required JSON): Standard Oracle graph format `{nodes: [...], edges: [...]}`
    - Nodes: `{node_id, node_key, layer, node_type, domain_key, name, confidence, ...}`
    - Edges: `{from_node_id, to_node_id, edge_type, derivation, confidence, ...}`
- **Returns:** `{status: "ok", node_count: N, edge_count: M}`
- **Behavior:** Calls admin server `PUT /api/diagram/{session_id}`. Admin stores in memory, broadcasts via WebSocket.

### `oracle_diagram_annotate(session_id, node_key, note?, highlight?)`

Adds annotations and highlighting to specific nodes.

- **Parameters:**
  - `session_id` (required string)
  - `node_key` (required string): Target node to annotate
  - `note` (optional string): Text shown near the node, e.g. "This is the bottleneck"
  - `highlight` (optional string): Color name or hex, e.g. "red", "#ff0000"
  - At least one of `note` or `highlight` required.
- **Returns:** `{status: "ok"}`
- **Behavior:** Calls admin server `PUT /api/diagram/{session_id}/annotate`. Stored in annotations map on the session. Broadcast via WebSocket.

## Admin Server

### In-Memory State

```go
type DiagramSession struct {
    ID          string
    Title       string
    Nodes       []map[string]any
    Edges       []map[string]any
    Annotations map[string]Annotation  // node_key → annotation
    CreatedAt   time.Time
    UpdatedAt   time.Time
}

type Annotation struct {
    Note      string `json:"note,omitempty"`
    Highlight string `json:"highlight,omitempty"`
}
```

Sessions stored in `map[string]*DiagramSession` on the `Server` struct, protected by `sync.RWMutex`. Lost on server restart (diagrams are ephemeral conversation artifacts).

### New Endpoints

| Method | Path | Purpose |
|--------|------|---------|
| `POST` | `/api/diagram` | Create session → `{session_id, url}` |
| `GET` | `/api/diagram/{id}` | Fetch session state (initial page load) |
| `PUT` | `/api/diagram/{id}` | Update nodes/edges → broadcast |
| `PUT` | `/api/diagram/{id}/annotate` | Add/update annotation → broadcast |

### WebSocket Messages

```json
{"type": "diagram_update", "session_id": "a1b2c3d4", "nodes": [...], "edges": [...], "annotations": {...}}
```

### MCP → Admin Communication

MCP tools call admin endpoints via HTTP: `http.Post("http://localhost:{adminPort}/api/diagram/...")`. Admin port already available via `SetAdminPort()`.

## Dashboard

### Route-Based Navigation

The dashboard switches from hash-based tab switching to path-based routes:

| Path | Tab |
|------|-----|
| `/` or `/overview` | Overview |
| `/graph` | Graph (Tree/Explore) |
| `/workspace` | Workspace |
| `/language` | Language |
| `/settings` | Settings |
| `/diagram/{session_id}` | Live diagram from Claude |

- Dashboard reads `window.location.pathname` on load
- Tab switching uses `history.pushState()` — no page reload
- Admin server serves `index.html` for all paths (SPA catch-all)
- Claude gives user URL like `http://localhost:4200/diagram/a1b2c3d4` — opens directly

### Diagram Tab

- Appears in tab bar as "Diagram: {title}" (or "Diagram") only when navigated via URL
- On load: `GET /api/diagram/{session_id}` → get initial nodes/edges/annotations
- Renders using the existing workspace canvas (`renderWorkspaceGraph` or similar)
- Listens for `diagram_update` WebSocket messages with matching session_id → re-renders

### What's Different from Workspace

- No palette panel (Claude controls the content, not the user)
- No investigation mode (no drag-from-palette, no BFS paths)
- Grouping by `domain_key` works (nodes in payload have domain_key)
- Filter sidebar available (user can hide node types)
- Pan/zoom/node-drag work
- Side panel on click works (shows node details + edges)
- Grid/Force layout toggle works

### Annotation Rendering

- **Highlighted node:** Thicker border (3px) + colored glow (`filter: drop-shadow(0 0 6px {color})`) in the specified color
- **Note:** Small text label rendered below the node rect. Italic, semi-transparent background pill, max-width 150px with word wrap.

## Guide & Command Integration

### Compact Guide Addition

Appended to the extraction guide JSON:

```json
{
  "diagrams": {
    "when": "When explaining architecture, dependencies, impact, or flows to the user, offer to show a live diagram",
    "how": "Call oracle_diagram_create() to get a URL, share it, then oracle_diagram_update() with relevant nodes/edges",
    "tips": [
      "Start simple — 3-5 key nodes, add detail incrementally",
      "Use oracle_diagram_annotate to highlight what you're talking about",
      "Pull nodes from oracle_node_list or oracle_query_deps results",
      "Update the diagram as the conversation evolves"
    ]
  }
}
```

### New Command: `oracle_command(command='diagram')`

Returns step-by-step instructions:

1. Ask user: "Want me to show this as a diagram?"
2. `oracle_diagram_create(title="descriptive name")` → get URL
3. Share URL with user: "Open {url} to see the diagram"
4. Build payload from graph queries (`node_list`, `query_deps`, `impact`, etc.)
5. `oracle_diagram_update(session_id, {nodes, edges})`
6. `oracle_diagram_annotate(session_id, node_key, note="...", highlight="red")`
7. As conversation continues, call `oracle_diagram_update` again to evolve

### Diagram Types (documented in command instructions)

| Type | How Claude builds it |
|------|---------------------|
| **Dependency diagram** | `oracle_query_deps(node_key)` → show node + dependencies |
| **Impact diagram** | `oracle_impact(node_key)` → show changed node + impacted nodes |
| **Path diagram** | `oracle_query_path(from, to)` → show path between two nodes |
| **Service map** | `oracle_node_list(layer='service')` + their edges |
| **Custom** | Claude builds ad-hoc nodes/edges for explanatory purposes |

### How Claude Constructs a Diagram Payload

The command instructions guide Claude through building the payload. The key principle: **query the graph, transform results into diagram format**.

**Step-by-step process documented in the command:**

1. **Decide what to show.** Based on the conversation, Claude picks a diagram type (dependency, impact, path, service map, or custom).

2. **Query the graph.** Call the appropriate query tool:
   - `oracle_query_deps(node_key, depth=2)` → returns `[{node_key, name, layer, node_type, depth, trust_score}]`
   - `oracle_impact(node_key)` → returns `{impacts: [{node_key, name, layer, node_type, impact_score, path, edge_types}]}`
   - `oracle_query_path(from, to)` → returns `{paths: [{nodes: [keys], edges: [{from, to, type}]}]}`
   - `oracle_node_list(layer='service')` + `oracle_edge_list()` → raw nodes and edges

3. **Build the payload.** Transform query results into `{nodes: [...], edges: [...]}` format:
   - Each node needs at minimum: `node_id` (numeric — use incrementing counter starting from 1), `node_key`, `name`, `layer`, `node_type`
   - Optionally include: `domain_key` (for grouping), `confidence`
   - Each edge needs: `from_node_id`, `to_node_id`, `edge_type`
   - Optionally include: `derivation`, `confidence`

4. **For custom/explanatory diagrams** where nodes don't exist in the graph:
   - Claude invents node_keys like `custom:box:explain:user-request`
   - Uses layer/node_type to control visual style (service=red, data=purple, flow=pink, etc.)
   - Edges use standard edge_types or custom labels
   - This lets Claude draw arbitrary architecture diagrams, not just graph subsets

5. **Push and annotate:**
   - `oracle_diagram_update(session_id, payload)` — shows the diagram
   - `oracle_diagram_annotate(session_id, "service:myapp:orderservice", note="Start here", highlight="green")` — draws attention
   - Call update again to add/remove nodes as explanation evolves

**Example: Claude explains "what happens when a user places an order"**

```
// 1. Query
deps = oracle_query_deps("flow:use_case:myapp:place-order", depth=2)

// 2. Build payload from results
payload = {
  nodes: deps.map(d => ({
    node_id: hash(d.node_key),
    node_key: d.node_key,
    name: d.name,
    layer: d.layer,
    node_type: d.node_type,
    domain_key: d.domain_key
  })),
  edges: // extracted from query results or fetched via oracle_edge_list
}

// 3. Push
oracle_diagram_update(session_id, payload)

// 4. Annotate
oracle_diagram_annotate(session_id, "flow:use_case:myapp:place-order",
  note="Entry point — triggered by POST /orders", highlight="green")
oracle_diagram_annotate(session_id, "service:myapp:paymentservice",
  note="This is where the Stripe call happens", highlight="orange")
```

## Scope Boundaries

**In scope:**
- 3 MCP tools (create, update, annotate)
- In-memory session store on admin server
- 4 HTTP endpoints + WebSocket broadcast
- Path-based routing for dashboard
- Diagram tab rendering with existing canvas
- Annotation rendering (highlight + notes)
- Guide + command integration

**Out of scope (future):**
- Persistent diagram storage (SQLite)
- Workspace tabs (multiple diagrams open at once)
- User editing diagram content (currently read-only from Claude)
- Diagram sharing/export
- Diagram history/undo
