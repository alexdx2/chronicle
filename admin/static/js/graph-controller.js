// graph-controller.js — Orchestrator. Bridges modules into Alpine.
// Only module that talks to Alpine (via callbacks, not imports).

import { GraphStore } from './graph-store.js';
import { GraphPresets } from './graph-presets.js';
import { GraphRenderer } from './graph-renderer.js';
import { filterGraph } from './graph-filter.js';

export class GraphController {
  constructor({ onStateChange } = {}) {
    this.store = new GraphStore();
    this.presets = null;
    this.renderer = null;
    this._onStateChange = onStateChange || (() => {});
    this._currentPresetName = 'all';
    this._currentPreset = null;
    this._sidebarState = {};
    this._focusNodeIds = null;
    this._layoutMode = 'dagre';
    this._edgeCategoryLookup = {};
    this._showEdgeLabels = true;
    this._projectPath = '';
  }

  async init(containerEl) {
    let registry;
    try {
      const res = await fetch('/api/registry');
      registry = await res.json();
    } catch {
      registry = { layers: [], traversal_policy: { structural_edge_types: [] } };
    }

    this.presets = new GraphPresets(registry);
    this.renderer = new GraphRenderer(containerEl);
    this._currentPreset = this.presets.getPreset('all');
  }

  async loadGraph(graphApiResponse) {
    this.store.load(graphApiResponse);
    this.render();
  }

  setPreset(name) {
    this._currentPresetName = name;
    this._currentPreset = this.presets.getPreset(name);
    this._focusNodeIds = null;
    this._onStateChange({ preset: name, focus: null });
    // Clear focus from URL so it doesn't re-apply on next render
    try {
      const url = new URL(window.location);
      url.searchParams.delete('focus');
      url.searchParams.set('preset', name);
      history.replaceState(null, '', url);
    } catch {}
    this.render();
  }

  get currentPresetName() {
    return this._currentPresetName;
  }

  updateFilters(sidebarState) {
    this._sidebarState = sidebarState;
    this.render();
  }

  focusOn(nodeId) {
    const node = this.store.getNode(nodeId);
    if (!node) return;

    const focusIds = new Set([node.node_id]);
    const neighbors = this.store.getNeighbors(node.node_id);
    neighbors.forEach(id => focusIds.add(id));

    this._focusNodeIds = focusIds;
    this._onStateChange({ focus: node.name || node.node_id });
    this.render();
  }

  focusOnDomain(domainKey) {
    // Show all nodes in this domain + their neighbors (for cross-domain edges)
    const domainNodes = this.store.nodes.filter(n => n.domain_key === domainKey);
    if (domainNodes.length === 0) return;

    const focusIds = new Set();
    for (const n of domainNodes) {
      focusIds.add(n.node_id);
      // Include neighbors so cross-domain edges show
      const neighbors = this.store.getNeighbors(n.node_id);
      neighbors.forEach(id => focusIds.add(id));
    }

    this._focusNodeIds = focusIds;
    this._onStateChange({ focus: domainKey });
    this.render();
  }

  clearFocus() {
    this._focusNodeIds = null;
    this._onStateChange({ focus: null });
    this.render();
  }

  setLayout(mode) {
    this._layoutMode = mode;
    this.render();
  }

  setEdgeCategoryLookup(lookup) {
    this._edgeCategoryLookup = lookup || {};
  }

  setShowEdgeLabels(show) {
    this._showEdgeLabels = show;
    this.render();
  }

  render() {
    if (!this.renderer || !this.presets) return;

    // Always start with the preset-filtered overview
    const config = {
      ...this._currentPreset,
      ...this._sidebarState,
      // Preserve preset's nodeTypes whitelist (sidebar can't override it)
      nodeTypes: this._currentPreset.nodeTypes,
      focusNodeIds: this._focusNodeIds,
    };
    const filtered = filterGraph(this.store, config);
    const groupField = this._currentPreset.groupByField;

    let nodes = filtered.nodes.map(n => ({
      ...n,
      // Infra nodes are shared resources — never grouped into a domain
      _group: (groupField && n.layer !== 'infra') ? (n[groupField] || null) : null,
    }));
    let edges = filtered.edges;
    let marks = {};
    let layout = this._layoutMode;
    let positionCache = {};

    this.renderer.render({
      nodes,
      edges,
      marks,
      layout,
      positionCache,
      groups: groupField ? { field: groupField } : null,
      callbacks: this._makeCallbacks(),
      edgeCategoryLookup: this._edgeCategoryLookup,
      showEdgeLabels: this._showEdgeLabels,
    });
  }

  exportGraphText() {
    // Export the same merged view that render() shows
    const config = { ...this._currentPreset, ...this._sidebarState, nodeTypes: this._currentPreset.nodeTypes, focusNodeIds: this._focusNodeIds };
    const filtered = filterGraph(this.store, config);
    let nodes = [...filtered.nodes];
    let edges = [...filtered.edges];

    const nodeById = {};
    nodes.forEach(n => { nodeById[n.node_id] = n; });

    const byDomain = {};
    nodes.forEach(n => {
      // Infra is shared — group separately, not under any domain
      const dk = n.layer === 'infra' ? '_infrastructure' : (n.domain_key || '_unassigned');
      if (!byDomain[dk]) byDomain[dk] = [];
      byDomain[dk].push(n);
    });

    let text = `# Graph Export (${nodes.length} nodes, ${edges.length} edges)\n`;
    text += `# Preset: ${this._currentPresetName} | Mode: overview`;
    if (this._projectPath) text += ` | Project: ${this._projectPath}`;
    text += '\n\n';

    Object.keys(byDomain).sort().forEach(dk => {
      text += `## ${dk}\n`;
      byDomain[dk].sort((a, b) => (a.node_type + a.name).localeCompare(b.node_type + b.name)).forEach(n => {
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

  syncToUrl() {
    const params = new URLSearchParams(window.location.search);
    params.set('preset', this._currentPresetName);
    if (this._focusNodeIds) {
      const firstId = [...this._focusNodeIds][0];
      const node = this.store.getNode(firstId);
      if (node) params.set('focus', node.name || firstId);
    } else {
      params.delete('focus');
    }
    const newUrl = window.location.pathname + '?' + params.toString();
    window.history.replaceState(null, '', newUrl);
  }

  restoreFromUrl() {
    const params = new URLSearchParams(window.location.search);
    const preset = params.get('preset');
    if (preset && this.presets.availablePresets.includes(preset)) {
      this._currentPresetName = preset;
      this._currentPreset = this.presets.getPreset(preset);
      this._onStateChange({ preset });
    }
    const focus = params.get('focus');
    if (focus) {
      const node = this.store.nodes.find(n => n.name === focus);
      if (node) this.focusOn(node.node_id);
    }
  }

  _makeCallbacks() {
    return {
      onClick: (d, edges) => this._onStateChange({ selectedNode: d }),
      onDblClick: (d) => this._onStateChange({ zoomNode: d }),
      onBackgroundClick: () => {
        this.renderer.clearHighlight();
        this._onStateChange({ selectedNode: null });
      },
    };
  }
}
