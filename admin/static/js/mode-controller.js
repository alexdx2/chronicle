// mode-controller.js — Graph-tab orchestrator for Explore (All) mode.
// Bridges the store/renderer/explore modules into Alpine via the
// onStateChange callback. Workspace/Investigate modes were removed —
// path/impact analysis lives in the view-mode node pivots.

import { GraphStore } from './graph-store.js';
import { GraphRenderer } from './graph-renderer.js';
import { SelectionState } from './selection-state.js';
import { ExploreController } from './explore-controller.js';

export class ModeController {
  constructor({ onStateChange } = {}) {
    this.store = new GraphStore();
    this.renderer = null;
    this.selection = null;
    this.explore = null;
    this._onStateChange = onStateChange || (() => {});
    this._edgeCategoryLookup = {};
  }

  async init(containerEl) {
    this.renderer = new GraphRenderer(containerEl);
    this.selection = new SelectionState({
      onChange: (state) => this._onStateChange(state),
    });

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
  }

  loadGraph(apiResponse) {
    this.store.load(apiResponse);
    if (this.explore) this.explore.setEdgeCategoryLookup(this._edgeCategoryLookup);
    this.render();
  }

  setEdgeCategoryLookup(lookup) {
    this._edgeCategoryLookup = lookup || {};
    if (this.explore) this.explore.setEdgeCategoryLookup(lookup);
  }

  get mode() { return 'explore'; }

  setMode(mode) {
    // Only Explore remains; kept for call-site compatibility.
    this._onStateChange({ mode: 'explore' });
    this.render();
  }

  render() {
    if (!this.explore) return;
    this.explore.render();
  }

  exportText() {
    if (!this.explore) return '';
    return this.explore.exportText();
  }
}
