// graph-presets.js — Registry-driven preset configurations.
// No hardcoded type lists. Presets select layers, the registry defines what's in each layer.

const PRESET_DEFS = {
  // C1: system context — the system as one box + external systems + infra (no grouping — containers ARE domains)
  c1:       { layers: ['service', 'infra'], nodeTypes: ['container', 'external_system', 'broker', 'database', 'cache', 'queue', 'infrastructure'], hideStructural: true, groupByField: null },
  // C2: container — services + messaging + infra (how services communicate, no individual endpoints)
  c2:       { layers: ['service', 'contract', 'infra'], nodeTypes: ['service', 'worker', 'deployment', 'external_system', 'topic', 'async_channel', 'broker', 'database', 'cache', 'queue', 'infrastructure'], hideStructural: true, groupByField: 'domain_key' },
  // C3: components — all code + contracts + data + flow
  c3:       { layers: ['code', 'contract', 'data', 'flow'], hideStructural: true, groupByField: 'domain_key' },
  all:      { layers: null, hideStructural: false, groupByField: 'domain_key' },
  data:     { layers: ['data'], hideStructural: false, groupByField: 'domain_key' },
  api:      { layers: ['contract', 'service'], hideStructural: true, groupByField: null },
  services: { layers: ['service', 'code'], hideStructural: true, groupByField: null },
  flows:    { layers: ['contract', 'flow'], hideStructural: false, groupByField: null },
};

export class GraphPresets {
  constructor(registry) {
    this._registry = registry;
    this._allLayers = registry.layers || [];
    this._structuralEdgeTypes = new Set(
      (registry.traversal_policy && registry.traversal_policy.structural_edge_types) || []
    );
  }

  getPreset(name) {
    const def = PRESET_DEFS[name];
    if (!def) return this.getPreset('all');

    return {
      layers: def.layers || [...this._allLayers],
      nodeTypes: def.nodeTypes ? new Set(def.nodeTypes) : null, // null = show all types in selected layers
      hideStructural: def.hideStructural,
      groupByField: def.groupByField,
      structuralEdgeTypes: this._structuralEdgeTypes,
    };
  }

  get structuralEdgeTypes() {
    return this._structuralEdgeTypes;
  }

  get allLayers() {
    return this._allLayers;
  }

  get availablePresets() {
    return Object.keys(PRESET_DEFS);
  }
}
