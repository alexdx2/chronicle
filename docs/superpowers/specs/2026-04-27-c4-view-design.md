# C4 View — Design Spec

## Overview

A new top-level route (`/c4`) providing a C4-model diagram of the knowledge graph with animated drill-down, flow trace overlays, lens overlays, group-by pivots, and Claude-driven explanation sessions. The existing Graph tab (Tree, Explore, Workspace) and Diagram tab remain unchanged.

## Navigation & Routing

- New top-level menu item: **C4** (alongside Graph, Diagram, etc.)
- Route: `/c4`
- State via query params: `/c4?level=2&focus=OrderService&groupBy=team&lens=ownership`
- All zoom/filter state is in the URL — shareable and bookmarkable
- Params: `level` (1/2/3), `focus` (node key of the zoomed-into node), `groupBy`, `lens`, hidden types

## C4 Levels

Three levels only (no L4 Code):

| Level | What's shown | Nodes from |
|---|---|---|
| **L1 System Context** | Services + external systems, inter-service edges | `service` layer top-level nodes |
| **L2 Container** | Modules, controllers contained by the focused service | `code` layer nodes with CONTAINS edge from focused service |
| **L3 Component** | Providers, functions, schemas inside a module — leaf level | `code`/`data` layer nodes contained by focused module |

## Node Rendering

- Each node is a rounded rectangle (C4-style box)
- Box content: **name** (bold), **type** (subtitle), **child count** ("12 components") if drillable
- **Zoom icon** (▸ or 🔍) on nodes that have children — indicates click-to-zoom
- **Flow trace icon** (⚡) on nodes that have traceable call chains — indicates click for flow overlay
- Leaf nodes (L3 with no children) show neither icon
- A node can have both icons simultaneously

## Edge Rendering

- Labeled arrows between boxes with edge type as label (CALLS, INJECTS, PUBLISHES, etc.)
- **Sync edges**: solid line, blue
- **Async edges** (topics): dashed line, orange
- **External references**: edges to nodes outside current scope shown as ghost arrows pointing off-canvas with text label (e.g., "→ PaymentSvc")
- Edge colors follow existing type-based coloring

## Transitive Edges (existing logic, applied to C4)

When node types are hidden via filters, collapsible types (topic, endpoint, function, schema) generate transitive edges with italic "via X" labels and dashed strokes. Non-collapsible types just disappear. The existing transitive edge bridging logic applies unchanged.

## Zoom Animation

- **Duration**: ~300ms ease-in-out
- **Zoom in**: Click a node with zoom icon → node scales up to fill viewport, siblings fade out (opacity 0), then children fade in with fresh Dagre layout
- **Zoom out**: Click breadcrumb → children shrink back into parent box, siblings fade back in
- **Breadcrumb**: Always visible below the top bar. Shows navigation path: `System Context › OrderService › OrderModule`. Each segment is clickable to zoom back to that level.
- **URL updates** on each zoom to reflect current level and focus node

## Minimap

- Bottom-right corner, always visible
- Shows L1 System Context as small boxes
- Highlights the currently focused node (the one you've zoomed into)
- Clicking a node in the minimap zooms there directly

## Layout

- **Dagre** (hierarchical, rankdir: TB) is the primary layout engine for each level
- Layout is computed per level on zoom — only the current level's nodes are in the DOM
- Position caching: when zooming back to a previously visited level, reuse the last Dagre positions

## Group-By

At any C4 level, nodes can be grouped by different criteria. Grouping draws dashed boundary rectangles around clustered nodes with a label.

| Group-by | What it does |
|---|---|
| **None** (default) | Flat Dagre layout, no grouping |
| **Team** | Nodes clustered by ownership team |
| **Component type** | Controllers together, providers together, schemas together |
| **Domain** | Grouped by domain key / repo (multi-project) |
| **Infra** | Grouped by deployment target |

Group-by is a dropdown/selector in the top bar. Changing it re-runs Dagre layout within group boxes. Cross-group edges are drawn across boundaries.

## Lens Overlays

Toggle pills in the top bar. Multiple can be active. They add visual information without changing the layout.

| Lens | What it adds |
|---|---|
| **Ownership** | Dashed boundary boxes grouping nodes by team + team name label. Shows cross-team edges clearly. |
| **Infra** | Badges on nodes showing deployment target (k8s, lambda, etc). Color-coded by infra type. |
| **CI** | Badges showing pipeline/build system. Highlights shared CI pipelines. |

Note: Ownership lens and group-by-team serve different purposes. Group-by physically rearranges layout. Lens just overlays boundaries without moving nodes.

## Filters

The existing filter bar is reused unchanged:
- Node type toggles (service, module, controller, topic, schema, endpoint, etc.)
- Domain filter
- Confidence threshold slider
- Repo filter
- Structural edge toggle (CONTAINS/OWNS/HAS_FIELD)

Hidden collapsible types produce transitive "via X" edges (existing behavior).

## Flow Trace Overlay

- **Not a mode** — a contextual fullscreen overlay
- Triggered by clicking the ⚡ icon on a node that has traceable paths
- Appears as a fullscreen overlay on top of the C4 view with a close button
- **Layout**: Left-to-right Dagre (rankdir: LR), sequential flow with numbered steps
- **Crosses C4 levels**: shows the full path regardless of what level you were on
- Each node in the flow shows: name, function/method, parent service name
- **Visual language**: solid blue = sync, dashed orange = async/topic, purple = external, green = data
- **Branching**: when a step fans out (topic consumed by multiple services), the flow branches
- **Powered by existing BFS**: uses `oracle_query_path` to find the chain, renders it sequentially
- **Legend** at the bottom showing edge type meanings
- Close button returns to C4 view at the same state you left

## Investigation Mode

- Toggle button in the top bar (alongside group-by and lenses): `C4 | Investigation`
- When active, the canvas switches to a blank drag-and-drop surface
- Drag nodes from a searchable palette (same as current Workspace) — nodes can come from any C4 level
- Auto-find paths between dropped nodes using existing BFS
- Uses Dagre layout only (no grid/force toggle — simplified from current Workspace)
- Toggling back to C4 returns to the last C4 state (level, focus, filters preserved)

## Claude Session

Claude can create a live explanation session that appears as a menu tab (only 1 active session at a time). The session controls the C4 view to walk the user through architecture, impact analysis, or other explanations.

### Session Lifecycle

1. Claude calls `oracle_c4_session_create` → session tab appears in menu, C4 view enters session mode
2. Claude pushes view state changes via `oracle_c4_session_update` → C4 view reacts in real-time (WebSocket)
3. User watches in browser, can pan/zoom freely, but Claude controls navigation state
4. Claude calls `oracle_c4_session_end` → tab disappears, C4 view returns to user control

These are new MCP tools, separate from the existing diagram session tools.

### Claude Actions Within a Session

| Action | MCP call | Effect on C4 view |
|---|---|---|
| **Navigate** | Set level + focus node | View zooms to that level, centered on that node |
| **Highlight** | Mark nodes/edges with color + note | Nodes glow with highlight color, notes appear as tooltips |
| **Show flow** | Open flow trace for a specific path | Flow trace overlay appears with Claude's annotations |
| **Toggle lens** | Activate/deactivate a lens | Lens turns on/off to support explanation |
| **Filter** | Hide/show node types | Types toggle to focus on what Claude is explaining |
| **Group-by** | Change grouping | Layout regroups to support Claude's point |
| **Step through** | Multi-step walkthrough with titles + descriptions | Step counter appears, each step has its own view state (level, focus, highlights, notes) |
| **Annotate** | Add text notes to nodes/edges | Notes appear on canvas near the relevant element |

### Step-Through Presentations

- Each step stores: level, focus node, active lens, group-by, highlighted nodes, annotations, title, description
- User sees step counter (e.g., "Step 2 of 5") with prev/next controls
- Each step transition animates the C4 view to the step's stored state
- Claude can build these programmatically via MCP to create guided architecture walkthroughs

## What Stays Unchanged

- **Graph tab** (Tree, Explore, Workspace views) — untouched, existing route
- **Diagram tab** — untouched, existing route
- **Backend data model** — no schema changes needed, C4 levels are derived from existing layers + CONTAINS edges
- **Filter logic** — reused from existing implementation
- **Transitive edge bridging** — reused from existing implementation
- **WebSocket broadcast** — reused for Claude session real-time updates

## Technical Approach

- **Frontend**: New Alpine.js component(s) for the C4 route within the existing `index.html` SPA
- **Rendering**: D3.js + SVG for the C4 canvas, same as existing views
- **Layout**: Dagre for each level, with group-box support for group-by
- **Animation**: D3 transitions for zoom in/out (~300ms)
- **Backend**: New `/api/c4` endpoint returning children of a node at a given level (wraps existing graph queries with CONTAINS traversal). New MCP tools: `oracle_c4_session_create`, `oracle_c4_session_update`, `oracle_c4_session_end`. Existing `/api/graph` stays unchanged.
- **URL state**: Alpine.js watches query params and syncs view state bidirectionally
