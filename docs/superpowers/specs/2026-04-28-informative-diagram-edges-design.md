# Informative Diagram Edges

**Date:** 2026-04-28
**Status:** Approved

## Problem

Diagram edges (arrows) in the graph workspace / live session are visually uniform — gray curved lines with no distinction between relationship types. The backend stores rich edge metadata (30+ types, derivation confidence) that is invisible to the user.

## Solution

A category-based visual encoding system for edges that communicates relationship type through color and derivation confidence through dash pattern, with always-visible short labels.

## Edge Categories

Five default categories, each with a distinct color:

| Category   | Color              | Short Labels                          | Edge Types                                                        |
|------------|--------------------|---------------------------------------|-------------------------------------------------------------------|
| Code       | `#6b9bd2` (blue)   | imp, exp, call, decl, inj             | IMPORTS, EXPORTS, CALLS_SYMBOL, DECLARES, INJECTS                 |
| Service    | `#7db87d` (green)  | calls, handles, exposes               | CALLS_ENDPOINT, CALLS_SERVICE, EXPOSES_ENDPOINT, HANDLES_OPERATION|
| Async      | `#d4915e` (orange) | pub, sub, queue                       | PUBLISHES_TOPIC, CONSUMES_TOPIC, USES_QUEUE                      |
| Data       | `#a87dc2` (purple) | reads, writes, schema                 | READS_DB, WRITES_DB, USES_SCHEMA, RETURNS_TYPE                   |
| Structural | `#b8a898` (gray)   | contains, deploys, owns               | CONTAINS, DEPLOYS_AS, OWNED_BY, PART_OF_FLOW, etc.               |

Unrecognized edge types fall back to gray (current "auto" behavior).

### Dash Pattern (Derivation)

- **Solid line** = hard or linked derivation
- **Dashed line** = inferred or unknown derivation

Two independent visual channels: color encodes *what*, dash encodes *how certain*.

## Label Behavior

1. **Short labels always visible** by default — abbreviated 3-5 char labels rendered at edge midpoint
2. **Global toggle** in the filter sidebar to hide all labels
3. **Node selection overrides** — clicking a node shows **full labels** on all connected edges, regardless of toggle state

## Backend Changes

### Default Category Map

A built-in Go map defining the default categories. This is visual presentation config, not type validation — kept separate from `oracle.types.yaml`.

```go
var DefaultEdgeCategories = map[string]EdgeCategory{
    "code":       {Color: "#6b9bd2", Types: map[string]string{"IMPORTS": "imp", "EXPORTS": "exp", ...}},
    "service":    {Color: "#7db87d", Types: map[string]string{"CALLS_ENDPOINT": "calls", ...}},
    "async":      {Color: "#d4915e", Types: map[string]string{"PUBLISHES_TOPIC": "pub", ...}},
    "data":       {Color: "#a87dc2", Types: map[string]string{"READS_DB": "reads", ...}},
    "structural": {Color: "#b8a898", Types: map[string]string{"CONTAINS": "contains", ...}},
}
```

### API Response

The `/api/graph` response gains a new `edgeCategories` field — the merged result of defaults + project overrides:

```json
{
  "nodes": [...],
  "edges": [...],
  "edgeCategories": {
    "code": {
      "color": "#6b9bd2",
      "types": {
        "IMPORTS": "imp",
        "EXPORTS": "exp",
        "CALLS_SYMBOL": "call",
        "DECLARES": "decl",
        "INJECTS": "inj"
      }
    }
  }
}
```

### Project-Level Overrides

Stored in `.depbot/edge-categories.json`. When a user customizes categories via the settings tab, changes are saved there. The backend merges defaults with project overrides (project wins) before serving.

No new API endpoints — a POST to save overrides, and the existing `/api/graph` response carries the merged categories.

## Frontend Changes

### Edge Rendering (renderDiagram)

- Look up each edge's `edge_type` in the category map received from the API
- Apply category color to `stroke`
- Apply dash pattern based on `derivation` field (solid = hard/linked, dashed = inferred/unknown)
- Render short label as `<text>` element at the midpoint of the SVG path
- New SVG arrow markers per category color: `#dia-arrow-code`, `#dia-arrow-service`, `#dia-arrow-async`, `#dia-arrow-data` (structural keeps existing `#dia-arrow`)

### Label Visibility CSS

```css
.edge-label { display: block; font-size: 9px; }
.hide-edge-labels .edge-label { display: none; }
.edge-line.highlighted + .edge-label { display: block !important; }
```

When a node is selected, connected edges get `.highlighted` which forces full labels visible.

### Legend

Small legend in the bottom corner of the diagram showing category name + color swatch. Clicking a category toggles visibility of all edges in that category (integrates with existing `hiddenEdgeTypes` filtering).

### Settings Tab — Category Customization

In the filter/settings sidebar:

- **"Edge Categories" section** listing all categories with color swatches
- Click a category to edit: change color, reassign edge types between categories, rename short labels
- **"+ Custom Category"** button to create a new one
- Changes POST to backend, saved to `.depbot/edge-categories.json`
- Reset button to restore defaults

## Scope

This applies to the **Diagram tab** (live session) — both strict and free modes. The same visual system should also apply to the **Workspace tab** for consistency, but the workspace can be a follow-up if needed.

Transitive edges retain their existing amber dashed style — they are synthetic and don't have a real edge type.
