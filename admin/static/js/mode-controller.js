// mode-controller.js — Top-level orchestrator.
// Manages mode switching between Explore and Investigate.
// Bridges modules into Alpine via onStateChange callback.

import { GraphStore } from './graph-store.js';
import { GraphRenderer } from './graph-renderer.js';
import { WorkspaceManager } from './workspace-manager.js';
import { SelectionState } from './selection-state.js';
import { ExploreController } from './explore-controller.js';
import { InvestigateController } from './investigate-controller.js';

export class ModeController {
  constructor({ onStateChange } = {}) {
    this.store = new GraphStore();
    this.renderer = null;
    this.selection = null;
    this.explore = null;
    this.investigate = null;
    this._onStateChange = onStateChange || (() => {});
    this._mode = 'explore';
    this._edgeCategoryLookup = {};
  }

  async init(containerEl) {
    this.renderer = new GraphRenderer(containerEl);
    this.selection = new SelectionState({
      onChange: (state) => this._onStateChange(state),
    });

    const workspace = new WorkspaceManager(this.store);

    // Fetch registry for structural edge types
    let structuralEdgeTypes = new Set();
    try {
      const res = await fetch('/api/registry');
      const registry = await res.json();
      if (registry.traversal_policy && registry.traversal_policy.structural_edge_types) {
        structuralEdgeTypes = new Set(registry.traversal_policy.structural_edge_types);
      }
    } catch {}

    this.explore = new ExploreController({
      store: this.store,
      renderer: this.renderer,
      selection: this.selection,
      onStateChange: (state) => this._onStateChange(state),
    });
    this.explore.setStructuralEdgeTypes(structuralEdgeTypes);

    this.investigate = new InvestigateController({
      store: this.store,
      renderer: this.renderer,
      workspace,
      selection: this.selection,
      onStateChange: (state) => this._onStateChange(state),
    });
  }

  loadGraph(apiResponse) {
    this.store.load(apiResponse);
    if (this.explore) this.explore.setEdgeCategoryLookup(this._edgeCategoryLookup);
    if (this.investigate) this.investigate.setEdgeCategoryLookup(this._edgeCategoryLookup);
    this.render();
  }

  setEdgeCategoryLookup(lookup) {
    this._edgeCategoryLookup = lookup || {};
    if (this.explore) this.explore.setEdgeCategoryLookup(lookup);
    if (this.investigate) this.investigate.setEdgeCategoryLookup(lookup);
  }

  get mode() { return this._mode; }

  setMode(mode) {
    if (mode !== 'explore' && mode !== 'investigate') return;
    this._mode = mode;

    if (mode === 'investigate') {
      this.investigate.applyPendingNodes();
    }

    this._onStateChange({ mode });
    this.render();
  }

  get activeController() {
    return this._mode === 'explore' ? this.explore : this.investigate;
  }

  render() {
    if (!this.activeController) return;
    this.activeController.render();
  }

  exportText() {
    if (!this.activeController) return '';
    return this.activeController.exportText();
  }

  addToInvestigation(nodeId) {
    this.investigate.queueNode(nodeId);
    this._onStateChange({ investigateQueued: true });
  }

  investigateScope(selectedNodeId = null) {
    const scopeNodes = this.explore.getScopeNodes();
    if (selectedNodeId) {
      const node = this.store.getNode(selectedNodeId);
      if (node && !scopeNodes.some(n => n.node_id === node.node_id)) {
        scopeNodes.unshift(node);
      }
    }
    if (scopeNodes.length > 30) {
      return { tooMany: true, count: scopeNodes.length };
    }
    this._mode = 'investigate';
    this.investigate.populateFromScope(scopeNodes);
    this._onStateChange({ mode: 'investigate' });
    return { tooMany: false };
  }
}
