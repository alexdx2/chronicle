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
	return e.Confidence < 0.7
}

// statOrder is the fixed, meaningful order of frame stats: what a frame
// holds first, then whom it talks to. Verbs read "publishes 2", nouns
// "5 endpoints".
var statOrder = []string{"services", "endpoints", "components", "models", "topics", "publishes", "consumes", "calls", "publishers", "subscribers"}

// groupStats formats a group's stats in container language:
// "5 endpoints · publishes 2 · calls 2 services".
func groupStats(stats map[string]int) string {
	var parts []string
	for _, k := range statOrder {
		v := stats[k]
		if v <= 0 {
			continue
		}
		switch k {
		case "publishes", "consumes":
			parts = append(parts, fmt.Sprintf("%s %d", k, v))
		case "calls":
			parts = append(parts, fmt.Sprintf("calls %d %s", v, plural(v, "service")))
		default:
			parts = append(parts, fmt.Sprintf("%d %s", v, plural(v, strings.TrimSuffix(k, "s"))))
		}
	}
	return strings.Join(parts, " · ")
}

func plural(n int, word string) string {
	if n == 1 {
		return word
	}
	return word + "s"
}

// pillsLabel lists a card's endpoints capped at max: "GET /foo, POST /bar,
// +6 more" — a card names what it exposes without turning into a list.
func pillsLabel(eps []Endpoint, max int) string {
	if len(eps) == 0 {
		return ""
	}
	var names []string
	for i, ep := range eps {
		if i == max && len(eps) > max {
			names = append(names, fmt.Sprintf("+%d more", len(eps)-max))
			break
		}
		names = append(names, ep.Name)
	}
	return strings.Join(names, ", ")
}

// markColors are the stroke colors of mark kinds (the dashboard's
// live-client.js MARK_COLORS carries the same values).
var markColors = map[string]string{"breaks": "#c25757", "changed": "#d08a2e", "focus": "#3b6fd4", "note": "#8a7355"}

// markText reads marks in one line: the element's own mark as "[kind]
// note", marks folded into it as "name [kind] note", "; " between.
func markText(refs []MarkRef, ownKey string) string {
	parts := make([]string, 0, len(refs))
	for _, r := range refs {
		s := "[" + r.Kind + "]"
		if r.Key != ownKey {
			s = r.Name + " " + s
		}
		if r.Note != "" {
			s += " " + r.Note
		}
		parts = append(parts, s)
	}
	return strings.Join(parts, "; ")
}

// arrowMarkText reads an arrow's marks as "[kind] note": the arrow line
// already names its ends.
func arrowMarkText(refs []MarkRef) string {
	parts := make([]string, 0, len(refs))
	for _, r := range refs {
		s := "[" + r.Kind + "]"
		if r.Note != "" {
			s += " " + r.Note
		}
		parts = append(parts, s)
	}
	return strings.Join(parts, "; ")
}

// splitOwnMarks separates a group's own mark from marks folded into it.
func splitOwnMarks(refs []MarkRef, ownKey string) (own, folded []MarkRef) {
	for _, r := range refs {
		if r.Key == ownKey {
			own = append(own, r)
		} else {
			folded = append(folded, r)
		}
	}
	return own, folded
}

// clipRunes shortens s to n characters (never inside a UTF-8 sequence),
// marking the cut with "…".
func clipRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}

// mermaidText escapes untrusted text (agent notes) for a mermaid label: no
// markup survives, not even <br/>.
func mermaidText(s string) string {
	return strings.NewReplacer(`"`, "#quot;", "<", "#lt;", ">", "#gt;", "|", "#124;", "\r", " ", "\n", " ").Replace(s)
}

// sanitizeNoteLine keeps a View.Notes sentence on one line inside a mermaid
// "%% " comment: a literal newline would start a new, unprefixed line and
// break the diagram's syntax. Unlike mermaidText this is not a quoted
// label, so quotes/brackets stay human-readable — only line breaks matter.
func sanitizeNoteLine(s string) string {
	return strings.NewReplacer("\r", " ", "\n", " ").Replace(s)
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
	// A node named "end" (a flow outcome) would terminate the subgraph it
	// sits in; reserved words get a suffix so no id is ever a keyword.
	if mermaidReserved[s] {
		s += "_n"
	}
	return s
}

var mermaidReserved = map[string]bool{
	"end": true, "graph": true, "subgraph": true, "style": true, "classdef": true, "class": true,
	"click": true, "linkstyle": true, "direction": true, "flowchart": true, "default": true,
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

// mermaidEscape makes a name safe inside a quoted mermaid label: quotes,
// angle brackets (an agent pastes this into PRs and wikis — a name must not
// become markup) and line breaks. The <br/> the renderer itself inserts
// between name and repo/stats survives.
func mermaidEscape(s string) string {
	const br = "\x00br\x00"
	s = strings.ReplaceAll(s, "<br/>", br)
	r := strings.NewReplacer(`"`, "#quot;", "<", "#lt;", ">", "#gt;", "|", "#124;", "\r", " ", "\n", " ")
	s = r.Replace(s)
	return strings.ReplaceAll(s, br, "<br/>")
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
	if v.Table != nil {
		return "" // a list view has no picture; see RenderMarkdown
	}
	ids := idTable(v)
	var b strings.Builder
	usedMarks := make(map[string]bool)
	var classLines, styleLines, linkLines []string
	fmt.Fprintf(&b, "flowchart %s\n", mermaidDirection(v))
	if v.Title != "" {
		fmt.Fprintf(&b, "%%%% %s\n", mermaidEscape(v.Title))
	}
	for _, note := range v.Notes {
		fmt.Fprintf(&b, "%%%% note: %s\n", sanitizeNoteLine(note))
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
		if n.Domain != "" {
			// rev3 task 8a: a foreign neighbour's own domain, right after
			// the name — same ordering as RenderText's " (domain …)" tag.
			label += "<br/>domain " + n.Domain
		}
		if n.Repo != "" {
			label += "<br/>@" + n.Repo
		}
		if n.Boundary {
			label += "<br/>outside"
		}
		if p := pillsLabel(n.Endpoints, 2); p != "" {
			label += "<br/>" + p
		}
		if p := nodeProvenanceText(&n); p != "" {
			label += "<br/>" + p
		}
		if len(n.Marks) > 0 {
			label += "<br/>" + mermaidText(clipRunes(markText(n.Marks, n.Key), 60))
			if k := StrongestMark(n.Marks); k != "" {
				classLines = append(classLines, fmt.Sprintf("class %s mark_%s", ids[n.Key], k))
				usedMarks[k] = true
			}
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
			if len(g.Marks) > 0 {
				label += "<br/>" + mermaidText(clipRunes(markText(g.Marks, g.Key), 60))
				if k := StrongestMark(g.Marks); k != "" {
					classLines = append(classLines, fmt.Sprintf("class %s mark_%s", ids[g.Key], k))
					usedMarks[k] = true
				}
			}
			fmt.Fprintf(&b, "%s[\"%s\"]\n", ids[g.Key], mermaidEscape(label))
			continue
		}
		title := g.Name
		if g.Kind != "" && g.Kind != "service" && g.Kind != "domain" && g.Kind != "unknown" {
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
		if len(g.Marks) > 0 {
			title += " · " + mermaidText(clipRunes(markText(g.Marks, g.Key), 60))
			if k := StrongestMark(g.Marks); k != "" {
				styleLines = append(styleLines, fmt.Sprintf("style %s stroke:%s,stroke-width:3px", ids[g.Key], markColors[k]))
			}
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
	drawnIdx := 0
	for i := range edges {
		e := &edges[i]
		if !drawn[e.From] || !drawn[e.To] {
			continue // an arrow to a hidden node is not drawn either
		}
		arrow := "-->"
		if arrowWeak(e) {
			arrow = "-.->"
		}
		label := arrowLabel(e)
		if k := StrongestMark(e.Marks); k != "" {
			label += " · " + k
			linkLines = append(linkLines, fmt.Sprintf("linkStyle %d stroke:%s,stroke-width:3px", drawnIdx, markColors[k]))
			usedMarks[k] = true
		}
		fmt.Fprintf(&b, "%s %s|\"%s\"| %s\n", ids[e.From], arrow, mermaidEscape(label), ids[e.To])
		drawnIdx++
	}
	for _, k := range MarkKinds {
		if usedMarks[k] {
			fmt.Fprintf(&b, "classDef mark_%s stroke:%s,stroke-width:3px\n", k, markColors[k])
		}
	}
	for _, l := range classLines {
		b.WriteString(l + "\n")
	}
	for _, l := range styleLines {
		b.WriteString(l + "\n")
	}
	for _, l := range linkLines {
		b.WriteString(l + "\n")
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

// evidenceBar is the confidence below which provenance becomes a signal
// worth a line — the same bar the dashboard uses to dash a stroke.
const evidenceBar = 0.7

// nodeProvenanceText is the evidence line of a card, printed ONLY on an
// absolute signal: a non-active status, confidence below the bar
// (including 0), or no evidence at all. A healthy node says nothing —
// the line is an alarm, not a caption, and it never compares to the
// rest of the graph (a uniformly weak graph must light up everywhere).
func nodeProvenanceText(n *VNode) string {
	var parts []string
	if n.Status != "" && n.Status != "active" {
		parts = append(parts, n.Status)
	}
	c := n.Trust
	if c == 0 && n.Confidence > 0 {
		c = n.Confidence
	}
	if c < evidenceBar {
		parts = append(parts, "confidence "+pct(c))
	}
	if n.EvidenceCount == 0 {
		parts = append(parts, "no evidence")
	}
	return strings.Join(parts, " · ")
}

// edgeProvenanceText is the evidence line of an arrow, on signal only:
// a non-hard derivation (linked is still evidence-backed and stays
// silent; inferred/unknown/synthesized speak), confidence below the bar,
// or no evidence behind a graph-backed arrow.
func edgeProvenanceText(e *VEdge) string {
	var parts []string
	switch e.Derivation {
	case "inferred", "unknown", "synthesized":
		parts = append(parts, e.Derivation)
	}
	if e.Derivation == "synthesized" {
		return strings.Join(parts, " · ")
	}
	c := e.Trust
	if c == 0 && e.Confidence > 0 {
		c = e.Confidence
	}
	if c < evidenceBar {
		parts = append(parts, "confidence "+pct(c))
	}
	if e.EvidenceCount == 0 {
		parts = append(parts, "no evidence")
	}
	return strings.Join(parts, " · ")
}

// RenderMarkdown renders a list view (View.Table) as a markdown table.
// Empty for picture views.
func RenderMarkdown(v *View) string {
	if v.Table == nil {
		return ""
	}
	var b strings.Builder
	if v.Title != "" {
		fmt.Fprintf(&b, "# %s\n\n", v.Title)
	}
	for _, note := range v.Notes {
		fmt.Fprintf(&b, "note: %s\n", note)
	}
	if len(v.Notes) > 0 {
		b.WriteString("\n")
	}
	fmt.Fprintf(&b, "| %s |\n", strings.Join(v.Table.Columns, " | "))
	fmt.Fprintf(&b, "|%s\n", strings.Repeat(" --- |", len(v.Table.Columns)))
	for i, r := range v.Table.Rows {
		row := append([]string(nil), r...)
		if i < len(v.Table.Keys) && len(row) > 0 {
			if refs := tableMarks(v, v.Table.Keys[i]); len(refs) > 0 {
				row[len(row)-1] += " " + markText(refs, v.Table.Keys[i])
			}
		}
		cells := make([]string, len(row))
		for i, c := range row {
			cells[i] = strings.ReplaceAll(c, "|", "\\|")
		}
		fmt.Fprintf(&b, "| %s |\n", strings.Join(cells, " | "))
	}
	return b.String()
}

// writeUnplaced lists marked keys this view does not draw.
func writeUnplaced(b *strings.Builder, v *View) {
	if len(v.Unplaced) == 0 {
		return
	}
	b.WriteString("\n## Marked but not in this view\n")
	for _, k := range v.Unplaced {
		fmt.Fprintf(b, "- %s\n", k)
	}
	b.WriteString("(add view_spec.pin or use deps/impact to draw them)\n")
}

// tableMarks are the marks of a table row's subject node.
func tableMarks(v *View, key string) []MarkRef {
	for _, n := range v.Nodes {
		if n.Key == key {
			return n.Marks
		}
	}
	return nil
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
	// Notes (rev3 task 2) explain an empty or partial answer — right under
	// the header, before the reader reaches the sections (or the table) and
	// draws a conclusion the view itself does not back.
	for _, note := range v.Notes {
		fmt.Fprintf(&b, "note: %s\n", note)
	}
	level := v.Spec.Layout.Preset
	if level == "" {
		level = "custom"
	}
	visible, hidden := 0, 0
	hiddenKeys := make(map[string]bool)
	for _, n := range v.Nodes {
		if n.RenderMode == "hidden" {
			hidden++
			hiddenKeys[n.Key] = true
		} else {
			visible++
		}
	}
	// Arrows into hidden nodes are not drawn (mermaid, dashboard) — the text
	// describes the same diagram, so it skips them too.
	var drawn []VEdge
	for _, e := range v.Edges {
		if hiddenKeys[e.From] || hiddenKeys[e.To] {
			continue
		}
		drawn = append(drawn, e)
	}
	fmt.Fprintf(&b, "level: %s · domain: %s · %d groups · %d nodes · %d arrows · %d hidden\n",
		level, v.Spec.Scope.Domain, len(v.Groups), visible, len(drawn), hidden)

	// A list view: rows grouped by their first column (the service), one
	// line per row — the flows lens reads "## arena-api" then its flows.
	if t := v.Table; t != nil {
		last := ""
		for i, r := range t.Rows {
			if len(r) == 0 {
				continue
			}
			if r[0] != last {
				fmt.Fprintf(&b, "\n## %s\n", r[0])
				last = r[0]
			}
			line := "- " + strings.Join(r[1:], " → ")
			if i < len(t.Keys) {
				if refs := tableMarks(v, t.Keys[i]); len(refs) > 0 {
					line += " " + markText(refs, t.Keys[i])
				}
			}
			b.WriteString(line + "\n")
		}
		if len(v.Hidden) > 0 {
			b.WriteString("\n## Hidden\n")
			for _, h := range v.Hidden {
				fmt.Fprintf(&b, "- %d %s — %s\n", h.Count, h.Type, h.Reason)
			}
		}
		writeUnplaced(&b, v)
		return b.String()
	}

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
		if n.Domain != "" {
			// rev3 task 8a: a foreign neighbour names its own domain right
			// after the type, before repo/marks/boundary — "what it is,
			// then where it actually lives".
			line += fmt.Sprintf(" (domain %s)", n.Domain)
		}
		if n.Repo != "" {
			line += " @" + n.Repo
		}
		if len(n.Marks) > 0 {
			line += " " + markText(n.Marks, n.Key)
		}
		if n.Boundary {
			line += " (outside scope)"
		}
		if p := pillsLabel(n.Endpoints, 4); p != "" {
			line += " exposes " + p
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
		if own, folded := splitOwnMarks(g.Marks, g.Key); len(own)+len(folded) > 0 {
			if len(own) > 0 {
				head += " · " + arrowMarkText(own)
			}
			if len(folded) > 0 {
				head += " · marked: " + markText(folded, g.Key)
			}
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
	if len(drawn) > 0 {
		b.WriteString("\n## Arrows\n")
		edges := append([]VEdge(nil), drawn...)
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
			if len(e.Marks) > 0 {
				line += " " + arrowMarkText(e.Marks)
			}
			if p := edgeProvenanceText(e); p != "" {
				line += " — " + p
			}
			b.WriteString(line + "\n")
		}
	}
	if len(v.AlsoRequires) > 0 {
		// rev3 task 6: requires arrows the transitive-reduction rule
		// skipped, named here so nothing disappears silently. Deliberately
		// not a View.Notes entry: it is specific to the Arrows section and
		// stays out of mermaid (see the AlsoRequires doc comment).
		fmt.Fprintf(&b, "also requires (reached via injects): %s\n", strings.Join(v.AlsoRequires, ", "))
	}
	if v.Boundary != nil && (len(v.Boundary.In) > 0 || len(v.Boundary.Out) > 0) {
		b.WriteString("\n## Boundary\n")
		for _, in := range v.Boundary.In {
			line := fmt.Sprintf("- in: %s (%s)", in.FromName, in.Kind)
			if in.Domain != "" {
				// rev3 task 8a: right after the kind, same placement as a
				// node card's own " (domain …)" tag above.
				line += fmt.Sprintf(" (domain %s)", in.Domain)
			}
			b.WriteString(line + "\n")
		}
		for _, out := range v.Boundary.Out {
			line := fmt.Sprintf("- out: %s [%s] (%s)", out.ToName, out.ToKind, out.Kind)
			if out.Domain != "" {
				line += fmt.Sprintf(" (domain %s)", out.Domain)
			}
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
	writeUnplaced(&b, v)
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
