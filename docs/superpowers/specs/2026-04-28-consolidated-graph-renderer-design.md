# Consolidated Graph Renderer

**Date:** 2026-04-28
**Status:** Approved

## Problem

`renderWorkspaceGraph` (~400 lines) and `renderDiagram` (~250 lines) duplicate edge rendering, marker creation, node shapes, label handling, and zoom setup. They drift apart (e.g., workspace had no short labels until a patch). Bugs must be fixed in two places.

## Solution

Replace both with a single `renderGraph(config)` function. Both tabs call it with different configs. Drop grid and force layouts — keep only strict (dagre, no drag) and free (dagre initial + draggable).

## Config Object

```js
{
  container: HTMLElement,
  nodes: [],
  edges: [],
  mode: 'strict' | 'free',
  positionCache: {},               // node_id → {x,y}, free mode persistence
  features: {
    annotations: null | {global, steps, currentStep},
    investigation: false,
    neighborExpansion: true,        // show grayed linked nodes
  },
  callbacks: {
    onNodeClick: (node, isAlreadySelected) => {},
    onBackgroundClick: () => {},
    onDrag: (nodeId, x, y) => {},
    onExpandNeighbor: (nodeId) => {},
  }
}
```

## Callers

**Workspace tab:**
```js
this.renderGraph({
  container,
  nodes: filtered.nodes,
  edges: filtered.edges,
  mode: this.wsMode,               // 'strict' or 'free'
  positionCache: this._wsPositionCache,
  features: { investigation: true, neighborExpansion: true },
  callbacks: {
    onNodeClick: (n, already) => { this.selectedNode = already ? null : n; this.highlightNode(...); },
    onBackgroundClick: () => { this.selectedNode = null; this.highlightNode(null); },
    onDrag: (id, x, y) => { this._wsPositionCache[id] = {x, y}; },
    onExpandNeighbor: (id) => { this.expandWorkspaceNode(id); },
  }
});
```

**Diagram tab:**
```js
const result = this.renderGraph({
  container,
  nodes: diagramFiltered.nodes,
  edges: diagramFiltered.edges,
  mode: this.diagramMode,
  positionCache: this._diagramPositions,
  features: {
    annotations: { global: this.diagramData.annotations, steps: this.diagramData.steps, currentStep: this.diagramStep },
  },
  callbacks: {
    onNodeClick: (n, already) => { this.selectedNode = already ? null : n; this.highlightNode(...); },
    onBackgroundClick: () => { this.selectedNode = null; this.highlightNode(null); },
    onDrag: (id, x, y) => { this._diagramPositions[id] = {x, y}; },
  }
});
this.$nextTick(() => this.updateDiagramAnnotations());
```

## Layout Modes

### Strict (dagre)
- Dagre computes all node positions and edge waypoints
- `compound: true` when investigation mode has groups
- Edges rendered with `d3.curveBasis` through dagre waypoints
- No dragging — cursor stays default

### Free (dagre + drag)
- Dagre computes initial positions
- `positionCache` overrides where available (persisted from previous renders/drags)
- Nodes draggable — on drag:
  - Update node position
  - Recalculate connected edge paths (straight lines to dragged node, dagre waypoints become stale)
  - Reposition connected labels using `getPointAtLength` at path midpoint
  - Call `callbacks.onDrag(id, x, y)` to persist
- Cursor: `grab` / `grabbing`

## Rendering Pipeline (inside renderGraph)

1. **SVG + defs** — create SVG (responsive sizing), zoom, gRoot
2. **Markers** — default gray, transitive amber, per-category from `edgeCategories`
3. **Edges** — `<path>` with category color stroke, derivation dash, marker-end
4. **Via labels** — transitive edge "via X" labels
5. **Short labels** — non-transitive edge labels from `edgeCategoryLookup`, inline fill
6. **Nodes** — white background rect + colored `node-rect` + name text + type text
7. **Investigation badges** — if `features.investigation`: manual dot, auto badge, neighbor expand "+"
8. **Annotation styling** — if `features.annotations`: highlight strokes, drop-shadow, note text
9. **Layout** — dagre positions for all, then apply positionCache overrides in free mode
10. **Edge paths** — compute `d` attr: curveBasis through waypoints or arc fallback
11. **Label positions** — `getPointAtLength` at midpoint for short labels, midpoint math for via labels
12. **Drag handlers** — free mode only: attach d3.drag to nodes
13. **Click handlers** — node click, background click via callbacks

**Return:** `{svg, gRoot, link, node, shortLabels, viaLabels, simEdges, simNodes}` for post-render use (annotations).

## Neighbor Expansion in Diagram

When `features.neighborExpansion` is true and a node is added to the canvas, the system also adds its directly connected nodes with `_neighbor: true` flag. These render with reduced opacity (grayed out). This already works in the workspace investigation mode — the consolidated renderer makes it available to the diagram tab too.

## What Gets Deleted

- `renderWorkspaceGraph()` — replaced entirely
- `renderDiagram()` — replaced entirely
- Duplicate marker creation loops
- Duplicate edge/node/label rendering code
- Grid layout mode
- Force simulation layout mode
- `wsLayout` state variable (replaced by `wsMode: 'strict' | 'free'`)

## What Stays Unchanged

- `getFilteredData()` — workspace data filtering
- `_filterDiagramData()` — diagram data filtering
- `computeTransitiveEdges()` — shared transitive edge engine
- `highlightNode()` — shared highlight logic
- `updateDiagramAnnotations()` — diagram-specific post-render
- `getDiagramEdgeTypes()`, `getDiagramLayers()` — sidebar helpers
- All sidebar HTML — filter toggles, category editor
- Step navigation UI
- Data fetching (`fetchGraphData`, diagram WebSocket)
