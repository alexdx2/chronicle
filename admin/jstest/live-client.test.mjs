import test from 'node:test';
import assert from 'node:assert/strict';
import { webcrypto } from 'node:crypto';
import {
  LiveFeed, LiveClient, newClientId, screenPayload, beaconBody,
  strongestMark, markLine, MARK_COLORS,
} from '../static/js/live-client.js';

function fakeDeps(routes = {}) {
  const listeners = { document: {}, window: {} };
  const d = {
    routes: Object.assign({}, routes), gets: [], posts: [], beacons: [], feed: [], cards: [], timers: [], intervals: [],
    fetch: async (url, opts) => {
      if (opts && opts.method === 'POST') {
        d.posts.push({ url, body: opts.body, headers: opts.headers });
        return { ok: true, json: async () => ({}) };
      }
      d.gets.push(url);
      return { ok: true, json: async () => ({ events: d.routes[url] || [] }) };
    },
    crypto: webcrypto,
    document: { visibilityState: 'visible', addEventListener: (n, f) => { listeners.document[n] = f; } },
    window: { addEventListener: (n, f) => { listeners.window[n] = f; } },
    setTimeout: (f, ms) => { d.timers.push({ f, ms }); return d.timers.length; },
    clearTimeout: (h) => { if (d.timers[h - 1]) d.timers[h - 1].f = null; },
    setInterval: (f, ms) => { d.intervals.push({ f, ms }); return d.intervals.length; },
    sendBeacon: (url, body) => { d.beacons.push({ url, body }); return true; },
    getScreen: () => ({ tab: 'graph', title: 'C2 Containers', spec: { layout: { preset: 'c2' } }, selected: null, breadcrumbs: ['C2 Containers'] }),
    onFeedChange: (items) => d.feed.push(items.slice()),
    onDiagrams: (fresh, o) => d.cards.push({ fresh, o }),
    fire: (target, name) => listeners[target][name](),
    runTimers: () => { const ts = d.timers; d.timers = []; ts.forEach(t => t.f && t.f()); },
  };
  return d;
}

const ev = (id, at, extra = {}) => Object.assign({ id, at, kind: 'diagram', payload: { title: 'D' + id } }, extra);
const flush = () => new Promise(r => setImmediate(r));

test('newClientId: a fresh id per page; getRandomValues when randomUUID is missing', () => {
  assert.notEqual(newClientId(webcrypto), newClientId(webcrypto));
  const lan = { getRandomValues: (a) => a.fill(10) }; // http://192.168.x.x has no randomUUID
  assert.equal(newClientId(lan), '0a'.repeat(16));
});

test('LiveFeed: dedupe by (repo, id, at), per-repo cursor, menu capped newest first', () => {
  const f = new LiveFeed(3);
  assert.equal(f.add([ev(1, 't1'), ev(2, 't2')]).length, 2);
  assert.equal(f.add([ev(2, 't2')]).length, 0);
  assert.equal(f.add([ev(2, 't3', { repo: 'orders' })]).length, 1);
  assert.equal(f.cursor.get(''), 2);
  assert.equal(f.cursor.get('orders'), 2);
  f.add([ev(3, 't4'), ev(4, 't5')]);
  assert.deepEqual(f.items.map(e => e.id), [4, 3, 2]);
});

test('LiveFeed: a reset database reuses ids — a newer at is a new event and moves the cursor back', () => {
  const f = new LiveFeed();
  f.add([ev(40, '2026-09-24T10:00:00.000Z')]);
  assert.equal(f.add([ev(1, '2026-09-24T11:00:00.000Z')]).length, 1);
  assert.equal(f.cursor.get(''), 1);
});

test('start: menu from the start-up load without cards, then an immediate screen report', async () => {
  const d = fakeDeps({ '/api/live?kind=diagram&limit=20': [ev(5, 't5')] });
  const c = new LiveClient(d);
  await c.start();
  await flush();
  assert.deepEqual(d.feed.at(-1).map(e => e.id), [5]);
  assert.equal(d.cards.length, 0, 'start-up load raises the badge only');
  assert.equal(d.posts.length, 1, 'first report without waiting for the debounce or the heartbeat');
  const body = JSON.parse(d.posts[0].body);
  assert.equal(body.client_id, c.clientId);
  assert.equal(body.visible, true);
  assert.equal(d.posts[0].headers['Content-Type'], 'application/json');
  assert.equal(d.intervals[0].ms, 60000);
});

test('socket: a new diagram is a card once; a hidden tab gets one summary on return', async () => {
  const d = fakeDeps();
  const c = new LiveClient(d);
  await c.start();
  c.onSocketEvent(ev(7, 't7'));
  c.onSocketEvent(ev(7, 't7'));
  assert.equal(d.cards.length, 1);
  assert.equal(d.cards[0].o.summary, false);
  d.document.visibilityState = 'hidden';
  c.onSocketEvent(ev(8, 't8'));
  c.onSocketEvent(ev(9, 't9'));
  assert.equal(d.cards.length, 1, 'hidden: no cards yet');
  d.document.visibilityState = 'visible';
  d.fire('document', 'visibilitychange');
  assert.equal(d.cards.length, 2);
  assert.deepEqual(d.cards[1].fresh.map(e => e.id), [8, 9]);
  assert.equal(d.cards[1].o.summary, true);
});

test('resync pages each repo from its cursor, then restores the menu', async () => {
  const d = fakeDeps({ '/api/live?kind=diagram&limit=20': [ev(10, 't010', { repo: 'orders' })] });
  const c = new LiveClient(d);
  await c.start();
  const page1 = Array.from({ length: 200 }, (_, i) => ev(11 + i, 't' + String(100 + i), { repo: 'orders' }));
  d.routes['/api/live?kind=diagram&limit=200&after=10&repo=orders'] = page1;
  d.routes['/api/live?kind=diagram&limit=200&after=210&repo=orders'] = [ev(211, 't400', { repo: 'orders' })];
  d.routes['/api/live?kind=diagram&limit=20'] = [ev(3, 't500', { repo: 'analytics' })];
  await c.resync();
  assert.deepEqual(d.gets.slice(1), [
    '/api/live?kind=diagram&limit=200&after=10&repo=orders',
    '/api/live?kind=diagram&limit=200&after=210&repo=orders',
    '/api/live?kind=diagram&limit=20',
  ]);
  const got = d.cards.at(-1).fresh.map(e => (e.repo || '') + ':' + e.id);
  assert.equal(got.length, 202);
  assert.ok(got.includes('analytics:3'));
  assert.equal(d.cards.at(-1).o.summary, true);
});

test('reportSoon debounces a burst into one report', async () => {
  const d = fakeDeps();
  const c = new LiveClient(d);
  await c.start();
  await flush();
  const before = d.posts.length;
  c.reportSoon(); c.reportSoon(); c.reportSoon();
  assert.equal(d.timers.at(-1).ms, 300);
  d.runTimers();
  await flush();
  assert.equal(d.posts.length, before + 1);
});

test('pagehide sends a JSON Blob beacon with closed:true', async () => {
  const d = fakeDeps();
  const c = new LiveClient(d);
  await c.start();
  d.fire('window', 'pagehide');
  assert.equal(d.beacons.length, 1);
  assert.equal(d.beacons[0].url, '/api/live/screen');
  assert.equal(d.beacons[0].body.type, 'application/json');
  const body = JSON.parse(await d.beacons[0].body.text());
  assert.equal(body.closed, true);
  assert.equal(body.visible, false);
  assert.equal(body.client_id, c.clientId);
});

test('screenPayload bounds titles and crumbs', () => {
  const p = screenPayload({ clientId: 'a', tab: 'graph', title: 'x'.repeat(300), breadcrumbs: Array.from({ length: 30 }, (_, i) => 'c' + i), visible: true });
  assert.equal(p.title.length, 120);
  assert.equal(p.breadcrumbs.length, 20);
  assert.equal(p.breadcrumbs[0], 'c10');
  assert.equal('closed' in p, false);
  assert.equal(beaconBody(p).type, 'application/json');
});

test('marks: strongest kind, card line, colors', () => {
  const own = { key: 'k', name: 'battle.queue', kind: 'breaks', note: 'loses its topic' };
  assert.equal(strongestMark([{ kind: 'note' }, { kind: 'breaks' }, { kind: 'focus' }]), 'breaks');
  assert.equal(strongestMark([]), '');
  assert.equal(markLine([own], 'k'), '[breaks] loses its topic');
  assert.equal(markLine([own], 'svc'), 'battle.queue [breaks]');
  assert.equal(markLine([own, { key: 'k2', kind: 'note' }], 'svc'), '2 marked');
  assert.equal(MARK_COLORS.breaks, '#c25757');
});
