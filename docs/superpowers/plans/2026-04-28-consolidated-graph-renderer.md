# Consolidated Graph Renderer Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace `renderWorkspaceGraph` and `renderDiagram` with a single `_renderGraphSVG(config)` function. Drop grid and force layouts — keep only strict (dagre, no drag) and free (dagre initial + draggable).

**Architecture:** Extract shared SVG rendering (markers, edges, labels, nodes, zoom, drag) into `_renderGraphSVG(config)`. Both workspace and diagram tabs prepare their data (nodes, edges, groups, annotations) then call this one function. Workspace data prep stays in a new `_prepareWorkspaceData()`, diagram data prep stays in `_prepareDiagramData()`. The HTML toolbar switches from Grid/Dagre/Force to Strict/Free.

**Tech Stack:** Alpine.js, D3.js v7, dagre (SVG layout), single-file inline JS

---

### Task 1: Write `_renderGraphSVG(config)` — the consolidated renderer

**Files:**
- Modify: `internal/admin/static/index.html` — add new method after `renderWorkspace()` (around line 3380)

This is the core task. The function takes a config object and renders the complete graph SVG.

- [ ] **Step 1: Add the `_renderGraphSVG` method**

Add this method after `renderWorkspace()` (line ~3380). This is a large block — it consolidates all shared rendering from both `renderWorkspaceGraph` and `renderDiagram`:

```js
_renderGraphSVG(config) {
  // config: { container, nodes, edges, mode, positionCache, groups, features, callbacks }
  // features: { annotations, investigation, neighborExpansion }
  // callbacks: { onNodeClick, onBackgroundClick, onDrag, onExpandNeighbor }
  const self = this;
  const container = config.container;
  const isStrict = config.mode === 'strict';
  const posCache = config.positionCache || {};
  const annotations = config.features?.annotations || {};
  const isInvestigation = !!config.features?.investigation;
  const groups = config.groups || { keys: [], map: {}, field: '' };

  container.innerHTML = '';
  if (config.nodes.length === 0) {
    container.innerHTML = '<div style="padding:60px;text-align:center;color:var(--text-muted)">No nodes to display. Adjust filters or import data.</div>';
    return null;
  }

  const width = Math.max(900, container.clientWidth || 900);
  const height = Math.max(600, container.clientHeight || 600);

  // --- Dagre layout ---
  let simNodes = [], simNodeMap = {}, simEdges = [];
  const dagreEdgePoints = {};

  if (typeof dagre !== 'undefined') {
    const useCompound = groups.keys.length > 0;
    const gDag = new dagre.graphlib.Graph({ compound: useCompound });
    gDag.setGraph({ rankdir: 'TB', ranksep: 60, nodesep: 30, edgesep: 20, marginx: 30, marginy: 30 });
    gDag.setDefaultEdgeLabel(() => ({}));

    if (useCompound) {
      groups.keys.forEach(gk => {
        gDag.setNode('group_' + gk, { label: gk, paddingTop: 30, paddingBottom: 15, paddingLeft: 15, paddingRight: 15 });
      });
    }

    config.nodes.forEach(n => {
      gDag.setNode(String(n.node_id), { label: n.name || '', width: 110, height: 44 });
      if (useCompound && n._group && groups.keys.includes(n._group)) {
        gDag.setParent(String(n.node_id), 'group_' + n._group);
      }
    });

    config.edges.forEach(e => {
      const from = String(e.from_node_id), to = String(e.to_node_id);
      if (gDag.hasNode(from) && gDag.hasNode(to)) gDag.setEdge(from, to, { _edge: e });
    });

    dagre.layout(gDag);

    config.nodes.forEach(n => {
      const pos = gDag.node(String(n.node_id));
      if (!pos) return;
      let x, y;
      if (isStrict) {
        x = pos.x; y = pos.y;
      } else {
        const cached = posCache[n.node_id];
        x = cached ? cached.x : pos.x;
        y = cached ? cached.y : pos.y;
        posCache[n.node_id] = { x, y };
      }
      const sn = { ...n, id: n.node_id, x, y };
      simNodes.push(sn);
      simNodeMap[sn.id] = sn;
    });

    // In strict mode, use dagre's routed edge points
    if (isStrict) {
      gDag.edges().forEach(e => {
        const ed = gDag.edge(e);
        if (ed && ed.points) dagreEdgePoints[e.v + '->' + e.w] = ed.points;
      });
    }

    // Draw group bounding boxes from dagre compound layout
    if (useCompound) {
      // Store dagre group positions for box rendering below
      config._dagreGroups = {};
      groups.keys.forEach(gk => {
        const gn = gDag.node('group_' + gk);
        if (gn) config._dagreGroups[gk] = gn;
      });
    }
  }

  // Build sim edges
  simEdges = config.edges
    .filter(e => simNodeMap[e.from_node_id] && simNodeMap[e.to_node_id])
    .map(e => ({
      ...e,
      source: simNodeMap[e.from_node_id],
      target: simNodeMap[e.to_node_id],
      _points: dagreEdgePoints[e.from_node_id + '->' + e.to_node_id] || null
    }));

  // --- SVG setup ---
  const svg = d3.select(container).append('svg')
    .attr('class', 'graph-svg')
    .style('width', '100%').style('height', '100%');
  svg.classed('hide-edge-labels', !this.showEdgeLabels);

  const defs = svg.append('defs');
  // Default gray arrow
  defs.append('marker').attr('id', 'g-arrow').attr('viewBox', '0 0 10 10')
    .attr('refX', 10).attr('refY', 5).attr('markerWidth', 8).attr('markerHeight', 8)
    .attr('orient', 'auto')
    .append('path').attr('d', 'M 0 0 L 10 5 L 0 10 z').attr('class', 'arrow-head');
  // Transitive/path arrow (amber)
  defs.append('marker').attr('id', 'g-arrow-highlight').attr('viewBox', '0 0 10 10')
    .attr('refX', 10).attr('refY', 5).attr('markerWidth', 8).attr('markerHeight', 8)
    .attr('orient', 'auto')
    .append('path').attr('d', 'M 0 0 L 10 5 L 0 10 z').attr('fill', '#b8963e');
  // Per-category arrows
  for (const [cat, catDef] of Object.entries(this.edgeCategories)) {
    defs.append('marker').attr('id', 'g-arrow-' + cat)
      .attr('viewBox', '0 0 10 10').attr('refX', 10).attr('refY', 5)
      .attr('markerWidth', 8).attr('markerHeight', 8).attr('orient', 'auto')
      .append('path').attr('d', 'M 0 0 L 10 5 L 0 10 z').attr('fill', catDef.color);
  }

  const gRoot = svg.append('g');
  svg.call(d3.zoom().scaleExtent([0.1, 4]).on('zoom', (event) => {
    gRoot.attr('transform', event.transform);
  }));

  // --- Group bounding boxes (behind everything) ---
  const boxGroup = gRoot.append('g').attr('class', 'domain-boxes');
  if (config._dagreGroups && groups.keys.length > 0) {
    groups.keys.forEach(gk => {
      const gn = config._dagreGroups[gk];
      if (!gn) return;
      const color = groups.field === 'layer' ? self.layerColor(gk) : '#6b4423';
      const bg = boxGroup.append('g').attr('class', 'domain-g');
      bg.append('rect').attr('class', 'domain-box')
        .attr('x', gn.x - gn.width / 2).attr('y', gn.y - gn.height / 2)
        .attr('width', gn.width).attr('height', gn.height)
        .attr('stroke', color);
      bg.append('text').attr('class', 'domain-label')
        .attr('x', gn.x - gn.width / 2 + 10).attr('y', gn.y - gn.height / 2 + 16)
        .attr('fill', color).text(gk.toUpperCase());
    });
  }

  // --- Edges ---
  const link = gRoot.selectAll('.edge-line')
    .data(simEdges).join('path')
    .attr('fill', 'none')
    .attr('class', d => {
      let cls = 'edge-line';
      if (d._isPath) cls += ' path-edge';
      else if (d._transitive) cls += ' transitive';
      else if (d.derivation !== 'hard' && d.derivation !== 'linked') cls += ' dashed';
      return cls;
    })
    .attr('stroke', d => {
      if (d._isPath || d._transitive) return null;
      const info = self.edgeCategoryLookup[d.edge_type];
      return info ? info.color : null;
    })
    .attr('marker-end', d => {
      if (d._isPath || d._transitive) return 'url(#g-arrow-highlight)';
      const info = self.edgeCategoryLookup[d.edge_type];
      return info ? 'url(#g-arrow-' + info.category + ')' : 'url(#g-arrow)';
    });

  // --- Via labels (transitive edges) ---
  const viaLabels = gRoot.selectAll('.via-label')
    .data(simEdges.filter(e => e._transitive)).join('text')
    .attr('class', 'via-label').attr('text-anchor', 'middle')
    .text(d => d._viaLabel || '');

  // --- Short labels (non-transitive edges) ---
  const shortLabels = gRoot.selectAll('.edge-short-label')
    .data(simEdges.filter(e => !e._transitive)).join('text')
    .attr('class', 'edge-short-label')
    .attr('fill', d => {
      const info = self.edgeCategoryLookup[d.edge_type];
      return info ? info.color : '#b8a898';
    })
    .text(d => {
      const info = self.edgeCategoryLookup[d.edge_type];
      return info ? info.shortLabel : '';
    });

  // --- Edge path function ---
  const lineGen = d3.line().x(d => d.x).y(d => d.y).curve(d3.curveBasis);
  function edgePath(d) {
    if (d._points && d._points.length > 1) return lineGen(d._points);
    const dx = d.target.x - d.source.x, dy = d.target.y - d.source.y;
    const dr = Math.sqrt(dx * dx + dy * dy) * 1.5;
    return `M${d.source.x},${d.source.y}A${dr},${dr} 0 0,1 ${d.target.x},${d.target.y}`;
  }

  // Position edges
  link.attr('d', edgePath);
  viaLabels.attr('x', d => (d.source.x + d.target.x) / 2).attr('y', d => (d.source.y + d.target.y) / 2 - 6);

  // Position short labels at path midpoints
  shortLabels.each(function(d) {
    const pathEl = link.filter(e => e === d).node();
    if (pathEl) {
      const len = pathEl.getTotalLength();
      const mid = pathEl.getPointAtLength(len / 2);
      d3.select(this).attr('x', mid.x).attr('y', mid.y - 4);
    }
  });

  // --- Nodes ---
  const node = gRoot.selectAll('.node-g')
    .data(simNodes).join('g')
    .attr('class', d => 'node-g' + (d._neighbor ? ' neighbor-node' : ''))
    .attr('transform', d => `translate(${d.x},${d.y})`);

  // White background for cleaner rounded corners
  node.append('rect')
    .attr('width', 100).attr('height', 40).attr('x', -50).attr('y', -20)
    .attr('fill', '#fff').attr('rx', 6).attr('ry', 6);

  // Colored node rect
  node.append('rect')
    .attr('class', 'node-rect')
    .attr('width', 100).attr('height', 40).attr('x', -50).attr('y', -20)
    .attr('fill', d => self.layerColor(d.layer) + '40')
    .attr('stroke', d => {
      const ann = annotations[d.node_key];
      return (ann?.highlight) || self.layerColor(d.layer);
    })
    .attr('stroke-width', d => annotations[d.node_key]?.highlight ? 3 : 1.5)
    .style('filter', d => {
      const ann = annotations[d.node_key];
      return ann?.highlight ? 'drop-shadow(0 0 8px ' + ann.highlight + ')' : 'none';
    });

  // Node name
  node.append('text')
    .attr('text-anchor', 'middle').attr('dy', -2).attr('font-size', '10px')
    .text(d => d.name.length > 14 ? d.name.slice(0, 13) + '\u2026' : d.name);

  // Node type subtitle
  node.append('text')
    .attr('text-anchor', 'middle').attr('dy', 12).attr('font-size', '7px').attr('fill', 'var(--text-muted)')
    .text(d => d.node_type);

  // Annotation notes (diagram only)
  node.filter(d => annotations[d.node_key]?.note).append('text')
    .attr('class', 'ann-note')
    .attr('text-anchor', 'middle').attr('dy', 32).attr('font-size', '8px')
    .attr('fill', d => annotations[d.node_key]?.highlight || 'var(--primary)')
    .attr('font-style', 'italic')
    .text(d => { const n = annotations[d.node_key].note; return n.length > 30 ? n.slice(0, 29) + '\u2026' : n; });

  // --- Investigation badges (workspace only) ---
  if (isInvestigation) {
    node.filter(d => d._manual).append('circle')
      .attr('class', 'manual-dot').attr('cx', 45).attr('cy', -15).attr('r', 4);
    node.filter(d => d._path && !d._manual).each(function(d) {
      const g2 = d3.select(this);
      g2.append('rect').attr('class', 'auto-badge').attr('x', 30).attr('y', -19).attr('width', 24).attr('height', 13);
      g2.append('text').attr('class', 'auto-badge-text').attr('x', 42).attr('y', -10).attr('text-anchor', 'middle').text('auto');
    });
    node.filter(d => d._neighbor).each(function(d) {
      const g2 = d3.select(this);
      g2.append('circle').attr('class', 'expand-badge').attr('cx', 55).attr('cy', 0).attr('r', 10)
        .on('click', (event) => { event.stopPropagation(); if (config.callbacks?.onExpandNeighbor) config.callbacks.onExpandNeighbor(d.id); });
      g2.append('text').attr('class', 'expand-text').attr('x', 55).attr('y', 4).attr('text-anchor', 'middle').text('+')
        .on('click', (event) => { event.stopPropagation(); if (config.callbacks?.onExpandNeighbor) config.callbacks.onExpandNeighbor(d.id); });
    });
  }

  // --- Drag (free mode only) ---
  if (!isStrict) {
    node.style('cursor', 'grab');
    node.call(d3.drag()
      .on('start', (event) => { d3.select(event.sourceEvent.target.closest('.node-g')).style('cursor', 'grabbing'); })
      .on('drag', (event, d) => {
        d.x = event.x; d.y = event.y;
        if (config.callbacks?.onDrag) config.callbacks.onDrag(d.id, d.x, d.y);
        d3.select(event.sourceEvent.target.closest('.node-g')).attr('transform', `translate(${d.x},${d.y})`);
        // Recalculate connected edges — dagre waypoints become stale, use arcs
        link.filter(e => e.source.id === d.id || e.target.id === d.id)
          .each(function(e) { e._points = null; }) // clear stale dagre points
          .attr('d', edgePath);
        viaLabels.filter(e => e.source.id === d.id || e.target.id === d.id)
          .attr('x', e => (e.source.x + e.target.x) / 2).attr('y', e => (e.source.y + e.target.y) / 2 - 6);
        shortLabels.filter(e => e.source.id === d.id || e.target.id === d.id).each(function(e) {
          const pathEl = link.filter(p => p === e).node();
          if (pathEl) {
            const len = pathEl.getTotalLength();
            const mid = pathEl.getPointAtLength(len / 2);
            d3.select(this).attr('x', mid.x).attr('y', mid.y - 4);
          }
        });
      })
      .on('end', (event) => { d3.select(event.sourceEvent.target.closest('.node-g')).style('cursor', 'grab'); })
    );
  }

  // --- Click handlers ---
  node.on('click', (event, d) => {
    event.stopPropagation();
    if (config.callbacks?.onNodeClick) config.callbacks.onNodeClick(d, simEdges);
  });
  node.on('mouseenter', (event, d) => {
    self.highlightNode(d.node_id || d.id, simEdges);
  }).on('mouseleave', () => {
    if (!self.selectedNode) self.highlightNode(null, simEdges);
  });
  gRoot.on('click', () => {
    if (config.callbacks?.onBackgroundClick) config.callbacks.onBackgroundClick(simEdges);
  });

  return { svg, gRoot, link, node, shortLabels, viaLabels, simEdges, simNodes, simNodeMap };
},
```

- [ ] **Step 2: Verify it compiles (page loads without errors)**

Open the admin dashboard in a browser. The new function exists but isn't called yet. No errors expected.

- [ ] **Step 3: Commit**

```bash
git add internal/admin/static/index.html
git commit -m "feat: add consolidated _renderGraphSVG function"
```

---

### Task 2: Replace `renderDiagram` to use `_renderGraphSVG`

**Files:**
- Modify: `internal/admin/static/index.html` — rewrite `renderDiagram()` (lines ~3486-3705)

- [ ] **Step 1: Replace `renderDiagram` with a thin wrapper**

Find the `renderDiagram()` method and replace its entire body with:

```js
renderDiagram() {
  const container = document.getElementById('diagram-canvas') || document.getElementById('graph-canvas');
  if (!container || !this.diagramData) return;

  const filtered = this._filterDiagramData();
  if (filtered.nodes.length === 0) { container.innerHTML = ''; return; }

  const annotations = this._getDiagramAnnotations();
  const self = this;

  const result = this._renderGraphSVG({
    container,
    nodes: filtered.nodes,
    edges: filtered.edges,
    mode: this.diagramMode,
    positionCache: this._diagramPositions,
    groups: { keys: [], map: {}, field: '' },
    features: { annotations },
    callbacks: {
      onNodeClick(d, simEdges) {
        const isAlready = self.selectedNode && self.selectedNode.id === d.id;
        self.selectedNode = isAlready ? null : d;
        self.highlightNode(isAlready ? null : d.id, simEdges);
      },
      onBackgroundClick(simEdges) {
        self.selectedNode = null;
        self.highlightNode(null, simEdges);
      },
      onDrag(id, x, y) {
        self._diagramPositions[id] = { x, y };
      },
    }
  });

  // Re-apply step annotations after render
  this.$nextTick(() => this.updateDiagramAnnotations());
},
```

- [ ] **Step 2: Verify the diagram tab works**

Open the admin dashboard, navigate to the Diagram tab.
Expected: diagram renders with colored edges, short labels, drag works in free mode, strict mode has no drag, step annotations work, node click highlights edges with full labels.

- [ ] **Step 3: Commit**

```bash
git add internal/admin/static/index.html
git commit -m "refactor: renderDiagram now uses consolidated _renderGraphSVG"
```

---

### Task 3: Replace `renderWorkspaceGraph` to use `_renderGraphSVG`

**Files:**
- Modify: `internal/admin/static/index.html` — rewrite `renderWorkspaceGraph()` (lines ~2663-3186)

- [ ] **Step 1: Replace `renderWorkspaceGraph` with a thin wrapper**

Replace the entire `renderWorkspaceGraph(container, allFilteredNodes, allFilteredEdges)` method body with:

```js
renderWorkspaceGraph(container, allFilteredNodes, allFilteredEdges) {
  const self = this;
  const isInvestigating = this.workspaceNodes.length > 0;
  const filtered = this.getFilteredData();
  const nodeById = {};
  filtered.nodes.forEach(n => { nodeById[n.node_id] = n; });
  const filteredNodeIds = new Set(filtered.nodes.map(n => n.node_id));
  const groupField = this.groupByField || 'domain_key';

  // --- Determine which nodes to show ---
  let graphNodes, pathEdgeKeys = new Set(), neighborIds = new Set();

  if (!isInvestigating) {
    graphNodes = filtered.nodes.map(n => ({
      ...n, _group: n[groupField] || null,
      _manual: false, _path: false, _neighbor: false
    }));
  } else {
    const canvasNodeIds = new Set(this.workspaceNodes.map(w => w.node_id).filter(id => filteredNodeIds.has(id)));
    const pathNodeIds = new Set();
    this.workspacePaths.forEach(p => {
      p.path.forEach(nId => { if (filteredNodeIds.has(nId)) { pathNodeIds.add(nId); canvasNodeIds.add(nId); } });
      for (let i = 0; i < p.path.length - 1; i++) {
        pathEdgeKeys.add(p.path[i] + '->' + p.path[i + 1]);
        pathEdgeKeys.add(p.path[i + 1] + '->' + p.path[i]);
      }
    });
    this.workspaceNodes.filter(w => w.manual).forEach(w => {
      filtered.edges.forEach(e => {
        if (e.from_node_id === w.node_id && !canvasNodeIds.has(e.to_node_id)) neighborIds.add(e.to_node_id);
        if (e.to_node_id === w.node_id && !canvasNodeIds.has(e.from_node_id)) neighborIds.add(e.from_node_id);
      });
    });
    neighborIds.forEach(nId => canvasNodeIds.add(nId));

    graphNodes = [...canvasNodeIds].map(nId => {
      const n = nodeById[nId];
      if (!n) return null;
      const isManual = this.workspaceNodes.some(w => w.node_id === nId && w.manual);
      return {
        ...n, _group: n[groupField] || null,
        _manual: isManual,
        _path: pathNodeIds.has(nId) && !isManual,
        _neighbor: neighborIds.has(nId),
      };
    }).filter(Boolean);
  }

  // Build edges with path marking
  const nodeIdSet = new Set(graphNodes.map(n => n.node_id));
  const graphEdges = filtered.edges
    .filter(e => nodeIdSet.has(e.from_node_id) && nodeIdSet.has(e.to_node_id))
    .map(e => ({
      ...e,
      _isPath: pathEdgeKeys.has(e.from_node_id + '->' + e.to_node_id)
    }));

  // Build groups
  const groupMap = {};
  graphNodes.forEach(n => {
    const gk = n._group;
    if (gk) { if (!groupMap[gk]) groupMap[gk] = []; groupMap[gk].push(n); }
  });
  const groupKeys = Object.keys(groupMap).sort();

  const result = this._renderGraphSVG({
    container,
    nodes: graphNodes,
    edges: graphEdges,
    mode: this.wsLayout === 'free' ? 'free' : 'strict',
    positionCache: this._wsPositionCache || (this._wsPositionCache = {}),
    groups: { keys: groupKeys, map: groupMap, field: groupField },
    features: { investigation: isInvestigating },
    callbacks: {
      onNodeClick(d, simEdges) {
        self.selectedNode = d;
        self.highlightNode(d.node_id || d.id, simEdges);
      },
      onBackgroundClick(simEdges) {
        self.selectedNode = null;
        self.highlightNode(null, simEdges);
      },
      onDrag(id, x, y) {
        if (!self._wsPositionCache) self._wsPositionCache = {};
        self._wsPositionCache[id] = { x, y };
        const w = self.workspaceNodes.find(w2 => w2.node_id === id);
        if (w) { w.x = x; w.y = y; }
      },
      onExpandNeighbor(id) {
        self.expandWorkspaceNode(id);
      },
    }
  });
},
```

- [ ] **Step 2: Verify the workspace tab works**

Open the admin dashboard, navigate to Graph → Workspace.
Expected: workspace renders with dagre layout, colored edges, short labels, group boxes. Investigation mode (drag-drop entities) works. Node click highlights edges.

- [ ] **Step 3: Commit**

```bash
git add internal/admin/static/index.html
git commit -m "refactor: renderWorkspaceGraph now uses consolidated _renderGraphSVG"
```

---

### Task 4: Update toolbar — replace Grid/Dagre/Force with Strict/Free

**Files:**
- Modify: `internal/admin/static/index.html` — toolbar HTML (lines ~534-540) and state (line ~1262)

- [ ] **Step 1: Update the state variable**

Change line 1262 from:

```js
wsLayout: 'dagre',  // 'grid', 'dagre', or 'force'
```

to:

```js
wsLayout: 'strict',  // 'strict' or 'free'
```

- [ ] **Step 2: Update the toolbar buttons**

Replace the workspace layout toggle (lines ~534-540):

```html
<template x-if="graphMode==='workspace'">
  <div class="mode-toggle">
    <button class="mode-btn" :class="{active:wsLayout==='grid'}" @click="wsLayout='grid';renderWorkspace()">Grid</button>
    <button class="mode-btn" :class="{active:wsLayout==='dagre'}" @click="wsLayout='dagre';renderWorkspace()">Dagre</button>
    <button class="mode-btn" :class="{active:wsLayout==='force'}" @click="wsLayout='force';renderWorkspace()">Force</button>
  </div>
</template>
```

with:

```html
<template x-if="graphMode==='workspace'">
  <div class="mode-toggle">
    <button class="mode-btn" :class="{active:wsLayout==='strict'}" @click="wsLayout='strict';_wsPositionCache={};renderWorkspace()">Strict</button>
    <button class="mode-btn" :class="{active:wsLayout==='free'}" @click="wsLayout='free';renderWorkspace()">Free</button>
  </div>
</template>
```

Note: clicking Strict clears the position cache so dagre recomputes fresh layout.

- [ ] **Step 3: Update the diagram tab toolbar**

Find the diagram mode toggle (Strict/Free buttons for diagram). It should already exist. Verify it uses `diagramMode` not `wsLayout`. No change needed if they're already separate.

- [ ] **Step 4: Verify both toolbars work**

- Workspace: Strict/Free buttons switch between locked dagre and draggable mode
- Diagram: Strict/Free buttons still work independently

- [ ] **Step 5: Commit**

```bash
git add internal/admin/static/index.html
git commit -m "refactor: replace Grid/Dagre/Force with Strict/Free in workspace toolbar"
```

---

### Task 5: Clean up dead code

**Files:**
- Modify: `internal/admin/static/index.html`

- [ ] **Step 1: Remove old marker CSS/IDs references**

Search for any remaining references to `ws-arrow`, `dia-arrow`, `ws-arrow-path`, `dia-arrow-transitive` in CSS rules and replace with `g-arrow`, `g-arrow-highlight`, `g-arrow-<cat>`. Specifically check:

- CSS rule for `.arrow-head` fill — should still work since the new markers use `.attr('class', 'arrow-head')` on the default marker
- Any hardcoded marker URL references outside the renderers (e.g., in `highlightNode`, `updateDiagramAnnotations`, `renderExploreGraph`, `renderLayeredGraph`)

The explore and tree/layered renderers are NOT being consolidated — they keep their own markers (`explore-arrow`). Only clean up references that pointed to the old `ws-arrow` or `dia-arrow` markers.

- [ ] **Step 2: Remove unused state variables**

If `wsLayout` was the only consumer of grid/force layout logic, and the grid/force code blocks are now inside the deleted `renderWorkspaceGraph` body, no further cleanup is needed — they were already replaced in Task 3.

- [ ] **Step 3: Verify everything works**

Full smoke test:
1. Graph tab → Tree mode works
2. Graph tab → Explore mode works
3. Graph tab → Workspace Strict — dagre layout, no drag, edges follow dagre curves
4. Graph tab → Workspace Free — drag nodes, edges recalculate, labels follow
5. Diagram tab → Strict — dagre layout, no drag, step annotations work
6. Diagram tab → Free — drag nodes, edges recalculate, labels follow
7. Both tabs — node click highlights edges with full labels, background click clears
8. Both tabs — sidebar filters, edge category editor, label toggle
9. Workspace — investigation mode (drag-drop entities, neighbor expansion)

- [ ] **Step 4: Commit**

```bash
git add internal/admin/static/index.html
git commit -m "chore: remove dead marker references and unused layout code"
```

---

### Task 6: Add neighbor expansion to diagram tab

**Files:**
- Modify: `internal/admin/static/index.html` — diagram data prep

- [ ] **Step 1: Add neighbor expansion when diagram data has nodes**

In `renderDiagram()`, after `const filtered = this._filterDiagramData();`, add neighbor expansion logic that mirrors the workspace:

```js
// Expand neighbors for diagram nodes
if (this.graphData) {
  const diagramNodeIds = new Set(filtered.nodes.map(n => n.node_id));
  const allEdges = this.graphData.edges || [];
  const allNodes = {};
  (this.graphData.nodes || []).forEach(n => { allNodes[n.node_id] = n; });

  const neighborNodes = [];
  allEdges.forEach(e => {
    if (diagramNodeIds.has(e.from_node_id) && !diagramNodeIds.has(e.to_node_id) && allNodes[e.to_node_id]) {
      neighborNodes.push({ ...allNodes[e.to_node_id], _neighbor: true, _manual: false, _path: false });
      diagramNodeIds.add(e.to_node_id);
    }
    if (diagramNodeIds.has(e.to_node_id) && !diagramNodeIds.has(e.from_node_id) && allNodes[e.from_node_id]) {
      neighborNodes.push({ ...allNodes[e.from_node_id], _neighbor: true, _manual: false, _path: false });
      diagramNodeIds.add(e.from_node_id);
    }
  });
  filtered.nodes = filtered.nodes.concat(neighborNodes);

  // Also include edges between expanded set
  const expandedEdges = allEdges.filter(e => diagramNodeIds.has(e.from_node_id) && diagramNodeIds.has(e.to_node_id));
  filtered.edges = expandedEdges;
}
```

- [ ] **Step 2: Add toggle in diagram sidebar**

In the diagram sidebar's "Display" group (after the "Edge labels" checkbox), add:

```html
<label>
  <input type="checkbox"
         :checked="diagramShowNeighbors"
         @change="diagramShowNeighbors=!diagramShowNeighbors;renderDiagram()">
  <span>Show linked nodes</span>
</label>
```

- [ ] **Step 3: Add the state variable**

In the Alpine data block, near `diagramMode`, add:

```js
diagramShowNeighbors: true, // show grayed neighbor nodes in diagram
```

- [ ] **Step 4: Gate the expansion on the toggle**

Wrap the neighbor expansion code from Step 1 in `if (this.diagramShowNeighbors && this.graphData)`.

- [ ] **Step 5: Verify neighbor expansion**

Open diagram tab with a diagram that has nodes. Expected: connected nodes from the full graph appear grayed out around the diagram nodes. Toggle the checkbox to show/hide them.

- [ ] **Step 6: Commit**

```bash
git add internal/admin/static/index.html
git commit -m "feat: show linked neighbor nodes in diagram tab with toggle"
```
