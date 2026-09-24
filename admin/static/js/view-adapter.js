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
      // Expanded by default — impact must show WHICH components break.
      return { scope: { domain, nodes: [target] }, expand: { direction: 'in', depth: 99 },
               group: { by: 'service' }, collapse: false, layout: { preset: 'impact' } };
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
    // Federation-level views (Chronicle Pro serves them from /api/view;
    // scope "*" means every domain in the federation).
    case 'topology':
      return { scope: { domain: '*' }, group: { by: 'domain' }, collapse: true,
               layout: { preset: 'topology' } };
    case 'connections':
      return { scope: { domain: '*' }, group: { by: 'domain' }, collapse: false,
               layout: { preset: 'connections' } };
    default: // c2
      // Containers level: services + channels only — data models/enums belong to C3.
      // infra is in: brokers frame their topics; isolated infra hides itself
      // with a reason (mirrors presets.go).
      return { scope: { domain }, filter: { edge_kinds: ['sync', 'async'], layers: ['code', 'contract', 'service', 'infra'] },
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

function cardSubtitle(n) {
  const parts = [n.type || ''];
  const eps = (n.endpoints || []).length;
  if (eps) parts.push(eps + (eps === 1 ? ' endpoint' : ' endpoints'));
  const models = n.uses_models || [];
  if (models.length) parts.push(models.length <= 2 ? models.join(', ') : models.length + ' models');
  return parts.filter(Boolean).join(' · ') || null;
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
  if (p === 'topology')    return 'Federation topology';
  if (p === 'connections') return 'Cross-repo connections';
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
  // groupMeta (keyed by the group KEY — two services may share a name, a
  // federation routinely has an "api" per repo; the frame's label is the
  // name): kind/layer for frame styling, inferred+note so an inferred frame
  // (topics in the only declared broker) says so on canvas.
  const groupMeta = {};
  vgroups.forEach(g => {
    groupName[g.key] = g.name;
    groupMeta[g.key] = {
      key: g.key, label: g.name, kind: g.kind || '',
      layer: String(g.key).includes(':') ? String(g.key).split(':')[0] : 'service',
      inferred: !!g.inferred, note: g.note || '', stats: g.stats || {},
    };
  });

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
      // A card's note (e.g. a topology card's "domain tomandjerry" under
      // the repo name) leads the subtitle, then the stats.
      _subtitle: (g.note && !g.inferred ? g.note + ' · ' : '') + (isDomainBox
        ? (stats.services || 0) + ' services · ' + (stats.endpoints || 0) + ' endpoints'
        : ''),
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
      // Renderer frames key off the group KEY; groupMeta carries the label.
      _group: n.group || null,
      _endpoints: n.endpoints || [],
      _usesModels: n.uses_models || [],
      // Subtitle: a flow's trigger endpoint ("GET /api/score"), else what
      // the card holds at this altitude — "controller · 4 endpoints",
      // "provider · BattleEvent" — so a C3 card answers what it exposes and
      // touches without opening the details panel.
      _subtitle: n.detail || cardSubtitle(n),
      // Materialized boundary targets (C3): normal layer styling, dimmed
      // via the renderer's existing ref pattern (_isRef → opacity 0.4).
      _isRef: !!n.boundary,
      _boundary: !!n.boundary,
      // View-specific salience (registry-driven). _renderMode is the UI source
      // of truth; _tier is diagnostic. Used to hide/dim low-salience nodes.
      _renderMode: n.render_mode || '',
      _tier: n.tier || '',
      _salienceReason: n.salience_reason || '',
      // Federation: which repo the node was scanned from (tag on the card).
      _repo: n.repo || '',
      // Provenance (from the graph row + evidence digest): the card can
      // say how sure it is and where its proof lives.
      confidence: n.confidence,
      trust_score: n.trust,
      status: n.status || 'active',
      _evidenceCount: n.evidence_count || 0,
      _evidenceRef: n.evidence_ref || '',
    });
  });

  // Edges: kind → existing edge-category styling. weight>1 gets a
  // synthetic edge_type + overlay entry so the label reads "… ×N"
  // while stroke color stays the category color. Edges carrying an explicit
  // label (e.g. C1 uses_infra: "events"/"cache") get an overlay entry keyed
  // by kind|label so the label renders verbatim.
  const lookupOverlay = {};
  const nameOf = {};
  nodes.forEach(n => { nameOf[n.node_key] = n.name; });
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
      // Provenance of the arrow: weakest component's derivation/confidence/
      // trust, summed evidence. Synthesized arrows (uses_infra, via) say so.
      derivation: e.derivation || 'view',
      confidence: (e.confidence === undefined || e.confidence === null) ? 1 : e.confidence,
      trust_score: (e.trust === undefined || e.trust === null) ? 1 : e.trust,
      _evidenceCount: e.evidence_count || 0,
      _kind: e.kind,
      _label: e.label || '',
      _fromName: nameOf[e.from] || e.from,
      _toName: nameOf[e.to] || e.to,
    };
  });

  const useGroups = nodes.some(n => n._group);
  return {
    nodes,
    edges,
    groups: useGroups ? { field: '_group' } : null,
    groupMeta,
    lookupOverlay,
    missing: view.missing || [],
    trace: view.trace || [],
    boundary: view.boundary || null,
    hidden: view.hidden || [],
    // Layout direction follows the question: a path/flow reads left→right
    // in step order; impact puts the changed node on top and dependents
    // below by distance (edges point INTO the subject, so BT); everything
    // else is the top-down Dagre default.
    rankDir: (preset === 'path' || preset === 'flow') ? 'LR' : (preset === 'impact' ? 'BT' : 'TB'),
  };
}

window.ViewAdapter = { b64urlEncode, b64urlDecode, presetSpec, specPath, specFlow, presetTitle, shortName, adaptView };
})();
