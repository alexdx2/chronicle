package viewmodel

import (
	"path/filepath"
	"strings"

	"github.com/alexdx2/chronicle-core/store"
)

// ---------- C3: component diagram ----------

// Module is a module node and the keys of the members it CONTAINS.
type Module struct {
	Key     string   `json:"key"`
	Name    string   `json:"name"`
	Members []string `json:"members"`
}

// Endpoint is an endpoint exposed by a component.
type Endpoint struct {
	Key  string `json:"key"`
	Name string `json:"name"`
}

// Component is a controller or provider inside a service.
type Component struct {
	Key        string     `json:"key"`
	Name       string     `json:"name"`
	Type       string     `json:"type"`
	Endpoints  []Endpoint `json:"endpoints,omitempty"`
	UsesModels []string   `json:"uses_models,omitempty"`
}

// InternalEdge is an edge between two components inside the boundary.
type InternalEdge struct {
	From string `json:"from"`
	To   string `json:"to"`
	Kind string `json:"kind"`
}

// BoundaryOut is an outgoing dependency crossing the boundary.
type BoundaryOut struct {
	ToName string `json:"to_name"`
	ToKind string `json:"to_kind"`
	Via    string `json:"via"`
	Kind   string `json:"kind"`
}

// BoundaryIn is an incoming dependency crossing the boundary.
type BoundaryIn struct {
	FromName string `json:"from_name"`
	Kind     string `json:"kind"`
}

// Boundary groups the edges crossing the diagram boundary.
type Boundary struct {
	Outgoing []BoundaryOut `json:"outgoing"`
	Incoming []BoundaryIn  `json:"incoming"`
}

// C3 is the component-diagram view model (level 3) for one service.
type C3 struct {
	Service       map[string]string `json:"service"`
	Modules       []Module          `json:"modules"`
	Components    []Component       `json:"components"`
	InternalEdges []InternalEdge    `json:"internal_edges"`
	Boundary      Boundary          `json:"boundary"`
}

// BuildC3 builds the C3 component view model for one service, looked up by
// node key or name. Returns *NotFoundError if the service does not exist.
func BuildC3(st *store.Store, domain, serviceKeyOrName string) (*C3, error) {
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

	nodeByID := make(map[int64]*store.NodeRow, len(nodes))
	for i := range nodes {
		nodeByID[nodes[i].NodeID] = &nodes[i]
	}

	// Find the target service by key or name.
	var targetSvc *store.NodeRow
	for i := range nodes {
		n := &nodes[i]
		if n.Layer != "service" || n.NodeType != "service" {
			continue
		}
		if n.NodeKey == serviceKeyOrName || n.Name == serviceKeyOrName {
			targetSvc = n
			break
		}
	}
	if targetSvc == nil {
		return nil, notFoundf("service %q not found", serviceKeyOrName)
	}

	owned := ownershipMap(nodes)

	// Nodes owned by this service.
	ownedByTarget := make(map[int64]bool)
	for nid, svc := range owned {
		if svc.NodeID == targetSvc.NodeID {
			ownedByTarget[nid] = true
		}
	}

	// Modules: CONTAINS edges from module nodes.
	// A module is owned by the service if its file_path has the service root as prefix.
	svcRoot := ""
	if targetSvc.FilePath != "" {
		svcRoot = filepath.Dir(targetSvc.FilePath)
	}

	moduleMembers := make(map[int64][]string) // moduleID → member node keys
	for _, e := range edges {
		if e.EdgeType != "CONTAINS" {
			continue
		}
		fromNode := nodeByID[e.FromNodeID]
		if fromNode == nil || fromNode.NodeType != "module" {
			continue
		}
		// Is this module owned by targetSvc?
		if svcRoot != "" && fromNode.FilePath != "" {
			dirWithSep := svcRoot
			if !strings.HasSuffix(dirWithSep, "/") {
				dirWithSep += "/"
			}
			if !strings.HasPrefix(fromNode.FilePath, dirWithSep) && fromNode.FilePath != svcRoot {
				continue
			}
		} else if !ownedByTarget[e.FromNodeID] {
			continue
		}
		toNode := nodeByID[e.ToNodeID]
		if toNode != nil {
			moduleMembers[e.FromNodeID] = append(moduleMembers[e.FromNodeID], toNode.NodeKey)
		}
	}

	var modules []Module
	for mid, members := range moduleMembers {
		mn := nodeByID[mid]
		if mn == nil {
			continue
		}
		modules = append(modules, Module{
			Key:     mn.NodeKey,
			Name:    mn.Name,
			Members: members,
		})
	}
	sortModules(modules)

	// Components: owned controllers + providers (NOT modules).
	compEndpoints := make(map[int64][]Endpoint)
	compModels := make(map[int64][]string)

	for _, e := range edges {
		if e.EdgeType == "EXPOSES_ENDPOINT" {
			if !ownedByTarget[e.FromNodeID] {
				continue
			}
			toNode := nodeByID[e.ToNodeID]
			if toNode == nil {
				continue
			}
			compEndpoints[e.FromNodeID] = append(compEndpoints[e.FromNodeID], Endpoint{
				Key:  toNode.NodeKey,
				Name: toNode.Name,
			})
		}
		if e.EdgeType == "USES_MODEL" {
			if !ownedByTarget[e.FromNodeID] {
				continue
			}
			toNode := nodeByID[e.ToNodeID]
			if toNode == nil {
				continue
			}
			compModels[e.FromNodeID] = append(compModels[e.FromNodeID], toNode.Name)
		}
	}

	var components []Component
	for _, n := range nodes {
		if !ownedByTarget[n.NodeID] {
			continue
		}
		if n.NodeType != "controller" && n.NodeType != "provider" {
			continue
		}
		comp := Component{
			Key:        n.NodeKey,
			Name:       n.Name,
			Type:       n.NodeType,
			Endpoints:  compEndpoints[n.NodeID],
			UsesModels: compModels[n.NodeID],
		}
		components = append(components, comp)
	}
	sortComponents(components)

	// Internal edges: INJECTS between owned components.
	type internalKey struct{ from, to string }
	internalSeen := make(map[internalKey]bool)
	var internalEdges []InternalEdge
	for _, e := range edges {
		if e.EdgeType != "INJECTS" {
			continue
		}
		if !ownedByTarget[e.FromNodeID] || !ownedByTarget[e.ToNodeID] {
			continue
		}
		fromNode := nodeByID[e.FromNodeID]
		toNode := nodeByID[e.ToNodeID]
		if fromNode == nil || toNode == nil {
			continue
		}
		k := internalKey{fromNode.NodeKey, toNode.NodeKey}
		if internalSeen[k] {
			continue
		}
		internalSeen[k] = true
		internalEdges = append(internalEdges, InternalEdge{
			From: fromNode.NodeKey,
			To:   toNode.NodeKey,
			Kind: "injects",
		})
	}

	// Boundary edges.
	// Outgoing: from owned components → other services/topics/externals.
	type outKey struct{ toName, kind string }
	outSeen := make(map[outKey]bool)
	var outgoing []BoundaryOut

	// Incoming: from other services' components or topics → this service's components.
	type inKey struct{ fromName, kind string }
	inSeen := make(map[inKey]bool)
	var incoming []BoundaryIn

	externalByID := make(map[int64]*store.NodeRow)
	for i := range nodes {
		if nodes[i].NodeType == "external_system" {
			externalByID[nodes[i].NodeID] = &nodes[i]
		}
	}

	for _, e := range edges {
		fromNode := nodeByID[e.FromNodeID]
		toNode := nodeByID[e.ToNodeID]
		if fromNode == nil || toNode == nil {
			continue
		}

		switch e.EdgeType {
		case "CALLS_SERVICE":
			if ownedByTarget[e.FromNodeID] {
				// Outgoing HTTP.
				if toNode.NodeType == "external_system" {
					k := outKey{toNode.Name, "http"}
					if !outSeen[k] {
						outSeen[k] = true
						outgoing = append(outgoing, BoundaryOut{
							ToName: toNode.Name,
							ToKind: "external_system",
							Via:    fromNode.Name,
							Kind:   "http",
						})
					}
				} else if toNode.Layer == "service" && toNode.NodeType == "service" {
					k := outKey{toNode.Name, "http"}
					if !outSeen[k] {
						outSeen[k] = true
						outgoing = append(outgoing, BoundaryOut{
							ToName: toNode.Name,
							ToKind: "service",
							Via:    fromNode.Name,
							Kind:   "http",
						})
					}
				}
			}
		case "PUBLISHES_TOPIC":
			if ownedByTarget[e.FromNodeID] {
				k := outKey{toNode.Name, "async"}
				if !outSeen[k] {
					outSeen[k] = true
					outgoing = append(outgoing, BoundaryOut{
						ToName: toNode.Name,
						ToKind: "topic",
						Via:    fromNode.Name,
						Kind:   "async",
					})
				}
			}
		case "CONSUMES_TOPIC":
			if ownedByTarget[e.FromNodeID] {
				// Consuming a topic — incoming from the topic.
				k := inKey{toNode.Name, "async"}
				if !inSeen[k] {
					inSeen[k] = true
					incoming = append(incoming, BoundaryIn{
						FromName: toNode.Name,
						Kind:     "async",
					})
				}
			}
		}
	}

	// Also: incoming CALLS_SERVICE from other services into this service's code nodes.
	// (Represented as: code node of another service → a service node representing this service).
	// Actually: other providers CALLS_SERVICE → targetSvc directly.
	for _, e := range edges {
		if e.EdgeType != "CALLS_SERVICE" {
			continue
		}
		if e.ToNodeID != targetSvc.NodeID {
			continue
		}
		fromNode := nodeByID[e.FromNodeID]
		if fromNode == nil {
			continue
		}
		callerSvc := owned[e.FromNodeID]
		if callerSvc == nil {
			continue
		}
		if callerSvc.NodeID == targetSvc.NodeID {
			continue
		}
		k := inKey{callerSvc.Name, "http"}
		if !inSeen[k] {
			inSeen[k] = true
			incoming = append(incoming, BoundaryIn{
				FromName: callerSvc.Name,
				Kind:     "http",
			})
		}
	}

	sortOutgoing(outgoing)
	sortIncoming(incoming)

	return &C3{
		Service: map[string]string{
			"key":  targetSvc.NodeKey,
			"name": targetSvc.Name,
		},
		Modules:       modules,
		Components:    components,
		InternalEdges: internalEdges,
		Boundary: Boundary{
			Outgoing: outgoing,
			Incoming: incoming,
		},
	}, nil
}
