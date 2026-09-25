import test from 'node:test';
import assert from 'node:assert/strict';

// F3: the mark-rendering path (GraphRenderer._markLines, and ViewAdapter
// carrying marks from a View into the renderer's _marks) had no automated
// test. Agent-supplied text (a mark's name/note) must always land as
// textContent, never innerHTML — the DOM stub below throws if anything ever
// sets innerHTML, so a regression into string-concatenated markup fails loud.

function stubDocument() {
  globalThis.document = {
    createElement(tagName) {
      const node = {
        tagName,
        style: {},
        className: '',
        textContent: '',
        children: [],
        appendChild(child) { this.children.push(child); },
      };
      Object.defineProperty(node, 'innerHTML', {
        get() { throw new Error('innerHTML must never be read for agent-supplied text'); },
        set() { throw new Error('innerHTML must never be used for agent-supplied text — use textContent'); },
      });
      return node;
    },
  };
}

function stubEl(hint) {
  const inserted = [];
  return {
    querySelector: (sel) => (sel === '.nht-hint' ? hint : null),
    insertBefore: (box, ref) => { inserted.push({ box, ref }); },
    inserted,
  };
}

// graph-renderer.js only touches d3/document/window inside methods, never at
// module scope, but stub window defensively the way a real browser load
// would provide it (per the brief's own suggestion for this import).
globalThis.window = globalThis;
stubDocument();
const { GraphRenderer } = await import('../static/js/graph-renderer.js');
const { MARK_COLORS } = await import('../static/js/live-client.js');

test('_markLines: agent text is textContent-only, in mark order, colored from MARK_COLORS; own-key marks omit the name', () => {
  stubDocument();
  const hint = { tagName: 'div', className: 'nht-hint' };
  const el = stubEl(hint);
  const marks = [
    { key: 'own', kind: 'breaks', note: '<img src=x onerror=alert(1)>' },
    { key: 'other', name: 'battle.queue', kind: 'note', note: '"quoted" & <b>' },
  ];

  GraphRenderer.prototype._markLines(el, marks, 'own');

  assert.equal(el.inserted.length, 1, 'inserts exactly one box, before the hint');
  assert.equal(el.inserted[0].ref, hint);
  const box = el.inserted[0].box;
  assert.equal(box.children.length, 2, 'one line per mark, in mark order');

  const [line1, line2] = box.children;
  assert.equal(line1.className, 'nht-type');
  assert.equal(line1.style.color, MARK_COLORS.breaks);
  assert.equal(line1.textContent, '[breaks] <img src=x onerror=alert(1)>',
    'own-key mark omits the name; markup arrives verbatim as inert text');

  assert.equal(line2.style.color, MARK_COLORS.note);
  assert.equal(line2.textContent, 'battle.queue [note] "quoted" & <b>',
    'a foreign mark keeps its name; quotes/markup arrive verbatim as text');
});

test('_markLines: no marks touches neither document nor el', () => {
  stubDocument();
  const el = stubEl({ tagName: 'div', className: 'nht-hint' });
  GraphRenderer.prototype._markLines(el, [], 'own');
  assert.equal(el.inserted.length, 0);
  GraphRenderer.prototype._markLines(el, undefined, 'own');
  assert.equal(el.inserted.length, 0);
});

// view-adapter.js is a classic script (window.ViewAdapter = {...}), not an ES
// module — but it has no import/export statements either, so it is valid to
// load as one; it touches no browser API at load time (btoa/atob live inside
// b64url helpers, never called just by importing the file), only `window` to
// hang its namespace off. Confirmed loadable under node with `window` stubbed
// to globalThis first.
test('adaptView: marks flow from view nodes, groups and edges into _marks (empty array when absent)', async () => {
  globalThis.window = globalThis;
  await import('../static/js/view-adapter.js');
  const { adaptView } = globalThis.window.ViewAdapter;

  const view = {
    spec: { scope: { domain: 'd' }, layout: { preset: 'c2' } },
    groups: [
      { key: 'service:d:marked', name: 'Marked', kind: 'service', marks: [{ kind: 'breaks' }] },
      { key: 'service:d:plain', name: 'Plain', kind: 'service' },
    ],
    nodes: [
      { key: 'k1', name: 'n1', type: 'service', layer: 'service', marks: [{ kind: 'focus' }] },
      { key: 'k2', name: 'n2', type: 'service', layer: 'service' },
    ],
    edges: [
      { from: 'k1', to: 'k2', kind: 'CALLS_SERVICE', marks: [{ kind: 'note' }] },
      { from: 'k2', to: 'k1', kind: 'CALLS_SERVICE' },
    ],
  };

  const out = adaptView(view, {});

  const byKey = Object.fromEntries(out.nodes.map(n => [n.node_key, n]));
  assert.deepEqual(byKey['k1']._marks, [{ kind: 'focus' }], 'a marked view node carries its marks');
  assert.deepEqual(byKey['k2']._marks, [], 'an unmarked view node gets an empty array, not undefined');

  // service:d:marked has no visible members, so it collapses into a
  // synthesized node (not a frame) — its marks still ride on that node.
  assert.deepEqual(byKey['service:d:marked']._marks, [{ kind: 'breaks' }], 'a marked collapsed group carries its marks onto the synthesized node');
  assert.deepEqual(byKey['service:d:plain']._marks, [], 'an unmarked collapsed group gets an empty array');

  assert.deepEqual(out.edges[0]._marks, [{ kind: 'note' }], 'a marked edge carries its marks');
  assert.deepEqual(out.edges[1]._marks, [], 'an unmarked edge gets an empty array, not undefined');
});

test('adaptView: an expanded group (visible members) carries marks in groupMeta, not a synthesized node', async () => {
  globalThis.window = globalThis;
  await import('../static/js/view-adapter.js');
  const { adaptView } = globalThis.window.ViewAdapter;

  const view = {
    spec: { scope: { domain: 'd' }, layout: { preset: 'c3' } },
    groups: [{ key: 'service:d:svc', name: 'Svc', kind: 'service', marks: [{ kind: 'changed' }] }],
    nodes: [{ key: 'k1', name: 'n1', type: 'component', layer: 'code', group: 'service:d:svc' }],
    edges: [],
  };

  const out = adaptView(view, {});
  assert.equal(out.nodes.some(n => n.node_key === 'service:d:svc'), false, 'a group with visible members is a frame, not a synthesized node');
  assert.deepEqual(out.groupMeta['service:d:svc'].marks, [{ kind: 'changed' }]);
});
