package scan

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/alexdx2/chronicle-core/graph"
	"github.com/alexdx2/chronicle-core/store"
)

// Layer check statuses.
const (
	CheckOK     = "ok"
	CheckFailed = "failed"
	CheckNA     = "n/a"
)

// LayerChecks runs the five deterministic checks of the extraction layers over
// the finished graph, in a fixed order: data, structure, messaging,
// integrations, coverage. A check whose precondition is absent is n/a.
func LayerChecks(g *graph.Graph, domainKey string, prof Profile, rep *FullReport, inv *Inventory) []LayerCheck {
	return []LayerCheck{
		checkData(g, domainKey, inv),
		checkStructure(g, domainKey, inv),
		checkMessaging(g, domainKey, prof),
		checkIntegrations(g, domainKey),
		checkCoverage(domainKey, rep),
	}
}

// checkLimitations turns failed checks into limitation lines.
func checkLimitations(checks []LayerCheck) []string {
	var out []string
	for _, c := range checks {
		if c.Status == CheckFailed {
			out = append(out, fmt.Sprintf("layer %s: %s", c.Layer, c.Detail))
		}
	}
	return out
}

func countNodes(g *graph.Graph, domainKey, nodeType string) int {
	nodes, _ := g.Store().ListNodes(store.NodeFilter{Domain: domainKey, NodeType: nodeType})
	return len(nodes)
}

// countEdgesFrom counts edges of the types whose source node is in the domain.
func countEdgesFrom(g *graph.Graph, domainKey string, types ...string) int {
	n := 0
	for _, t := range types {
		edges, _ := g.Store().ListEdges(store.EdgeFilter{EdgeType: t})
		for _, e := range edges {
			if from, _ := g.Store().GetNodeByKey(e.FromNodeKey); from != nil && from.DomainKey == domainKey {
				n++
			}
		}
	}
	return n
}

func invPaths(inv *Inventory) []string {
	if inv == nil {
		return nil
	}
	out := make([]string, 0, len(inv.Files))
	for _, f := range inv.Files {
		if !f.Binary {
			out = append(out, f.Path)
		}
	}
	return out
}

func isSchemaFile(p string) bool {
	low := strings.ToLower(filepath.ToSlash(p))
	base := filepath.Base(low)
	return strings.HasSuffix(base, ".prisma") || base == "schema.sql" ||
		(strings.HasSuffix(base, ".sql") && (strings.Contains(low, "/migrations/") || strings.HasPrefix(low, "migrations/")))
}

func isStructureFile(p string) bool {
	if graph.LayerOf(p) != graph.LayerStructure {
		return false
	}
	low := strings.ToLower(filepath.Base(p))
	return strings.Contains(low, "controller") || strings.Contains(low, "handler")
}

func checkData(g *graph.Graph, domainKey string, inv *Inventory) LayerCheck {
	c := LayerCheck{Layer: "data", Status: CheckNA, Detail: "no schema files"}
	files := 0
	for _, p := range invPaths(inv) {
		if isSchemaFile(p) {
			files++
		}
	}
	if files == 0 {
		return c
	}
	if n := countNodes(g, domainKey, "model"); n > 0 {
		return LayerCheck{Layer: "data", Status: CheckOK, Detail: fmt.Sprintf("%d models from %d schema files", n, files)}
	}
	return LayerCheck{Layer: "data", Status: CheckFailed, Detail: fmt.Sprintf("%d schema files in scope, no models in the graph", files)}
}

func checkStructure(g *graph.Graph, domainKey string, inv *Inventory) LayerCheck {
	files := 0
	for _, p := range invPaths(inv) {
		if isStructureFile(p) {
			files++
		}
	}
	if files == 0 {
		return LayerCheck{Layer: "structure", Status: CheckNA, Detail: "no controller or handler files"}
	}
	if n := countNodes(g, domainKey, "endpoint"); n > 0 {
		return LayerCheck{Layer: "structure", Status: CheckOK, Detail: fmt.Sprintf("%d endpoints from %d controller or handler files", n, files)}
	}
	return LayerCheck{Layer: "structure", Status: CheckFailed, Detail: fmt.Sprintf("%d controller or handler files, no endpoints in the graph", files)}
}

func checkMessaging(g *graph.Graph, domainKey string, prof Profile) LayerCheck {
	has := false
	for _, t := range prof.Technologies {
		if t.Role == "messaging" {
			has = true
		}
	}
	if !has {
		return LayerCheck{Layer: "messaging", Status: CheckNA, Detail: "no messaging technology in the profile"}
	}
	topics := countNodes(g, domainKey, "topic") + countNodes(g, domainKey, "queue")
	links := countEdgesFrom(g, domainKey, "PUBLISHES_TOPIC", "CONSUMES_TOPIC")
	if links > 0 {
		return LayerCheck{Layer: "messaging", Status: CheckOK, Detail: fmt.Sprintf("%d producer or consumer links, %d topics and queues", links, topics)}
	}
	return LayerCheck{Layer: "messaging", Status: CheckFailed, Detail: fmt.Sprintf("messaging technology in the profile, %d topics and queues but no producer or consumer links", topics)}
}

func checkIntegrations(g *graph.Graph, domainKey string) LayerCheck {
	total := countEdgesFrom(g, domainKey, "CALLS_SERVICE")
	if total == 0 {
		return LayerCheck{Layer: "integrations", Status: CheckNA, Detail: "no http calls"}
	}
	unmatched, _ := g.FindUnmatchedHTTPCalls(domainKey)
	pct := (len(unmatched)*100 + total/2) / total
	if len(unmatched)*2 >= total {
		return LayerCheck{Layer: "integrations", Status: CheckFailed, Detail: fmt.Sprintf("%d%% unmatched (%d of %d http calls)", pct, len(unmatched), total)}
	}
	return LayerCheck{Layer: "integrations", Status: CheckOK, Detail: fmt.Sprintf("%d%% unmatched (%d of %d http calls)", pct, len(unmatched), total)}
}

func checkCoverage(domainKey string, rep *FullReport) LayerCheck {
	na := LayerCheck{Layer: "coverage", Status: CheckNA, Detail: "no files sent to the model"}
	if rep == nil {
		return na
	}
	sent := 0
	for _, d := range rep.Domains {
		if d.Domain == domainKey {
			sent += d.Sent
		}
	}
	if sent == 0 { // report not split by domain: use all of it
		for _, d := range rep.Domains {
			sent += d.Sent
		}
	}
	if sent == 0 {
		return na
	}
	failed := len(rep.Failed)
	d := fmt.Sprintf("%d of %d files failed", failed, sent)
	if failed*10 >= sent {
		return LayerCheck{Layer: "coverage", Status: CheckFailed, Detail: d}
	}
	return LayerCheck{Layer: "coverage", Status: CheckOK, Detail: d}
}
