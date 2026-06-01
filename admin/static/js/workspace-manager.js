// workspace-manager.js — Workspace investigation logic.
// Uses raw GraphStore data, ignores preset filters.
// Manages drag-drop state, BFS path finding, neighbor expansion.

export class WorkspaceManager {
  constructor(store) {
    this._store = store;
    this._nodes = [];
    this._paths = [];
    this._positions = {};
    this._expanded = new Set();
  }

  get droppedNodes() {
    return this._nodes.filter(n => n.manual);
  }

  get isInvestigating() {
    return this._nodes.some(n => n.manual);
  }

  dropNode(nodeId, x, y) {
    const id = String(nodeId);
    if (this._nodes.some(n => n.node_id === id)) return;
    this._nodes.push({ node_id: id, x, y, manual: true });
    this._positions[id] = { x, y };
    this.findPaths();
  }

  removeNode(nodeId) {
    const id = String(nodeId);
    this._nodes = this._nodes.filter(n => n.node_id !== id);
    delete this._positions[id];
    this.findPaths();
  }

  clear() {
    this._nodes = [];
    this._paths = [];
    this._positions = {};
    this._expanded = new Set();
  }

  findPaths() {
    const manualNodes = this.droppedNodes;
    this._paths = [];
    if (manualNodes.length < 2) return;

    for (let i = 0; i < manualNodes.length; i++) {
      for (let j = i + 1; j < manualNodes.length; j++) {
        const path = this._bfs(manualNodes[i].node_id, manualNodes[j].node_id);
        if (path) {
          this._paths.push({ from: manualNodes[i].node_id, to: manualNodes[j].node_id, path });
        }
      }
    }
  }

  expandNode(nodeId) {
    const id = String(nodeId);
    this._expanded.add(id);

    const existing = this._nodes.find(n => n.node_id === id);
    if (!existing) {
      this._nodes.push({ node_id: id, x: 400, y: 300, manual: true });
      this._positions[id] = { x: 400, y: 300 };
    } else if (!existing.manual) {
      existing.manual = true;
    }

    const neighbors = this._store.getNeighbors(id);
    const existingIds = new Set(this._nodes.map(n => n.node_id));
    const source = this._positions[id] || { x: 400, y: 300 };
    let added = 0;
    const total = neighbors.size;

    for (const nId of neighbors) {
      if (!existingIds.has(nId) && this._store.getNode(nId)) {
        const angle = (added / total) * Math.PI * 2;
        const x = source.x + Math.cos(angle) * 120;
        const y = source.y + Math.sin(angle) * 120;
        this._nodes.push({ node_id: nId, x, y, manual: false });
        this._positions[nId] = { x, y };
        added++;
      }
    }

    this.findPaths();
  }

  getViewData() {
    const manualIds = new Set(this.droppedNodes.map(n => n.node_id));
    const pathIds = new Set();
    const pathEdgeKeys = new Set();

    for (const p of this._paths) {
      for (const nId of p.path) pathIds.add(nId);
      for (let i = 0; i < p.path.length - 1; i++) {
        pathEdgeKeys.add(p.path[i] + '->' + p.path[i + 1]);
        pathEdgeKeys.add(p.path[i + 1] + '->' + p.path[i]);
      }
    }

    const allIds = new Set(this._nodes.map(n => n.node_id));
    pathIds.forEach(id => allIds.add(id));

    const nodes = [];
    for (const id of allIds) {
      const n = this._store.getNode(id);
      if (!n) continue;
      nodes.push({
        ...n,
        _group: n.domain_key || null,
        _manual: manualIds.has(id),
        _path: pathIds.has(id) && !manualIds.has(id),
        _neighbor: !manualIds.has(id) && !pathIds.has(id),
      });
    }

    const edges = this._store.getEdgesBetween(allIds);

    const marks = {
      manual: manualIds,
      path: new Set([...pathIds].filter(id => !manualIds.has(id))),
      neighbor: new Set(nodes.filter(n => n._neighbor).map(n => n.node_id)),
    };

    return { nodes, edges, marks, positionCache: { ...this._positions } };
  }

  updatePosition(nodeId, x, y) {
    const id = String(nodeId);
    this._positions[id] = { x, y };
    const n = this._nodes.find(n => n.node_id === id);
    if (n) { n.x = x; n.y = y; }
  }

  _bfs(fromId, toId) {
    const adj = new Map();
    for (const e of this._store.edges) {
      if (!adj.has(e.from_node_id)) adj.set(e.from_node_id, []);
      if (!adj.has(e.to_node_id)) adj.set(e.to_node_id, []);
      adj.get(e.from_node_id).push(e.to_node_id);
      adj.get(e.to_node_id).push(e.from_node_id);
    }

    const queue = [[fromId]];
    const visited = new Set([fromId]);
    while (queue.length > 0) {
      const path = queue.shift();
      const current = path[path.length - 1];
      if (current === toId) return path;
      for (const neighbor of (adj.get(current) || [])) {
        if (!visited.has(neighbor)) {
          visited.add(neighbor);
          queue.push([...path, neighbor]);
        }
      }
    }
    return null;
  }
}
