# Graph Dashboard: Architecture Mode, Workspace & Transitive Filtering — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace Force mode with Architecture (domain-clustered force layout) and Workspace (drag-and-drop investigation) modes, plus transitive edge collapsing filter sidebar.

**Architecture:** All changes are frontend-only in `internal/admin/static/index.html` (single-file Alpine.js + D3.js dashboard). No backend changes. The existing `/api/graph` endpoint returns all nodes/edges with `domain_key`, `node_type`, `layer`, `confidence` fields needed for grouping and filtering. Three new JS functions (`computeTransitiveEdges`, `renderArchitectureGraph`, `renderWorkspaceGraph`) replace the existing `renderForceGraph`.

**Key design decisions:**
- Transitive edge collapsing only applies to **collapsible types** (`endpoint`, `function`, `topic`, `schema`) — hiding services, databases, or teams removes them without creating false transitive connections.
- Alpine.js `Set` is not reactive — after mutation, we reassign `this.hiddenNodeTypes = new Set(this.hiddenNodeTypes)` to trigger reactivity.
- Workspace mode must use filtered data (not raw `graphData`) for neighbors, BFS, and edge computation, so sidebar filters actually apply.

**Tech Stack:** Alpine.js (reactive state), D3.js v7 (force simulation, SVG rendering), vanilla JS, HTML5 drag-and-drop.

---

## File Structure

All changes in a single file:

- **Modify:** `internal/admin/static/index.html`
  - **CSS (lines ~117-134):** Add styles for domain boxes, transitive edges, filter sidebar, workspace palette, expand badges
  - **HTML toolbar (lines ~462-512):** Replace Force button with Architecture + Workspace, add ☰ Filters toggle, add workspace palette panel
  - **HTML filter sidebar (new):** Collapsible right panel with per-type checkboxes, domain toggles, edge type toggles, confidence slider
  - **HTML workspace palette (new):** Left panel with searchable entity list
  - **JS data (lines ~791-806):** Add new state variables for filter sidebar, workspace canvas state
  - **JS `getFilteredData()` (lines ~1147-1162):** Add transitive edge computation
  - **JS `renderGraph()` (lines ~1244-1260):** Add architecture + workspace mode dispatch
  - **JS `renderForceGraph()` (lines ~1761-1851):** Replace entirely with `renderArchitectureGraph()` and `renderWorkspaceGraph()`

---

### Task 1: Add Filter Sidebar State & Transitive Edge Computation

**Files:**
- Modify: `internal/admin/static/index.html:791-806` (JS data state)
- Modify: `internal/admin/static/index.html:1147-1162` (getFilteredData)

This task adds the core data model and the transitive collapsing algorithm. Everything else builds on this.

- [ ] **Step 1: Add new state variables after line 806**

Add these properties to the Alpine.js data object, after `hideStructural: false,`:

```javascript
    // Filter sidebar state (use arrays, not Sets — Alpine can't observe Set mutations)
    filterSidebarOpen: false,
    hiddenNodeTypes: [],      // string[] of node_type values
    hiddenEdgeTypes: [],      // string[] of edge_type values
    hiddenDomains: [],        // string[] of domain_key values
    minConfidence: 0,
    // Only these types produce transitive edges when hidden; others just disappear
    collapsibleTypes: ['endpoint', 'function', 'topic', 'schema'],
    // Workspace state
    workspaceNodes: [],       // [{node_id, x, y, manual: bool}]
    workspacePaths: [],       // [{from, to, path: [node_ids]}]
    workspaceSearch: '',
```

- [ ] **Step 2: Add `computeTransitiveEdges()` method**

Add this method after the existing `getFilteredData()` method (after line 1162):

```javascript
    // Compute transitive edges when collapsible node types are hidden.
    // Only types in this.collapsibleTypes produce transitive edges.
    // Non-collapsible hidden types (service, database, team, etc.) just disappear — no false bridges.
    computeTransitiveEdges(visibleNodes, allNodes, allEdges) {
      const visibleIds = new Set(visibleNodes.map(n => n.node_id));
      const nodeById = {};
      allNodes.forEach(n => { nodeById[n.node_id] = n; });

      // Split hidden nodes into collapsible (produce transitive edges) vs opaque (just gone)
      const collapsibleSet = new Set(this.collapsibleTypes);
      const collapsibleIds = new Set();
      allNodes.forEach(n => {
        if (!visibleIds.has(n.node_id) && collapsibleSet.has(n.node_type)) {
          collapsibleIds.add(n.node_id);
        }
      });

      if (collapsibleIds.size === 0) {
        return { nodes: visibleNodes, edges: allEdges.filter(e => visibleIds.has(e.from_node_id) && visibleIds.has(e.to_node_id)) };
      }

      // Direct edges between visible nodes
      const directEdges = allEdges.filter(e => visibleIds.has(e.from_node_id) && visibleIds.has(e.to_node_id));
      const directEdgeKeys = new Set(directEdges.map(e => e.from_node_id + '->' + e.to_node_id));

      // Build adjacency only through collapsible hidden nodes (not opaque ones)
      const incomingToCollapsible = {};  // collapsible_id -> [{from_id, edge}]
      const outgoingFromCollapsible = {}; // collapsible_id -> [{to_id, edge}]
      allEdges.forEach(e => {
        const fromCollapsible = collapsibleIds.has(e.from_node_id);
        const toCollapsible = collapsibleIds.has(e.to_node_id);
        const fromVisible = visibleIds.has(e.from_node_id);
        const toVisible = visibleIds.has(e.to_node_id);

        if (toCollapsible && fromVisible) {
          if (!incomingToCollapsible[e.to_node_id]) incomingToCollapsible[e.to_node_id] = [];
          incomingToCollapsible[e.to_node_id].push({ from_id: e.from_node_id, edge: e });
        }
        if (fromCollapsible && toVisible) {
          if (!outgoingFromCollapsible[e.from_node_id]) outgoingFromCollapsible[e.from_node_id] = [];
          outgoingFromCollapsible[e.from_node_id].push({ to_id: e.to_node_id, edge: e });
        }
        // Collapsible-to-collapsible edges (for chaining, e.g. endpoint → schema)
        if (fromCollapsible && toCollapsible) {
          if (!outgoingFromCollapsible[e.from_node_id]) outgoingFromCollapsible[e.from_node_id] = [];
          outgoingFromCollapsible[e.from_node_id].push({ to_id: e.to_node_id, edge: e });
          if (!incomingToCollapsible[e.to_node_id]) incomingToCollapsible[e.to_node_id] = [];
          incomingToCollapsible[e.to_node_id].push({ from_id: e.from_node_id, edge: e });
        }
      });

      // BFS from each collapsible node to find paths: visible -> collapsible+ -> visible
      const transitiveMap = {}; // "fromId->toId" -> {viaNames: [], confidence: number}
      collapsibleIds.forEach(hId => {
        const incoming = incomingToCollapsible[hId] || [];
        const queue = [{ nodeId: hId, viaNames: [nodeById[hId]?.name || '?'], minConf: 1.0 }];
        const visited = new Set([hId]);
        const reachableVisible = [];

        while (queue.length > 0) {
          const cur = queue.shift();
          const outgoing = outgoingFromCollapsible[cur.nodeId] || [];
          for (const { to_id, edge } of outgoing) {
            const conf = Math.min(cur.minConf, edge.confidence || 1);
            if (visibleIds.has(to_id)) {
              reachableVisible.push({ to_id, viaNames: cur.viaNames, confidence: conf });
            } else if (collapsibleIds.has(to_id) && !visited.has(to_id)) {
              visited.add(to_id);
              queue.push({ nodeId: to_id, viaNames: [...cur.viaNames, nodeById[to_id]?.name || '?'], minConf: conf });
            }
            // If to_id is opaque-hidden (not collapsible, not visible), stop — no bridging through it
          }
        }

        for (const inc of incoming) {
          for (const reach of reachableVisible) {
            if (inc.from_id === reach.to_id) continue;
            const key = inc.from_id + '->' + reach.to_id;
            if (directEdgeKeys.has(key)) continue;
            const conf = Math.min(inc.edge.confidence || 1, reach.confidence);
            if (!transitiveMap[key] || conf > transitiveMap[key].confidence) {
              transitiveMap[key] = { viaNames: reach.viaNames, confidence: conf };
            }
          }
        }
      });

      const transitiveEdges = Object.entries(transitiveMap).map(([key, val]) => {
        const [fromId, toId] = key.split('->');
        return {
          from_node_id: fromId,
          to_node_id: toId,
          edge_type: 'TRANSITIVE',
          derivation: 'transitive',
          confidence: val.confidence,
          _transitive: true,
          _viaLabel: 'via ' + val.viaNames.join(', ')
        };
      });

      return { nodes: visibleNodes, edges: [...directEdges, ...transitiveEdges] };
    },
```

- [ ] **Step 3: Update `getFilteredData()` to use transitive computation**

Replace the existing `getFilteredData()` method (lines 1147-1162) with:

```javascript
    getFilteredData() {
      const structuralTypes = ['CONTAINS','DEPLOYS_AS','SELECTS_PODS','OWNED_BY','MAINTAINED_BY','PART_OF_FLOW','BUILDS_ARTIFACT','DEPLOYS_RESOURCE','HAS_FIELD'];
      const allNodes = this.graphData.nodes || [];
      const allEdges = this.graphData.edges || [];
      const hiddenTypesSet = new Set(this.hiddenNodeTypes);
      const hiddenDomainsSet = new Set(this.hiddenDomains);
      const hiddenEdgesSet = new Set(this.hiddenEdgeTypes);

      // Layer filter
      let nodes = allNodes.filter(n => this.layerFilters.includes(n.layer));
      // Repo filter
      if (this.graphRepoFilter) {
        nodes = nodes.filter(n => !n.repo_name || n.repo_name === this.graphRepoFilter);
      }
      // Node type filter (from sidebar)
      if (hiddenTypesSet.size > 0) {
        nodes = nodes.filter(n => !hiddenTypesSet.has(n.node_type));
      }
      // Domain filter
      if (hiddenDomainsSet.size > 0) {
        nodes = nodes.filter(n => !hiddenDomainsSet.has(n.domain_key || '_unassigned'));
      }
      // Confidence filter
      if (this.minConfidence > 0) {
        nodes = nodes.filter(n => (n.confidence || 1) >= this.minConfidence);
      }

      // Edge type filters
      let filteredEdges = allEdges;
      if (this.hideStructural) {
        filteredEdges = filteredEdges.filter(e => !structuralTypes.includes(e.edge_type));
      }
      if (hiddenEdgesSet.size > 0) {
        filteredEdges = filteredEdges.filter(e => !hiddenEdgesSet.has(e.edge_type));
      }

      // Check if any hidden types are collapsible (should produce transitive edges)
      const hasCollapsibleHidden = this.hiddenNodeTypes.some(t => this.collapsibleTypes.includes(t));
      if (hasCollapsibleHidden) {
        // Pass all layer-filtered nodes (before hiding) so collapsible nodes are available for bridging
        const allVisibleByLayer = allNodes.filter(n => this.layerFilters.includes(n.layer));
        return this.computeTransitiveEdges(nodes, allVisibleByLayer, filteredEdges);
      }

      // No collapsible types hidden — standard filtering (hidden non-collapsible types just vanish)
      const nodeIds = new Set(nodes.map(n => n.node_id));
      const edges = filteredEdges.filter(e => nodeIds.has(e.from_node_id) && nodeIds.has(e.to_node_id));
      return { nodes, edges };
    },
```

- [ ] **Step 4: Verify existing modes still work**

Open the dashboard, switch to Tree and Explore modes. Verify they render correctly with the updated `getFilteredData()`. The `hiddenNodeTypes`, `hiddenDomains` sets are empty by default, so behavior should be unchanged.

- [ ] **Step 5: Commit**

```bash
git add internal/admin/static/index.html
git commit -m "feat: add transitive edge collapsing engine and filter state variables"
```

---

### Task 2: Add Filter Sidebar HTML & CSS

**Files:**
- Modify: `internal/admin/static/index.html:117-134` (CSS)
- Modify: `internal/admin/static/index.html:514-518` (HTML layout area)

- [ ] **Step 1: Add CSS for filter sidebar, transitive edges, domain boxes, workspace**

Add these styles after the existing `.graph-svg .layer-label` rule (after line 134):

```css
/* Filter sidebar */
.filter-sidebar{width:200px;background:var(--card);border-left:1px solid var(--border);overflow-y:auto;flex-shrink:0;font-size:11px;padding:10px}
.filter-sidebar h4{font-size:10px;text-transform:uppercase;letter-spacing:1px;color:var(--text-muted);margin:10px 0 4px}
.filter-sidebar label{display:flex;align-items:center;gap:5px;padding:2px 0;cursor:pointer;color:var(--text)}
.filter-sidebar label input[type=checkbox]{margin:0}
.filter-sidebar .group{margin-bottom:8px;border-bottom:1px solid var(--border);padding-bottom:8px}
/* Transitive edges */
.graph-svg .edge-line.transitive{stroke:#f59e0b;stroke-dasharray:6,3;stroke-width:1.5}
.graph-svg .via-label{font-size:8px;fill:#f59e0b;font-weight:500}
.graph-svg .via-pill{fill:#fef3c7;stroke:#f59e0b;stroke-width:0.5;rx:4}
/* Domain boxes (Architecture mode) */
.graph-svg .domain-box{fill:none;stroke:#3b82f6;stroke-width:2;rx:10;opacity:.7}
.graph-svg .domain-label{font-size:11px;fill:#3b82f6;font-weight:700;text-transform:uppercase;letter-spacing:1px}
/* Workspace palette */
.ws-palette{width:220px;background:var(--card);border-right:1px solid var(--border);display:flex;flex-direction:column;flex-shrink:0}
.ws-palette input[type=text]{width:100%;box-sizing:border-box;padding:6px 10px;border:1px solid var(--border);border-radius:4px;font-size:11px;margin:8px;width:calc(100% - 16px)}
.ws-palette .ws-item{display:flex;align-items:center;gap:6px;padding:5px 8px;margin:2px 8px;border:1px solid var(--border);border-radius:4px;cursor:grab;font-size:11px}
.ws-palette .ws-item:hover{background:var(--primary-light)}
.ws-palette .ws-group-label{font-size:9px;text-transform:uppercase;letter-spacing:1px;color:var(--text-muted);padding:6px 8px 2px}
/* Workspace canvas indicators */
.graph-svg .manual-dot{fill:#3b82f6}
.graph-svg .auto-badge{fill:#f59e0b;rx:3}
.graph-svg .auto-badge-text{fill:#fff;font-size:7px;font-weight:700}
.graph-svg .expand-badge{fill:var(--card);stroke:var(--border);stroke-width:1;cursor:pointer}
.graph-svg .expand-badge:hover{fill:var(--primary-light);stroke:var(--primary)}
.graph-svg .expand-text{font-size:11px;fill:var(--text);cursor:pointer;font-weight:600}
.graph-svg .path-edge{stroke:#f59e0b;stroke-width:2.5}
.graph-svg .neighbor-node{opacity:.55}
```

- [ ] **Step 2: Add filter sidebar HTML**

Replace the main area section (lines 514-518) with this expanded layout:

```html
    <!-- Main area: palette (workspace) + canvas + side panel + filter sidebar -->
    <div style="display:flex;flex:1;overflow:hidden">

      <!-- Workspace search palette (only in workspace mode) -->
      <template x-if="graphMode==='workspace'">
        <div class="ws-palette">
          <input type="text" placeholder="Search entities..." x-model="workspaceSearch">
          <div style="flex:1;overflow-y:auto">
            <template x-for="group in getWorkspacePaletteGroups()" :key="group.type">
              <div>
                <div class="ws-group-label" x-text="group.type + ' (' + group.nodes.length + ')'"></div>
                <template x-for="n in group.nodes" :key="n.node_id">
                  <div class="ws-item"
                       :style="'border-left:3px solid '+layerColor(n.layer)"
                       draggable="true"
                       @dragstart="$event.dataTransfer.setData('text/plain', n.node_id); $event.dataTransfer.effectAllowed='copy'"
                       :class="{'opacity-50': workspaceNodes.some(w => w.node_id === n.node_id)}">
                    <span style="color:#999;font-size:9px">⠿</span>
                    <span x-text="n.name" :style="'color:'+layerColor(n.layer)"></span>
                    <span style="font-size:9px;color:var(--text-muted)" x-text="n.node_type"></span>
                  </div>
                </template>
              </div>
            </template>
          </div>
        </div>
      </template>

      <div id="graph-canvas" style="flex:1;overflow:auto"
           @dragover.prevent="$event.dataTransfer.dropEffect='copy'"
           @drop.prevent="handleWorkspaceDrop($event)">
        <template x-if="loading.graph"><div class="loader" style="min-height:200px"><div class="spinner"></div> Loading graph data...</div></template>
      </div>
      <!-- Side panel for selected node (Tree + Explore + Architecture mode) -->
```

- [ ] **Step 3: Add filter sidebar HTML after the side panel closing tag**

Find the closing `</div>` of the side panel (the `</template>` + `</div>` pair for `x-show="selectedNode && ..."`) and add the filter sidebar right after it, still inside the flex container:

```html
      <!-- Filter sidebar (Architecture + Workspace modes) -->
      <template x-if="filterSidebarOpen && (graphMode==='architecture' || graphMode==='workspace')">
        <div class="filter-sidebar">
          <div style="display:flex;justify-content:space-between;align-items:center;margin-bottom:8px">
            <strong>Filters</strong>
            <button @click="filterSidebarOpen=false" style="border:none;background:none;cursor:pointer;color:var(--text-muted);font-size:14px">&times;</button>
          </div>

          <!-- Node types by layer -->
          <div class="group">
            <h4>Node Types</h4>
            <template x-for="layer in allLayers" :key="layer">
              <div>
                <div style="font-size:9px;color:var(--text-muted);margin-top:6px;text-transform:uppercase" x-text="layer"></div>
                <template x-for="nt in getNodeTypesForLayer(layer)" :key="nt">
                  <label>
                    <input type="checkbox"
                           :checked="!hiddenNodeTypes.includes(nt)"
                           @change="toggleNodeType(nt)">
                    <span :style="'color:'+layerColor(layer)" x-text="nt"></span>
                  </label>
                </template>
              </div>
            </template>
          </div>

          <!-- Domains -->
          <div class="group">
            <h4>Domains</h4>
            <template x-for="d in getAllDomains()" :key="d">
              <label>
                <input type="checkbox"
                       :checked="!hiddenDomains.includes(d)"
                       @change="toggleDomain(d)">
                <span x-text="d === '_unassigned' ? 'Unassigned' : d"></span>
              </label>
            </template>
          </div>

          <!-- Edge types -->
          <div class="group">
            <h4>Edge Types</h4>
            <template x-for="et in getAllEdgeTypes()" :key="et">
              <label>
                <input type="checkbox"
                       :checked="!hiddenEdgeTypes.includes(et)"
                       @change="toggleEdgeType(et)">
                <span x-text="et"></span>
              </label>
            </template>
          </div>

          <!-- Confidence slider -->
          <div class="group">
            <h4>Min Confidence</h4>
            <div style="display:flex;align-items:center;gap:6px">
              <input type="range" min="0" max="100" step="5"
                     :value="minConfidence * 100"
                     @input="minConfidence = $event.target.value / 100; renderGraph()"
                     style="flex:1">
              <span style="font-size:10px;min-width:28px" x-text="minConfidence.toFixed(2)"></span>
            </div>
          </div>
        </div>
      </template>

    </div><!-- end flex container -->
```

- [ ] **Step 4: Update the side panel visibility to include architecture mode**

Find the side panel `x-show` condition (line ~520):

```html
<div x-show="selectedNode && (graphMode==='tree' || graphMode==='explore')" x-cloak
```

Replace with:

```html
<div x-show="selectedNode && (graphMode==='tree' || graphMode==='explore' || graphMode==='architecture')" x-cloak
```

- [ ] **Step 5: Commit**

```bash
git add internal/admin/static/index.html
git commit -m "feat: add filter sidebar HTML/CSS and workspace palette layout"
```

---

### Task 3: Update Toolbar & Add Filter Sidebar Helper Methods

**Files:**
- Modify: `internal/admin/static/index.html:464-511` (toolbar HTML)
- Modify: `internal/admin/static/index.html` (JS methods section)

- [ ] **Step 1: Replace toolbar mode buttons**

Replace lines 464-467 (the mode toggle div) with:

```html
        <button class="mode-btn" :class="{active:graphMode==='tree'}" @click="graphMode='tree';renderGraph()">Tree</button>
        <button class="mode-btn" :class="{active:graphMode==='explore'}" @click="graphMode='explore';renderGraph()">Explore</button>
        <button class="mode-btn" :class="{active:graphMode==='architecture'}" @click="graphMode='architecture';renderGraph()">Architecture</button>
        <button class="mode-btn" :class="{active:graphMode==='workspace'}" @click="graphMode='workspace';workspaceNodes=[];workspacePaths=[];renderGraph()">Workspace</button>
```

- [ ] **Step 2: Add ☰ Filters button and Workspace Clear button in right-side controls**

Find the right-side controls div (line ~506). Replace the contents with:

```html
      <div style="display:flex;align-items:center;gap:8px;margin-left:auto">
        <template x-if="graphMode==='workspace'">
          <button class="mode-btn" @click="workspaceNodes=[];workspacePaths=[];renderGraph()" style="color:var(--warning);border-color:var(--warning)">Clear Canvas</button>
        </template>
        <label style="font-size:11px;display:flex;align-items:center;gap:4px;cursor:pointer">
          <input type="checkbox" x-model="hideStructural" @change="renderGraph()"> Hide structural
        </label>
        <template x-if="graphMode==='architecture' || graphMode==='workspace'">
          <button class="mode-btn" @click="filterSidebarOpen=!filterSidebarOpen" :class="{active:filterSidebarOpen}">☰ Filters</button>
        </template>
        <button class="mode-btn" @click="graphFullscreen=!graphFullscreen;$nextTick(()=>renderGraph())" x-text="graphFullscreen ? '⊟' : '⊞'" style="font-size:14px;padding:2px 6px"></button>
      </div>
```

- [ ] **Step 3: Add filter sidebar helper methods**

Add these methods after `getNodeEdges()`:

```javascript
    getNodeTypesForLayer(layer) {
      const types = new Set();
      (this.graphData.nodes || []).forEach(n => {
        if (n.layer === layer) types.add(n.node_type);
      });
      return [...types].sort();
    },

    getAllDomains() {
      const domains = new Set();
      (this.graphData.nodes || []).forEach(n => {
        domains.add(n.domain_key || '_unassigned');
      });
      return [...domains].sort();
    },

    getAllEdgeTypes() {
      const types = new Set();
      (this.graphData.edges || []).forEach(e => types.add(e.edge_type));
      return [...types].sort();
    },

    toggleNodeType(nt) {
      const idx = this.hiddenNodeTypes.indexOf(nt);
      if (idx >= 0) this.hiddenNodeTypes.splice(idx, 1);
      else this.hiddenNodeTypes.push(nt);
      this.renderGraph();
    },

    toggleDomain(d) {
      const idx = this.hiddenDomains.indexOf(d);
      if (idx >= 0) this.hiddenDomains.splice(idx, 1);
      else this.hiddenDomains.push(d);
      this.renderGraph();
    },

    toggleEdgeType(et) {
      const idx = this.hiddenEdgeTypes.indexOf(et);
      if (idx >= 0) this.hiddenEdgeTypes.splice(idx, 1);
      else this.hiddenEdgeTypes.push(et);
      this.renderGraph();
    },

    getWorkspacePaletteGroups() {
      const search = (this.workspaceSearch || '').toLowerCase();
      const nodes = (this.graphData.nodes || []).filter(n => {
        if (search && !n.name.toLowerCase().includes(search) && !(n.node_type || '').toLowerCase().includes(search)) return false;
        return true;
      });
      const groups = {};
      nodes.forEach(n => {
        const t = n.node_type || 'unknown';
        if (!groups[t]) groups[t] = { type: t, nodes: [] };
        groups[t].nodes.push(n);
      });
      return Object.values(groups).sort((a, b) => a.type.localeCompare(b.type));
    },
```

- [ ] **Step 4: Commit**

```bash
git add internal/admin/static/index.html
git commit -m "feat: toolbar with Architecture/Workspace modes, filter sidebar helpers"
```

---

### Task 4: Architecture Mode — Render Function

**Files:**
- Modify: `internal/admin/static/index.html:1244-1260` (renderGraph dispatch)
- Modify: `internal/admin/static/index.html:1761-1851` (replace renderForceGraph)

- [ ] **Step 1: Update `renderGraph()` dispatch**

Replace the `renderGraph()` method (lines 1244-1260) with:

```javascript
    renderGraph() {
      const container = document.getElementById('graph-canvas');
      if (!container) return;
      // Don't clear workspace canvas if just re-filtering
      if (this.graphMode !== 'workspace') container.innerHTML = '';
      const data = this.getFilteredData();
      if (this.graphMode === 'workspace') {
        this.renderWorkspaceGraph(container, data.nodes, data.edges);
        return;
      }
      if (data.nodes.length === 0) {
        container.innerHTML = '<div style="padding:60px;text-align:center;color:var(--text-muted)">No nodes to display. Adjust filters or import data.</div>';
        return;
      }
      if (this.graphMode === 'tree') {
        this.renderLayeredGraph(container, data.nodes, data.edges);
      } else if (this.graphMode === 'explore') {
        this.renderExploreGraph(container, data.nodes, data.edges);
      } else if (this.graphMode === 'architecture') {
        this.renderArchitectureGraph(container, data.nodes, data.edges);
      }
    },
```

- [ ] **Step 2: Replace `renderForceGraph()` with `renderArchitectureGraph()`**

Replace the entire `renderForceGraph()` method (lines 1761-1851) with:

```javascript
    renderArchitectureGraph(container, nodes, edges) {
      const self = this;
      const width = container.clientWidth || 900;
      const height = Math.max(600, container.clientHeight || 600);

      const svg = d3.select(container).append('svg')
        .attr('class', 'graph-svg')
        .attr('width', width)
        .attr('height', height);

      // Arrowhead markers
      const defs = svg.append('defs');
      defs.append('marker').attr('id', 'arch-arrow').attr('viewBox', '0 0 10 10')
        .attr('refX', 20).attr('refY', 5).attr('markerWidth', 6).attr('markerHeight', 6)
        .attr('orient', 'auto-start-reverse')
        .append('path').attr('d', 'M 0 0 L 10 5 L 0 10 z').attr('class', 'arrow-head');
      defs.append('marker').attr('id', 'arch-arrow-transitive').attr('viewBox', '0 0 10 10')
        .attr('refX', 20).attr('refY', 5).attr('markerWidth', 6).attr('markerHeight', 6)
        .attr('orient', 'auto-start-reverse')
        .append('path').attr('d', 'M 0 0 L 10 5 L 0 10 z').attr('fill', '#f59e0b');

      const g = svg.append('g');

      // Zoom + pan
      svg.call(d3.zoom().scaleExtent([0.1, 4]).on('zoom', (event) => {
        g.attr('transform', event.transform);
      }));

      // Group nodes by domain
      const domainGroups = {};
      nodes.forEach(n => {
        const dk = n.domain_key || '_unassigned';
        if (!domainGroups[dk]) domainGroups[dk] = [];
        domainGroups[dk].push(n);
      });
      const domainKeys = Object.keys(domainGroups).sort();

      // Assign domain center positions (grid layout)
      const domainCenters = {};
      const cols = Math.ceil(Math.sqrt(domainKeys.length));
      const cellW = width / (cols + 0.5);
      const cellH = 300;
      domainKeys.forEach((dk, i) => {
        const col = i % cols;
        const row = Math.floor(i / cols);
        domainCenters[dk] = { x: cellW * (col + 0.75), y: cellH * (row + 0.75) };
      });

      // Create simulation nodes with domain grouping
      const nodeMap = {};
      const simNodes = nodes.map(n => {
        const dk = n.domain_key || '_unassigned';
        const center = domainCenters[dk];
        const sn = { ...n, id: n.node_id, _domain: dk, x: center.x + (Math.random() - 0.5) * 80, y: center.y + (Math.random() - 0.5) * 80 };
        nodeMap[n.node_id] = sn;
        return sn;
      });
      const simEdges = edges.filter(e => nodeMap[e.from_node_id] && nodeMap[e.to_node_id])
        .map(e => ({ source: e.from_node_id, target: e.to_node_id, ...e }));

      // Force simulation with domain clustering
      const sim = d3.forceSimulation(simNodes)
        .force('link', d3.forceLink(simEdges).id(d => d.id).distance(80).strength(0.3))
        .force('charge', d3.forceManyBody().strength(-150))
        .force('collision', d3.forceCollide().radius(25))
        // Pull nodes toward their domain center
        .force('x', d3.forceX(d => domainCenters[d._domain].x).strength(0.4))
        .force('y', d3.forceY(d => domainCenters[d._domain].y).strength(0.4));

      // Domain bounding box group (rendered behind everything)
      const domainBoxGroup = g.append('g').attr('class', 'domain-boxes');
      // Edge group
      const edgeGroup = g.append('g');
      // Node group
      const nodeGroup = g.append('g');

      // Draw edges
      const link = edgeGroup.selectAll('.edge-line')
        .data(simEdges).join('line')
        .attr('class', d => {
          let cls = 'edge-line';
          if (d._transitive) cls += ' transitive';
          else if (d.derivation === 'linked' || d.derivation === 'inferred') cls += ' dashed';
          if (d.confidence < 0.8 && !d._transitive) cls += ' low-conf';
          return cls;
        })
        .attr('marker-end', d => d._transitive ? 'url(#arch-arrow-transitive)' : 'url(#arch-arrow)');

      // Via labels for transitive edges
      const viaLabels = edgeGroup.selectAll('.via-label')
        .data(simEdges.filter(e => e._transitive)).join('text')
        .attr('class', 'via-label')
        .attr('text-anchor', 'middle')
        .text(d => d._viaLabel || '');

      // Draw nodes
      const node = nodeGroup.selectAll('.node-g')
        .data(simNodes).join('g')
        .attr('class', 'node-g')
        .call(d3.drag()
          .on('start', (event, d) => { if (!event.active) sim.alphaTarget(0.3).restart(); d.fx = d.x; d.fy = d.y; })
          .on('drag', (event, d) => { d.fx = event.x; d.fy = event.y; })
          .on('end', (event, d) => { if (!event.active) sim.alphaTarget(0); d.fx = null; d.fy = null; })
        );

      node.append('rect')
        .attr('class', d => 'node-rect' + (self.selectedNode?.node_id === d.node_id ? ' selected' : ''))
        .attr('width', 90).attr('height', 36).attr('x', -45).attr('y', -18)
        .attr('fill', d => d.confidence < 0.8 ? '#fef3c7' : self.layerColor(d.layer) + '20')
        .attr('stroke', d => self.layerColor(d.layer));

      node.append('text')
        .attr('text-anchor', 'middle').attr('dy', 1).attr('font-size', '9px')
        .text(d => d.name.length > 12 ? d.name.slice(0, 11) + '…' : d.name);

      node.append('text')
        .attr('text-anchor', 'middle').attr('dy', 12).attr('font-size', '7px').attr('fill', 'var(--text-muted)')
        .text(d => d.node_type);

      // Click handlers
      node.on('click', (event, d) => {
        event.stopPropagation();
        self.selectedNode = d;
        self.highlightNode(d.node_id, simEdges.map(e => ({
          from_node_id: e.source.id || e.source,
          to_node_id: e.target.id || e.target,
          edge_type: e.edge_type
        })));
      });

      svg.on('click', () => {
        self.selectedNode = null;
        self.highlightNode(null);
      });

      // Tick: update positions + domain bounding boxes
      sim.on('tick', () => {
        link
          .attr('x1', d => d.source.x).attr('y1', d => d.source.y)
          .attr('x2', d => d.target.x).attr('y2', d => d.target.y);
        viaLabels
          .attr('x', d => (d.source.x + d.target.x) / 2)
          .attr('y', d => (d.source.y + d.target.y) / 2 - 6);
        node.attr('transform', d => `translate(${d.x},${d.y})`);

        // Compute and render domain bounding boxes
        const padding = 30;
        const labelHeight = 20;
        const boxData = domainKeys.map(dk => {
          const members = simNodes.filter(n => n._domain === dk);
          if (members.length === 0) return null;
          const minX = d3.min(members, d => d.x) - 45 - padding;
          const maxX = d3.max(members, d => d.x) + 45 + padding;
          const minY = d3.min(members, d => d.y) - 18 - padding - labelHeight;
          const maxY = d3.max(members, d => d.y) + 18 + padding;
          return { dk, x: minX, y: minY, w: maxX - minX, h: maxY - minY };
        }).filter(Boolean);

        const boxes = domainBoxGroup.selectAll('.domain-g').data(boxData, d => d.dk);
        const enter = boxes.enter().append('g').attr('class', 'domain-g');
        enter.append('rect').attr('class', 'domain-box');
        enter.append('text').attr('class', 'domain-label');
        const merged = enter.merge(boxes);
        merged.select('.domain-box')
          .attr('x', d => d.x).attr('y', d => d.y)
          .attr('width', d => d.w).attr('height', d => d.h);
        merged.select('.domain-label')
          .attr('x', d => d.x + 12).attr('y', d => d.y + 16)
          .text(d => d.dk === '_unassigned' ? 'UNASSIGNED' : d.dk.toUpperCase());
        boxes.exit().remove();
      });
    },
```

- [ ] **Step 3: Verify Architecture mode renders**

Open the dashboard, click "Architecture" tab. Verify:
- Nodes appear grouped inside domain bounding boxes
- Blue domain box borders with uppercase labels
- Nodes are colored by layer
- Edges render between nodes (dashed for transitive)
- Pan/zoom works
- Click node shows side panel
- Hover highlights connected nodes

- [ ] **Step 4: Commit**

```bash
git add internal/admin/static/index.html
git commit -m "feat: Architecture mode with nested domain boxes and D3 force layout"
```

---

### Task 5: Workspace Mode — Render Function, Drag-and-Drop, BFS Path

**Files:**
- Modify: `internal/admin/static/index.html` (add renderWorkspaceGraph + helpers)

- [ ] **Step 1: Add BFS shortest path helper**

Add this method after the filter sidebar helpers:

```javascript
    // BFS shortest path using provided edges (filtered, not raw graphData)
    bfsShortestPath(fromId, toId, edges) {
      // Build undirected adjacency list from provided edges
      const adj = {};
      edges.forEach(e => {
        if (!adj[e.from_node_id]) adj[e.from_node_id] = [];
        if (!adj[e.to_node_id]) adj[e.to_node_id] = [];
        adj[e.from_node_id].push(e.to_node_id);
        adj[e.to_node_id].push(e.from_node_id);
      });
      // BFS
      const queue = [[fromId]];
      const visited = new Set([fromId]);
      while (queue.length > 0) {
        const path = queue.shift();
        const current = path[path.length - 1];
        if (current === toId) return path;
        for (const neighbor of (adj[current] || [])) {
          if (!visited.has(neighbor)) {
            visited.add(neighbor);
            queue.push([...path, neighbor]);
          }
        }
      }
      return null; // no path
    },
```

- [ ] **Step 2: Add `handleWorkspaceDrop()` method**

```javascript
    handleWorkspaceDrop(event) {
      if (this.graphMode !== 'workspace') return;
      const nodeId = event.dataTransfer.getData('text/plain');
      if (!nodeId) return;
      if (this.workspaceNodes.some(w => w.node_id === nodeId)) return;

      const canvas = document.getElementById('graph-canvas');
      const rect = canvas.getBoundingClientRect();
      const x = event.clientX - rect.left;
      const y = event.clientY - rect.top;

      this.workspaceNodes.push({ node_id: nodeId, x, y, manual: true });

      // Use filtered edges for BFS (respects sidebar filters)
      const filtered = this.getFilteredData();
      const manualNodes = this.workspaceNodes.filter(w => w.manual);
      if (manualNodes.length >= 2) {
        this.workspacePaths = [];
        for (let i = 0; i < manualNodes.length; i++) {
          for (let j = i + 1; j < manualNodes.length; j++) {
            const path = this.bfsShortestPath(manualNodes[i].node_id, manualNodes[j].node_id, filtered.edges);
            if (path) {
              this.workspacePaths.push({ from: manualNodes[i].node_id, to: manualNodes[j].node_id, path });
            }
          }
        }
      }

      this.renderGraph();
    },
```

- [ ] **Step 3: Add `expandWorkspaceNode()` method**

```javascript
    expandWorkspaceNode(nodeId) {
      const filtered = this.getFilteredData();
      const filteredNodeIds = new Set(filtered.nodes.map(n => n.node_id));
      const neighbors = new Set();
      filtered.edges.forEach(e => {
        if (e.from_node_id === nodeId) neighbors.add(e.to_node_id);
        if (e.to_node_id === nodeId) neighbors.add(e.from_node_id);
      });
      const existing = new Set(this.workspaceNodes.map(w => w.node_id));
      const nodeById = {};
      filtered.nodes.forEach(n => { nodeById[n.node_id] = n; });
      // Find position of the node being expanded
      const source = this.workspaceNodes.find(w => w.node_id === nodeId);
      const sx = source ? source.x : 400;
      const sy = source ? source.y : 300;
      let added = 0;
      neighbors.forEach(nId => {
        if (!existing.has(nId) && nodeById[nId]) {
          const angle = (added / neighbors.size) * Math.PI * 2;
          this.workspaceNodes.push({
            node_id: nId,
            x: sx + Math.cos(angle) * 120,
            y: sy + Math.sin(angle) * 120,
            manual: false
          });
          added++;
        }
      });
      this.renderGraph();
    },
```

- [ ] **Step 4: Add `renderWorkspaceGraph()` method**

```javascript
    renderWorkspaceGraph(container, allFilteredNodes, allFilteredEdges) {
      const self = this;
      container.innerHTML = '';
      const width = container.clientWidth || 900;
      const height = Math.max(500, container.clientHeight || 500);

      if (this.workspaceNodes.length === 0) {
        container.innerHTML = '<div style="padding:80px;text-align:center;color:var(--text-muted)"><div style="font-size:24px;margin-bottom:12px">⊕</div>Drag entities from the left palette onto this canvas.<br><span style="font-size:11px">Drop two entities to auto-discover the shortest path between them.</span></div>';
        return;
      }

      // Use filtered data — sidebar filters must apply to workspace too
      const filtered = this.getFilteredData();
      const filteredNodeIds = new Set(filtered.nodes.map(n => n.node_id));
      const nodeById = {};
      filtered.nodes.forEach(n => { nodeById[n.node_id] = n; });

      // Collect all node IDs that should be on canvas
      const canvasNodeIds = new Set(this.workspaceNodes.map(w => w.node_id).filter(id => filteredNodeIds.has(id)));
      // Add path intermediate nodes (only if they pass filters)
      const pathNodeIds = new Set();
      this.workspacePaths.forEach(p => {
        p.path.forEach(nId => {
          if (filteredNodeIds.has(nId)) {
            pathNodeIds.add(nId);
            canvasNodeIds.add(nId);
          }
        });
      });
      // Add direct neighbors of manual nodes (dimmed, with + badge)
      const allEdges = filtered.edges;
      const neighborIds = new Set();
      this.workspaceNodes.filter(w => w.manual).forEach(w => {
        allEdges.forEach(e => {
          if (e.from_node_id === w.node_id && !canvasNodeIds.has(e.to_node_id)) neighborIds.add(e.to_node_id);
          if (e.to_node_id === w.node_id && !canvasNodeIds.has(e.from_node_id)) neighborIds.add(e.from_node_id);
        });
      });
      neighborIds.forEach(nId => canvasNodeIds.add(nId));

      // Build sim nodes
      const posMap = {};
      this.workspaceNodes.forEach(w => { posMap[w.node_id] = w; });
      const simNodes = [...canvasNodeIds].map(nId => {
        const n = nodeById[nId];
        if (!n) return null;
        const pos = posMap[nId];
        const isManual = this.workspaceNodes.some(w => w.node_id === nId && w.manual);
        const isPath = pathNodeIds.has(nId) && !isManual;
        const isNeighbor = neighborIds.has(nId);
        return {
          ...n, id: nId,
          x: pos ? pos.x : width / 2 + (Math.random() - 0.5) * 200,
          y: pos ? pos.y : height / 2 + (Math.random() - 0.5) * 200,
          _manual: isManual, _path: isPath, _neighbor: isNeighbor,
          fx: isManual && pos ? pos.x : null,
          fy: isManual && pos ? pos.y : null
        };
      }).filter(Boolean);

      const simNodeMap = {};
      simNodes.forEach(n => { simNodeMap[n.id] = n; });

      // Build edges between canvas nodes
      const pathEdgeKeys = new Set();
      this.workspacePaths.forEach(p => {
        for (let i = 0; i < p.path.length - 1; i++) {
          pathEdgeKeys.add(p.path[i] + '->' + p.path[i + 1]);
          pathEdgeKeys.add(p.path[i + 1] + '->' + p.path[i]);
        }
      });

      const simEdges = allEdges
        .filter(e => simNodeMap[e.from_node_id] && simNodeMap[e.to_node_id])
        .map(e => ({
          source: e.from_node_id, target: e.to_node_id, ...e,
          _isPath: pathEdgeKeys.has(e.from_node_id + '->' + e.to_node_id)
        }));

      const svg = d3.select(container).append('svg')
        .attr('class', 'graph-svg')
        .attr('width', width).attr('height', height);

      const defs = svg.append('defs');
      defs.append('marker').attr('id', 'ws-arrow').attr('viewBox', '0 0 10 10')
        .attr('refX', 22).attr('refY', 5).attr('markerWidth', 6).attr('markerHeight', 6)
        .attr('orient', 'auto-start-reverse')
        .append('path').attr('d', 'M 0 0 L 10 5 L 0 10 z').attr('class', 'arrow-head');
      defs.append('marker').attr('id', 'ws-arrow-path').attr('viewBox', '0 0 10 10')
        .attr('refX', 22).attr('refY', 5).attr('markerWidth', 6).attr('markerHeight', 6)
        .attr('orient', 'auto-start-reverse')
        .append('path').attr('d', 'M 0 0 L 10 5 L 0 10 z').attr('fill', '#f59e0b');

      const gRoot = svg.append('g');
      svg.call(d3.zoom().scaleExtent([0.2, 4]).on('zoom', (event) => {
        gRoot.attr('transform', event.transform);
      }));

      // Edges
      const link = gRoot.selectAll('.edge-line')
        .data(simEdges).join('line')
        .attr('class', d => 'edge-line' + (d._isPath ? ' path-edge' : ''))
        .attr('marker-end', d => d._isPath ? 'url(#ws-arrow-path)' : 'url(#ws-arrow)');

      // Nodes
      const node = gRoot.selectAll('.node-g')
        .data(simNodes).join('g')
        .attr('class', d => 'node-g' + (d._neighbor ? ' neighbor-node' : ''))
        .call(d3.drag()
          .on('start', (event, d) => { if (!event.active) sim.alphaTarget(0.3).restart(); d.fx = d.x; d.fy = d.y; })
          .on('drag', (event, d) => { d.fx = event.x; d.fy = event.y; if (d._manual) { const w = self.workspaceNodes.find(w2 => w2.node_id === d.id); if (w) { w.x = d.x; w.y = d.y; } } })
          .on('end', (event, d) => { if (!event.active) sim.alphaTarget(0); if (!d._manual) { d.fx = null; d.fy = null; } })
        );

      node.append('rect')
        .attr('class', 'node-rect')
        .attr('width', 100).attr('height', 40).attr('x', -50).attr('y', -20)
        .attr('fill', d => self.layerColor(d.layer) + '20')
        .attr('stroke', d => self.layerColor(d.layer));

      node.append('text')
        .attr('text-anchor', 'middle').attr('dy', -2).attr('font-size', '10px')
        .text(d => d.name.length > 14 ? d.name.slice(0, 13) + '…' : d.name);

      node.append('text')
        .attr('text-anchor', 'middle').attr('dy', 12).attr('font-size', '7px').attr('fill', 'var(--text-muted)')
        .text(d => d.node_type);

      // Manual dot indicator
      node.filter(d => d._manual).append('circle')
        .attr('class', 'manual-dot').attr('cx', 45).attr('cy', -15).attr('r', 4);

      // Auto badge for path nodes
      node.filter(d => d._path && !d._manual).each(function(d) {
        const g2 = d3.select(this);
        g2.append('rect').attr('class', 'auto-badge').attr('x', 30).attr('y', -19).attr('width', 24).attr('height', 13);
        g2.append('text').attr('class', 'auto-badge-text').attr('x', 42).attr('y', -10).attr('text-anchor', 'middle').text('auto');
      });

      // + expand badge for neighbor nodes
      node.filter(d => d._neighbor).each(function(d) {
        const g2 = d3.select(this);
        g2.append('circle').attr('class', 'expand-badge').attr('cx', 55).attr('cy', 0).attr('r', 10)
          .on('click', (event) => { event.stopPropagation(); self.expandWorkspaceNode(d.id); });
        g2.append('text').attr('class', 'expand-text').attr('x', 55).attr('y', 4).attr('text-anchor', 'middle').text('+')
          .on('click', (event) => { event.stopPropagation(); self.expandWorkspaceNode(d.id); });
      });

      // Click handlers
      node.on('click', (event, d) => {
        event.stopPropagation();
        self.selectedNode = d;
      });
      svg.on('click', () => { self.selectedNode = null; });

      // Force simulation
      const sim = d3.forceSimulation(simNodes)
        .force('link', d3.forceLink(simEdges).id(d => d.id).distance(120).strength(0.2))
        .force('charge', d3.forceManyBody().strength(-100))
        .force('collision', d3.forceCollide().radius(55));

      sim.on('tick', () => {
        link
          .attr('x1', d => d.source.x).attr('y1', d => d.source.y)
          .attr('x2', d => d.target.x).attr('y2', d => d.target.y);
        node.attr('transform', d => `translate(${d.x},${d.y})`);
      });
    },
```

- [ ] **Step 5: Verify Workspace mode end-to-end**

Open the dashboard, click "Workspace" tab. Verify:
- Empty canvas shows placeholder message
- Left palette shows searchable entity list grouped by type
- Drag an entity onto canvas — it appears with blue dot, neighbors shown dimmed with + badges
- Drag a second entity — shortest path highlighted in orange, intermediate nodes tagged "auto"
- Click + on a neighbor — it expands
- Clear Canvas button resets everything
- ☰ Filters button toggles sidebar, hiding node types collapses edges transitively

- [ ] **Step 6: Commit**

```bash
git add internal/admin/static/index.html
git commit -m "feat: Workspace mode with drag-and-drop, BFS path discovery, smart expand"
```

---

### Task 6: Polish & Edge Cases

**Files:**
- Modify: `internal/admin/static/index.html`

- [ ] **Step 1: Handle domain box click-to-center in Architecture mode**

Add this inside `renderArchitectureGraph`, after the domain box rendering in the tick handler (after `boxes.exit().remove();`):

```javascript
        merged.on('click', (event, d) => {
          event.stopPropagation();
          const centerX = d.x + d.w / 2;
          const centerY = d.y + d.h / 2;
          svg.transition().duration(500).call(
            d3.zoom().scaleExtent([0.1, 4]).on('zoom', (ev) => { g.attr('transform', ev.transform); })
              .transform,
            d3.zoomIdentity.translate(width / 2 - centerX, height / 2 - centerY)
          );
        });
```

- [ ] **Step 2: Add hover highlighting for Architecture mode**

The `highlightNode()` method already handles highlighting via CSS classes `.dimmed` and `.highlighted`. Verify it works by adding a `mouseenter`/`mouseleave` to the node group in `renderArchitectureGraph`:

Add right after the `node.on('click', ...)` handler:

```javascript
      node.on('mouseenter', (event, d) => {
        self.highlightNode(d.node_id, simEdges.map(e => ({
          from_node_id: e.source.id || e.source,
          to_node_id: e.target.id || e.target,
          edge_type: e.edge_type
        })));
      }).on('mouseleave', () => {
        if (!self.selectedNode) self.highlightNode(null);
      });
```

- [ ] **Step 3: Ensure layer presets still work with new modes**

The existing `setPreset()` calls `renderGraph()` which will dispatch to the correct mode. No changes needed — but verify that changing a layer preset in Architecture mode re-renders with the correct filtered nodes.

- [ ] **Step 4: Handle empty workspace search gracefully**

In `getWorkspacePaletteGroups()`, the search is already optional (`if (search && ...)`). Verify the palette shows all entities when search is empty and filters correctly when typing.

- [ ] **Step 5: Commit**

```bash
git add internal/admin/static/index.html
git commit -m "feat: Architecture mode polish — domain click-to-center, hover highlighting"
```

---

### Task 7: Final Integration Verification

- [ ] **Step 1: Full end-to-end test**

Open the dashboard and verify all 4 modes work:

1. **Tree mode**: Unchanged behavior — hierarchical tree by layer
2. **Explore mode**: Unchanged behavior — breadcrumb drill-down
3. **Architecture mode**:
   - Nodes grouped in domain bounding boxes
   - Cross-domain edges render correctly
   - ☰ Filters opens sidebar
   - Uncheck "endpoint" type — endpoints disappear, transitive dashed edges appear with "via" labels
   - Uncheck a domain — its box disappears, transitive edges collapse
   - Confidence slider filters low-confidence nodes
   - Click node opens side panel
   - Pan/zoom works
4. **Workspace mode**:
   - Search palette filters entities
   - Drag entity onto canvas — neighbors appear with + badges
   - Drag second entity — shortest path highlighted
   - Click + — expands neighbors
   - Clear Canvas resets
   - Filters sidebar works (hiding types creates transitive edges)

- [ ] **Step 2: Verify no regressions in Overview, Language, Settings tabs**

Switch between all dashboard tabs. Verify stats load, glossary works, settings save correctly.

- [ ] **Step 3: Final commit**

```bash
git add internal/admin/static/index.html
git commit -m "feat: graph dashboard complete — Architecture + Workspace modes with transitive filtering"
```
