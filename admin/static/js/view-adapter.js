/* ════════════════════════════════════════════════════════
   ViewAdapter — view-algebra spec builders + View→GraphRenderer adapter.

   The backend view engine (POST /api/view) returns a View:
     { spec, title, groups[], nodes[], edges[], boundary, missing, trace }
   The dashboard's Graph tab renders through GraphRenderer
   (js/graph-renderer.js), which expects:
     nodes: [{node_id, node_key, name, node_type, layer, _group}]
     edges: [{from_node_id, to_node_id, edge_type}]
     groups: {field: '_group'} | null
     edgeCategoryLookup: EDGE_TYPE → {category, color, shortLabel}

   adaptView() maps one onto the other with ZERO changes to the
   renderer:
     - VGroups with visible members  → group boxes (renderer compound
       mechanism via node._group = group name)
     - VGroups with no visible members (collapse=true, e.g. C2 services)
       → synthesized nodes (the group IS the entity at that level);
       edges already reference the group key so they connect 1:1
     - VNodes → renderer nodes; layer kept verbatim so the existing
       layer→color styling applies unchanged
     - VEdges → renderer edges; kind maps onto the existing
       edge-category styling. weight>1 edges get a per-view overlay
       entry in the category lookup so the rendered short label gains
       "×N" while keeping the category color.
     - endpoints / uses_models ride along as _endpoints/_usesModels
       for the node-details panel (no new canvas visuals).

   Loaded as a classic script: window.ViewAdapter = {...}.
   ════════════════════════════════════════════════════════ */
(function () {
'use strict';

/* ── base64url(JSON) helpers (match Go base64.RawURLEncoding) ── */
function b64urlEncode(obj) {
  const s = JSON.stringify(obj);
  return btoa(unescape(encodeURIComponent(s)))
    .replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/, '');
}
function b64urlDecode(b) {
  b = String(b).replace(/-/g, '+').replace(/_/g, '/');
  while (b.length % 4) b += '=';
  return JSON.parse(decodeURIComponent(escape(atob(b))));
}

/* ── Preset spec builders (mirror graph/viewmodel/presets.go) ── */
function presetSpec(level, domain, target) {
  switch (level) {
    case 'c1':
      return { scope: { domain }, group: { by: 'domain' }, collapse: true,
               layout: { preset: 'c1' } };
    case 'c3':
      // No target → C3 across ALL services: domain-wide component view
      // grouped by service (mirrors presets.go).
      if (!target) {
        return { scope: { domain }, group: { by: 'service' }, collapse: false,
                 layout: { preset: 'c3' } };
      }
      return { scope: { domain, service: target }, group: { by: 'module' },
               layout: { preset: 'c3' } };
    case 'deps':
      return { scope: { domain, nodes: [target] }, expand: { direction: 'out', depth: 99 },
               group: { by: 'service' }, collapse: true, layout: { preset: 'deps' } };
    case 'impact':
      return { scope: { domain, nodes: [target] }, expand: { direction: 'in', depth: 99 },
               group: { by: 'service' }, collapse: true, layout: { preset: 'impact' } };
    // Lens presets (view bar) — mirror graph/viewmodel/presets.go
    case 'data':
      return { scope: { domain }, filter: { layers: ['data'] },
               group: { by: 'none' }, layout: { preset: 'data' } };
    case 'api':
      return { scope: { domain }, filter: { layers: ['contract', 'service'] },
               group: { by: 'service' }, collapse: false, layout: { preset: 'api' } };
    case 'services':
      return { scope: { domain }, filter: { layers: ['service', 'code'] },
               group: { by: 'service' }, collapse: true, layout: { preset: 'services' } };
    case 'flows':
      // Flows LIST view: flows only — each flow carries its trigger endpoint
      // as detail (rendered as the node subtitle); dblclick a flow to drill
      // into its flow-scoped composition (specFlow).
      return { scope: { domain }, filter: { layers: ['flow'] },
               group: { by: 'none' }, layout: { preset: 'flows' } };
    default: // c2
      // Containers level: services + channels only — data models/enums belong to C3.
      return { scope: { domain }, filter: { edge_kinds: ['sync', 'async'], layers: ['code', 'contract', 'service'] },
               group: { by: 'service' }, collapse: true, layout: { preset: 'c2' } };
  }
}
function specPath(domain, a, b) {
  return { scope: { domain, nodes: [a, b] }, expand: { mode: 'path' },
           group: { by: 'none' }, layout: { preset: 'path' } };
}
// Flow drill-down: flow card + trigger endpoint + REQUIRES targets with
// TRIGGERS_FLOW/REQUIRES edges (BuildView's scope.flow path).
function specFlow(domain, flowKey) {
  return { scope: { domain, flow: flowKey },
           group: { by: 'none' }, layout: { preset: 'flow' } };
}

function shortName(key) {
  if (!key) return '';
  return String(key).split('/').pop().split(':').pop();
}

function presetTitle(spec, view) {
  const p = (spec.layout && spec.layout.preset) || '';
  const nodes = (spec.scope && spec.scope.nodes) || [];
  if (p === 'c1')     return 'C1 Context';
  if (p === 'c2')     return 'C2 Containers';
  if (p === 'c3')     return (spec.scope && spec.scope.service)
                        ? 'C3: ' + spec.scope.service
                        : 'C3: all services';
  if (p === 'deps')   return 'Deps: ' + shortName(nodes[0]);
  if (p === 'impact') return 'Impact: ' + shortName(nodes[0]);
  if (p === 'path')   return 'Path';
  if (p === 'data')     return 'Data Models';
  if (p === 'api')      return 'API Surface';
  if (p === 'services') return 'Services';
  if (p === 'flows')    return 'Flows';
  if (p === 'flow')     return 'Flow: ' + ((view && view.title) || shortName(spec.scope && spec.scope.flow));
  if (p === 'custom')   return 'Custom';
  return (view && view.title) || 'View';
}

/* ── View → renderer input ──
   baseLookup is the dashboard's edgeCategoryLookup (EDGE_TYPE →
   {category,color,shortLabel}). Returns {nodes, edges, groups,
   lookupOverlay, missing, trace, boundary}. */
function adaptView(view, baseLookup) {
  baseLookup = baseLookup || {};
  const spec    = view.spec   || {};
  const vgroups = view.groups || [];
  const vnodes  = view.nodes  || [];
  const domain  = (spec.scope && spec.scope.domain) || '';
  const preset  = (spec.layout && spec.layout.preset) || '';
  // C2/Services: collapsed service cards sit INSIDE the domain system
  // boundary box (same renderer group mechanism as the All-mode domain box);
  // topics/externals/infra stay outside.
  const boxServices = preset === 'c2' || preset === 'services';

  // Which groups have visible member nodes? (collapse=false views)
  const memberCount = {};
  vnodes.forEach(n => { if (n.group) memberCount[n.group] = (memberCount[n.group] || 0) + 1; });

  const groupName = {};
  vgroups.forEach(g => { groupName[g.key] = g.name; });

  const nodes = [];

  // Collapsed groups (no visible members) become nodes — the group IS
  // the entity at this zoom level (e.g. C2 services). Their node key
  // equals the group key, which is what collapsed VEdges reference.
  vgroups.forEach(g => {
    if (memberCount[g.key]) return;
    // The C1 system box is the synthesized domain group: its key is the bare
    // domain (no layer: prefix), so it gets the service-layer styling and a
    // "N services · M endpoints" stats subtitle instead of falling through to
    // the renderer's default (black) color.
    const isDomainBox = g.kind === 'domain' || !String(g.key).includes(':');
    const stats = g.stats || {};
    nodes.push({
      node_id: g.key,
      node_key: g.key,
      name: g.name,
      node_type: g.kind || 'group',
      // node keys are layer:type:domain:name — first segment is the layer,
      // so the synthesized node keeps the exact layer color of the real node.
      layer: isDomainBox ? 'service' : (String(g.key).split(':')[0] || 'service'),
      domain_key: domain,
      // C2/Services system boundary: service group-cards are grouped under
      // the domain name. Non-service groups (modules, the C1 domain box
      // itself) stay ungrouped.
      _group: (boxServices && !isDomainBox && g.kind === 'service') ? (domain || null) : null,
      _vgroup: true,
      _stats: stats,
      _subtitle: isDomainBox
        ? (stats.services || 0) + ' services · ' + (stats.endpoints || 0) + ' endpoints'
        : null,
      // The C1 system box gets an explicit "→ drill" affordance (renderer
      // draws it; click = enter C2, same as dblclick).
      _drill: isDomainBox,
    });
  });

  vnodes.forEach(n => {
    nodes.push({
      node_id: n.key,
      node_key: n.key,
      name: n.name,
      node_type: n.type,
      layer: n.layer,
      domain_key: domain,
      // Renderer group boxes key off the group NAME (rendered as the box label).
      _group: n.group ? (groupName[n.group] || n.group) : null,
      _endpoints: n.endpoints || [],
      _usesModels: n.uses_models || [],
      // Detail (e.g. a flow's trigger endpoint "GET /api/score") renders as
      // the node subtitle via the renderer's existing _subtitle hook.
      _subtitle: n.detail || null,
      // Materialized boundary targets (C3): normal layer styling, dimmed
      // via the renderer's existing ref pattern (_isRef → opacity 0.4).
      _isRef: !!n.boundary,
      _boundary: !!n.boundary,
    });
  });

  // Edges: kind → existing edge-category styling. weight>1 gets a
  // synthetic edge_type + overlay entry so the label reads "… ×N"
  // while stroke color stays the category color. Edges carrying an explicit
  // label (e.g. C1 uses_infra: "events"/"cache") get an overlay entry keyed
  // by kind|label so the label renders verbatim.
  const lookupOverlay = {};
  const edges = (view.edges || []).map(e => {
    let edgeType = e.kind;
    const base = baseLookup[e.kind] || baseLookup[String(e.kind).toUpperCase()];
    if (e.weight > 1) {
      edgeType = e.kind + '×' + e.weight;
      lookupOverlay[edgeType] = {
        category: base ? base.category : 'view',
        color: base ? base.color : '#666',
        shortLabel: ((base && base.shortLabel) || String(e.kind).toLowerCase()) + ' ×' + e.weight,
      };
    } else if (e.label) {
      edgeType = e.kind + '|' + e.label;
      lookupOverlay[edgeType] = {
        category: base ? base.category : 'view',
        color: base ? base.color : '#b8a898',
        shortLabel: e.label,
      };
    }
    return {
      from_node_id: e.from,
      to_node_id: e.to,
      edge_type: edgeType,
      transport: e.transport || '',
      weight: e.weight || 1,
      collapsed_from: e.collapsed_from || [],
      derivation: 'view',
      confidence: 1,
      _kind: e.kind,
      _label: e.label || '',
    };
  });

  const useGroups = nodes.some(n => n._group);
  return {
    nodes,
    edges,
    groups: useGroups ? { field: '_group' } : null,
    lookupOverlay,
    missing: view.missing || [],
    trace: view.trace || [],
    boundary: view.boundary || null,
  };
}

window.ViewAdapter = { b64urlEncode, b64urlDecode, presetSpec, specPath, specFlow, presetTitle, shortName, adaptView };
})();
