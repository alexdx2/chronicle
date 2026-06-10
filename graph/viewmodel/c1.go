package viewmodel

import (
	"github.com/alexdx2/chronicle-core/manifest"
	"github.com/alexdx2/chronicle-core/store"
)

// ---------- C1: system context ----------

// External is an external system plus the services that call it.
// Shared by the C1 and C2 responses.
type External struct {
	Key       string   `json:"key"`
	Name      string   `json:"name"`
	CallsFrom []string `json:"calls_from"`
}

// Infra is an infrastructure entry from the manifest (broker, cache, ...).
type Infra struct {
	Key  string `json:"key"`
	Name string `json:"name"`
	Kind string `json:"kind"`
	Used bool   `json:"used"`
}

// C1Edge is a system-context edge.
type C1Edge struct {
	From  string `json:"from"`
	To    string `json:"to"`
	Label string `json:"label"`
}

// C1Stats are the domain-level node counts.
type C1Stats struct {
	Services  int `json:"services"`
	Endpoints int `json:"endpoints"`
	Models    int `json:"models"`
	Topics    int `json:"topics"`
	Flows     int `json:"flows"`
}

// C1 is the system-context view model (level 1).
type C1 struct {
	Domain    string     `json:"domain"`
	Name      string     `json:"name"`
	Stats     C1Stats    `json:"stats"`
	Externals []External `json:"externals"`
	Infra     []Infra    `json:"infra"`
	Edges     []C1Edge   `json:"edges"`
}

// BuildC1 builds the C1 system-context view model for a domain. The manifest
// (for infra + display name) is resolved relative to the store directory; use
// BuildC1Manifest to pass an explicit manifest path.
func BuildC1(st *store.Store, domain string) (*C1, error) {
	return BuildC1Manifest(st, domain, DefaultManifestPath(st))
}

// BuildC1Manifest is BuildC1 with an explicit manifest path.
func BuildC1Manifest(st *store.Store, domain, manifestPath string) (*C1, error) {
	allNodes, err := st.ListNodes(store.NodeFilter{Domain: domain})
	if err != nil {
		return nil, err
	}
	nodes := activeOnly(allNodes)

	allEdges, err := st.ListEdges(store.EdgeFilter{})
	if err != nil {
		return nil, err
	}
	edges := activeEdges(allEdges)

	// Build node index.
	nodeByID := make(map[int64]*store.NodeRow, len(nodes))
	for i := range nodes {
		nodeByID[nodes[i].NodeID] = &nodes[i]
	}

	// Ownership: code node → owning service.
	owned := ownershipMap(nodes)

	// Stats.
	stats := C1Stats{}
	for _, n := range nodes {
		switch {
		case n.Layer == "service" && n.NodeType == "service":
			stats.Services++
		case n.NodeType == "endpoint":
			stats.Endpoints++
		case n.Layer == "data" && (n.NodeType == "model" || n.NodeType == "enum"):
			// only count models (not enums) per spec
			if n.NodeType == "model" {
				stats.Models++
			}
		case n.NodeType == "topic":
			stats.Topics++
		case n.Layer == "flow" && n.NodeType == "use_case":
			stats.Flows++
		}
	}

	// Externals: external_system nodes + which services call them.
	// calls_from: the owning service(s) of providers with CALLS_SERVICE → that external.
	externalByID := make(map[int64]*store.NodeRow)
	for i := range nodes {
		if nodes[i].NodeType == "external_system" {
			externalByID[nodes[i].NodeID] = &nodes[i]
		}
	}

	externalCallers := make(map[int64]map[string]bool) // externalID → set of service names
	for _, e := range edges {
		if e.EdgeType != "CALLS_SERVICE" {
			continue
		}
		ext, ok := externalByID[e.ToNodeID]
		if !ok {
			continue
		}
		callerSvc := owned[e.FromNodeID]
		if callerSvc == nil {
			// Fallback: check if from-node is itself a service.
			if sn := nodeByID[e.FromNodeID]; sn != nil && sn.Layer == "service" {
				callerSvc = sn
			}
		}
		if callerSvc == nil {
			continue
		}
		if externalCallers[ext.NodeID] == nil {
			externalCallers[ext.NodeID] = make(map[string]bool)
		}
		externalCallers[ext.NodeID][callerSvc.Name] = true
	}

	var externals []External
	for id, ext := range externalByID {
		callers := sortedKeys(externalCallers[id])
		externals = append(externals, External{
			Key:       ext.NodeKey,
			Name:      ext.Name,
			CallsFrom: callers,
		})
	}
	sortExternals(externals)

	// Infra: from manifest.
	var infra []Infra
	var c1Edges []C1Edge

	if m, err := manifest.LoadFile(manifestPath); err == nil {
		// Determine which topics are kafka (non-queue transport).
		hasKafkaTopic := false
		for _, n := range nodes {
			if n.NodeType == "topic" && topicTransport(n) == "kafka" {
				hasKafkaTopic = true
				break
			}
		}
		for _, inf := range m.Infrastructure {
			used := false
			switch inf.Type {
			case "broker":
				used = hasKafkaTopic
			case "cache":
				used = false // no direct evidence from graph without extra metadata
			}
			key := inf.InfraNodeKey()
			infra = append(infra, Infra{
				Key:  key,
				Name: inf.Name,
				Kind: inf.Type,
				Used: used,
			})
			// Edge: system → infra
			if used {
				c1Edges = append(c1Edges, C1Edge{From: "system", To: key, Label: "events"})
			}
		}
	}

	// Add edges for externals.
	for _, ext := range externals {
		c1Edges = append(c1Edges, C1Edge{From: "system", To: ext.Key, Label: "HTTP"})
	}

	name := domainDisplayName(manifestPath, domain)
	return &C1{
		Domain:    domain,
		Name:      name,
		Stats:     stats,
		Externals: externals,
		Infra:     infra,
		Edges:     c1Edges,
	}, nil
}
