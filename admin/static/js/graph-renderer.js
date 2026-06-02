// graph-renderer.js — Single D3/Dagre render pipeline.
// Stateless: render what you're given. No filtering, no state management, no preset awareness.

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

const LAYER_COLORS = {
  code: '#4fc3f7',
  service: '#81c784',
  contract: '#ffb74d',
  data: '#ce93d8',
  flow: '#4dd0e1',
  ownership: '#a1887f',
  infra: '#90a4ae',
  ci: '#fff176',
};

export class GraphRenderer {
  constructor(container) {
    this._container = container;
    this._svg = null;
    this._currentEdges = null;
  }

  layerColor(layer) {
    return LAYER_COLORS[layer] || '#bbb';
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
        const sharedTypes = new Set(['broker', 'database', 'cache', 'queue', 'infrastructure', 'external_system', 'topic', 'async_channel']);
        if (n.layer === 'infra' || sharedTypes.has(n.node_type)) return;
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
        members.forEach(n => { n._group = null; });
      }
    }
    const useCompound = groupKeys.length > 0;

    let simNodes = [], simNodeMap = {}, simEdges = [];
    const dagreEdgePoints = {};
    let gDag;

    if (typeof dagre !== 'undefined') {
      gDag = new dagre.graphlib.Graph({ compound: useCompound });
      gDag.setGraph({
        rankdir: LAYOUT.rankDir, ranksep: LAYOUT.rankSep,
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

      if (isStrict) {
        gDag.edges().forEach(e => {
          const ed = gDag.edge(e);
          if (ed && ed.points) dagreEdgePoints[e.v + '->' + e.w] = ed.points;
        });
      }
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

    const svg = d3.select(this._container).append('svg')
      .attr('class', 'graph-svg')
      .attr('width', '100%').attr('height', '100%')
      .attr('viewBox', `0 0 ${width} ${height}`)
      .style('min-height', '500px');

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
    svg.call(zoom);

    if (simNodes.length > 0) {
      const xs = simNodes.map(n => n.x), ys = simNodes.map(n => n.y);
      const pad = 80;
      const bx = Math.min(...xs) - pad, by = Math.min(...ys) - pad;
      const bw = Math.max(...xs) - bx + pad * 2, bh = Math.max(...ys) - by + pad * 2;
      const scale = Math.min(width / bw, height / bh, 1.5);
      const tx = (width - bw * scale) / 2 - bx * scale;
      const ty = (height - bh * scale) / 2 - by * scale;
      svg.call(zoom.transform, d3.zoomIdentity.translate(tx, ty).scale(scale));
    }

    if (useCompound && gDag) {
      const boxGroup = g.append('g').attr('class', 'domain-boxes');
      groupKeys.forEach(gk => {
        const gNode = gDag.node('group_' + gk);
        if (!gNode) return;
        const gg = boxGroup.append('g').attr('class', 'domain-g');
        gg.append('rect')
          .attr('class', 'domain-box')
          .attr('x', gNode.x - gNode.width / 2).attr('y', gNode.y - gNode.height / 2)
          .attr('width', gNode.width).attr('height', gNode.height);
        gg.append('text')
          .attr('class', 'domain-label')
          .attr('x', gNode.x - gNode.width / 2 + 10)
          .attr('y', gNode.y - gNode.height / 2 + 14)
          .text(gk.toUpperCase());
      });
    }

    const edgeG = g.selectAll('.edge-g').data(simEdges).enter().append('g').attr('class', 'edge-g');

    edgeG.append('path')
      .attr('class', 'edge-path')
      .attr('d', e => {
        if (e._points && e._points.length >= 2) {
          return 'M' + e._points.map(p => p.x + ',' + p.y).join('L');
        }
        return `M${e.source.x},${e.source.y}L${e.target.x},${e.target.y}`;
      })
      .attr('stroke', e => {
        const cat = edgeCategoryLookup[e.edge_type];
        return cat ? cat.color : '#666';
      })
      .attr('stroke-width', 1.5)
      .attr('fill', 'none')
      .attr('marker-end', 'url(#arrowhead)')
      .attr('opacity', e => (marks.path && marks.path.size > 0) ?
        (e._isPath ? 1 : 0.3) : 0.7);

    if (showEdgeLabels) {
      edgeG.append('text')
        .attr('class', 'edge-label')
        .attr('x', e => (e.source.x + e.target.x) / 2)
        .attr('y', e => (e.source.y + e.target.y) / 2 - 6)
        .attr('text-anchor', 'middle')
        .attr('fill', 'var(--text-muted, #888)')
        .attr('font-size', 9)
        .text(e => {
          const cat = edgeCategoryLookup[e.edge_type];
          return cat ? cat.shortLabel : '';
        });
    }

    const self = this;
    const nw = LAYOUT.nodeWidth, nh = LAYOUT.nodeHeight;

    const nodeG = g.selectAll('.node-g').data(simNodes).enter().append('g')
      .attr('class', 'node-g')
      .attr('transform', d => `translate(${d.x},${d.y})`)
      .style('cursor', 'pointer');

    nodeG.attr('opacity', d => {
      // Ref nodes (external references in Explore) are always dimmed
      if (d._isRef) return 0.4;
      if (!marks.manual || marks.manual.size === 0) return 1;
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
      .attr('fill', d => self.layerColor(d.layer) + '40')
      .attr('stroke', d => self.layerColor(d.layer))
      .attr('stroke-width', d => (marks.manual && marks.manual.has(d.id)) ? 2.5 : 1.5);

    nodeG.append('text')
      .attr('text-anchor', 'middle').attr('dy', -2)
      .attr('fill', 'var(--text, #333)').attr('font-size', 11)
      .text(d => {
        const name = d.name || d.node_key || d.id;
        return name.length > 18 ? name.slice(0, 16) + '...' : name;
      });

    nodeG.append('text')
      .attr('text-anchor', 'middle').attr('dy', 14)
      .attr('fill', d => self.layerColor(d.layer))
      .attr('font-size', 9).attr('opacity', 0.8)
      .text(d => d.node_type || '');

    nodeG.on('click', (event, d) => {
      event.stopPropagation();
      if (callbacks.onClick) callbacks.onClick(d, simEdges);
      self.highlightNode(d.id);
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
              eg.select('.edge-label')
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

    this._nodeG.attr('opacity', d => connected.has(String(d.id)) ? 1 : 0.15);
    this._edgeG.attr('opacity', e => {
      const eid = String(e.source.id || e.source);
      const tid = String(e.target.id || e.target);
      return (eid === id || tid === id) ? 1 : 0.05;
    });
  }

  clearHighlight() {
    if (!this._svg) return;
    this._nodeG.attr('opacity', 1);
    this._edgeG.attr('opacity', 0.7);
  }

  destroy() {
    if (this._container) this._container.innerHTML = '';
    this._svg = null;
    this._currentEdges = null;
    this._nodeG = null;
    this._edgeG = null;
  }
}
