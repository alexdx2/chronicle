# Informative Diagram Edges — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make diagram edges visually encode their relationship type (color), derivation confidence (dash pattern), and short text labels — with backend-driven defaults and user-customizable overrides via the settings UI.

**Architecture:** Default edge category map defined as a Go constant in `server.go`, served via the existing `/api/graph` response with a new `edgeCategories` field. Project-level overrides stored via `store.SetSetting("edge_categories", json)`. Frontend reads the merged map and applies color, dash, labels, and per-category arrow markers in `renderDiagram()`. A settings UI section in the diagram sidebar lets users customize categories.

**Tech Stack:** Go (backend), Alpine.js + D3.js + SVG (frontend), SQLite settings table (persistence)

---

### Task 1: Backend — Default Edge Category Map & API

**Files:**
- Modify: `internal/admin/server.go:455-482` (handleGraph)
- Modify: `internal/admin/server.go:70-82` (add helper near Server struct)

- [ ] **Step 1: Add the EdgeCategory type and default map**

Add after the `DiagramNote` struct (after line 68) in `internal/admin/server.go`:

```go
// EdgeCategory defines visual encoding for a group of edge types.
type EdgeCategory struct {
	Color string            `json:"color"`
	Types map[string]string `json:"types"` // edge_type → short label
}

// DefaultEdgeCategories is the built-in category map for edge visual encoding.
var DefaultEdgeCategories = map[string]EdgeCategory{
	"code": {
		Color: "#6b9bd2",
		Types: map[string]string{
			"IMPORTS":      "imp",
			"EXPORTS":      "exp",
			"CALLS_SYMBOL": "call",
			"DECLARES":     "decl",
			"INJECTS":      "inj",
		},
	},
	"service": {
		Color: "#7db87d",
		Types: map[string]string{
			"CALLS_ENDPOINT":    "calls",
			"CALLS_SERVICE":     "calls",
			"EXPOSES_ENDPOINT":  "exposes",
			"HANDLES_OPERATION": "handles",
		},
	},
	"async": {
		Color: "#d4915e",
		Types: map[string]string{
			"PUBLISHES_TOPIC": "pub",
			"CONSUMES_TOPIC":  "sub",
			"USES_QUEUE":      "queue",
		},
	},
	"data": {
		Color: "#a87dc2",
		Types: map[string]string{
			"READS_DB":           "reads",
			"WRITES_DB":          "writes",
			"USES_SCHEMA":        "schema",
			"RETURNS_TYPE":       "returns",
			"REGISTERS_SUBJECT":  "reg",
		},
	},
	"structural": {
		Color: "#b8a898",
		Types: map[string]string{
			"CONTAINS":          "contains",
			"DEPLOYS_AS":        "deploys",
			"OWNED_BY":          "owns",
			"MAINTAINED_BY":     "maintains",
			"PART_OF_FLOW":      "flow",
			"SELECTS_PODS":      "selects",
			"BUILDS_ARTIFACT":   "builds",
			"DEPLOYS_RESOURCE":  "deploys",
			"DEPENDS_ON_DOMAIN": "dep",
			"PRECEDES":          "precedes",
			"EMITS_AFTER":       "emits",
			"REQUIRES":          "requires",
			"TRIGGERS_ANALYSIS": "triggers",
			"READS_OUTPUT":      "reads",
			"ROUTES_TO":         "routes",
			"TARGETS_SERVICE":   "targets",
		},
	},
}
```

- [ ] **Step 2: Add the merge helper**

Add after the default map in `internal/admin/server.go`:

```go
// mergedEdgeCategories returns DefaultEdgeCategories merged with project overrides.
// Project overrides can add new categories, change colors, or reassign edge types.
func (s *Server) mergedEdgeCategories() map[string]EdgeCategory {
	result := make(map[string]EdgeCategory, len(DefaultEdgeCategories))
	for k, v := range DefaultEdgeCategories {
		copyTypes := make(map[string]string, len(v.Types))
		for tk, tv := range v.Types {
			copyTypes[tk] = tv
		}
		result[k] = EdgeCategory{Color: v.Color, Types: copyTypes}
	}

	raw, err := s.getStore().GetSetting("edge_categories")
	if err != nil || raw == "" {
		return result
	}
	var overrides map[string]EdgeCategory
	if err := json.Unmarshal([]byte(raw), &overrides); err != nil {
		return result
	}
	for k, v := range overrides {
		result[k] = v // full category replacement — user controls the whole category
	}
	return result
}
```

- [ ] **Step 3: Update handleGraph to include edgeCategories**

In `internal/admin/server.go`, change line 481 from:

```go
httpJSON(w, map[string]any{"nodes": nodeList, "edges": edgeList})
```

to:

```go
httpJSON(w, map[string]any{"nodes": nodeList, "edges": edgeList, "edgeCategories": s.mergedEdgeCategories()})
```

- [ ] **Step 4: Add the settings endpoint for saving overrides**

Add a new handler in `internal/admin/server.go`:

```go
func (s *Server) handleEdgeCategorySettings(w http.ResponseWriter, r *http.Request) {
	if r.Method == "GET" {
		httpJSON(w, s.mergedEdgeCategories())
		return
	}
	if r.Method == "PUT" || r.Method == "POST" {
		var cats map[string]EdgeCategory
		if err := json.NewDecoder(r.Body).Decode(&cats); err != nil {
			httpError(w, err, 400)
			return
		}
		data, _ := json.Marshal(cats)
		s.getStore().SetSetting("edge_categories", string(data))
		httpJSON(w, s.mergedEdgeCategories())
		return
	}
	if r.Method == "DELETE" {
		s.getStore().SetSetting("edge_categories", "")
		httpJSON(w, s.mergedEdgeCategories())
		return
	}
	http.Error(w, "method not allowed", 405)
}
```

- [ ] **Step 5: Register the route**

In the route registration block (after line 236), add:

```go
mux.HandleFunc("/api/settings/edge-categories", s.handleEdgeCategorySettings)
```

- [ ] **Step 6: Verify it compiles**

Run: `cd /home/alex/personal/depbot && go build ./...`
Expected: no errors

- [ ] **Step 7: Commit**

```bash
git add internal/admin/server.go
git commit -m "feat: add edge category map and API endpoint

Default categories (code/service/async/data/structural) with colors
and short labels. Served via /api/graph edgeCategories field.
Project overrides via /api/settings/edge-categories."
```

---

### Task 2: Frontend — Store Edge Categories & Build Lookup

**Files:**
- Modify: `internal/admin/static/index.html:1140-1170` (Alpine data)
- Modify: `internal/admin/static/index.html:1486-1503` (fetchGraphData)

- [ ] **Step 1: Add state for edge categories and label visibility**

In the Alpine `data()` block (around line 1148), add after the `hiddenEdgeTypes` line:

```js
edgeCategories: {},       // category_name → {color, types: {EDGE_TYPE: shortLabel}}
edgeCategoryLookup: {},   // EDGE_TYPE → {category, color, shortLabel}
showEdgeLabels: true,     // global toggle for short labels
```

- [ ] **Step 2: Add a method to rebuild the lookup from categories**

Add a new method in the methods section (near `getDiagramEdgeTypes` around line 3085):

```js
buildEdgeCategoryLookup() {
  const lookup = {};
  for (const [cat, def] of Object.entries(this.edgeCategories)) {
    for (const [edgeType, shortLabel] of Object.entries(def.types || {})) {
      lookup[edgeType] = { category: cat, color: def.color, shortLabel };
    }
  }
  this.edgeCategoryLookup = lookup;
},
```

- [ ] **Step 3: Update fetchGraphData to consume edgeCategories**

In `fetchGraphData()` (line ~1489), after `this.graphData = await r.json();`, add:

```js
if (this.graphData.edgeCategories) {
  this.edgeCategories = this.graphData.edgeCategories;
  this.buildEdgeCategoryLookup();
}
```

- [ ] **Step 4: Verify the page loads without errors**

Open the admin dashboard in a browser, check the browser console for errors.
Expected: no errors, edge categories loaded into state.

- [ ] **Step 5: Commit**

```bash
git add internal/admin/static/index.html
git commit -m "feat: store edge categories from API and build lookup table"
```

---

### Task 3: Frontend — Colored Arrow Markers

**Files:**
- Modify: `internal/admin/static/index.html:3294-3302` (SVG defs in renderDiagram)
- Modify: `internal/admin/static/index.html:134-164` (CSS)

- [ ] **Step 1: Generate per-category arrow markers in renderDiagram**

Replace the marker defs block (lines 3294-3302) with:

```js
const defs = svg.append('defs');
// Default gray arrow (fallback for unknown types)
defs.append('marker').attr('id', 'dia-arrow')
  .attr('viewBox', '0 0 10 10').attr('refX', 10).attr('refY', 5)
  .attr('markerWidth', 8).attr('markerHeight', 8).attr('orient', 'auto')
  .append('path').attr('d', 'M 0 0 L 10 5 L 0 10 z').attr('class', 'arrow-head');
// Transitive arrow (amber)
defs.append('marker').attr('id', 'dia-arrow-transitive')
  .attr('viewBox', '0 0 10 10').attr('refX', 10).attr('refY', 5)
  .attr('markerWidth', 8).attr('markerHeight', 8).attr('orient', 'auto')
  .append('path').attr('d', 'M 0 0 L 10 5 L 0 10 z').attr('fill', '#b8963e');
// Per-category arrows
const seenColors = new Set();
for (const [cat, def] of Object.entries(this.edgeCategories)) {
  if (seenColors.has(def.color)) continue;
  seenColors.add(def.color);
  defs.append('marker').attr('id', 'dia-arrow-' + cat)
    .attr('viewBox', '0 0 10 10').attr('refX', 10).attr('refY', 5)
    .attr('markerWidth', 8).attr('markerHeight', 8).attr('orient', 'auto')
    .append('path').attr('d', 'M 0 0 L 10 5 L 0 10 z').attr('fill', def.color);
}
```

- [ ] **Step 2: Update edge class and style assignment in the edge rendering loop**

Replace the edge rendering block (lines 3310-3319) with:

```js
const self = this;
const link = gRoot.selectAll('.edge-path')
  .data(simEdges).join('path')
  .attr('class', d => {
    let cls = 'edge-line';
    if (d._transitive) cls += ' transitive';
    else if (d.derivation === 'linked' || d.derivation === 'inferred') cls += ' dashed';
    return cls;
  })
  .attr('fill', 'none')
  .attr('stroke', d => {
    if (d._transitive) return null; // CSS handles transitive color
    const info = self.edgeCategoryLookup[d.edge_type];
    return info ? info.color : null; // null falls back to CSS default
  })
  .attr('marker-end', d => {
    if (d._transitive) return 'url(#dia-arrow-transitive)';
    const info = self.edgeCategoryLookup[d.edge_type];
    return info ? 'url(#dia-arrow-' + info.category + ')' : 'url(#dia-arrow)';
  });
```

- [ ] **Step 3: Verify edges render with category colors**

Open the admin dashboard, navigate to a diagram with typed edges.
Expected: edges appear in category colors (blue, green, orange, purple, gray). Dashed for inferred edges. Transitive edges still amber dashed.

- [ ] **Step 4: Commit**

```bash
git add internal/admin/static/index.html
git commit -m "feat: color-coded edges and per-category arrow markers in diagram"
```

---

### Task 4: Frontend — Edge Short Labels

**Files:**
- Modify: `internal/admin/static/index.html:3322-3340` (after edge paths, before edgePath function)
- Modify: `internal/admin/static/index.html:134-164` (CSS)

- [ ] **Step 1: Add CSS for always-visible short labels**

Add after the existing `.via-label` rule (after line 160):

```css
.graph-svg .edge-short-label { font-size:8px; fill:#b8a898; pointer-events:none; text-anchor:middle; }
.graph-svg .edge-short-label.cat-code { fill:#6b9bd2; }
.graph-svg .edge-short-label.cat-service { fill:#7db87d; }
.graph-svg .edge-short-label.cat-async { fill:#d4915e; }
.graph-svg .edge-short-label.cat-data { fill:#a87dc2; }
.graph-svg.hide-edge-labels .edge-short-label { display:none; }
.graph-svg.has-highlight .edge-short-label.dimmed { display:none; }
.graph-svg.has-highlight .edge-short-label.highlighted { display:block; font-size:10px; font-weight:600; }
```

- [ ] **Step 2: Render short labels on non-transitive edges**

After the `viaLabels` block (after line 3325), add:

```js
const shortLabels = gRoot.selectAll('.edge-short-label')
  .data(simEdges.filter(e => !e._transitive)).join('text')
  .attr('class', d => {
    const info = self.edgeCategoryLookup[d.edge_type];
    let cls = 'edge-short-label';
    if (info) cls += ' cat-' + info.category;
    return cls;
  })
  .text(d => {
    const info = self.edgeCategoryLookup[d.edge_type];
    return info ? info.shortLabel : '';
  });
```

- [ ] **Step 3: Position labels at edge midpoints**

After the existing `viaLabels` positioning line (line ~3336), add positioning for short labels. Add this inside the `edgePath` function area or right after `link.attr('d', edgePath)`:

```js
// Position short labels at midpoint of each edge path
shortLabels.each(function(d) {
  const pathEl = link.filter(e => e === d).node();
  if (pathEl) {
    const len = pathEl.getTotalLength();
    const mid = pathEl.getPointAtLength(len / 2);
    d3.select(this).attr('x', mid.x).attr('y', mid.y - 4);
  }
});
```

- [ ] **Step 4: Apply the hide-edge-labels class based on toggle state**

In the SVG creation (around line 3291), add after `const svg = ...`:

```js
svg.classed('hide-edge-labels', !this.showEdgeLabels);
```

- [ ] **Step 5: Update label positions on drag (free mode)**

In the drag handler (around line 3353), after the existing `link.filter(...)` re-routing, add:

```js
shortLabels.filter(e => e.source.id === d.id || e.target.id === d.id).each(function(e) {
  const pathEl = link.filter(p => p === e).node();
  if (pathEl) {
    const len = pathEl.getTotalLength();
    const mid = pathEl.getPointAtLength(len / 2);
    d3.select(this).attr('x', mid.x).attr('y', mid.y - 4);
  }
});
```

- [ ] **Step 6: Verify labels render and follow edges**

Open the dashboard, check that short labels appear at edge midpoints in category colors. Drag nodes in free mode — labels should follow. Toggle `showEdgeLabels` in console (`$store.showEdgeLabels = false`) to verify hide works.

- [ ] **Step 7: Commit**

```bash
git add internal/admin/static/index.html
git commit -m "feat: always-visible short labels on diagram edges with category colors"
```

---

### Task 5: Frontend — Node Selection Shows Full Labels

**Files:**
- Modify: `internal/admin/static/index.html:3386-3391` (node click in renderDiagram)
- Modify: `internal/admin/static/index.html:1841-1893` (highlightNode)

- [ ] **Step 1: Add full-label display logic to highlightNode**

In the `highlightNode` function (line 1841), after the edge highlight loop (after line 1892), add:

```js
// Update short labels: dim non-connected, highlight+expand connected
svg.selectAll('.edge-short-label').each(function() {
  const el = d3.select(this);
  const d = el.datum?.();
  const fromId = d ? (d.source?.id || d.source || d.from_node_id) : null;
  const toId   = d ? (d.target?.id || d.target || d.to_node_id)   : null;
  const key = fromId + '->' + toId;
  const connected = connectedEdgeKeys.has(key);
  el.classed('dimmed', !connected);
  el.classed('highlighted', connected);
  if (connected && d) {
    // Show full edge type instead of short label
    el.text(d.edge_type ? d.edge_type.toLowerCase().replace(/_/g, ' ') : '');
  }
});
```

- [ ] **Step 2: Restore short labels on unhighlight**

In the clear-highlight path of `highlightNode` (lines 1843-1848), after the existing `.highlighted` clear, add:

```js
svg.selectAll('.edge-short-label').classed('dimmed', false).classed('highlighted', false)
  .each(function() {
    const d = d3.select(this).datum?.();
    const info = d ? (window._edgeCategoryLookup || {})[d.edge_type] : null;
    d3.select(this).text(info ? info.shortLabel : '');
  });
```

- [ ] **Step 3: Expose the lookup globally for highlightNode**

In `buildEdgeCategoryLookup()` (added in Task 2), add at the end:

```js
window._edgeCategoryLookup = this.edgeCategoryLookup;
```

- [ ] **Step 4: Wire node click in renderDiagram to highlightNode**

In `renderDiagram`, the node click handler (around line 3386), update or add:

```js
node.on('click', (event, d) => {
  event.stopPropagation();
  const isAlready = d3.select(event.currentTarget).classed('highlighted');
  this.highlightNode(isAlready ? null : d.id, simEdges);
});
gRoot.on('click', () => this.highlightNode(null, simEdges));
```

- [ ] **Step 5: Verify click behavior**

Click a node in the diagram — connected edges should highlight with full labels ("calls endpoint", "publishes topic"). Click the background to clear. Non-connected edges and labels should dim.

- [ ] **Step 6: Commit**

```bash
git add internal/admin/static/index.html
git commit -m "feat: node click shows full edge labels on connected edges"
```

---

### Task 6: Frontend — Filter Sidebar Toggle & Legend

**Files:**
- Modify: `internal/admin/static/index.html:1039-1081` (diagram sidebar)

- [ ] **Step 1: Add the label toggle to the filter sidebar**

In the diagram sidebar (after the "Edge Types" group closing `</div>` at line 1079), add a new group:

```html
<div class="group">
  <h4>Display</h4>
  <label>
    <input type="checkbox"
           :checked="showEdgeLabels"
           @change="showEdgeLabels=!showEdgeLabels;renderDiagram()">
    <span>Edge labels</span>
  </label>
</div>
```

- [ ] **Step 2: Add a color legend to the filter sidebar**

After the Display group, add:

```html
<div class="group">
  <h4>Edge Legend</h4>
  <template x-for="[cat, def] in Object.entries(edgeCategories)" :key="cat">
    <div style="display:flex;align-items:center;gap:6px;margin-bottom:3px;font-size:11px">
      <span :style="'display:inline-block;width:12px;height:3px;background:'+def.color+';border-radius:1px'"></span>
      <span x-text="cat" style="text-transform:capitalize"></span>
    </div>
  </template>
</div>
```

- [ ] **Step 3: Verify sidebar shows toggle and legend**

Open the diagram sidebar. Expected: "Edge labels" checkbox (checked by default), and a legend showing category names with color swatches. Unchecking the toggle hides all short labels.

- [ ] **Step 4: Commit**

```bash
git add internal/admin/static/index.html
git commit -m "feat: edge label toggle and category legend in diagram sidebar"
```

---

### Task 7: Frontend — Edge Opacity in Step Annotations

**Files:**
- Modify: `internal/admin/static/index.html:3181-3227` (updateDiagramAnnotations)

- [ ] **Step 1: Update edge opacity to preserve category colors**

In `updateDiagramAnnotations` (line 3223), update the edge opacity block to also handle short labels:

```js
svg.selectAll('.edge-line').style('opacity', function(d) {
  if (!hasAny) return 0.6;
  return (highlightedIds.has(d.source.id || d.source) ||
          highlightedIds.has(d.target.id || d.target)) ? 0.8 : 0.1;
});
svg.selectAll('.edge-short-label').style('opacity', function(d) {
  if (!hasAny) return 0.8;
  return (highlightedIds.has(d.source?.id || d.source || d.from_node_id) ||
          highlightedIds.has(d.target?.id || d.target || d.to_node_id)) ? 0.8 : 0.05;
});
```

- [ ] **Step 2: Verify step navigation dims labels correctly**

Navigate through diagram steps. Expected: labels on non-highlighted edges fade out along with the edges. Labels on highlighted edges remain visible.

- [ ] **Step 3: Commit**

```bash
git add internal/admin/static/index.html
git commit -m "feat: edge labels respect step annotation opacity"
```

---

### Task 8: Settings UI — Category Customization

**Files:**
- Modify: `internal/admin/static/index.html` (add settings section in sidebar)

- [ ] **Step 1: Add state for category editing**

In the Alpine `data()` block (near the `edgeCategories` line added in Task 2), add:

```js
editingCategory: null,    // category name being edited, or null
editCategoryData: null,   // temp copy of the category being edited
```

- [ ] **Step 2: Add save/reset methods**

Add near `buildEdgeCategoryLookup`:

```js
async saveEdgeCategories() {
  await fetch('/api/settings/edge-categories', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(this.edgeCategories),
  });
  this.buildEdgeCategoryLookup();
  this.renderDiagram();
},
async resetEdgeCategories() {
  await fetch('/api/settings/edge-categories', { method: 'DELETE' });
  const r = await fetch('/api/settings/edge-categories');
  this.edgeCategories = await r.json();
  this.buildEdgeCategoryLookup();
  this.renderDiagram();
},
startEditCategory(cat) {
  this.editingCategory = cat;
  this.editCategoryData = JSON.parse(JSON.stringify(this.edgeCategories[cat]));
},
saveEditCategory() {
  if (this.editingCategory && this.editCategoryData) {
    this.edgeCategories[this.editingCategory] = this.editCategoryData;
    this.saveEdgeCategories();
  }
  this.editingCategory = null;
  this.editCategoryData = null;
},
cancelEditCategory() {
  this.editingCategory = null;
  this.editCategoryData = null;
},
addCustomCategory() {
  const name = prompt('Category name:');
  if (!name || this.edgeCategories[name]) return;
  this.edgeCategories[name] = { color: '#888888', types: {} };
  this.saveEdgeCategories();
},
```

- [ ] **Step 3: Add the category editor UI to the sidebar**

In the sidebar, replace the "Edge Legend" group (from Task 6 Step 2) with an interactive version:

```html
<div class="group">
  <div style="display:flex;justify-content:space-between;align-items:center">
    <h4>Edge Categories</h4>
    <div style="display:flex;gap:4px">
      <button @click="addCustomCategory()" style="border:none;background:none;cursor:pointer;color:var(--primary);font-size:10px">+ Add</button>
      <button @click="resetEdgeCategories()" style="border:none;background:none;cursor:pointer;color:var(--text-muted);font-size:10px">Reset</button>
    </div>
  </div>

  <template x-for="[cat, def] in Object.entries(edgeCategories)" :key="cat">
    <div>
      <!-- Category header row -->
      <div @click="editingCategory === cat ? cancelEditCategory() : startEditCategory(cat)"
           style="display:flex;align-items:center;gap:6px;margin-bottom:2px;font-size:11px;cursor:pointer;padding:2px 0">
        <input type="color" :value="def.color"
               @click.stop
               @change="edgeCategories[cat].color = $event.target.value; saveEdgeCategories()"
               style="width:16px;height:16px;border:none;padding:0;cursor:pointer;background:none">
        <span x-text="cat" style="text-transform:capitalize;flex:1"></span>
        <span style="font-size:9px;color:var(--text-muted)" x-text="Object.keys(def.types||{}).length + ' types'"></span>
      </div>

      <!-- Expanded editor -->
      <template x-if="editingCategory === cat">
        <div style="margin-left:22px;margin-bottom:6px;font-size:10px">
          <template x-for="[etype, label] in Object.entries(editCategoryData.types || {})" :key="etype">
            <div style="display:flex;align-items:center;gap:4px;margin-bottom:2px">
              <span style="color:var(--text-muted);flex:1" x-text="etype"></span>
              <input type="text" :value="label"
                     @change="editCategoryData.types[etype] = $event.target.value"
                     style="width:40px;font-size:10px;padding:1px 3px;border:1px solid var(--border);border-radius:2px;background:var(--bg);color:var(--text)">
              <button @click="delete editCategoryData.types[etype]"
                      style="border:none;background:none;cursor:pointer;color:var(--text-muted);font-size:10px">&times;</button>
            </div>
          </template>
          <div style="display:flex;gap:4px;margin-top:4px">
            <button @click="saveEditCategory()" style="font-size:10px;padding:2px 6px;border:1px solid var(--primary);background:var(--primary);color:white;border-radius:3px;cursor:pointer">Save</button>
            <button @click="cancelEditCategory()" style="font-size:10px;padding:2px 6px;border:1px solid var(--border);background:none;color:var(--text-muted);border-radius:3px;cursor:pointer">Cancel</button>
          </div>
        </div>
      </template>
    </div>
  </template>
</div>
```

- [ ] **Step 4: Verify the category editor works**

Open the sidebar, click a category to expand it. Change a short label, save. Change a color via the color picker. Add a custom category. Reset to defaults. Each action should update the diagram immediately.

- [ ] **Step 5: Commit**

```bash
git add internal/admin/static/index.html
git commit -m "feat: edge category customization UI in diagram sidebar"
```

---

### Task 9: Integration — Workspace Tab Consistency

**Files:**
- Modify: `internal/admin/static/index.html` (workspace edge rendering, around lines 2629-2740)

- [ ] **Step 1: Apply the same color logic to workspace edges**

In `renderWorkspaceGraph`, find the edge rendering (the `.edge-line` creation). Apply the same color/dash/label logic used in `renderDiagram`:

```js
// In workspace edge rendering, update stroke color:
.attr('stroke', d => {
  if (d._isPath) return null; // path edges keep amber
  if (d._transitive) return null; // transitive keep amber
  const info = this.edgeCategoryLookup[d.edge_type];
  return info ? info.color : null;
})
```

And update marker-end to use per-category arrows (same pattern as Task 3 Step 2).

- [ ] **Step 2: Add per-category marker defs to workspace SVG**

Copy the same marker generation loop from Task 3 Step 1 into the workspace SVG defs section (around line 2629-2633).

- [ ] **Step 3: Verify workspace edges use category colors**

Navigate to the Workspace tab. Expected: edges show category colors, same as diagram tab.

- [ ] **Step 4: Commit**

```bash
git add internal/admin/static/index.html
git commit -m "feat: apply edge category colors to workspace graph"
```

---

### Task 10: Final Verification

- [ ] **Step 1: Full smoke test**

Test all these scenarios:
1. Open diagram — edges show category colors and short labels
2. Toggle to free mode — drag nodes, labels follow edges
3. Click a node — connected edges highlight with full labels
4. Click background — highlights clear, short labels restore
5. Navigate steps — edge labels dim/show with step annotations
6. Open sidebar — legend shows categories, label toggle works
7. Edit a category color — diagram updates immediately
8. Edit a short label — diagram updates on save
9. Reset to defaults — everything reverts
10. Workspace tab — edges use same category colors

- [ ] **Step 2: Commit any final fixes**

```bash
git add internal/admin/static/index.html internal/admin/server.go
git commit -m "fix: final adjustments for informative diagram edges"
```
