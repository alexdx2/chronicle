// selection-state.js — Shared selection/highlight state for both Explore and Investigate modes.

export class SelectionState {
  constructor({ onChange } = {}) {
    this._selectedNodeId = null;
    this._selectedEdgeId = null;
    this._onChange = onChange || (() => {});
  }

  get selectedNodeId() { return this._selectedNodeId; }
  get selectedEdgeId() { return this._selectedEdgeId; }

  selectNode(nodeId) {
    this._selectedNodeId = nodeId ? String(nodeId) : null;
    this._selectedEdgeId = null;
    this._onChange({ selectedNodeId: this._selectedNodeId });
  }

  selectEdge(edgeId) {
    this._selectedEdgeId = edgeId ? String(edgeId) : null;
    this._selectedNodeId = null;
    this._onChange({ selectedEdgeId: this._selectedEdgeId });
  }

  clear() {
    this._selectedNodeId = null;
    this._selectedEdgeId = null;
    this._onChange({ selectedNodeId: null, selectedEdgeId: null });
  }
}
