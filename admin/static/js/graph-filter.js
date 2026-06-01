// graph-filter.js — Pure filter function. No state, no side effects.
// Takes store data + config → returns filtered { nodes, edges }.

export function filterGraph(store, config) {
  const layerSet = new Set(config.layers);
  const hiddenDomains = new Set(config.hiddenDomains || []);
  const hiddenEdgeTypes = new Set(config.hiddenEdgeTypes || []);
  const hiddenNodeTypes = new Set(config.hiddenNodeTypes || []);
  const minConfidence = config.minConfidence || 0;

  let nodes = store.nodes;

  // 1. Layer
  nodes = nodes.filter(n => layerSet.has(n.layer));

  // 2. Node type whitelist (from preset — e.g. C1 only shows modules, not providers)
  if (config.nodeTypes) {
    nodes = nodes.filter(n => config.nodeTypes.has(n.node_type));
  }

  // 3. Repo
  if (config.repoFilter) {
    nodes = nodes.filter(n => !n.repo_name || n.repo_name === config.repoFilter);
  }

  // 4. Node type blacklist (from sidebar toggles)
  if (hiddenNodeTypes.size > 0) {
    nodes = nodes.filter(n => !hiddenNodeTypes.has(n.node_type));
  }

  // 4. Domain
  if (hiddenDomains.size > 0) {
    nodes = nodes.filter(n => !hiddenDomains.has(n.domain_key || '_unassigned'));
  }

  // 5. Focus
  if (config.focusNodeIds) {
    nodes = nodes.filter(n => config.focusNodeIds.has(n.node_id));
  }

  // 6. Confidence
  if (minConfidence > 0) {
    nodes = nodes.filter(n => (n.confidence || 1) >= minConfidence);
  }

  // 7. Edges
  const nodeIds = new Set(nodes.map(n => n.node_id));
  let edges = store.edges.filter(e =>
    nodeIds.has(e.from_node_id) && nodeIds.has(e.to_node_id)
  );

  if (config.hideStructural && config.structuralEdgeTypes) {
    edges = edges.filter(e => !config.structuralEdgeTypes.has(e.edge_type));
  }

  if (hiddenEdgeTypes.size > 0) {
    edges = edges.filter(e => !hiddenEdgeTypes.has(e.edge_type));
  }

  return { nodes, edges };
}
