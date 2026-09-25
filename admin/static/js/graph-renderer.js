// graph-renderer.js — Single D3/Dagre render pipeline.
// Stateless: render what you're given. No filtering, no state management, no preset awareness.

import { MARK_COLORS, strongestMark, markLine, markDetail } from './live-client.js';

// clip: truncate to `max` chars with an ellipsis. Module scope so both the
// node caption (inside the render method) and the frame-mark caption
// (which runs before the node section, same method) can use it — a local
// const declared later in the method is in its temporal dead zone here.
function clip(t, max) { t = String(t || ''); return t.length > max ? t.slice(0, max - 1) + '…' : t; }

const LAYOUT = {
  rankDir: 'TB',
  rankSep: 60,
  nodeSep: 30,
  edgeSep: 20,
  marginX: 30,
  marginY: 30,
  nodeWidth: 120,
  nodeHeight: 50,
  groupPadding: { top: 30, bottom: 15, left: 15, right: 15 },
};

// Fallbacks mirror the :root layer tokens in index.html — the live values are
// read from CSS so every surface (sidebar, legend, nodes) shares one palette.
// Fills derive the light ramp via alpha ('40' suffix at the call sites).
const LAYER_COLORS = {
  code: '#8b5a2b',
  service: '#c23030',
  contract: '#1a7a2e',
  data: '#6a3d9a',
  flow: '#b85c2e',
  ownership: '#c4880a',
  infra: '#2e5f8a',
  ci: '#a83262',
};
let layerColorCache = null;

// Evidence is a line only on an absolute signal — the same rule as the
// Go renderers (viewmodel.nodeProvenanceText): a non-active status,
// confidence below the bar (including 0), or no evidence at all. A
// healthy element says nothing; the bar is absolute, never relative to
// the rest of the graph, so a uniformly weak graph lights up everywhere.
const EVIDENCE_BAR = 0.7;
function nodeSignalLine(d, esc) {
  const parts = [];
  if (d.status && d.status !== 'active') parts.push(esc(d.status));
  let c = d.trust_score;
  if (!c && d.confidence > 0) c = d.confidence;
  c = c || 0;
  if (c < EVIDENCE_BAR) parts.push('confidence ' + Math.round(c * 100) + '%');
  if (!d._evidenceCount) parts.push('no evidence');
  if (!parts.length) return '';
  return '<div class="nht-type nht-signal">' + parts.join(' · ') + (d._evidenceRef ? ' · ' + esc(d._evidenceRef) : '') + '</div>';
}
function edgeSignalLine(e, esc) {
  if (!e.derivation || e.derivation === 'view') return '';
  const parts = [];
  if (e.derivation === 'synthesized') return 'synthesized (no graph edge)';
  if (e.derivation === 'inferred' || e.derivation === 'unknown') parts.push(esc(e.derivation));
  let c = e.trust_score;
  if (!c && e.confidence > 0) c = e.confidence;
  c = c || 0;
  if (c < EVIDENCE_BAR) parts.push('confidence ' + Math.round(c * 100) + '%');
  if (!e._evidenceCount) parts.push('no evidence');
  return parts.join(' · ');
}

export class GraphRenderer {
  constructor(container) {
    this._container = container;
    this._svg = null;
    this._currentEdges = null;
  }

  layerColor(layer) {
    if (!layerColorCache) {
      layerColorCache = {};
      const rootStyle = getComputedStyle(document.documentElement);
      for (const l of Object.keys(LAYER_COLORS)) {
        layerColorCache[l] = (rootStyle.getPropertyValue('--' + l) || '').trim() || LAYER_COLORS[l];
      }
    }
    return layerColorCache[layer] || '#b8a898';
  }

  render({
    nodes,
    edges,
    groups = null,
    layout = 'dagre',
    positionCache = {},
    marks = {},
    callbacks = {},
    edgeCategoryLookup = {},
    showEdgeLabels = true,
    rankDir = LAYOUT.rankDir,
    groupMeta = {},
    // compact: an inline figure (wiki page diagrams) — no pan/zoom, no
    // 500px floor; the SVG fits its content to the container box.
    compact = false,
  }) {
    if (!this._container) return;
    this._container.innerHTML = '';
    this._currentEdges = null;

    if (!nodes || nodes.length === 0) {
      this._container.innerHTML =
        '<div style="padding:60px;text-align:center;color:var(--text-muted)">' +
        'No nodes to display. Adjust filters or import data.</div>';
      return;
    }

    const isStrict = layout === 'dagre';
    const width = Math.max(900, this._container.clientWidth || 900);
    const height = Math.max(600, this._container.clientHeight || 600);

    const groupField = groups && groups.field;
    const groupMap = {};
    if (groupField) {
      nodes.forEach(n => {
        // Infra nodes are shared resources — never inside domain groups
        // Shared resources always float outside domain groups
        // …unless the VIEW placed the node in a frame explicitly (_group):
        // a topic inside its broker frame is the view engine's decision.
        const sharedTypes = new Set(['broker', 'database', 'cache', 'queue', 'infrastructure', 'external_system', 'topic', 'async_channel']);
        if (!n._group && (n.layer === 'infra' || sharedTypes.has(n.node_type))) return;
        const gk = n._group || n[groupField];
        if (gk) {
          if (!groupMap[gk]) groupMap[gk] = [];
          groupMap[gk].push(n);
        }
      });
    }
    // Groups with 1 node: the node IS the group, no box needed
    const groupKeys = Object.keys(groupMap).filter(gk => groupMap[gk].length >= 2).sort();
    const activeGroups = new Set(groupKeys);
    for (const [gk, members] of Object.entries(groupMap)) {
      if (!activeGroups.has(gk)) {
        // The frame is dissolved, the membership is not: the card keeps its
        // group as a tag ("topic · orders") so it never reads as unaffiliated.
        members.forEach(n => { n._group = null; n._dissolvedGroup = (groupMeta[gk] && groupMeta[gk].label) || gk; });
      }
    }
    const useCompound = groupKeys.length > 0;

    let simNodes = [], simNodeMap = {}, simEdges = [];
    const dagreEdgePoints = {};
    let gDag;

    if (typeof dagre !== 'undefined') {
      gDag = new dagre.graphlib.Graph({ compound: useCompound });
      gDag.setGraph({
        rankdir: rankDir || LAYOUT.rankDir, ranksep: LAYOUT.rankSep,
        nodesep: LAYOUT.nodeSep, edgesep: LAYOUT.edgeSep,
        marginx: LAYOUT.marginX, marginy: LAYOUT.marginY,
      });
      gDag.setDefaultEdgeLabel(() => ({}));

      if (useCompound) {
        groupKeys.forEach(gk => {
          gDag.setNode('group_' + gk, {
            label: gk,
            paddingTop: LAYOUT.groupPadding.top,
            paddingBottom: LAYOUT.groupPadding.bottom,
            paddingLeft: LAYOUT.groupPadding.left,
            paddingRight: LAYOUT.groupPadding.right,
          });
        });
      }

      nodes.forEach(n => {
        const nodeKey = n.node_id;
        gDag.setNode(nodeKey, {
          width: LAYOUT.nodeWidth,
          height: LAYOUT.nodeHeight,
          label: n.name || nodeKey,
        });
        if (useCompound) {
          const gk = n[groupField] || n._group;
          if (gk && groupKeys.includes(gk)) {
            gDag.setParent(nodeKey, 'group_' + gk);
          }
        }
      });

      edges.forEach(e => {
        if (gDag.hasNode(e.from_node_id) && gDag.hasNode(e.to_node_id)) {
          gDag.setEdge(e.from_node_id, e.to_node_id, { _edge: e });
        }
      });

      dagre.layout(gDag);

      nodes.forEach(n => {
        const pos = gDag.node(n.node_id);
        if (!pos) return;
        let x, y;
        if (isStrict) {
          x = pos.x; y = pos.y;
        } else {
          const cached = positionCache[n.node_id];
          x = cached ? cached.x : pos.x;
          y = cached ? cached.y : pos.y;
          positionCache[n.node_id] = { x, y };
        }
        const sn = { ...n, id: n.node_id, x, y };
        simNodes.push(sn);
        simNodeMap[sn.id] = sn;
      });

      // Always capture dagre edge routes (strict uses fresh layout; free/cached reuse positions).
      gDag.edges().forEach(e => {
        const ed = gDag.edge(e);
        if (ed && ed.points) dagreEdgePoints[e.v + '->' + e.w] = ed.points;
      });
    }

    simEdges = edges
      .filter(e => simNodeMap[e.from_node_id] && simNodeMap[e.to_node_id])
      .map(e => ({
        ...e,
        source: simNodeMap[e.from_node_id],
        target: simNodeMap[e.to_node_id],
        _points: dagreEdgePoints[e.from_node_id + '->' + e.to_node_id] || null,
      }));

    this._currentEdges = simEdges;

    const self = this;
    const svg = d3.select(this._container).append('svg')
      .attr('class', 'graph-svg')
      .attr('width', '100%').attr('height', '100%')
      .attr('viewBox', `0 0 ${width} ${height}`);
    if (!compact) svg.style('min-height', '500px');

    const defs = svg.append('defs');
    defs.append('marker')
      .attr('id', 'arrowhead').attr('viewBox', '0 0 10 6')
      .attr('refX', 10).attr('refY', 3)
      .attr('markerWidth', 8).attr('markerHeight', 6)
      .attr('orient', 'auto')
      .append('path').attr('d', 'M0,0L10,3L0,6').attr('fill', '#999');

    const g = svg.append('g');

    const zoom = d3.zoom().scaleExtent([0.1, 4]).on('zoom', (event) => {
      g.attr('transform', event.transform);
    });
    if (!compact) svg.call(zoom);
    this._zoom = zoom;
    this._viewport = { width, height };

    if (compact && simNodes.length > 0) {
      const xs = simNodes.map(n => n.x), ys = simNodes.map(n => n.y);
      const pad = 70;
      const bx = Math.min(...xs) - pad, by = Math.min(...ys) - pad;
      const bw = Math.max(...xs) - bx + pad, bh = Math.max(...ys) - by + pad;
      svg.attr('viewBox', `${bx} ${by} ${bw} ${bh}`).attr('preserveAspectRatio', 'xMidYMid meet');
    } else if (simNodes.length > 0) {
      const xs = simNodes.map(n => n.x), ys = simNodes.map(n => n.y);
      const pad = 80;
      const bx = Math.min(...xs) - pad, by = Math.min(...ys) - pad;
      const bw = Math.max(...xs) - bx + pad * 2, bh = Math.max(...ys) - by + pad * 2;
      // Fit everything when it stays readable (node text is 11px; below
      // ~0.55 it turns to fuzz). Beyond that, keep 0.55 and center on the
      // content — both edges are cut equally and panning is obvious. The
      // old "fit the smaller dimension" rule cut off whole frames on the
      // first paint, which read as a broken diagram.
      const fitAll = Math.min(width / bw, height / bh);
      const scale = Math.min(Math.max(fitAll, 0.55), 1.5);
      const cx = bx + bw / 2, cy = by + bh / 2;
      const tx = width / 2 - cx * scale;
      const ty = height / 2 - cy * scale;
      svg.call(zoom.transform, d3.zoomIdentity.translate(tx, ty).scale(scale));
    }

    if (useCompound && gDag) {
      const boxGroup = g.append('g').attr('class', 'domain-boxes');
      groupKeys.forEach(gk => {
        const gNode = gDag.node('group_' + gk);
        if (!gNode) return;
        const gg = boxGroup.append('g').attr('class', 'domain-g');
        const meta = groupMeta[gk] || {};
        // Frame color follows the group's own layer (a broker frame is
        // infra-blue, a service frame service-red); inferred frames are
        // dashed and say on what basis they exist.
        const frameColor = meta.layer ? self.layerColor(meta.layer) : null;
        const rect = gg.append('rect')
          .attr('class', 'domain-box')
          .attr('x', gNode.x - gNode.width / 2).attr('y', gNode.y - gNode.height / 2)
          .attr('width', gNode.width).attr('height', gNode.height);
        if (frameColor) rect.attr('stroke', frameColor);
        if (meta.inferred) rect.attr('stroke-dasharray', '6,3');
        const frameMark = strongestMark(meta.marks);
        if (frameMark) {
          rect.style('stroke', MARK_COLORS[frameMark]).style('stroke-width', '3px');
          // A long note easily overruns a small frame and collides with the
          // domain label on the same baseline — clip to a budget derived
          // from the frame's own width (half the frame, ~5.5px/char at this
          // font-size); the full line still reads on hover via <title>.
          const line = markLine(meta.marks, gk);
          const budget = Math.max(10, Math.floor((gNode.width / 2 - 16) / 5.5));
          gg.append('text').attr('class', 'domain-mark')
            .attr('text-anchor', 'end')
            .attr('x', gNode.x + gNode.width / 2 - 10).attr('y', gNode.y - gNode.height / 2 + 14)
            .attr('font-size', 9).attr('font-weight', 600).attr('fill', MARK_COLORS[frameMark])
            .text(clip(line, budget))
            .append('title').text(line);
        }
        const label = gg.append('text')
          .attr('class', 'domain-label')
          .attr('x', gNode.x - gNode.width / 2 + 10)
          .attr('y', gNode.y - gNode.height / 2 + 14);
        if (frameColor) label.attr('fill', frameColor);
        label.text(String(meta.label || gk).toUpperCase() + (meta.kind && meta.kind !== 'service' && meta.kind !== 'domain' && meta.kind !== 'unknown' ? ' · ' + meta.kind : ''));
        if (meta.note) {
          gg.append('text')
            .attr('class', 'domain-note')
            .attr('x', gNode.x - gNode.width / 2 + 10)
            .attr('y', gNode.y - gNode.height / 2 + 26)
            .attr('font-size', 8).attr('fill', 'var(--text-muted, #8a7355)')
            .attr('font-style', 'italic')
            .text((meta.inferred ? 'inferred: ' : '') + meta.note);
        }
        // Group frames are drillable (e.g. C3-all service frame → that
        // service's focused C3). The handler decides what a group means.
        if (callbacks.onGroupDblClick) {
          gg.style('cursor', 'pointer')
            .on('dblclick', (event) => {
              event.stopPropagation();
              callbacks.onGroupDblClick(gk, (groupMeta[gk] && groupMeta[gk].label) || gk);
            });
        }
      });
    }

    const edgeG = g.selectAll('.edge-g').data(simEdges).enter().append('g').attr('class', 'edge-g');

    const edgePathD = e => {
      if (e._points && e._points.length >= 2) {
        return 'M' + e._points.map(p => p.x + ',' + p.y).join('L');
      }
      return `M${e.source.x},${e.source.y}L${e.target.x},${e.target.y}`;
    };
    // Provenance on the stroke: an arrow the graph only inferred, or that
    // no evidence row backs, is dashed; a synthesized arrow (uses_infra,
    // "via") is dotted. Solid = hard, evidence-backed.
    const edgeDash = e => {
      if (e.derivation === 'synthesized') return '2,3';
      if (e.derivation === 'inferred' || e.derivation === 'unknown') return '6,3';
      if (e.derivation !== 'view' && e._evidenceCount === 0) return '6,3';
      if (typeof e.confidence === 'number' && e.confidence < 0.7) return '6,3';
      return null;
    };

    // Wide invisible hit area so an arrow is clickable/hoverable like a card.
    edgeG.append('path')
      .attr('class', 'edge-hit')
      .attr('d', edgePathD)
      .attr('stroke', 'transparent')
      .attr('stroke-width', 12)
      .attr('fill', 'none')
      .style('cursor', callbacks.onEdgeClick ? 'pointer' : 'default');

    edgeG.append('path')
      .attr('class', 'edge-path')
      .attr('d', edgePathD)
      .attr('stroke', e => {
        const cat = edgeCategoryLookup[e.edge_type];
        return cat ? cat.color : '#666';
      })
      .attr('stroke-width', 1.5)
      // Agent marks win over the category color (inline style beats CSS).
      .style('stroke', e => { const k = strongestMark(e._marks); return k ? MARK_COLORS[k] : null; })
      .style('stroke-width', e => strongestMark(e._marks) ? '3px' : null)
      .attr('stroke-dasharray', edgeDash)
      .attr('fill', 'none')
      .attr('marker-end', 'url(#arrowhead)')
      .attr('opacity', e => (marks.path && marks.path.size > 0) ?
        (e._isPath ? 1 : 0.3) : 0.7);

    edgeG.on('mouseenter', (event, e) => {
      self._showEdgeTooltip(event, e, edgeCategoryLookup);
      d3.select(event.currentTarget).select('.edge-path').attr('stroke-width', 3);
    });
    edgeG.on('mousemove', (event) => self._moveTooltip(event));
    edgeG.on('mouseleave', (event, e) => {
      self._hideTooltip();
      d3.select(event.currentTarget).select('.edge-path').attr('stroke-width', strongestMark(e._marks) ? 3 : 1.5);
    });
    edgeG.on('click', (event, e) => {
      if (!callbacks.onEdgeClick) return;
      event.stopPropagation();
      callbacks.onEdgeClick(e);
    });

    // Edge labels sit on the routed path (its middle point), not on the
    // straight source→target chord, and read at 10px with a background halo
    // — a diagram whose arrows have no readable protocol answers nothing.
    const labelPoint = e => {
      if (e._points && e._points.length >= 2) {
        const pts = e._points;
        if (pts.length % 2 === 1) return pts[(pts.length - 1) / 2];
        const a = pts[pts.length / 2 - 1], b = pts[pts.length / 2];
        return { x: (a.x + b.x) / 2, y: (a.y + b.y) / 2 };
      }
      return { x: (e.source.x + e.target.x) / 2, y: (e.source.y + e.target.y) / 2 };
    };
    // Parallel arrows between the same two nodes (pub + sub, calls + sub)
    // would stack their labels on one point: stagger them.
    const pairIndex = new Map();
    const pairSlot = new Map();
    simEdges.forEach(e => {
      const a = String(e.source.id), b = String(e.target.id);
      const key = a < b ? a + '|' + b : b + '|' + a;
      const n = pairIndex.get(key) || 0;
      pairSlot.set(e, n);
      pairIndex.set(key, n + 1);
    });
    if (showEdgeLabels) {
      edgeG.append('text')
        .attr('class', 'view-edge-label')
        .attr('x', e => labelPoint(e).x)
        .attr('y', e => labelPoint(e).y - 5 + (pairSlot.get(e) || 0) * 12)
        .attr('text-anchor', 'middle')
        .attr('fill', e => {
          const cat = edgeCategoryLookup[e.edge_type];
          return cat ? cat.color : 'var(--text-secondary, #5c4e3c)';
        })
        .attr('font-size', 10)
        .attr('font-weight', 600)
        .attr('paint-order', 'stroke')
        .attr('stroke', 'var(--bg, #faf6ef)')
        .attr('stroke-width', 3)
        .attr('stroke-linejoin', 'round')
        .text(e => {
          const cat = edgeCategoryLookup[e.edge_type];
          return cat ? cat.shortLabel : '';
        });
    }

    const nw = LAYOUT.nodeWidth, nh = LAYOUT.nodeHeight;

    const nodeG = g.selectAll('.node-g').data(simNodes).enter().append('g')
      .attr('class', 'node-g')
      .attr('transform', d => `translate(${d.x},${d.y})`)
      .style('cursor', 'pointer');

    // Salience tier → resting opacity: primary full, secondary slightly muted,
    // detail clearly recessed. Gives a visual importance hierarchy without
    // hiding anything. (Undefined tier — Explore/raw graph — reads as primary.)
    const tierOpacity = d => d._tier === 'secondary' ? 0.85 : (d._tier === 'detail' ? 0.6 : 1);
    nodeG.attr('opacity', d => {
      // Ref nodes (external references in Explore) are always dimmed
      if (d._isRef) return 0.4;
      if (!marks.manual || marks.manual.size === 0) return tierOpacity(d);
      if (marks.manual.has(d.id)) return 1;
      if (marks.path && marks.path.has(d.id)) return 0.9;
      if (marks.neighbor && marks.neighbor.has(d.id)) return 0.5;
      return 0.5;
    });

    // White background for clean rounded corners
    nodeG.append('rect')
      .attr('x', -nw / 2).attr('y', -nh / 2)
      .attr('width', nw).attr('height', nh)
      .attr('rx', 6).attr('ry', 6)
      .attr('fill', '#fff');

    // Semi-transparent layer-colored rect on top
    nodeG.append('rect')
      .attr('class', 'node-rect')
      .attr('x', -nw / 2).attr('y', -nh / 2)
      .attr('width', nw).attr('height', nh)
      .attr('rx', 6).attr('ry', 6)
      // Stale cards grey out (awaiting re-verification); contradicted
      // cards keep their layer fill but get a red border.
      .attr('fill', d => d.status === 'stale' ? '#e8e2d8' : self.layerColor(d.layer) + '40')
      .attr('stroke', d => d.status === 'stale' ? '#9a8f7f' : (d.status === 'contradicted' ? '#c25757' : self.layerColor(d.layer)))
      // Primary salience gets a heavier border; collapsed/badge nodes get a
      // dashed border to read as "folded / secondary form".
      .attr('stroke-width', d => (marks.manual && marks.manual.has(d.id)) ? 2.5 : (d._tier === 'primary' ? 2 : 1.2))
      .attr('stroke-dasharray', d => (d.status === 'stale' || d._renderMode === 'collapsed_group' || d._renderMode === 'badge') ? '4,2' : null)
      .style('stroke', d => { const k = strongestMark(d._marks); return k ? MARK_COLORS[k] : null; })
      .style('stroke-width', d => strongestMark(d._marks) ? '3px' : null);

    // Repo tag (federation) in the card's top-right corner: "@analytics".
    nodeG.filter(d => !!d._repo).append('text')
      .attr('class', 'node-repo')
      .attr('text-anchor', 'end')
      .attr('x', nw / 2 - 5).attr('y', -nh / 2 + 10)
      .attr('font-size', 8).attr('font-weight', 600)
      .attr('fill', 'var(--text-muted, #8a7355)')
      .text(d => '@' + d._repo);

    // Status tag (stale / contradicted) in the card's top-left corner.
    nodeG.filter(d => d.status === 'stale' || d.status === 'contradicted').append('text')
      .attr('class', 'node-status')
      .attr('x', -nw / 2 + 5).attr('y', -nh / 2 + 10)
      .attr('font-size', 8).attr('font-weight', 700)
      .attr('fill', d => d.status === 'stale' ? '#9a8f7f' : '#c25757')
      .text(d => d.status);

    nodeG.append('text')
      .attr('text-anchor', 'middle').attr('dy', d => d._subtitle2 ? -6 : -2)
      .attr('fill', 'var(--text, #333)').attr('font-size', 11)
      .text(d => {
        const name = d.name || d.node_key || d.id;
        return name.length > 18 ? name.slice(0, 16) + '...' : name;
      });

    nodeG.append('text')
      .attr('text-anchor', 'middle').attr('dy', d => d._subtitle2 ? 8 : 14)
      .attr('fill', d => self.layerColor(d.layer))
      .attr('font-size', 9).attr('opacity', 0.8)
      .text(d => clip((d._subtitle || d.node_type || '') + (d._dissolvedGroup && d._dissolvedGroup !== d._repo ? ' · ' + d._dissolvedGroup : ''), 26));
    // Second subtitle line: endpoint pills ("GET /x, +3 more") or the
    // rest of a frame's stats; the tooltip carries the full text.
    nodeG.filter(d => !!d._subtitle2).append('text')
      .attr('text-anchor', 'middle').attr('dy', 19)
      .attr('fill', 'var(--text-muted, #8a7355)')
      .attr('font-size', 8).attr('opacity', 0.9)
      .text(d => clip(d._subtitle2, 28));

    // Agent marks: a caption under the card in the mark's color; the
    // tooltip carries every mark in full.
    nodeG.filter(d => (d._marks || []).length > 0).append('text')
      .attr('class', 'node-mark')
      .attr('text-anchor', 'middle').attr('y', nh / 2 + 11)
      .attr('font-size', 8.5).attr('font-weight', 600)
      .attr('fill', d => MARK_COLORS[strongestMark(d._marks)])
      .text(d => clip(markLine(d._marks, d.node_key), 34));

    // Drill affordance (e.g. the C1 system box): small "→ drill" link in the
    // bottom-right corner; click = callbacks.onDrill (same as dblclick).
    nodeG.filter(d => !!d._drill).append('text')
      .attr('class', 'node-drill')
      .attr('text-anchor', 'end')
      .attr('x', nw / 2 - 5).attr('y', -nh / 2 + 11)
      .attr('fill', d => self.layerColor(d.layer))
      .attr('font-size', 9).attr('font-weight', 600)
      .style('cursor', 'pointer')
      .text('→ drill')
      .on('click', (event, d) => {
        event.stopPropagation();
        if (callbacks.onDrill) callbacks.onDrill(d);
        else if (callbacks.onDblClick) callbacks.onDblClick(d);
      });

    nodeG.on('click', (event, d) => {
      event.stopPropagation();
      if (callbacks.onClick) callbacks.onClick(d, simEdges);
      self.highlightNode(d.id);
    });

    nodeG.on('mouseenter', (event, d) => {
      self._showTooltip(event, d);
      self._emphasizeEdges(d.id, true);
    });
    nodeG.on('mousemove', (event) => self._moveTooltip(event));
    nodeG.on('mouseleave', (event, d) => {
      self._hideTooltip();
      self._emphasizeEdges(d.id, false);
    });

    nodeG.on('dblclick', (event, d) => {
      event.stopPropagation();
      if (callbacks.onDblClick) callbacks.onDblClick(d);
    });

    if (!isStrict) {
      const drag = d3.drag()
        .on('drag', (event, d) => {
          d.x = event.x; d.y = event.y;
          d3.select(event.sourceEvent.target.closest('.node-g'))
            .attr('transform', `translate(${d.x},${d.y})`);
          simEdges.forEach(e => {
            if (e.source.id === d.id || e.target.id === d.id) {
              const eg = edgeG.filter(ed => ed === e);
              eg.select('.edge-path').attr('d',
                `M${e.source.x},${e.source.y}L${e.target.x},${e.target.y}`);
              eg.select('.view-edge-label')
                .attr('x', (e.source.x + e.target.x) / 2)
                .attr('y', (e.source.y + e.target.y) / 2 - 6);
            }
          });
          if (callbacks.onDrag) callbacks.onDrag(d.id, d.x, d.y);
        });
      nodeG.call(drag);
    }

    svg.on('click', () => {
      self.clearHighlight();
      if (callbacks.onBackgroundClick) callbacks.onBackgroundClick();
    });

    this._svg = svg;
    this._nodeG = nodeG;
    this._edgeG = edgeG;
  }

  highlightNode(nodeId) {
    if (!this._svg || !this._currentEdges) return;
    const id = String(nodeId);
    const connected = new Set([id]);
    this._currentEdges.forEach(e => {
      if (String(e.source.id || e.source) === id) connected.add(String(e.target.id || e.target));
      if (String(e.target.id || e.target) === id) connected.add(String(e.source.id || e.source));
    });

    this._nodeG.attr('opacity', d => connected.has(String(d.id)) ? 1 : 0.2);
    this._edgeG.attr('opacity', e => {
      const eid = String(e.source.id || e.source);
      const tid = String(e.target.id || e.target);
      return (eid === id || tid === id) ? 1 : 0.08;
    });
    // Bring the selected neighborhood into view — a highlight the user
    // can't see (neighbors offscreen) reads as "everything went grey".
    this._zoomToIds(connected, id);
  }

  clearHighlight() {
    if (!this._svg) return;
    // Boundary/ref nodes keep their dim — only highlight overrides reset.
    this._nodeG.attr('opacity', d => (d._isRef ? 0.4 : 1));
    this._edgeG.attr('opacity', 0.7);
  }

  // Search hook: emphasize matching nodes and bring them into view.
  // Returns the number of matches (0 = nothing found, caller may hint).
  focusMatches(query) {
    if (!this._svg || !this._nodeG) return 0;
    const q = String(query || '').trim().toLowerCase();
    if (!q) { this.clearHighlight(); return 0; }
    const ids = new Set();
    this._nodeG.each(d => {
      const hay = ((d.name || '') + ' ' + (d.node_key || '')).toLowerCase();
      if (hay.includes(q)) ids.add(String(d.id));
    });
    if (ids.size === 0) return 0;
    this._nodeG.attr('opacity', d => ids.has(String(d.id)) ? 1 : 0.2);
    this._edgeG.attr('opacity', e => {
      const eid = String(e.source.id || e.source);
      const tid = String(e.target.id || e.target);
      return (ids.has(eid) && ids.has(tid)) ? 0.9 : 0.08;
    });
    this._zoomToIds(ids);
    return ids.size;
  }

  _zoomToIds(ids, centerId) {
    if (!this._zoom || !this._nodeG || !this._viewport) return;
    const pts = [];
    this._nodeG.each(d => { if (ids.has(String(d.id))) pts.push(d); });
    if (pts.length === 0) return;
    const pad = 90;
    const xs = pts.map(n => n.x), ys = pts.map(n => n.y);
    const bx = Math.min(...xs) - pad, by = Math.min(...ys) - pad;
    const bw = Math.max(...xs) - bx + pad * 2, bh = Math.max(...ys) - by + pad * 2;
    const { width, height } = this._viewport;
    let scale = Math.min(width / bw, height / bh, 1.2);
    let cx = bx + bw / 2, cy = by + bh / 2;
    // Fitting a neighborhood that spans the whole layout would zoom back out
    // to illegible scale. Stay readable instead and center on the subject —
    // far neighbors are reachable by pan, near ones are visible right away.
    const MIN_READABLE = 0.45;
    if (scale < MIN_READABLE) {
      scale = MIN_READABLE;
      const center = centerId && pts.find(d => String(d.id) === String(centerId));
      if (center) { cx = center.x; cy = center.y; }
    }
    this._svg.transition().duration(350).call(
      this._zoom.transform,
      d3.zoomIdentity.translate(width / 2 - cx * scale, height / 2 - cy * scale).scale(scale)
    );
  }

  _emphasizeEdges(nodeId, on) {
    if (!this._edgeG) return;
    const id = String(nodeId);
    const touches = e =>
      String(e.source.id || e.source) === id || String(e.target.id || e.target) === id;
    this._edgeG.selectAll('.edge-path')
      .attr('stroke-width', function() {
        const e = d3.select(this.parentNode).datum();
        return (on && touches(e)) ? 2.5 : 1.5;
      });
  }

  _tooltipEl() {
    let el = document.getElementById('__node_tooltip');
    if (!el) {
      el = document.createElement('div');
      el.id = '__node_tooltip';
      el.className = 'node-hover-tooltip';
      el.style.display = 'none';
      document.body.appendChild(el);
    }
    return el;
  }

  // Mark lines for a tooltip, built with textContent (agent text is never markup).
  _markLines(el, marks, ownKey) {
    if (!(marks || []).length) return;
    const box = document.createElement('div');
    marks.forEach(m => {
      const line = document.createElement('div');
      line.className = 'nht-type';
      line.style.color = MARK_COLORS[m.kind] || '';
      line.style.opacity = '1';
      line.textContent = markDetail(m, ownKey);
      box.appendChild(line);
    });
    el.insertBefore(box, el.querySelector('.nht-hint'));
  }

  _showTooltip(event, d) {
    const el = this._tooltipEl();
    const esc = t => String(t || '').replace(/[&<>]/g, c => ({'&':'&amp;','<':'&lt;','>':'&gt;'}[c]));
    el.innerHTML =
      '<div class="nht-name">' + esc(d.name || d.node_key) + '</div>' +
      '<div class="nht-type">' + esc(d.node_type || '') + (d.layer ? ' · ' + esc(d.layer) : '') + (d._repo ? ' · repo ' + esc(d._repo) : '') + '</div>' +
      (d.file_path ? '<div class="nht-type" style="font-family:var(--font-mono)">' + esc(d.file_path) + '</div>' : '') +
      (d._renderMode && d._renderMode !== 'box' ? '<div class="nht-type">' + esc(d._renderMode) + (d._salienceReason ? ' · ' + esc(d._salienceReason) : '') + '</div>' : '') +
      (d._vgroup && d._statsFull ? '<div class="nht-type">' + esc(d._statsFull) + '</div>' : '') +
      (d._pillsFull ? '<div class="nht-type">exposes ' + esc(d._pillsFull) + '</div>' : '') +
      (d._boundary ? '<div class="nht-type">outside this scope</div>' : '') +
      (typeof d.confidence === 'number' && !d._vgroup ? nodeSignalLine(d, esc) : '') +
      '<div class="nht-hint">click — select · double-click — drill</div>';
    this._markLines(el, d._marks, d.node_key);
    el.style.display = 'block';
    this._moveTooltip(event);
  }

  _showEdgeTooltip(event, e, lookup) {
    const el = this._tooltipEl();
    const esc = t => String(t || '').replace(/[&<>]/g, c => ({'&':'&amp;','<':'&lt;','>':'&gt;'}[c]));
    const cat = (lookup || {})[e.edge_type];
    const kind = e._kind || e.edge_type;
    const from = e._fromName || (e.source && e.source.name) || e.from_node_id;
    const to = e._toName || (e.target && e.target.name) || e.to_node_id;
    const prov = edgeSignalLine(e, esc);
    el.innerHTML =
      '<div class="nht-name">' + esc(from) + ' → ' + esc(to) + '</div>' +
      '<div class="nht-type">' + esc(cat ? cat.shortLabel : kind) + (e.weight > 1 ? ' · ' + e.weight + ' edges' : '') + '</div>' +
      (prov ? '<div class="nht-type">' + prov + '</div>' : '') +
      '<div class="nht-hint">click — inspect evidence</div>';
    this._markLines(el, e._marks, null);
    el.style.display = 'block';
    this._moveTooltip(event);
  }

  _moveTooltip(event) {
    const el = this._tooltipEl();
    if (el.style.display === 'none') return;
    const pad = 14;
    let x = event.clientX + pad, y = event.clientY + pad;
    const r = el.getBoundingClientRect();
    if (x + r.width > innerWidth - 8) x = event.clientX - r.width - pad;
    if (y + r.height > innerHeight - 8) y = event.clientY - r.height - pad;
    el.style.left = x + 'px';
    el.style.top = y + 'px';
  }

  _hideTooltip() {
    const el = document.getElementById('__node_tooltip');
    if (el) el.style.display = 'none';
  }

  destroy() {
    this._hideTooltip();
    if (this._container) this._container.innerHTML = '';
    this._svg = null;
    this._currentEdges = null;
    this._nodeG = null;
    this._edgeG = null;
  }
}
