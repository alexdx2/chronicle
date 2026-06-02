// investigate-controller.js — Free-form investigation canvas.
// Wraps WorkspaceManager. No hierarchy constraints — drag any entity from any level.

export class InvestigateController {
  constructor({ store, renderer, workspace, selection, onStateChange }) {
    this._store = store;
    this._renderer = renderer;
    this._workspace = workspace;
    this._selection = selection;
    this._onStateChange = onStateChange || (() => {});

    this._filters = {
      hiddenNodeTypes: [],
      hiddenEdgeTypes: [],
      hiddenDomains: [],
      minConfidence: 0,
    };

    this._pendingNodes = [];
    this._edgeCategoryLookup = {};
  }

  get isInvestigating() { return this._workspace.isInvestigating; }

  handleDrop(nodeId, x, y) {
    this._workspace.dropNode(nodeId, x, y);
    this.render();
  }

  handleExpand(nodeId) {
    this._workspace.expandNode(nodeId);
    this.render();
  }

  clear() {
    this._workspace.clear();
    this.render();
  }

  queueNode(nodeId) {
    const id = String(nodeId);
    if (!this._pendingNodes.includes(id)) {
      this._pendingNodes.push(id);
    }
  }

  populateFromScope(scopeNodes) {
    const count = scopeNodes.length;
    if (count > 30) return false;
    const cx = 400, cy = 300;
    scopeNodes.forEach((n, i) => {
      const angle = (i / count) * Math.PI * 2;
      this._workspace.dropNode(n.node_id, cx + Math.cos(angle) * 200, cy + Math.sin(angle) * 200);
    });
    this.render();
    return true;
  }

  applyPendingNodes() {
    if (this._pendingNodes.length === 0) return;
    const cx = 400, cy = 300;
    this._pendingNodes.forEach((id, i) => {
      const angle = (i / this._pendingNodes.length) * Math.PI * 2;
      this._workspace.dropNode(id, cx + Math.cos(angle) * 150, cy + Math.sin(angle) * 150);
    });
    this._pendingNodes = [];
    this.render();
  }

  updateFilters(filters) {
    this._filters = { ...this._filters, ...filters };
    this.render();
  }

  render() {
    if (!this._renderer) return;

    if (!this._workspace.isInvestigating) {
      this._renderer.render({
        nodes: [], edges: [],
        callbacks: this._makeCallbacks(),
      });
      return;
    }

    const view = this._workspace.getViewData();

    const hiddenNodeTypes = new Set(this._filters.hiddenNodeTypes || []);
    const hiddenEdgeTypes = new Set(this._filters.hiddenEdgeTypes || []);
    const hiddenDomains = new Set(this._filters.hiddenDomains || []);

    let nodes = view.nodes;
    if (hiddenNodeTypes.size > 0) {
      nodes = nodes.filter(n => !hiddenNodeTypes.has(n.node_type));
    }
    if (hiddenDomains.size > 0) {
      nodes = nodes.filter(n => !hiddenDomains.has(n.domain_key || '_unassigned'));
    }

    const nodeIds = new Set(nodes.map(n => n.node_id));
    let edges = view.edges.filter(e =>
      nodeIds.has(e.from_node_id) && nodeIds.has(e.to_node_id)
    );
    if (hiddenEdgeTypes.size > 0) {
      edges = edges.filter(e => !hiddenEdgeTypes.has(e.edge_type));
    }

    this._renderer.render({
      nodes,
      edges,
      marks: view.marks,
      layout: 'cached',
      positionCache: view.positionCache,
      groups: null,
      callbacks: this._makeCallbacks(),
      edgeCategoryLookup: this._edgeCategoryLookup || {},
      showEdgeLabels: true,
    });
  }

  setEdgeCategoryLookup(lookup) {
    this._edgeCategoryLookup = lookup || {};
  }

  exportText() {
    if (!this._workspace.isInvestigating) return '# Empty investigation\n';

    const view = this._workspace.getViewData();
    const nodes = view.nodes;
    const edges = view.edges;
    const nodeById = {};
    nodes.forEach(n => { nodeById[n.node_id] = n; });

    let text = `# Investigation (${nodes.length} nodes, ${edges.length} edges)\n\n`;

    nodes.sort((a, b) => (a.node_type + a.name).localeCompare(b.node_type + b.name)).forEach(n => {
      text += `  ${n.node_type}: ${n.name || n.node_key}${n.layer !== 'code' ? ' [' + n.layer + ']' : ''}\n`;
    });

    text += '\n## Edges\n';
    edges.forEach(e => {
      const from = nodeById[e.from_node_id];
      const to = nodeById[e.to_node_id];
      if (from && to) {
        text += `  ${from.name || from.node_key} --${e.edge_type}--> ${to.name || to.node_key}\n`;
      }
    });

    return text;
  }

  _makeCallbacks() {
    const self = this;
    return {
      onClick: (d) => {
        self._selection.selectNode(d.node_id || d.id);
        self._onStateChange({ selectedNode: d });
      },
      onDblClick: () => {},
      onDrag: (id, x, y) => self._workspace.updatePosition(id, x, y),
      onBackgroundClick: () => {
        self._selection.clear();
        self._renderer.clearHighlight();
        self._onStateChange({ selectedNode: null });
      },
      onExpand: (id) => self.handleExpand(id),
    };
  }
}
