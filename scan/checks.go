package scan

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/alexdx2/chronicle-core/graph"
	"github.com/alexdx2/chronicle-core/manifest"
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

// LayerChecksScoped is LayerChecks plus the scope check, and a coverage check
// that reports against the inventory: scope is the domain's scan config and
// owns says which inventory files belong to the domain (nil = all of them).
// The scope check follows the structure check.
func LayerChecksScoped(g *graph.Graph, domainKey string, prof Profile, rep *FullReport, inv *Inventory, scope *manifest.ScanConfig, owns func(string) bool) []LayerCheck {
	return LayerChecksScopedFrom(g, domainKey, prof, rep, inv, scope, owns, false)
}

// LayerChecksScopedFrom is LayerChecksScoped that knows whether the scope is
// the user's own (their manifest). A scope the user wrote is not a defect the
// scan can fix: the scope and coverage checks report it as ok, with the same
// detail, instead of failed.
func LayerChecksScopedFrom(g *graph.Graph, domainKey string, prof Profile, rep *FullReport, inv *Inventory, scope *manifest.ScanConfig, owns func(string) bool, userScope bool) []LayerCheck {
	base := LayerChecks(g, domainKey, prof, rep, inv)
	out := make([]LayerCheck, 0, len(base)+1)
	for _, c := range base {
		if c.Layer == "coverage" {
			c = checkCoverageInventory(c, domainKey, rep, inv, scope, owns, userScope)
		}
		out = append(out, c)
		if c.Layer == "structure" {
			out = append(out, checkScope(inv, scope, owns, userScope))
		}
	}
	return out
}

// ownedPaths are the inventory files of the domain.
func ownedPaths(inv *Inventory, owns func(string) bool) []string {
	var out []string
	for _, p := range invPaths(inv) {
		if owns == nil || owns(p) {
			out = append(out, p)
		}
	}
	return out
}

// isSourceFile: a file a bootstrap manifest would send (source extension,
// not under the bootstrap exclusions).
func isSourceFile(p string) bool {
	ext := strings.ToLower(filepath.Ext(p))
	ok := false
	for _, e := range bootstrapSourceExts {
		if e == ext {
			ok = true
			break
		}
	}
	if !ok {
		return false
	}
	for _, g := range bootstrapExclude {
		if manifest.MatchGlob(p, g) {
			return false
		}
	}
	return true
}

// topDirOf is the first path segment ("." for a file at the root).
func topDirOf(p string) string {
	p = filepath.ToSlash(p)
	if i := strings.Index(p, "/"); i > 0 {
		return p[:i]
	}
	return "."
}

// checkScope fails when controller or handler files of the inventory lie
// outside the scan scope: they are never sent to the model, so their
// endpoints cannot exist in the graph and nothing else would say so.
func checkScope(inv *Inventory, scope *manifest.ScanConfig, owns func(string) bool, userScope bool) LayerCheck {
	na := LayerCheck{Layer: "scope", Status: CheckNA, Detail: "no controller or handler files"}
	if scope == nil {
		return LayerCheck{Layer: "scope", Status: CheckNA, Detail: "no scan scope"}
	}
	total, out := 0, 0
	dirs := map[string]bool{}
	for _, p := range ownedPaths(inv, owns) {
		if !isStructureFile(p) && !inv.HasRoutes(p) {
			continue
		}
		total++
		if !graph.InScope(p, scope) {
			out++
			dirs[topDirOf(p)] = true
		}
	}
	if total == 0 {
		return na
	}
	if out == 0 {
		return LayerCheck{Layer: "scope", Status: CheckOK, Detail: fmt.Sprintf("all %d controller or handler files are in scope", total)}
	}
	names := make([]string, 0, len(dirs))
	for d := range dirs {
		names = append(names, d)
	}
	sort.Strings(names)
	detail := fmt.Sprintf("%d of %d controller or handler files are outside the scan scope (%s)", out, total, strings.Join(names, ", "))
	if userScope {
		return LayerCheck{Layer: "scope", Status: CheckOK,
			Detail: fmt.Sprintf("%d of %d controller or handler files excluded by the scan scope; endpoints in %s are not in the graph", out, total, strings.Join(names, ", "))}
	}
	return LayerCheck{Layer: "scope", Status: CheckFailed, Detail: detail}
}

// checkCoverageInventory extends the coverage check with the inventory: the
// files the scope reaches against the source files the repository has.
func checkCoverageInventory(c LayerCheck, domainKey string, rep *FullReport, inv *Inventory, scope *manifest.ScanConfig, owns func(string) bool, userScope bool) LayerCheck {
	if scope == nil || inv == nil {
		return c
	}
	total, in := 0, 0
	var routesOut []string
	for _, p := range ownedPaths(inv, owns) {
		if !isSourceFile(p) {
			continue
		}
		total++
		if graph.InScope(p, scope) {
			in++
		} else if inv.HasRoutes(p) {
			routesOut = append(routesOut, p)
		}
	}
	if total == 0 {
		return c
	}
	inv2 := fmt.Sprintf("%d of %d inventory source files in scope", in, total)
	if c.Status == CheckNA {
		c.Detail = inv2
	} else {
		c.Detail += "; " + inv2
	}
	if len(routesOut) > 0 && !userScope {
		sort.Strings(routesOut)
		c.Detail += "; files registering routes are outside the scope: " + strings.Join(routesOut, ", ")
		c.Status = CheckFailed
	} else if in*2 < total && !userScope {
		c.Status = CheckFailed
	} else if c.Status == CheckNA {
		c.Status = CheckOK
	}
	return c
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

// entryFileNames are the files that conventionally hold the application's
// bootstrap and, in minimal-API style code, all of its routes.
var entryFileNames = map[string]bool{
	"program.cs": true, "startup.cs": true, "main.ts": true, "server.ts": true,
	"app.ts": true, "main.py": true, "app.py": true,
}

func isStructureFile(p string) bool {
	if !strings.Contains(filepath.ToSlash(p), "/") && entryFileNames[strings.ToLower(p)] {
		return true
	}
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
	if len(unmatched)*2 > total {
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
	failed := failedFor(domainKey, rep.Failed)
	d := fmt.Sprintf("%d of %d files failed", failed, sent)
	if failed*10 > sent {
		return LayerCheck{Layer: "coverage", Status: CheckFailed, Detail: d}
	}
	return LayerCheck{Layer: "coverage", Status: CheckOK, Detail: d}
}

// failedFor counts the failures of one domain; reports whose failures carry no
// domain (older ones) count as a whole.
func failedFor(domainKey string, failed []FailedFile) int {
	tagged := false
	for _, f := range failed {
		if f.Domain != "" {
			tagged = true
			break
		}
	}
	if !tagged {
		return len(failed)
	}
	n := 0
	for _, f := range failed {
		if f.Domain == domainKey {
			n++
		}
	}
	return n
}
