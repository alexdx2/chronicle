# Knowledge Growth Chart Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the flat "Scan History" card with a stacked area chart showing node and edge count growth across scans.

**Architecture:** Single-file change to `internal/admin/static/index.html`. Replace the Scan History card HTML with an SVG chart container, add CSS for the chart and tooltip, add an Alpine `x-effect` watcher that renders the chart with D3 (already loaded) when scan data arrives.

**Tech Stack:** D3.js (already loaded), Alpine.js (already used), inline SVG

---

### Task 1: Replace Scan History card HTML with chart container

**Files:**
- Modify: `internal/admin/static/index.html:455-474` (Scan History card)

- [ ] **Step 1: Replace the Scan History card with the chart container**

Replace lines 455-474 (the entire `<!-- Scan History -->` card) with:

```html
      <!-- Knowledge Growth -->
      <div class="card">
        <div class="card-header"><h3>Knowledge Growth</h3></div>
        <div class="card-body" style="padding:12px">
          <template x-if="loading.scans">
            <div class="loader"><div class="spinner"></div></div>
          </template>
          <template x-if="!loading.scans && (!scans || scans.length < 2)">
            <div style="text-align:center;color:var(--text-muted);padding:12px">Not enough scans to chart</div>
          </template>
          <template x-if="!loading.scans && scans && scans.length >= 2">
            <div x-effect="renderGrowthChart($el, scans)" style="width:100%;height:200px"></div>
          </template>
        </div>
      </div>
```

- [ ] **Step 2: Verify the page loads without errors**

Open the admin dashboard in a browser. The card should say "Not enough scans to chart" or show the loading spinner (the `renderGrowthChart` function doesn't exist yet, so if there are scans, the console will show an error — that's expected at this step).

- [ ] **Step 3: Commit**

```bash
git add internal/admin/static/index.html
git commit -m "feat: replace Scan History card with Knowledge Growth chart container"
```

---

### Task 2: Add CSS for the growth chart and tooltip

**Files:**
- Modify: `internal/admin/static/index.html:186-188` (CSS section, replace `.scan-row` styles)

- [ ] **Step 1: Replace the `.scan-row` CSS with growth chart styles**

Replace lines 186-188 (the `/* Scan history */` comment and `.scan-row` rules):

```css
/* Scan history */
.scan-row{display:grid;grid-template-columns:1fr 60px 60px auto;gap:8px;padding:6px 0;border-bottom:1px solid var(--border);font-size:12px;align-items:center}
.scan-row:last-child{border:none}
```

with:

```css
/* Knowledge growth chart */
.growth-chart .area-nodes{fill:#6b8f71;fill-opacity:.2}
.growth-chart .area-edges{fill:#b8860b;fill-opacity:.15}
.growth-chart .line-nodes{fill:none;stroke:#6b8f71;stroke-width:2}
.growth-chart .line-edges{fill:none;stroke:#b8860b;stroke-width:2}
.growth-chart .dot-nodes{fill:#6b8f71}
.growth-chart .dot-edges{fill:#b8860b}
.growth-chart .grid-line{stroke:var(--border);stroke-width:0.5;stroke-dasharray:4}
.growth-chart .axis text{font-size:10px;fill:var(--text-muted);font-family:var(--font-mono)}
.growth-chart .axis line,.growth-chart .axis path{stroke:var(--border)}
.growth-chart .legend{font-size:11px;fill:var(--text-muted)}
.growth-tooltip{position:absolute;pointer-events:none;background:var(--card);border:1px solid var(--border);border-radius:var(--radius);padding:8px 10px;font-size:11px;line-height:1.5;box-shadow:var(--shadow);white-space:nowrap;z-index:10}
.growth-tooltip .tt-title{font-weight:600;color:var(--text);margin-bottom:2px}
.growth-tooltip .tt-nodes{color:#6b8f71}
.growth-tooltip .tt-edges{color:#b8860b}
```

- [ ] **Step 2: Commit**

```bash
git add internal/admin/static/index.html
git commit -m "style: add CSS for knowledge growth chart and tooltip"
```

---

### Task 3: Implement `renderGrowthChart` function

**Files:**
- Modify: `internal/admin/static/index.html` (add function before the closing `</script>` tag, after the `dashboard()` function)

- [ ] **Step 1: Find the closing script tag**

Look for the `</script>` tag that closes the main script block (after the `dashboard()` function ends). Add the `renderGrowthChart` function just before it.

- [ ] **Step 2: Add the `renderGrowthChart` function**

Insert before the closing `</script>`:

```javascript
function renderGrowthChart(el, scans) {
  if (!el || !scans || scans.length < 2) return;

  // Reverse to oldest-first, dedupe by revision_id
  const seen = new Set();
  const data = scans.slice().reverse().filter(s => {
    if (seen.has(s.revision_id)) return false;
    seen.add(s.revision_id);
    return true;
  });

  // Clear previous render
  el.innerHTML = '';

  const margin = {top: 20, right: 16, bottom: 28, left: 40};
  const width = el.clientWidth - margin.left - margin.right;
  const height = 200 - margin.top - margin.bottom;

  if (width <= 0) return;

  const svg = d3.select(el)
    .append('svg')
    .attr('width', el.clientWidth)
    .attr('height', 200)
    .attr('class', 'growth-chart')
    .append('g')
    .attr('transform', `translate(${margin.left},${margin.top})`);

  // Scales
  const x = d3.scaleLinear()
    .domain([0, data.length - 1])
    .range([0, width]);

  const maxVal = d3.max(data, d => Math.max(d.node_count, d.edge_count)) || 10;
  const y = d3.scaleLinear()
    .domain([0, maxVal * 1.1])
    .range([height, 0]);

  // Grid lines
  const ticks = y.ticks(4);
  svg.selectAll('.grid-line')
    .data(ticks.slice(1))
    .join('line')
    .attr('class', 'grid-line')
    .attr('x1', 0).attr('x2', width)
    .attr('y1', d => y(d)).attr('y2', d => y(d));

  // Y axis (counts)
  const yAxis = d3.axisLeft(y).ticks(4).tickSize(0).tickPadding(6);
  svg.append('g').attr('class', 'axis').call(yAxis).select('.domain').remove();

  // X axis (dates) — show a few labels
  const labelCount = Math.min(data.length, 5);
  const labelIndices = [];
  for (let i = 0; i < labelCount; i++) {
    labelIndices.push(Math.round(i * (data.length - 1) / (labelCount - 1)));
  }

  const xAxisG = svg.append('g')
    .attr('class', 'axis')
    .attr('transform', `translate(0,${height})`);

  xAxisG.selectAll('text')
    .data(labelIndices)
    .join('text')
    .attr('x', i => x(i))
    .attr('y', 16)
    .attr('text-anchor', 'middle')
    .text(i => {
      const d = new Date(data[i].created_at);
      return d.toLocaleDateString('en-US', {month: 'short', day: 'numeric'});
    });

  xAxisG.append('line')
    .attr('x1', 0).attr('x2', width)
    .attr('y1', 0).attr('y2', 0)
    .attr('stroke', 'var(--border)');

  // Area + line generators
  const areaGen = (key) => d3.area()
    .x((d, i) => x(i))
    .y0(height)
    .y1(d => y(d[key]));

  const lineGen = (key) => d3.line()
    .x((d, i) => x(i))
    .y(d => y(d[key]));

  // Draw areas
  svg.append('path').datum(data).attr('class', 'area-nodes').attr('d', areaGen('node_count'));
  svg.append('path').datum(data).attr('class', 'area-edges').attr('d', areaGen('edge_count'));

  // Draw lines
  svg.append('path').datum(data).attr('class', 'line-nodes').attr('d', lineGen('node_count'));
  svg.append('path').datum(data).attr('class', 'line-edges').attr('d', lineGen('edge_count'));

  // Legend
  const legend = svg.append('g').attr('transform', `translate(${width - 100}, -8)`);
  legend.append('rect').attr('width', 8).attr('height', 8).attr('rx', 2).attr('fill', '#6b8f71');
  legend.append('text').attr('class', 'legend').attr('x', 12).attr('y', 8).text('Nodes');
  legend.append('rect').attr('x', 55).attr('width', 8).attr('height', 8).attr('rx', 2).attr('fill', '#b8860b');
  legend.append('text').attr('class', 'legend').attr('x', 67).attr('y', 8).text('Edges');

  // Tooltip
  let tooltip = el.querySelector('.growth-tooltip');
  if (!tooltip) {
    tooltip = document.createElement('div');
    tooltip.className = 'growth-tooltip';
    tooltip.style.display = 'none';
    el.style.position = 'relative';
    el.appendChild(tooltip);
  }

  // Dots for nodes
  svg.selectAll('.dot-nodes')
    .data(data)
    .join('circle')
    .attr('class', 'dot-nodes')
    .attr('cx', (d, i) => x(i))
    .attr('cy', d => y(d.node_count))
    .attr('r', 3)
    .on('mouseenter', function(event, d) {
      d3.select(this).attr('r', 5);
      const dt = new Date(d.created_at);
      const dateStr = dt.toLocaleDateString('en-US', {month:'short', day:'numeric'})
        + ' ' + dt.toLocaleTimeString('en-US', {hour:'2-digit', minute:'2-digit', hour12:false});
      tooltip.innerHTML = `<div class="tt-title">Rev ${d.revision_id} · ${dateStr}</div>`
        + `<div class="tt-nodes">${d.node_count} nodes</div>`
        + `<div class="tt-edges">${d.edge_count} edges</div>`
        + `<div style="color:var(--text-muted)">${d.snapshot_kind}</div>`;
      tooltip.style.display = 'block';
      const rect = el.getBoundingClientRect();
      const cx = event.clientX - rect.left;
      tooltip.style.left = (cx + 12) + 'px';
      tooltip.style.top = '8px';
    })
    .on('mouseleave', function() {
      d3.select(this).attr('r', 3);
      tooltip.style.display = 'none';
    });

  // Dots for edges
  svg.selectAll('.dot-edges')
    .data(data)
    .join('circle')
    .attr('class', 'dot-edges')
    .attr('cx', (d, i) => x(i))
    .attr('cy', d => y(d.edge_count))
    .attr('r', 3)
    .on('mouseenter', function(event, d) {
      d3.select(this).attr('r', 5);
      const dt = new Date(d.created_at);
      const dateStr = dt.toLocaleDateString('en-US', {month:'short', day:'numeric'})
        + ' ' + dt.toLocaleTimeString('en-US', {hour:'2-digit', minute:'2-digit', hour12:false});
      tooltip.innerHTML = `<div class="tt-title">Rev ${d.revision_id} · ${dateStr}</div>`
        + `<div class="tt-nodes">${d.node_count} nodes</div>`
        + `<div class="tt-edges">${d.edge_count} edges</div>`
        + `<div style="color:var(--text-muted)">${d.snapshot_kind}</div>`;
      tooltip.style.display = 'block';
      const rect = el.getBoundingClientRect();
      const cx = event.clientX - rect.left;
      tooltip.style.left = (cx + 12) + 'px';
      tooltip.style.top = '8px';
    })
    .on('mouseleave', function() {
      d3.select(this).attr('r', 3);
      tooltip.style.display = 'none';
    });
}
```

- [ ] **Step 3: Verify the chart renders**

Open the admin dashboard. If there are 2+ scans for the domain, the chart should render with two colored area/line series. Hover a dot to see the tooltip with revision details.

- [ ] **Step 4: Commit**

```bash
git add internal/admin/static/index.html
git commit -m "feat: implement knowledge growth chart with D3"
```

---

### Task 4: Manual smoke test

- [ ] **Step 1: Test with no scans**

Open the dashboard for a domain with no scans. Should show "Not enough scans to chart".

- [ ] **Step 2: Test with 1 scan**

If possible, run a single scan. Should still show "Not enough scans to chart" (need >= 2).

- [ ] **Step 3: Test with multiple scans**

Run multiple scans (or use a domain that already has them). Verify:
- Two area series render (green for nodes, gold for edges)
- Dots appear at each scan point
- Hovering a dot shows tooltip with rev ID, date, node count, edge count, kind
- Y-axis auto-scales to the data
- X-axis shows date labels
- Legend shows "Nodes" and "Edges" in top-right

- [ ] **Step 4: Test responsive behavior**

Resize the browser window. The chart should scale with the card width via the SVG viewBox.
