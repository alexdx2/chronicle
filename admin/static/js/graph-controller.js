// graph-controller.js — Orchestrator. Bridges modules into Alpine.
// Only module that talks to Alpine (via callbacks, not imports).

import { GraphStore } from './graph-store.js';
import { GraphPresets } from './graph-presets.js';
import { GraphRenderer } from './graph-renderer.js';
import { WorkspaceManager } from './workspace-manager.js';
import { filterGraph } from './graph-filter.js';

export class GraphController {
  constructor({ onStateChange } = {}) {
    this.store = new GraphStore();
    this.presets = null;
    this.renderer = null;
    this.workspace = null;
    this._onStateChange = onStateChange || (() => {});
    this._currentPresetName = 'all';
    this._currentPreset = null;
    this._sidebarState = {};
    this._focusNodeIds = null;
    this._layoutMode = 'dagre';
    this._edgeCategoryLookup = {};
    this._showEdgeLabels = true;
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
    this.workspace = new WorkspaceManager(this.store);
    this._currentPreset = this.presets.getPreset('all');
  }

  async loadGraph(graphApiResponse) {
    this.store.load(graphApiResponse);
    this.workspace.clear();
    this.render();
  }

  setPreset(name) {
    this._currentPresetName = name;
    this._currentPreset = this.presets.getPreset(name);
    this._focusNodeIds = null;
    this.workspace.clear();
    this._onStateChange({ preset: name, focus: null });
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

  clearFocus() {
    this._focusNodeIds = null;
    this._onStateChange({ focus: null });
    this.render();
  }

  handleDrop(nodeId, x, y) {
    this.workspace.dropNode(nodeId, x, y);
    this.render();
  }

  handleExpand(nodeId) {
    this.workspace.expandNode(nodeId);
    this.render();
  }

  clearWorkspace() {
    this.workspace.clear();
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

    if (this.workspace.isInvestigating) {
      const view = this.workspace.getViewData();
      this.renderer.render({
        nodes: view.nodes,
        edges: view.edges,
        marks: view.marks,
        layout: 'cached',
        positionCache: view.positionCache,
        groups: this._currentPreset.groupByField
          ? { field: this._currentPreset.groupByField }
          : null,
        callbacks: this._makeCallbacks(),
        edgeCategoryLookup: this._edgeCategoryLookup,
        showEdgeLabels: this._showEdgeLabels,
      });
    } else {
      const config = {
        ...this._currentPreset,
        ...this._sidebarState,
        focusNodeIds: this._focusNodeIds,
      };
      const { nodes, edges } = filterGraph(this.store, config);
      this.renderer.render({
        nodes: nodes.map(n => ({
          ...n,
          _group: this._currentPreset.groupByField
            ? (n[this._currentPreset.groupByField] || null)
            : null,
        })),
        edges,
        marks: {},
        layout: this._layoutMode,
        groups: this._currentPreset.groupByField
          ? { field: this._currentPreset.groupByField }
          : null,
        callbacks: this._makeCallbacks(),
        edgeCategoryLookup: this._edgeCategoryLookup,
        showEdgeLabels: this._showEdgeLabels,
      });
    }
  }

  exportGraphText() {
    let nodes, edges;
    if (this.workspace.isInvestigating) {
      const view = this.workspace.getViewData();
      nodes = view.nodes;
      edges = view.edges;
    } else {
      const config = { ...this._currentPreset, ...this._sidebarState, focusNodeIds: this._focusNodeIds };
      const filtered = filterGraph(this.store, config);
      nodes = filtered.nodes;
      edges = filtered.edges;
    }

    const nodeById = {};
    nodes.forEach(n => { nodeById[n.node_id] = n; });

    const byDomain = {};
    nodes.forEach(n => {
      const dk = n.domain_key || '_unassigned';
      if (!byDomain[dk]) byDomain[dk] = [];
      byDomain[dk].push(n);
    });

    let text = `# Graph Export (${nodes.length} nodes, ${edges.length} edges)\n`;
    text += `# Preset: ${this._currentPresetName} | Mode: ${this.workspace.isInvestigating ? 'investigation' : 'overview'}\n\n`;

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
    params.set('mode', 'workspace');
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
      onDrag: (id, x, y) => this.workspace.updatePosition(id, x, y),
      onBackgroundClick: () => {
        this.renderer.clearHighlight();
        this._onStateChange({ selectedNode: null });
      },
      onExpand: (id) => this.handleExpand(id),
    };
  }
}
