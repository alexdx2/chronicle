// Live channel between the agent and this dashboard (spec
// docs/superpowers/specs/2026-09-24-live-diagrams-v2-design.md). The pure
// parts are exported for node --test (admin/jstest); LiveClient wires them
// to fetch, the WebSocket feed and timers through injected deps.

export const MARK_COLORS = { breaks: '#c25757', changed: '#d08a2e', focus: '#3b6fd4', note: '#8a7355' };
const MARK_ORDER = ['breaks', 'changed', 'focus', 'note'];

export const MENU_SIZE = 20;
export const PAGE_SIZE = 200;
export const RESYNC_MAX_PAGES = 10;
export const HEARTBEAT_MS = 60000;
export const DEBOUNCE_MS = 300;

// strongestMark: the strongest kind among marks ('' when none).
export function strongestMark(marks) {
  let best = '';
  for (const m of marks || []) {
    const r = MARK_ORDER.indexOf(m && m.kind);
    if (r >= 0 && (best === '' || r < MARK_ORDER.indexOf(best))) best = m.kind;
  }
  return best;
}

// markLine: one caption line for a card — its own mark as "[kind] note",
// one folded mark as "name [kind]", several as "N marked".
export function markLine(marks, ownKey) {
  const list = marks || [];
  if (!list.length) return '';
  const own = list.find(m => m.key === ownKey);
  if (own) return '[' + own.kind + ']' + (own.note ? ' ' + own.note : '');
  if (list.length === 1) return (list[0].name || list[0].key) + ' [' + list[0].kind + ']';
  return list.length + ' marked';
}

// markDetail: the full reading of one mark, for tooltips.
export function markDetail(m, ownKey) {
  return (m.key === ownKey ? '' : (m.name || m.key) + ' ') + '[' + m.kind + ']' + (m.note ? ' ' + m.note : '');
}

// newClientId: one id per page lifetime, kept in memory — a duplicated tab
// or a reload is a new client (sessionStorage would be copied into a
// duplicate). Falls back to getRandomValues where randomUUID is missing
// (plain http on a LAN address is not a secure context).
export function newClientId(c) {
  const cr = c || globalThis.crypto;
  if (cr && typeof cr.randomUUID === 'function') return cr.randomUUID();
  const b = new Uint8Array(16);
  cr.getRandomValues(b);
  return Array.from(b, x => x.toString(16).padStart(2, '0')).join('');
}

function byAtThenId(a, b) {
  const x = a.at || '', y = b.at || '';
  if (x !== y) return x < y ? -1 : 1;
  return a.id - b.id;
}

// LiveFeed keeps the diagrams this page knows: dedupe by (repo, id, at) —
// a reset database reuses ids, never (id, at) — a cursor per repo (the id
// of the newest event seen there), and the menu, newest first.
export class LiveFeed {
  constructor(max = MENU_SIZE) {
    this.max = max;
    this.seen = new Set();
    this.cursor = new Map();
    this.newestAt = new Map();
    this.items = [];
  }

  // key must not collide when repo or at happen to contain '|' — join with
  // JSON.stringify on the tuple instead of a delimiter.
  static key(ev) { return JSON.stringify([ev.repo || '', ev.id, ev.at || '']); }

  // add keeps what is new to this page and returns it, oldest first.
  add(events) {
    const fresh = [];
    for (const ev of events || []) {
      const k = LiveFeed.key(ev);
      if (this.seen.has(k)) continue;
      this.seen.add(k);
      fresh.push(ev);
      const repo = ev.repo || '';
      const at = ev.at || '';
      const cur = this.newestAt.get(repo);
      if (cur === undefined || at > cur || (at === cur && ev.id > this.cursor.get(repo))) {
        this.newestAt.set(repo, at);
        this.cursor.set(repo, ev.id);
      }
    }
    fresh.sort(byAtThenId);
    if (fresh.length) {
      this.items = fresh.slice().reverse().concat(this.items).sort((a, b) => byAtThenId(b, a)).slice(0, this.max);
    }
    return fresh;
  }

  repos() { return Array.from(this.cursor.keys()); }
}

// clipChars clips by Unicode code points, not UTF-16 code units — matching
// the server's rune count (live/screen.go). A plain .slice(0, n) can cut an
// astral character's surrogate pair in half and send a lone surrogate,
// which the browser then encodes as U+FFFD.
function clipChars(s, n) {
  return Array.from(String(s)).slice(0, n).join('');
}

// screenPayload shapes what the page reports (live.Screen on the server).
export function screenPayload(s) {
  const p = { client_id: s.clientId, tab: s.tab || '', visible: !!s.visible };
  if (s.title) p.title = clipChars(s.title, 120);
  if (s.spec) p.spec = s.spec;
  if (s.selected) p.selected = s.selected;
  if (s.breadcrumbs && s.breadcrumbs.length) p.breadcrumbs = s.breadcrumbs.slice(-20).map(x => clipChars(x, 120));
  if (s.closed) p.closed = true;
  return p;
}

// beaconBody wraps a report for navigator.sendBeacon: a Blob typed
// application/json. A plain string goes out as text/plain and the server
// answers 415 (checked in Chromium 138).
export function beaconBody(payload) {
  return new Blob([JSON.stringify(payload)], { type: 'application/json' });
}

export class LiveClient {
  constructor(deps) {
    this.d = deps;
    this.clientId = newClientId(deps.crypto);
    this.feed = new LiveFeed(MENU_SIZE);
    this.pending = [];
    this._debounce = null;
    this._started = false;
  }

  // start: the agent's recent diagrams fill the menu (no cards), the page
  // reports itself at once, then on a heartbeat, on visibility changes and
  // one last time on pagehide.
  async start() {
    if (this._started) return;
    this._started = true;
    this.feed.add(await this._events('/api/live?kind=diagram&limit=' + MENU_SIZE));
    this.d.onFeedChange(this.feed.items);
    this.reportNow();
    this.d.setInterval(() => this.reportNow(), HEARTBEAT_MS);
    this.d.document.addEventListener('visibilitychange', () => this._onVisibility());
    this.d.window.addEventListener('pagehide', () => this.beaconClosed());
  }

  onSocketEvent(ev) { this._deliver(this.feed.add([ev])); }

  // resync after a reconnect: page every known repo from its cursor (taken
  // before anything moves), then restore the menu — it also brings repos
  // first seen while the socket was down. Bounded at RESYNC_MAX_PAGES pages
  // per repo, and stops a repo early once a page makes no progress (a last
  // id no greater than "after", e.g. the same page again) — either way the
  // other repos and the trailing menu reload still run.
  async resync() {
    const fresh = [];
    for (const [repo, from] of new Map(this.feed.cursor)) {
      let after = from;
      for (let i = 0; i < RESYNC_MAX_PAGES; i++) {
        const page = await this._events('/api/live?kind=diagram&limit=' + PAGE_SIZE + '&after=' + after +
          (repo ? '&repo=' + encodeURIComponent(repo) : ''));
        fresh.push(...this.feed.add(page));
        if (page.length < PAGE_SIZE) break;
        const last = page[page.length - 1].id;
        if (last <= after) break; // no progress — stop this repo, not the others
        after = last;
      }
    }
    fresh.push(...this.feed.add(await this._events('/api/live?kind=diagram&limit=' + MENU_SIZE)));
    fresh.sort(byAtThenId);
    this._deliver(fresh);
  }

  reportSoon() {
    if (this._debounce !== null) this.d.clearTimeout(this._debounce);
    this._debounce = this.d.setTimeout(() => { this._debounce = null; this.reportNow(); }, DEBOUNCE_MS);
  }

  reportNow() {
    const body = JSON.stringify(this._payload(false));
    return Promise.resolve()
      .then(() => this.d.fetch('/api/live/screen', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body }))
      .catch(() => {}); // the next change or heartbeat repeats it
  }

  // beaconClosed must not throw and must not silently drop the closing
  // report: sendBeacon is best-effort (missing on some embedders, and the
  // browser may refuse it under queued-bytes pressure), so a missing
  // function, a thrown error or a false return all fall back to a keepalive
  // fetch — the one request allowed to outlive an unloading page.
  beaconClosed() {
    const payload = this._payload(true);
    let ok = false;
    try {
      ok = !!(this.d.sendBeacon && this.d.sendBeacon('/api/live/screen', beaconBody(payload)));
    } catch (e) {
      ok = false;
    }
    if (ok) return;
    Promise.resolve()
      .then(() => this.d.fetch('/api/live/screen', {
        method: 'POST',
        keepalive: true,
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(payload),
      }))
      .catch(() => {});
  }

  _payload(closed) {
    const s = Object.assign({}, this.d.getScreen() || {});
    s.clientId = this.clientId;
    s.visible = !closed && this.d.document.visibilityState !== 'hidden';
    s.closed = closed;
    return screenPayload(s);
  }

  _onVisibility() {
    if (this.d.document.visibilityState !== 'hidden' && this.pending.length) {
      const batch = this.pending;
      this.pending = [];
      this.d.onDiagrams(batch, { summary: batch.length > 1 });
    }
    this.reportNow();
  }

  _deliver(fresh) {
    if (!fresh.length) return;
    this.d.onFeedChange(this.feed.items);
    if (this.d.document.visibilityState === 'hidden') {
      this.pending.push(...fresh);
      return;
    }
    this.d.onDiagrams(fresh, { summary: fresh.length > 1 });
  }

  async _events(url) {
    try {
      const r = await this.d.fetch(url);
      if (!r || !r.ok) return [];
      const j = await r.json();
      return Array.isArray(j.events) ? j.events : [];
    } catch (e) {
      return [];
    }
  }
}
