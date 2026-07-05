// Package viewmodel builds C4-style diagram view models from the Chronicle
// graph store. It is the single engine behind the admin dashboard C4 API,
// the MCP diagram tools, and chronicle-pro.
//
// Levels:
//
//	C1 — system context: services, externals, infra
//	C2 — container diagram: services, topics, lifted edges
//	C3 — component diagram: one service
//	Selection — custom diagram: arbitrary node keys, C3-shaped response
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
	"os"
	"path/filepath"
	"strings"

	"github.com/alexdx2/chronicle-core/manifest"
	"github.com/alexdx2/chronicle-core/paths"
	"github.com/alexdx2/chronicle-core/store"
)

// NotFoundError reports that a requested entity (e.g. a service) does not
// exist in the graph. Callers can map it to HTTP 404.
type NotFoundError struct{ Msg string }

func (e *NotFoundError) Error() string { return e.Msg }

func notFoundf(format string, args ...any) error {
	return &NotFoundError{Msg: fmt.Sprintf(format, args...)}
}

// DefaultManifestPath resolves the domain manifest path relative to the
// store's directory. It mirrors the admin server lookup: when the DB lives in
// a .depbot directory, the project-root chronicle.domain.yaml wins if it
// exists, otherwise .depbot/chronicle.domain.yaml is used.
func DefaultManifestPath(st *store.Store) string {
	dir := st.Dir()
	base := filepath.Base(dir)
	if base == ".depbot" || base == filepath.Base(paths.ConfiguredDir()) {
		rootPath := filepath.Join(filepath.Dir(dir), "chronicle.domain.yaml")
		if _, err := os.Stat(rootPath); err == nil {
			return rootPath
		}
	}
	return filepath.Join(dir, "chronicle.domain.yaml")
}

// ---------- shared helpers ----------

// ownershipMap builds a map from code-node nodeID → owning service NodeRow.
// Ownership is determined by path-prefix match: the service whose root directory
// (filepath.Dir of its FilePath) is the longest prefix of the code node's FilePath.
func ownershipMap(nodes []store.NodeRow) map[int64]*store.NodeRow {
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

	for i := range nodes {
		n := &nodes[i]
		if n.Layer != "code" {
			continue
		}
		fp := n.FilePath
		if fp == "" {
			continue
		}
		// Find the service whose root dir is the longest prefix of fp.
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
			// Fallback for services imported without file_path: the common
			// repo convention names the service after its top-level dir
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

// domainDisplayName returns the human-readable domain name from the manifest if
// available, else returns the domain key unchanged.
func domainDisplayName(manifestPath, domain string) string {
	if m, err := manifest.LoadFile(manifestPath); err == nil {
		for _, d := range m.Domains {
			if d.Key == domain || d.Name == domain {
				if d.Name != "" {
					return d.Name
				}
			}
		}
	}
	return domain
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

func sortExternals(e []External) {
	for i := 1; i < len(e); i++ {
		for j := i; j > 0 && e[j].Name < e[j-1].Name; j-- {
			e[j], e[j-1] = e[j-1], e[j]
		}
	}
}

func sortTopics(t []Topic) {
	for i := 1; i < len(t); i++ {
		for j := i; j > 0 && t[j].Name < t[j-1].Name; j-- {
			t[j], t[j-1] = t[j-1], t[j]
		}
	}
}

func sortModules(m []Module) {
	for i := 1; i < len(m); i++ {
		for j := i; j > 0 && m[j].Name < m[j-1].Name; j-- {
			m[j], m[j-1] = m[j-1], m[j]
		}
	}
}

func sortComponents(c []Component) {
	for i := 1; i < len(c); i++ {
		for j := i; j > 0 && c[j].Name < c[j-1].Name; j-- {
			c[j], c[j-1] = c[j-1], c[j]
		}
	}
}

func sortOutgoing(o []BoundaryOut) {
	for i := 1; i < len(o); i++ {
		for j := i; j > 0 && o[j].ToName < o[j-1].ToName; j-- {
			o[j], o[j-1] = o[j-1], o[j]
		}
	}
}

func sortIncoming(in []BoundaryIn) {
	for i := 1; i < len(in); i++ {
		for j := i; j > 0 && in[j].FromName < in[j-1].FromName; j-- {
			in[j], in[j-1] = in[j-1], in[j]
		}
	}
}

func isInternalTopic(publishers, consumers []string) bool {
	if len(publishers) == 1 && len(consumers) == 1 && publishers[0] == consumers[0] {
		return true
	}
	return false
}
