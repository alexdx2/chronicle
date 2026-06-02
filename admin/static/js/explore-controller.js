// explore-controller.js — C4 drill-down via progressive filtering.
// Each drill level sets node type + domain filters automatically.
// Uses the same GraphFilter + GraphRenderer pipeline as Workspace.

import { filterGraph } from './graph-filter.js';

// What's visible at each drill depth
// Each level shows specific types. Shared infra/external always visible.
const SHARED_TYPES = ['external_system', 'broker', 'database', 'cache', 'queue', 'infrastructure'];
const LEVEL_CONFIG = [
  // C1: system context
  { nodeTypes: new Set(['container', ...SHARED_TYPES]), groupBy: 'domain_key', label: 'C1' },
  // C2: services, topics
  { nodeTypes: new Set(['service', 'topic', 'async_channel', ...SHARED_TYPES]), groupBy: 'domain_key', label: 'C2' },
  // C3: modules, endpoints, models
  { nodeTypes: new Set(['module', 'endpoint', 'topic', 'http_api', 'async_channel', 'model', 'enum', ...SHARED_TYPES]), groupBy: null, label: 'C3' },
  // C4: code internals
  { nodeTypes: new Set(['controller', 'provider', 'resolver', 'endpoint', 'topic', 'model', 'enum', 'use_case', ...SHARED_TYPES]), groupBy: null, label: 'C4' },
];

export class ExploreController {
  constructor({ store, renderer, selection, onStateChange }) {
    this._store = store;
    this._renderer = renderer;
    this._selection = selection;
    this._onStateChange = onStateChange || (() => {});

    this._scopeStack = []; // [{ nodeId, label, domainKey }]
    this._edgeCategoryLookup = {};
    this._structuralEdgeTypes = new Set();

    // User-adjustable filters (sidebar)
    this._sidebarFilters = {
      hiddenEdgeTypes: [],
      hiddenNodeTypes: [],
      hiddenDomains: [],
      minConfidence: 0,
    };
  }

  get breadcrumbs() {
    return [
      { nodeId: null, label: 'System', depth: 0 },
      ...this._scopeStack.map((s, i) => ({ nodeId: s.nodeId, label: s.label, depth: i + 1 })),
    ];
  }

  get currentDepthLabel() {
    const depth = this._scopeStack.length;
    return (LEVEL_CONFIG[depth] || LEVEL_CONFIG[LEVEL_CONFIG.length - 1]).label;
  }

  get scopeNodeId() {
    return this._scopeStack.length > 0 ? this._scopeStack[this._scopeStack.length - 1].nodeId : null;
  }

  // --- Navigation ---

  drillInto(nodeId) {
    const id = String(nodeId);
    const node = this._store.getNode(id);
    if (!node) return;

    this._scopeStack.push({
      nodeId: id,
      label: node.name || id,
      domainKey: node.domain_key,
    });

    this._onStateChange({ breadcrumbs: this.breadcrumbs });
    this.render();
  }

  navigateTo(breadcrumbIndex) {
    if (breadcrumbIndex <= 0) {
      this._scopeStack = [];
    } else {
      this._scopeStack = this._scopeStack.slice(0, breadcrumbIndex);
    }
    this._onStateChange({ breadcrumbs: this.breadcrumbs });
    this.render();
  }

  goToRoot() {
    this._scopeStack = [];
    this._onStateChange({ breadcrumbs: this.breadcrumbs });
    this.render();
  }

  // --- Filters ---

  updateFilters(filters) {
    this._sidebarFilters = { ...this._sidebarFilters, ...filters };
    this.render();
  }

  setStructuralEdgeTypes(types) {
    this._structuralEdgeTypes = types instanceof Set ? types : new Set(types || []);
  }

  // --- Rendering ---

  render() {
    if (!this._renderer) return;

    const depth = this._scopeStack.length;
    const levelCfg = LEVEL_CONFIG[Math.min(depth, LEVEL_CONFIG.length - 1)];
    const scope = this._scopeStack.length > 0 ? this._scopeStack[this._scopeStack.length - 1] : null;

    // Build filter config — combines level config + scope focus + sidebar
    const config = {
      layers: [...this._store.layers], // all layers
      nodeTypes: levelCfg.nodeTypes,
      hideStructural: true,
      structuralEdgeTypes: this._structuralEdgeTypes,
      hiddenEdgeTypes: this._sidebarFilters.hiddenEdgeTypes || [],
      hiddenNodeTypes: this._sidebarFilters.hiddenNodeTypes || [],
      hiddenDomains: this._sidebarFilters.hiddenDomains || [],
      minConfidence: this._sidebarFilters.minConfidence || 0,
      repoFilter: '',
      focusNodeIds: null,
    };

    // Apply domain focus if drilled into a domain-specific scope
    if (scope && scope.domainKey) {
      // Show nodes in this domain + infra (shared)
      const domainFocusIds = new Set();
      for (const n of this._store.nodes) {
        if (n.domain_key === scope.domainKey || n.layer === 'infra') {
          domainFocusIds.add(n.node_id);
        }
      }
      // Also include cross-domain nodes connected to this domain
      for (const n of this._store.nodes) {
        if (domainFocusIds.has(n.node_id)) continue;
        const edges = this._store.getEdges(n.node_id);
        const connected = [...edges.incoming, ...edges.outgoing].some(e =>
          domainFocusIds.has(e.from_node_id) || domainFocusIds.has(e.to_node_id)
        );
        if (connected) domainFocusIds.add(n.node_id);
      }
      config.focusNodeIds = domainFocusIds;
    }

    const { nodes: filteredNodes, edges: filteredEdges } = filterGraph(this._store, config);
    const filteredNodeIds = new Set(filteredNodes.map(n => n.node_id));

    // At C3+, include the parent scope node so its edges to children are visible
    // e.g. at C3 inside petshop-api, include petshop-api so service→endpoint edges show
    // At C2 this isn't needed — rollup edges connect services directly
    const refNodeIds = new Set();
    if (scope && depth >= 2) {
      const parentNode = this._store.getNode(scope.nodeId);
      if (parentNode && !filteredNodeIds.has(parentNode.node_id)) {
        refNodeIds.add(parentNode.node_id);
      }
    }
    const refNodes = [...refNodeIds].map(id => this._store.getNode(id)).filter(Boolean);
    const allNodes = [...filteredNodes, ...refNodes];
    const allNodeIds = new Set(allNodes.map(n => n.node_id));

    // Edges between all visible nodes (filtered + parent scope)
    const visibleEdges = this._store.getEdgesBetween(allNodeIds).filter(e => {
      if (e.edge_type === 'CONTAINS') return false;
      if (this._structuralEdgeTypes.has(e.edge_type)) return false;
      const hiddenET = new Set(this._sidebarFilters.hiddenEdgeTypes || []);
      return !hiddenET.has(e.edge_type);
    });

    // Group by — infra always outside
    const infraTypes = new Set(['broker', 'database', 'cache', 'queue', 'infrastructure']);
    const groupField = levelCfg.groupBy;
    const scopeLabel = scope ? scope.label : null;

    const sharedTypes = new Set([...infraTypes, 'external_system', 'topic', 'async_channel']);

    const nodes = allNodes.map(n => {
      const isShared = n.layer === 'infra' || sharedTypes.has(n.node_type);
      const isRef = refNodeIds.has(n.node_id);
      let group = null;
      if (!isShared && !isRef) {
        if (scopeLabel && depth >= 2) {
          group = n.domain_key === scope.domainKey ? scopeLabel : '_external';
        } else if (groupField) {
          group = n[groupField] || null;
        }
      }

      return {
        ...n,
        _group: group,
        _isRef: isRef,
        _drillable: !isRef && depth < LEVEL_CONFIG.length - 1,
      };
    });

    // Debug: expose on window — call window.__explore in console
    const _nb = {};
    allNodes.forEach(n => { _nb[n.node_id] = n; });
    window.__explore = {
      depth, level: levelCfg.label, scope: scope?.label,
      nodes: nodes.map(n => ({ name: n.name, type: n.node_type, layer: n.layer, domain: n.domain_key, group: n._group, ref: n._isRef, drillable: n._drillable })),
      edges: visibleEdges.map(e => ({ from: _nb[e.from_node_id]?.name || '?', to: _nb[e.to_node_id]?.name || '?', type: e.edge_type, derived: !!e.derived })),
      groups: [...new Set(nodes.map(n => n._group).filter(Boolean))],
      summary: `${levelCfg.label}: ${nodes.length} nodes, ${visibleEdges.length} edges, groups=[${[...new Set(nodes.map(n => n._group).filter(Boolean))].join(',')}]`,
    };
    console.log('[explore]', window.__explore.summary);
    console.table(window.__explore.nodes);
    if (visibleEdges.length) console.table(window.__explore.edges);

    // Marks: dim ref nodes
    const marks = {
      manual: new Set(),
      path: new Set(),
      neighbor: new Set(nodes.filter(n => n._isRef).map(n => n.node_id)),
    };

    // Determine if we need group boxes
    const groups = new Set(nodes.filter(n => n._group).map(n => n._group));
    const useGroups = groups.size > 0;

    const self = this;
    this._renderer.render({
      nodes,
      edges: visibleEdges,
      groups: useGroups ? { field: '_group' } : null,
      layout: 'dagre',
      marks,
      callbacks: {
        onClick: (d) => {
          self._selection.selectNode(d.node_id || d.id);
          self._onStateChange({ selectedNode: d });
        },
        onDblClick: (d) => {
          if (depth < LEVEL_CONFIG.length - 1) {
            self.drillInto(d.node_id || d.id);
          }
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
  }

  // --- Utilities ---

  setEdgeCategoryLookup(lookup) {
    this._edgeCategoryLookup = lookup || {};
  }

  getScopeNodes() {
    // For "Investigate this scope" — return currently visible nodes
    const depth = this._scopeStack.length;
    const levelCfg = LEVEL_CONFIG[Math.min(depth, LEVEL_CONFIG.length - 1)];
    return this._store.nodes.filter(n => levelCfg.nodeTypes.has(n.node_type));
  }

  // Get the current view data (same as what render() uses)
  _getViewData() {
    const depth = this._scopeStack.length;
    const levelCfg = LEVEL_CONFIG[Math.min(depth, LEVEL_CONFIG.length - 1)];
    const scope = this._scopeStack.length > 0 ? this._scopeStack[this._scopeStack.length - 1] : null;

    const config = {
      layers: [...this._store.layers],
      nodeTypes: levelCfg.nodeTypes,
      hideStructural: true,
      structuralEdgeTypes: this._structuralEdgeTypes,
      hiddenEdgeTypes: this._sidebarFilters.hiddenEdgeTypes || [],
      hiddenNodeTypes: this._sidebarFilters.hiddenNodeTypes || [],
      hiddenDomains: this._sidebarFilters.hiddenDomains || [],
      minConfidence: this._sidebarFilters.minConfidence || 0,
      repoFilter: '',
      focusNodeIds: null,
    };

    if (scope && scope.domainKey) {
      const domainFocusIds = new Set();
      for (const n of this._store.nodes) {
        if (n.domain_key === scope.domainKey || n.layer === 'infra') domainFocusIds.add(n.node_id);
      }
      for (const n of this._store.nodes) {
        if (domainFocusIds.has(n.node_id)) continue;
        const edges = this._store.getEdges(n.node_id);
        if ([...edges.incoming, ...edges.outgoing].some(e => domainFocusIds.has(e.from_node_id) || domainFocusIds.has(e.to_node_id))) {
          domainFocusIds.add(n.node_id);
        }
      }
      config.focusNodeIds = domainFocusIds;
    }

    const { nodes: filteredNodes } = filterGraph(this._store, config);
    const filteredNodeIds = new Set(filteredNodes.map(n => n.node_id));

    const refNodeIds = new Set();
    if (scope && depth >= 2) {
      const parentNode = this._store.getNode(scope.nodeId);
      if (parentNode && !filteredNodeIds.has(parentNode.node_id)) {
        refNodeIds.add(parentNode.node_id);
      }
    }
    const refNodes = [...refNodeIds].map(id => this._store.getNode(id)).filter(Boolean);
    const allNodes = [...filteredNodes, ...refNodes];
    const allNodeIds = new Set(allNodes.map(n => n.node_id));

    const edges = this._store.getEdgesBetween(allNodeIds).filter(e => {
      if (e.edge_type === 'CONTAINS') return false;
      if (this._structuralEdgeTypes.has(e.edge_type)) return false;
      const hiddenET = new Set(this._sidebarFilters.hiddenEdgeTypes || []);
      return !hiddenET.has(e.edge_type);
    });

    return { allNodes, edges, refNodeIds, scope, depth };
  }

  exportText() {
    const { allNodes, edges, refNodeIds, scope, depth } = this._getViewData();

    const nodeById = {};
    allNodes.forEach(n => { nodeById[n.node_id] = n; });

    const sharedTypes = new Set(['broker', 'database', 'cache', 'queue', 'infrastructure', 'external_system', 'topic', 'async_channel']);
    const scopeLabel = scope ? scope.label : null;

    const byGroup = {};
    allNodes.forEach(n => {
      const isShared = n.layer === 'infra' || sharedTypes.has(n.node_type);
      const isRef = refNodeIds.has(n.node_id);
      let gk;
      if (isRef) gk = '_ref (' + n.node_type + ')';
      else if (isShared) gk = '_shared';
      else if (scope && depth >= 2 && n.domain_key !== scope.domainKey) gk = '_external';
      else gk = scopeLabel || n.domain_key || '_unassigned';
      if (!byGroup[gk]) byGroup[gk] = [];
      byGroup[gk].push(n);
    });

    const path = this.breadcrumbs.map(b => b.label).join(' > ');
    let text = `# ${path} (${allNodes.length} nodes, ${edges.length} edges)\n`;
    text += `# Level: ${this.currentDepthLabel}\n\n`;

    Object.keys(byGroup).sort().forEach(gk => {
      text += `## ${gk}\n`;
      byGroup[gk].sort((a, b) => (a.node_type + a.name).localeCompare(b.node_type + b.name)).forEach(n => {
        const flags = [];
        if (refNodeIds.has(n.node_id)) flags.push('ref');
        if (n.layer !== 'code') flags.push(n.layer);
        text += `  ${n.node_type}: ${n.name || n.node_key}${flags.length ? ' [' + flags.join(', ') + ']' : ''}\n`;
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
