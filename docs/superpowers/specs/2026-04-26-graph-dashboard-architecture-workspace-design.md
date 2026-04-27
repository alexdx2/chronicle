# Graph Dashboard: Architecture Mode, Workspace & Transitive Filtering

**Date:** 2026-04-26
**Status:** Draft

## Problem

The current Graph tab has three modes (Tree, Explore, Force) but none provide:
1. A "big picture" architectural view with domains/services as nested containers (domain-clustered)
2. A focused investigation tool to explore relationships between specific entities
3. Noise reduction via transitive edge collapsing (e.g., hide endpoints but keep service-to-service connections)

The Force mode is the weakest of the three existing modes and will be replaced.

## Solution Overview

Replace Force mode with two new modes:

- **Architecture mode** — nested containment diagram: domain boxes contain service/model/flow nodes. Filter sidebar controls detail level.
- **Workspace mode** — ephemeral drag-and-drop investigation canvas. Drag entities from a search palette; auto-discover shortest paths between them.

Both modes share a **filter sidebar** with per-type toggles that perform **transitive edge collapsing** — hiding a node type removes those nodes but preserves logical connections through them as dashed "via X" edges.

## Mode Structure

```
Graph Tab Toolbar: [ Tree | Explore | Architecture | Workspace ]  [ ☰ Filters ]  [ ⛶ Fullscreen ]
```

- **Tree** — unchanged (hierarchical HTML tree by layer)
- **Explore** — unchanged (breadcrumb drill-down navigation)
- **Architecture** — new (nested containment, replaces Force)
- **Workspace** — new (drag-and-drop investigation scratch pad)

## Architecture Mode

### Layout

- **Domain boxes**: Large rounded rectangles, blue (#3b82f6) border, domain name as header label.
- **Nodes inside domain boxes**: Positioned using a force layout constrained within the domain boundary. Each node is a small rectangle colored by its layer.
- **Cross-domain edges**: Lines connecting nodes across domain boxes. Styled by derivation kind (solid = hard, dashed = inferred).
- **Nodes without a domain**: Grouped into an "Unassigned" box.

### Detail Level

There is no separate zoom control. The filter sidebar checkboxes control which node types are visible. To see only services, uncheck everything except `service`. To see the full picture, check all types.

### Interactions

- **Pan & zoom**: Mouse drag to pan, scroll to zoom (same as old Force mode).
- **Click node**: Opens the existing side panel showing dependencies, dependents, file path, layer, type, confidence.
- **Click domain box header**: Scrolls/zooms to center that domain.
- **Hover node**: Highlights connected nodes and edges, dims everything else (existing behavior from Force mode).

### Grouping

Nodes are assigned to domains via the `domain_key` field on `graph_nodes`. The Architecture mode groups nodes by `domain_key`. If `domain_key` is empty, the node goes into the "Unassigned" group.

## Workspace Mode

### Layout

Two-panel layout:
- **Left panel (220px)**: Search palette with all entities, searchable, grouped by node type.
- **Right panel (flex)**: Blank SVG/D3 canvas.

### Core Interactions

1. **Drag entity from palette → canvas**: Places the node on the canvas at the drop position.
2. **First entity placed**: Auto-shows its direct neighbors as dimmed nodes with **+** expand badges.
3. **Second entity placed**: Auto-discovers and highlights the **shortest path** between the two entities. Intermediate nodes along the path are auto-placed and tagged with an orange "auto" badge. Direct neighbors of both entities also shown (dimmed, with + badges).
4. **Click + badge on any neighbor**: Expands that node's neighbors incrementally (smart expand).
5. **Additional entities dragged**: Each new entity triggers shortest-path discovery to all other manually-placed entities on the canvas. Neighbors shown with + badges.
6. **Clear Canvas** button: Resets the workspace entirely.

### Visual Indicators

- **Blue dot** on a node: manually placed by user (dragged from palette).
- **Orange "auto" badge**: placed automatically by shortest-path discovery.
- **Dimmed nodes** (lower opacity): neighbors, not yet expanded.
- **+ badge** (circle with +): click to expand that node's neighbors.
- **Orange highlighted edges**: edges that are part of a discovered shortest path.
- **Gray edges**: neighborhood edges (non-path).

### Domain Toggle

The filter sidebar includes **domain toggles** in both Architecture and Workspace modes. Turning off a domain hides all its entities and collapses edges transitively. In Architecture mode, the domain box itself disappears.

### Persistence

None. The workspace is an ephemeral scratch pad. Closing the tab or switching modes clears it.

### Path Discovery

Uses the existing graph path-finding capability (`oracle path` / `internal/graph/query.go`). The query runs client-side on the already-loaded graph data: BFS on the full node/edge set to find the shortest path between two node IDs.

## Transitive Edge Collapsing

Shared by Architecture and Workspace modes via the filter sidebar.

### Filter Sidebar

- Positioned as a collapsible right panel (toggled via ☰ Filters button).
- Contains:
  - **Node type toggles**: Grouped by layer (Code: module, class, function; Contract: endpoint, topic, schema; Service: service; Data: model; Flow: flow; Infra: database, queue, cache, etc.; Ownership: team, owner; CI: pipeline, job).
  - **Domain toggles**: Show/hide entire domains. In Architecture mode, hides the domain box and its contents. In Workspace mode, hides entities from that domain.
  - **Edge type toggles**: DEPENDS_ON, CALLS, CONTAINS, INJECTS, etc.
  - **Confidence slider**: Min confidence threshold.

### Algorithm

When a node type is unchecked (hidden):

1. Remove all nodes of that type from the visible set.
2. For each hidden node H:
   - Find all incoming edges: A → H (where A is visible).
   - Find all outgoing edges: H → B (where B is visible).
   - For each pair (A, B): create a **transitive edge** A → B.
3. Transitive edge properties:
   - **Visual style**: Dashed line (distinct from real edges).
   - **Label**: "via {hidden node name}" (e.g., "via POST /payments").
   - **Confidence**: min(confidence of A→H, confidence of H→B).
4. Deduplication:
   - If A→B already has a direct (non-transitive) edge, show only the direct edge.
   - If multiple hidden nodes connect A to B, collapse into one transitive edge labeled "via X, Y".
   - If multiple paths exist through hidden nodes between A and B, use the highest-confidence path.

### Chained Collapsing

If hiding type T1 creates transitive edges through nodes of type T2, and T2 is also hidden, the algorithm chains: A → T1 → T2 → B becomes A → B with label "via T1, T2". The algorithm runs iteratively until no hidden nodes remain in any transitive edge path.

### Performance

Runs entirely client-side on the already-fetched graph data. The `/api/graph` endpoint returns all nodes and edges. Filtering + transitive computation happens in JS on toggle. For graphs up to ~5,000 nodes this should be instant. For larger graphs, debounce the toggle (200ms) and compute in a requestAnimationFrame callback.

## Backend Changes

### No new endpoints required

The existing `/api/graph` endpoint returns all nodes and edges with sufficient data:
- `node_type`, `layer`, `domain_key` — needed for grouping and filtering
- `confidence` — needed for confidence slider and transitive confidence calculation
- `edge_type`, `derivation` — needed for edge type toggles and styling

### Possible optimization (not required for v1)

If graphs become very large, add an optional `?domain=X` or `?layers=code,service` filter parameter to `/api/graph` to reduce payload size. But for v1, client-side filtering of the full graph is sufficient.

## Frontend Implementation Notes

### D3.js Compound Nodes (Architecture Mode)

The existing Force mode already uses D3 force simulation. Architecture mode extends this with:
- **Group forces**: Nodes with the same `domain_key` are attracted to a shared center point.
- **Bounding boxes**: Each domain group's bounding box is computed from its member nodes + padding. Rendered as an SVG `<rect>` behind the nodes.
- **Collision avoidance**: Domain bounding boxes repel each other to prevent overlap.
- The D3 `forceX`/`forceY` forces per domain group, combined with a custom bounding-box force, achieve nested containment without needing a dedicated compound graph library.

### Workspace Canvas

- Same D3 SVG canvas as Architecture mode.
- Drag-and-drop from palette uses HTML5 drag events (dragstart on palette items, drop on SVG canvas).
- BFS path finding: simple JS implementation on the in-memory graph adjacency list. No server round-trip needed.
- Node expansion (+ badge click): queries the in-memory adjacency list for neighbors of the clicked node, adds them to the canvas with force simulation.

### Filter Sidebar Component

- Shared React-style component (or vanilla JS module) used by both Architecture and Workspace.
- State: `Set<string>` of hidden node types, `Set<string>` of hidden edge types, `Set<string>` of hidden domains, `number` min confidence.
- On state change: recompute visible nodes → compute transitive edges → update D3 simulation.

## UI Polish

- Transitive edges use the warning color (#f59e0b) with dashed stroke.
- "via X" labels rendered as small pill badges on the edge midpoint.
- Domain box headers use the blue layer color (#3b82f6) with uppercase text.
- Nodes inside domain boxes use their own layer colors (service = red, model = purple, etc.).
- Workspace palette items show a drag handle (⠿) and are colored by layer.
- Workspace "auto" badges are small orange pills on auto-placed nodes.
- The + expand badge is a circle with a + character, styled as a button.

## Scope Boundaries

**In scope:**
- Architecture mode (nested containment with domain boxes)
- Workspace mode (drag-and-drop, path discovery, smart expand)
- Transitive edge collapsing (filter sidebar with per-type toggles)
- Domain toggles in workspace filter sidebar
- Removing the old Force mode

**Out of scope (future work):**
- Manual graph editing (add/remove nodes/edges as manual evidence) — separate feature
- Saving/exporting workspace diagrams
- Workspace persistence across sessions
- Server-side graph filtering optimization
- Touch/mobile support
