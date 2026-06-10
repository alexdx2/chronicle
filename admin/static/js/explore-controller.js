// explore-controller.js — Explore "All" mode: the FULL raw graph.
// Renders every active node/edge from /api/graph, honoring the side Filters
// panel. No level/drill mechanism here — the ONLY level mechanism is the
// view bar (All·C1·C2·C3·Data·API·Services·Flows), which renders through
// the server view engine instead of this controller.

import { filterGraph } from './graph-filter.js';

export class ExploreController {
  constructor({ store, renderer, selection, onStateChange }) {
    this._store = store;
    this._renderer = renderer;
    this._selection = selection;
    this._onStateChange = onStateChange || (() => {});

    this._edgeCategoryLookup = {};
    this._structuralEdgeTypes = new Set();

    // User-adjustable filters (sidebar + toolbar)
    this._sidebarFilters = {
      hiddenEdgeTypes: [],
      hiddenNodeTypes: [],
      hiddenDomains: [],
      minConfidence: 0,
      layers: null,          // null → all store layers
      groupBy: 'domain_key', // group boxes field ('' → none)
      hideStructural: false, // toolbar "Hide CONTAINS/OWNS"
      repoFilter: '',
    };
  }

  // --- Filters ---

  updateFilters(filters) {
    this._sidebarFilters = { ...this._sidebarFilters, ...filters };
    this.render();
  }

  // Set filters without re-rendering (caller renders).
  setFilters(filters) {
    this._sidebarFilters = { ...this._sidebarFilters, ...filters };
  }

  setStructuralEdgeTypes(types) {
    this._structuralEdgeTypes = types instanceof Set ? types : new Set(types || []);
  }

  setEdgeCategoryLookup(lookup) {
    this._edgeCategoryLookup = lookup || {};
  }

  // --- Data (shared by render + export) ---

  _getViewData() {
    const f = this._sidebarFilters;
    const config = {
      layers: (f.layers && f.layers.length) ? f.layers : [...this._store.layers],
      nodeTypes: null, // no whitelist — All mode is the full graph
      hideStructural: !!f.hideStructural,
      structuralEdgeTypes: this._structuralEdgeTypes,
      hiddenEdgeTypes: f.hiddenEdgeTypes || [],
      hiddenNodeTypes: f.hiddenNodeTypes || [],
      hiddenDomains: f.hiddenDomains || [],
      minConfidence: f.minConfidence || 0,
      repoFilter: f.repoFilter || '',
      focusNodeIds: null,
    };
    return filterGraph(this._store, config);
  }

  // --- Rendering ---

  render() {
    if (!this._renderer) return;

    const { nodes: filteredNodes, edges } = this._getViewData();
    const groupField = this._sidebarFilters.groupBy || null;

    // Infra is shared — never grouped into a domain box
    const nodes = filteredNodes.map(n => ({
      ...n,
      _group: (groupField && n.layer !== 'infra') ? (n[groupField] || null) : null,
    }));

    const self = this;
    this._renderer.render({
      nodes,
      edges,
      groups: groupField ? { field: '_group' } : null,
      layout: 'dagre',
      marks: {},
      callbacks: {
        onClick: (d) => {
          self._selection.selectNode(d.node_id || d.id);
          self._onStateChange({ selectedNode: d });
        },
        onBackgroundClick: () => {
          self._selection.clear();
          self._renderer.clearHighlight();
          self._onStateChange({ selectedNode: null });
        },
      },
      edgeCategoryLookup: this._edgeCategoryLookup || {},
      showEdgeLabels: true,
    });

    // Validation hook: machine-readable snapshot of the rendered All-mode set.
    window.__chronicleViewState = {
      mode: 'all',
      preset: 'all',
      spec: null,
      filters: { ...this._sidebarFilters },
      nodes: nodes.map(n => ({ key: n.node_key, name: n.name, type: n.node_type, layer: n.layer, group: n._group || null })),
      edges: edges.map(e => ({ from: e.from_node_key, to: e.to_node_key, kind: e.edge_type })),
      groups: groupField ? [...new Set(nodes.map(n => n._group).filter(Boolean))] : [],
      ts: Date.now(),
    };
  }

  // --- Export ---

  exportText() {
    const { nodes, edges } = this._getViewData();

    const nodeById = {};
    nodes.forEach(n => { nodeById[n.node_id] = n; });

    const byGroup = {};
    nodes.forEach(n => {
      const gk = n.layer === 'infra' ? '_infrastructure' : (n.domain_key || '_unassigned');
      if (!byGroup[gk]) byGroup[gk] = [];
      byGroup[gk].push(n);
    });

    let text = `# Graph Export (${nodes.length} nodes, ${edges.length} edges)\n`;
    text += '# Mode: explore (all)\n\n';

    Object.keys(byGroup).sort().forEach(gk => {
      text += `## ${gk}\n`;
      byGroup[gk].sort((a, b) => (a.node_type + a.name).localeCompare(b.node_type + b.name)).forEach(n => {
        text += `  ${n.node_type}: ${n.name || n.node_key}${n.layer !== 'code' ? ' [' + n.layer + ']' : ''}\n`;
      });
      text += '\n';
    });

    text += '## Edges\n';
    edges.forEach(e => {
      const from = nodeById[e.from_node_id];
      const to = nodeById[e.to_node_id];
      if (from && to) {
        text += `  ${from.name || from.node_key} --${e.edge_type}--> ${to.name || to.node_key}\n`;
      }
    });

    return text;
  }
}
