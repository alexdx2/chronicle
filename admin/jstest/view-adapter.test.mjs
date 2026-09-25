import test from 'node:test';
import assert from 'node:assert/strict';

// view-adapter.js is a classic script (window.ViewAdapter = {...}, no ES
// exports) — same load pattern index.html uses, just with window stubbed
// to globalThis so the assignment lands somewhere this test can read it
// back (see marks-render.test.mjs for the same pattern on graph-renderer.js).
globalThis.window = globalThis;
await import('../static/js/view-adapter.js');
const { adaptView } = globalThis.ViewAdapter;

function baseView(overrides) {
  return Object.assign({
    spec: { scope: { domain: 'tomandjerry' }, group: { by: 'service' }, collapse: true, layout: { preset: 'c2' } },
    groups: [],
    nodes: [],
    edges: [],
  }, overrides);
}

// ── V3: edge labels come from VEdge.Label, with the ×N weight suffix
// appended the way render.go's arrowLabel appends it — falling back to the
// old category-table label only when a server hasn't set `label` yet. ──

test('adaptView: edge label wins over the category table, verbatim, when weight is 1', () => {
  const view = baseView({
    nodes: [
      { key: 'svc:a', name: 'A', type: 'service', layer: 'service' },
      { key: 'svc:b', name: 'B', type: 'service', layer: 'service' },
    ],
    edges: [{ from: 'svc:a', to: 'svc:b', kind: 'CONSUMES_TOPIC', label: 'sub', weight: 1 }],
  });
  const baseLookup = { CONSUMES_TOPIC: { category: 'async', color: '#2e5f8a', shortLabel: 'consumes' } };

  const adapted = adaptView(view, baseLookup);

  assert.equal(adapted.edges.length, 1);
  const e = adapted.edges[0];
  const overlay = adapted.lookupOverlay[e.edge_type];
  assert.ok(overlay, 'edge carrying a label gets an overlay entry');
  assert.equal(overlay.shortLabel, 'sub', 'label alone, no ×N at weight 1');
  assert.equal(overlay.category, 'async', 'category still comes from the base lookup');
});

test('adaptView: edge label gets the ×N weight suffix appended, same rule as render.go’s arrowLabel', () => {
  const view = baseView({
    nodes: [
      { key: 'svc:a', name: 'A', type: 'service', layer: 'service' },
      { key: 'svc:b', name: 'B', type: 'service', layer: 'service' },
    ],
    edges: [{ from: 'svc:a', to: 'svc:b', kind: 'CALLS_SERVICE', label: 'HTTP', weight: 2 }],
  });
  const baseLookup = { CALLS_SERVICE: { category: 'sync', color: '#c23030', shortLabel: 'calls' } };

  const adapted = adaptView(view, baseLookup);

  const e = adapted.edges[0];
  const overlay = adapted.lookupOverlay[e.edge_type];
  assert.equal(overlay.shortLabel, 'HTTP ×2', 'base label + ×N, not the category’s "calls ×2"');
});

test('adaptView: no label (old server) falls back to the category table’s short label + ×N', () => {
  const view = baseView({
    nodes: [
      { key: 'svc:a', name: 'A', type: 'service', layer: 'service' },
      { key: 'svc:b', name: 'B', type: 'service', layer: 'service' },
    ],
    edges: [{ from: 'svc:a', to: 'svc:b', kind: 'CALLS_SERVICE', weight: 2 }], // no `label` field at all
  });
  const baseLookup = { CALLS_SERVICE: { category: 'sync', color: '#c23030', shortLabel: 'calls' } };

  const adapted = adaptView(view, baseLookup);

  const e = adapted.edges[0];
  const overlay = adapted.lookupOverlay[e.edge_type];
  assert.equal(overlay.shortLabel, 'calls ×2', 'unchanged pre-F9 behaviour when label is absent');
});

test('adaptView: no label, weight 1 — no overlay entry, renderer falls through to the plain category lookup', () => {
  const view = baseView({
    nodes: [
      { key: 'svc:a', name: 'A', type: 'service', layer: 'service' },
      { key: 'svc:b', name: 'B', type: 'service', layer: 'service' },
    ],
    edges: [{ from: 'svc:a', to: 'svc:b', kind: 'CALLS_SERVICE', weight: 1 }],
  });
  const baseLookup = { CALLS_SERVICE: { category: 'sync', color: '#c23030', shortLabel: 'calls' } };

  const adapted = adaptView(view, baseLookup);

  const e = adapted.edges[0];
  assert.equal(e.edge_type, 'CALLS_SERVICE', 'edge_type is the raw kind, untouched');
  assert.equal(adapted.lookupOverlay[e.edge_type], undefined, 'no synthetic overlay entry created');
});

// ── V2: a foreign node/card (VNode.domain set, or a C1 "other domain"
// collapsed card) carries a _foreign flag through the adapter so the
// renderer can style it as external. ──

test('adaptView: a VNode with domain set (a foreign neighbour) gets _foreign: true', () => {
  const view = baseView({
    nodes: [{ key: 'service:service:analytics:billing-api', name: 'billing-api', type: 'service', layer: 'service', boundary: true, domain: 'analytics' }],
  });

  const adapted = adaptView(view, {});

  assert.equal(adapted.nodes.length, 1);
  assert.equal(adapted.nodes[0]._foreign, true);
});

test('adaptView: an ordinary in-domain VNode (no domain field) gets _foreign: false', () => {
  const view = baseView({
    nodes: [{ key: 'service:service:tomandjerry:scoreboard-api', name: 'scoreboard-api', type: 'service', layer: 'service' }],
  });

  const adapted = adaptView(view, {});

  assert.equal(adapted.nodes[0]._foreign, false);
});

test('adaptView: a C1 other-domain card (VGroup kind domain, note "other domain") gets _foreign: true and no drill', () => {
  const view = baseView({
    spec: { scope: { domain: 'tomandjerry' }, group: { by: 'domain' }, collapse: true, layout: { preset: 'c1' } },
    groups: [
      { key: 'tomandjerry', name: 'tomandjerry', kind: 'domain', stats: { services: 3 } }, // own system box
      { key: 'analytics', name: 'analytics', kind: 'domain', note: 'other domain', stats: { services: 2 } }, // foreign
    ],
  });

  const adapted = adaptView(view, {});
  const own = adapted.nodes.find(n => n.node_key === 'tomandjerry');
  const foreign = adapted.nodes.find(n => n.node_key === 'analytics');

  assert.equal(own._foreign, false, 'this view’s own system box is not foreign');
  assert.equal(own._drill, true, 'the own system box keeps its drill affordance');

  assert.equal(foreign._foreign, true, 'the other-domain card is foreign');
  assert.equal(foreign._drill, false, 'a foreign card gets no drill affordance');
  assert.match(foreign._subtitle, /other domain/, 'the subtitle still names it as another domain');
});
