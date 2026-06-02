// graph-store.js — Normalized graph data store with O(1) lookups.
// All IDs normalized to String on load. Single source of truth for graph data.

export class GraphStore {
  constructor() {
    this._nodes = [];
    this._edges = [];
    this._nodeById = new Map();
    this._edgesByNode = new Map();
    this._nodeTypes = new Set();
    this._layers = new Set();
    this._domains = new Set();
    this._repos = new Set();
  }

  load(apiResponse) {
    const rawNodes = apiResponse.nodes || [];
    const rawEdges = apiResponse.edges || [];

    this._nodes = rawNodes.map(n => ({
      ...n,
      node_id: String(n.node_id),
    }));

    this._edges = rawEdges.map(e => ({
      ...e,
      edge_id: e.edge_id != null ? String(e.edge_id) : undefined,
      from_node_id: String(e.from_node_id),
      to_node_id: String(e.to_node_id),
    }));

    this._nodeById = new Map();
    this._edgesByNode = new Map();
    this._nodeTypes = new Set();
    this._layers = new Set();
    this._domains = new Set();
    this._repos = new Set();

    for (const n of this._nodes) {
      this._nodeById.set(n.node_id, n);
      this._nodeTypes.add(n.node_type);
      if (n.layer) this._layers.add(n.layer);
      if (n.domain_key) this._domains.add(n.domain_key);
      if (n.repo_name) this._repos.add(n.repo_name);
    }

    for (const e of this._edges) {
      if (!this._edgesByNode.has(e.from_node_id)) {
        this._edgesByNode.set(e.from_node_id, { incoming: [], outgoing: [] });
      }
      if (!this._edgesByNode.has(e.to_node_id)) {
        this._edgesByNode.set(e.to_node_id, { incoming: [], outgoing: [] });
      }
      this._edgesByNode.get(e.from_node_id).outgoing.push(e);
      this._edgesByNode.get(e.to_node_id).incoming.push(e);
    }

    // Always reset hierarchy — even if API response has no hierarchy field
    this._loadHierarchy(apiResponse.hierarchy || { roots: [], links: [] });
  }

  _loadHierarchy(hierarchy) {
    this._hierarchyRoots = (hierarchy.roots || []).map(String);
    this._childrenOf = new Map();
    this._parentOf = new Map();
    this._hierarchyLinksByChild = new Map();

    for (const link of (hierarchy.links || [])) {
      const parentId = String(link.parentId);
      const childId = String(link.childId);

      if (!this._childrenOf.has(parentId)) {
        this._childrenOf.set(parentId, []);
      }
      this._childrenOf.get(parentId).push(childId);
      this._parentOf.set(childId, parentId);
      this._hierarchyLinksByChild.set(childId, {
        parentId, childId,
        kind: link.kind, reason: link.reason, confidence: link.confidence,
      });
    }
  }

  get hierarchyRoots() { return this._hierarchyRoots || []; }

  getChildren(nodeId) {
    return this._childrenOf ? (this._childrenOf.get(String(nodeId)) || []) : [];
  }

  getParent(nodeId) {
    return this._parentOf ? (this._parentOf.get(String(nodeId)) || null) : null;
  }

  hasChildren(nodeId) {
    const children = this._childrenOf ? this._childrenOf.get(String(nodeId)) : null;
    return children != null && children.length > 0;
  }

  getHierarchyLink(childNodeId) {
    if (!this._hierarchyLinksByChild) return null;
    return this._hierarchyLinksByChild.get(String(childNodeId)) || null;
  }

  getNode(id) {
    return this._nodeById.get(String(id)) || null;
  }

  getEdges(nodeId) {
    const id = String(nodeId);
    return this._edgesByNode.get(id) || { incoming: [], outgoing: [] };
  }

  getNeighbors(nodeId) {
    const id = String(nodeId);
    const result = new Set();
    const edges = this._edgesByNode.get(id);
    if (!edges) return result;
    for (const e of edges.outgoing) result.add(e.to_node_id);
    for (const e of edges.incoming) result.add(e.from_node_id);
    return result;
  }

  getEdgesBetween(nodeIds) {
    const idSet = nodeIds instanceof Set ? nodeIds : new Set(nodeIds.map(String));
    return this._edges.filter(e => idSet.has(e.from_node_id) && idSet.has(e.to_node_id));
  }

  get nodes() { return this._nodes; }
  get edges() { return this._edges; }
  get nodeTypes() { return this._nodeTypes; }
  get layers() { return this._layers; }
  get domains() { return this._domains; }
  get repos() { return this._repos; }
}
