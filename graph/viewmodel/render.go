package viewmodel

import (
	"fmt"
	"sort"
	"strings"
)

// Agent-facing renderings of a View. The dashboard draws the View with
// D3; RenderMermaid and RenderText draw the SAME View for a reader who
// cannot open a browser — an agent answering "show me the architecture",
// a PR description, a wiki page. Same nodes, same arrows, same salience,
// same provenance; deterministic output (sorted, stable ids).

// arrowLabel is the human label of an arrow: the explicit label when the
// engine set one (uses_infra "events"), else the protocol/relationship,
// plus "×N" when the arrow folds several graph edges.
func arrowLabel(e *VEdge) string {
	label := e.Label
	if label == "" {
		switch e.Kind {
		case "CALLS_SERVICE", "CALLS_ENDPOINT":
			label = "HTTP"
		case "PUBLISHES_TOPIC":
			label = "pub"
		case "CONSUMES_TOPIC":
			label = "sub"
		case "INJECTS":
			label = "injects"
		case "USES_MODEL":
			label = "uses"
		case "REFERENCES_MODEL":
			label = "refs"
		case "TRIGGERS_FLOW":
			label = "triggers"
		case "REQUIRES":
			label = "requires"
		case "EXPOSES_ENDPOINT":
			label = "exposes"
		default:
			label = strings.ToLower(strings.ReplaceAll(e.Kind, "_", " "))
		}
	}
	if e.Weight > 1 {
		label += fmt.Sprintf(" ×%d", e.Weight)
	}
	return label
}

// arrowWeak reports whether the arrow's provenance is weak — inferred or
// synthesized, backed by no evidence, or below the confidence bar — the
// same rule the dashboard uses to dash a stroke.
func arrowWeak(e *VEdge) bool {
	switch e.Derivation {
	case "inferred", "unknown", "synthesized":
		return true
	case "":
		return false
	}
	if e.EvidenceCount == 0 {
		return true
	}
	return e.Confidence > 0 && e.Confidence < 0.7
}

// groupStats formats a group's stats in a fixed, meaningful order.
func groupStats(stats map[string]int) string {
	var parts []string
	for _, k := range []string{"services", "endpoints", "models", "nodes"} {
		if v := stats[k]; v > 0 {
			label := k
			if v == 1 {
				label = strings.TrimSuffix(k, "s")
			}
			parts = append(parts, fmt.Sprintf("%d %s", v, label))
		}
	}
	return strings.Join(parts, " · ")
}

// slug turns a display name into a mermaid-safe identifier.
func slug(name string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(name) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	s := strings.Trim(b.String(), "_")
	for strings.Contains(s, "__") {
		s = strings.ReplaceAll(s, "__", "_")
	}
	if s == "" {
		s = "n"
	}
	if s[0] >= '0' && s[0] <= '9' {
		s = "n" + s
	}
	return s
}

// idTable assigns stable, unique mermaid ids to every group and node key:
// slug of the name, "_2"/"_3"… on collision, keys visited in sorted order.
func idTable(v *View) map[string]string {
	type item struct{ key, name string }
	var items []item
	for _, g := range v.Groups {
		items = append(items, item{g.Key, g.Name})
	}
	for _, n := range v.Nodes {
		items = append(items, item{n.Key, n.Name})
	}
	sort.Slice(items, func(i, j int) bool { return items[i].key < items[j].key })
	used := make(map[string]int)
	ids := make(map[string]string, len(items))
	for _, it := range items {
		base := slug(it.name)
		used[base]++
		id := base
		if used[base] > 1 {
			id = fmt.Sprintf("%s_%d", base, used[base])
		}
		ids[it.key] = id
	}
	return ids
}

func mermaidEscape(s string) string {
	return strings.ReplaceAll(strings.ReplaceAll(s, `"`, "#quot;"), "\n", " ")
}

// mermaidShape wraps a label in the shape that tells the node's kind at a
// glance: services and code as boxes, externals as double boxes, channels
// as stadiums, infrastructure as hexagons, data as cylinders.
func mermaidShape(nodeType, label string) string {
	label = mermaidEscape(label)
	switch nodeType {
	case "external_system", "external":
		return fmt.Sprintf(`[["%s"]]`, label)
	case "topic", "queue", "async_channel":
		return fmt.Sprintf(`(["%s"])`, label)
	case "broker", "cache", "infrastructure":
		return fmt.Sprintf(`{{"%s"}}`, label)
	case "database", "model", "entity", "enum":
		return fmt.Sprintf(`[("%s")]`, label)
	case "endpoint":
		return fmt.Sprintf(`[/"%s"/]`, label)
	}
	return fmt.Sprintf(`["%s"]`, label)
}

// mermaidDirection follows the question (mirrors the dashboard): a path or
// a flow reads left→right; impact puts the changed node on top.
func mermaidDirection(v *View) string {
	switch v.Spec.Layout.Preset {
	case "path", "flow":
		return "LR"
	case "impact":
		return "BT"
	}
	return "TB"
}

// RenderMermaid renders the View as a mermaid flowchart. Frames (groups
// with visible members) become subgraphs; collapsed groups become nodes
// carrying their stats; hidden nodes are not drawn but are accounted for
// in a trailing comment; weak-provenance arrows are dashed.
func RenderMermaid(v *View) string {
	ids := idTable(v)
	var b strings.Builder
	fmt.Fprintf(&b, "flowchart %s\n", mermaidDirection(v))
	if v.Title != "" {
		fmt.Fprintf(&b, "%%%% %s\n", mermaidEscape(v.Title))
	}

	members := make(map[string][]VNode)
	var free []VNode
	drawn := make(map[string]bool)
	for _, n := range v.Nodes {
		if n.RenderMode == "hidden" {
			continue
		}
		drawn[n.Key] = true
		if n.Group != "" {
			members[n.Group] = append(members[n.Group], n)
		} else {
			free = append(free, n)
		}
	}
	nodeLine := func(indent string, n VNode) {
		label := n.Name
		if n.Repo != "" {
			label += "<br/>@" + n.Repo
		}
		line := indent + ids[n.Key] + mermaidShape(n.Type, label)
		if n.Boundary {
			line += ":::boundary"
		} else if n.Status == "stale" {
			line += ":::stale"
		}
		b.WriteString(line + "\n")
	}
	var boundary, stale bool
	groups := append([]VGroup(nil), v.Groups...)
	sort.Slice(groups, func(i, j int) bool { return groups[i].Key < groups[j].Key })
	for _, g := range groups {
		drawn[g.Key] = true
		ms := members[g.Key]
		if len(ms) == 0 {
			label := g.Name
			if g.Note != "" && !g.Inferred {
				label += "<br/>" + g.Note
			}
			if st := groupStats(g.Stats); st != "" {
				label += "<br/>" + st
			}
			fmt.Fprintf(&b, "%s[\"%s\"]\n", ids[g.Key], mermaidEscape(label))
			continue
		}
		title := g.Name
		if g.Kind != "" && g.Kind != "service" && g.Kind != "domain" {
			title += " · " + g.Kind
		}
		if g.Inferred {
			title += " (inferred"
			if g.Note != "" {
				title += ": " + g.Note
			}
			title += ")"
		} else if g.Note != "" {
			title += " (" + g.Note + ")"
		}
		fmt.Fprintf(&b, "subgraph %s[\"%s\"]\n", ids[g.Key], mermaidEscape(title))
		sort.Slice(ms, func(i, j int) bool { return ms[i].Key < ms[j].Key })
		for _, n := range ms {
			nodeLine("  ", n)
			boundary = boundary || n.Boundary
			stale = stale || n.Status == "stale"
		}
		b.WriteString("end\n")
	}
	sort.Slice(free, func(i, j int) bool { return free[i].Key < free[j].Key })
	for _, n := range free {
		nodeLine("", n)
		boundary = boundary || n.Boundary
		stale = stale || n.Status == "stale"
	}

	edges := append([]VEdge(nil), v.Edges...)
	sort.Slice(edges, func(i, j int) bool {
		if edges[i].From != edges[j].From {
			return edges[i].From < edges[j].From
		}
		if edges[i].To != edges[j].To {
			return edges[i].To < edges[j].To
		}
		return edges[i].Kind < edges[j].Kind
	})
	for i := range edges {
		e := &edges[i]
		if !drawn[e.From] || !drawn[e.To] {
			continue // an arrow to a hidden node is not drawn either
		}
		arrow := "-->"
		if arrowWeak(e) {
			arrow = "-.->"
		}
		fmt.Fprintf(&b, "%s %s|\"%s\"| %s\n", ids[e.From], arrow, mermaidEscape(arrowLabel(e)), ids[e.To])
	}
	if boundary {
		b.WriteString("classDef boundary stroke-dasharray:4 2,opacity:0.6\n")
	}
	if stale {
		b.WriteString("classDef stale fill:#e8e2d8,stroke:#9a8f7f,stroke-dasharray:4 2\n")
	}
	if len(v.Hidden) > 0 {
		var parts []string
		for _, h := range v.Hidden {
			parts = append(parts, fmt.Sprintf("%d %s (%s)", h.Count, h.Type, h.Reason))
		}
		fmt.Fprintf(&b, "%%%% hidden: %s\n", strings.Join(parts, ", "))
	}
	return b.String()
}

func pct(f float64) string { return fmt.Sprintf("%d%%", int(f*100+0.5)) }

func nodeProvenanceText(n *VNode) string {
	var parts []string
	if n.Status != "" && n.Status != "active" {
		parts = append(parts, n.Status)
	}
	if n.Trust > 0 || n.Confidence > 0 {
		c := n.Trust
		if c == 0 {
			c = n.Confidence
		}
		parts = append(parts, "confidence "+pct(c))
	}
	if n.EvidenceCount > 0 {
		parts = append(parts, fmt.Sprintf("%d evidence", n.EvidenceCount))
	} else if n.Confidence > 0 {
		parts = append(parts, "no evidence")
	}
	return strings.Join(parts, " · ")
}

// RenderText renders the View as structured plain text: header with the
// question and counts, one section per group (members with provenance),
// free nodes, arrows with protocol and provenance, and the hidden summary.
func RenderText(v *View) string {
	var b strings.Builder
	title := v.Title
	if title == "" {
		title = "View"
	}
	fmt.Fprintf(&b, "# %s\n", title)
	level := v.Spec.Layout.Preset
	if level == "" {
		level = "custom"
	}
	visible, hidden := 0, 0
	for _, n := range v.Nodes {
		if n.RenderMode == "hidden" {
			hidden++
		} else {
			visible++
		}
	}
	fmt.Fprintf(&b, "level: %s · domain: %s · %d groups · %d nodes · %d arrows · %d hidden\n",
		level, v.Spec.Scope.Domain, len(v.Groups), visible, len(v.Edges), hidden)

	members := make(map[string][]VNode)
	var free []VNode
	names := make(map[string]string)
	for _, g := range v.Groups {
		names[g.Key] = g.Name
	}
	for _, n := range v.Nodes {
		names[n.Key] = n.Name
		if n.RenderMode == "hidden" {
			continue
		}
		if n.Group != "" {
			members[n.Group] = append(members[n.Group], n)
		} else {
			free = append(free, n)
		}
	}
	nodeLine := func(n VNode) {
		line := fmt.Sprintf("- %s [%s]", n.Name, n.Type)
		if n.Repo != "" {
			line += " @" + n.Repo
		}
		if n.Boundary {
			line += " (outside scope)"
		}
		if len(n.Endpoints) > 0 {
			var eps []string
			for _, ep := range n.Endpoints {
				eps = append(eps, ep.Name)
			}
			line += " exposes " + strings.Join(eps, ", ")
		}
		if len(n.UsesModels) > 0 {
			line += " uses " + strings.Join(n.UsesModels, ", ")
		}
		if n.Detail != "" {
			line += " — " + n.Detail
		}
		if p := nodeProvenanceText(&n); p != "" {
			line += " " + p
		}
		b.WriteString(line + "\n")
	}
	groups := append([]VGroup(nil), v.Groups...)
	sort.Slice(groups, func(i, j int) bool { return groups[i].Key < groups[j].Key })
	for _, g := range groups {
		head := fmt.Sprintf("\n## %s (%s", g.Name, g.Kind)
		if g.Inferred {
			head += ", inferred"
			if g.Note != "" {
				head += ": " + g.Note
			}
		} else if g.Note != "" {
			head += ", " + g.Note
		}
		head += ")"
		if st := groupStats(g.Stats); st != "" {
			head += " — " + st
		}
		b.WriteString(head + "\n")
		ms := members[g.Key]
		sort.Slice(ms, func(i, j int) bool { return ms[i].Key < ms[j].Key })
		for _, n := range ms {
			nodeLine(n)
		}
	}
	if len(free) > 0 {
		b.WriteString("\n## Free nodes\n")
		sort.Slice(free, func(i, j int) bool { return free[i].Key < free[j].Key })
		for _, n := range free {
			nodeLine(n)
		}
	}
	if len(v.Edges) > 0 {
		b.WriteString("\n## Arrows\n")
		edges := append([]VEdge(nil), v.Edges...)
		sort.Slice(edges, func(i, j int) bool {
			if edges[i].From != edges[j].From {
				return edges[i].From < edges[j].From
			}
			if edges[i].To != edges[j].To {
				return edges[i].To < edges[j].To
			}
			return edges[i].Kind < edges[j].Kind
		})
		name := func(k string) string {
			if n, ok := names[k]; ok && n != "" {
				return n
			}
			return k
		}
		for i := range edges {
			e := &edges[i]
			line := fmt.Sprintf("- %s → %s: %s", name(e.From), name(e.To), arrowLabel(e))
			if e.Transport != "" && e.Transport != "http" && e.Transport != "local" {
				line += " (" + e.Transport + ")"
			}
			var prov []string
			if e.Derivation != "" {
				prov = append(prov, e.Derivation)
			}
			if e.Derivation != "synthesized" && (e.Trust > 0 || e.Confidence > 0) {
				c := e.Trust
				if c == 0 {
					c = e.Confidence
				}
				prov = append(prov, pct(c))
				if e.EvidenceCount > 0 {
					prov = append(prov, fmt.Sprintf("%d evidence", e.EvidenceCount))
				} else {
					prov = append(prov, "no evidence")
				}
			}
			if len(prov) > 0 {
				line += " — " + strings.Join(prov, " · ")
			}
			b.WriteString(line + "\n")
		}
	}
	if v.Boundary != nil && (len(v.Boundary.In) > 0 || len(v.Boundary.Out) > 0) {
		b.WriteString("\n## Boundary\n")
		for _, in := range v.Boundary.In {
			fmt.Fprintf(&b, "- in: %s (%s)\n", in.FromName, in.Kind)
		}
		for _, out := range v.Boundary.Out {
			line := fmt.Sprintf("- out: %s [%s] (%s)", out.ToName, out.ToKind, out.Kind)
			if out.Via != "" {
				line += " via " + out.Via
			}
			b.WriteString(line + "\n")
		}
	}
	if len(v.Hidden) > 0 {
		b.WriteString("\n## Hidden\n")
		for _, h := range v.Hidden {
			fmt.Fprintf(&b, "- %d %s — %s\n", h.Count, h.Type, h.Reason)
		}
	}
	if len(v.Trace) > 0 {
		b.WriteString("\n## Path\n")
		var names2 []string
		for _, k := range v.Trace {
			if n, ok := names[k]; ok && n != "" {
				names2 = append(names2, n)
			} else {
				names2 = append(names2, k)
			}
		}
		b.WriteString(strings.Join(names2, " → ") + "\n")
	}
	return b.String()
}
