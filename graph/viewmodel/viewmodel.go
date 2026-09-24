// Package viewmodel is the diagram engine: one View built from a ViewSpec
// (scope · expand · filter · group · collapse · layout) by BuildView, with
// named presets (C1/C2/C3, lenses, deps/impact/path/flow) and renderings
// to mermaid and text. It is the single engine behind the dashboard's
// /api/view, the MCP diagram tool, and chronicle-pro.
//
// Ownership rule: a code node belongs to the service whose root directory
// (= filepath.Dir(service.FilePath)) is a path prefix of the code node's
// FilePath. This is used to "lift" provider-level CALLS_SERVICE /
// PUBLISHES_TOPIC / CONSUMES_TOPIC edges to their owning service.
//
// This package is public API: it imports only store, manifest, registry,
// graph/salience and stdlib (no internal/* packages).
package viewmodel

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/alexdx2/chronicle-core/store"
)

// NotFoundError reports that a requested entity (e.g. a service) does not
// exist in the graph. Callers can map it to HTTP 404.
type NotFoundError struct{ Msg string }

func (e *NotFoundError) Error() string { return e.Msg }

func notFoundf(format string, args ...any) error {
	return &NotFoundError{Msg: fmt.Sprintf(format, args...)}
}

// ---------- shared helpers ----------

// ownershipMap builds a map from code-node nodeID → owning service NodeRow.
// ownershipMap attributes every code node to a service, in order of evidence:
//
//  1. containment — the CONTAINS chain above the node reaches a service
//     node, or a code:repository named like a service (the graph said so);
//  2. path prefix — the service whose root directory (filepath.Dir of its
//     FilePath) is the longest prefix of the code node's FilePath;
//  3. name segment — a path segment equal to a service name (services
//     imported without a file path).
//
// edges may be nil (path/name attribution only).
func ownershipMap(nodes []store.NodeRow, edges []store.EdgeRow) map[int64]*store.NodeRow {
	// Collect services (layer=service, node_type=service).
	var services []store.NodeRow
	for i := range nodes {
		if nodes[i].Layer == "service" && nodes[i].NodeType == "service" {
			services = append(services, nodes[i])
		}
	}

	out := make(map[int64]*store.NodeRow, len(nodes))
	// Index services by root dir for lookup.
	type svcEntry struct {
		dir string
		row *store.NodeRow
	}
	svcDirs := make([]svcEntry, 0, len(services))
	svcByID := make(map[int64]*store.NodeRow, len(services))
	svcByName := make(map[string]*store.NodeRow, len(services))
	for i := range services {
		fp := services[i].FilePath
		if fp != "" {
			dir := filepath.Dir(fp)
			if dir == "." {
				dir = ""
			}
			svcDirs = append(svcDirs, svcEntry{dir: dir, row: &services[i]})
		}
		svcByID[services[i].NodeID] = &services[i]
		if name := services[i].Name; name != "" {
			svcByName[name] = &services[i]
		}
	}

	// 1. Containment: parent chain over active CONTAINS edges.
	parent := make(map[int64]int64)
	nodeByID := make(map[int64]*store.NodeRow, len(nodes))
	for i := range nodes {
		nodeByID[nodes[i].NodeID] = &nodes[i]
	}
	for i := range edges {
		e := &edges[i]
		if e.EdgeType == "CONTAINS" && e.Active && nodeByID[e.FromNodeID] != nil && nodeByID[e.ToNodeID] != nil {
			if _, seen := parent[e.ToNodeID]; !seen {
				parent[e.ToNodeID] = e.FromNodeID
			}
		}
	}
	containedBy := func(id int64) *store.NodeRow {
		for hops := 0; hops < 16; hops++ {
			pid, ok := parent[id]
			if !ok {
				return nil
			}
			p := nodeByID[pid]
			if p == nil {
				return nil
			}
			if p.Layer == "service" && p.NodeType == "service" {
				return p
			}
			if p.NodeType == "repository" {
				if svc, ok := svcByName[p.Name]; ok {
					return svc
				}
			}
			id = pid
		}
		return nil
	}

	for i := range nodes {
		n := &nodes[i]
		if n.Layer != "code" {
			continue
		}
		if svc := containedBy(n.NodeID); svc != nil {
			out[n.NodeID] = svc
			continue
		}
		// A code repository named like a service IS that service's code root.
		if n.NodeType == "repository" {
			if svc, ok := svcByName[n.Name]; ok {
				out[n.NodeID] = svc
				continue
			}
		}
		fp := n.FilePath
		if fp == "" {
			continue
		}
		// 2. Find the service whose root dir is the longest prefix of fp.
		bestLen := -1
		var bestSvc *store.NodeRow
		for _, se := range svcDirs {
			if se.dir == "" {
				continue
			}
			// Normalise: ensure directory prefix match at path boundary.
			dirWithSep := se.dir
			if !strings.HasSuffix(dirWithSep, "/") {
				dirWithSep += "/"
			}
			if strings.HasPrefix(fp, dirWithSep) || fp == se.dir {
				if len(se.dir) > bestLen {
					bestLen = len(se.dir)
					bestSvc = se.row
				}
			}
		}
		if bestSvc == nil {
			// 3. Services imported without file_path: the common repo
			// convention names the service after its top-level dir
			// ("arena-api/src/…" → service "arena-api").
			for _, seg := range strings.Split(fp, "/") {
				if svc, ok := svcByName[seg]; ok {
					bestSvc = svc
					break
				}
			}
		}
		if bestSvc != nil {
			out[n.NodeID] = bestSvc
		}
	}
	return out
}

// activeOnly filters to status='active' nodes.
func activeOnly(nodes []store.NodeRow) []store.NodeRow {
	out := make([]store.NodeRow, 0, len(nodes))
	for _, n := range nodes {
		if n.Status == "active" {
			out = append(out, n)
		}
	}
	return out
}

// activeEdges returns only active edges (active=1).
func activeEdges(edges []store.EdgeRow) []store.EdgeRow {
	out := make([]store.EdgeRow, 0, len(edges))
	for _, e := range edges {
		if e.Active {
			out = append(out, e)
		}
	}
	return out
}

// topicTransport returns the transport string for a topic node.
// Falls back to: metadata.transport → "queue" if name contains "queue" → "kafka".
func topicTransport(n store.NodeRow) string {
	if n.Metadata != "" && n.Metadata != "{}" {
		var meta map[string]any
		if json.Unmarshal([]byte(n.Metadata), &meta) == nil {
			if t, ok := meta["transport"].(string); ok && t != "" {
				return t
			}
		}
	}
	if strings.Contains(strings.ToLower(n.Name), "queue") {
		return "queue"
	}
	return "kafka"
}

// ---------- sort helpers ----------

func sortedKeys(m map[string]bool) []string {
	if len(m) == 0 {
		return nil
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	// Simple insertion sort for small slices.
	for i := 1; i < len(keys); i++ {
		for j := i; j > 0 && keys[j] < keys[j-1]; j-- {
			keys[j], keys[j-1] = keys[j-1], keys[j]
		}
	}
	return keys
}
