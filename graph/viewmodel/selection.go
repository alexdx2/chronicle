package viewmodel

import (
	"strings"

	"github.com/alexdx2/chronicle-core/store"
)

// ---------- Selection: custom diagram from explicit node keys ----------

// Selection is a custom view model built from an explicit set of node keys.
// It is shaped like the C3 response (components + internal_edges + boundary)
// so the same frontend renderer can draw it, with level="custom" and a title.
type Selection struct {
	Level         string         `json:"level"` // always "custom"
	Title         string         `json:"title"`
	Components    []Component    `json:"components"`
	InternalEdges []InternalEdge `json:"internal_edges"`
	Boundary      Boundary       `json:"boundary"`
	Missing       []string       `json:"missing,omitempty"` // keys that did not resolve to active nodes
}

// BuildSelection resolves nodeKeys to active nodes (GetNodesByKeys filters
// status='active'), lifts them to components like C3 does, includes all
// active edges among the selected set as internal edges, and adds one-hop
// boundary edges (selected → topic/service/external system) like C3.
func BuildSelection(st *store.Store, domain string, nodeKeys []string, title string) (*Selection, error) {
	found, missing, err := st.GetNodesByKeys(nodeKeys)
	if err != nil {
		return nil, err
	}

	sel := &Selection{
		Level:   "custom",
		Title:   title,
		Missing: missing,
	}

	selected := make(map[int64]bool, len(found))
	selectedIDs := make([]int64, 0, len(found))
	for i := range found {
		selected[found[i].NodeID] = true
		selectedIDs = append(selectedIDs, found[i].NodeID)
	}

	// Node index for resolving edge targets (endpoints, models, boundary
	// targets): all active nodes in the domain, plus the selected nodes
	// themselves (in case a selected node lives outside the domain filter).
	allNodes, err := st.ListNodes(store.NodeFilter{Domain: domain})
	if err != nil {
		return nil, err
	}
	nodes := activeOnly(allNodes)

	nodeByID := make(map[int64]*store.NodeRow, len(nodes)+len(found))
	for i := range nodes {
		nodeByID[nodes[i].NodeID] = &nodes[i]
	}
	for i := range found {
		if _, ok := nodeByID[found[i].NodeID]; !ok {
			nodeByID[found[i].NodeID] = &found[i]
		}
	}

	allEdges, err := st.ListEdges(store.EdgeFilter{})
	if err != nil {
		return nil, err
	}
	edges := activeEdges(allEdges)

	// Components: every selected node, decorated like C3 components
	// (EXPOSES_ENDPOINT → endpoints, USES_MODEL → uses_models).
	compEndpoints := make(map[int64][]Endpoint)
	compModels := make(map[int64][]string)
	for _, e := range edges {
		if !selected[e.FromNodeID] {
			continue
		}
		switch e.EdgeType {
		case "EXPOSES_ENDPOINT":
			toNode := nodeByID[e.ToNodeID]
			if toNode == nil {
				continue
			}
			compEndpoints[e.FromNodeID] = append(compEndpoints[e.FromNodeID], Endpoint{
				Key:  toNode.NodeKey,
				Name: toNode.Name,
			})
		case "USES_MODEL":
			toNode := nodeByID[e.ToNodeID]
			if toNode == nil {
				continue
			}
			compModels[e.FromNodeID] = append(compModels[e.FromNodeID], toNode.Name)
		}
	}

	for i := range found {
		n := &found[i]
		sel.Components = append(sel.Components, Component{
			Key:        n.NodeKey,
			Name:       n.Name,
			Type:       n.NodeType,
			Endpoints:  compEndpoints[n.NodeID],
			UsesModels: compModels[n.NodeID],
		})
	}
	sortComponents(sel.Components)

	// Internal edges: ALL active edges among the selected set.
	internalEdges, err := st.GetEdgesBetweenNodes(selectedIDs)
	if err != nil {
		return nil, err
	}
	type internalKey struct{ from, to, kind string }
	internalSeen := make(map[internalKey]bool)
	for _, e := range internalEdges {
		if !e.Active {
			continue
		}
		fromNode := nodeByID[e.FromNodeID]
		toNode := nodeByID[e.ToNodeID]
		if fromNode == nil || toNode == nil {
			continue
		}
		kind := strings.ToLower(e.EdgeType)
		k := internalKey{fromNode.NodeKey, toNode.NodeKey, kind}
		if internalSeen[k] {
			continue
		}
		internalSeen[k] = true
		sel.InternalEdges = append(sel.InternalEdges, InternalEdge{
			From: fromNode.NodeKey,
			To:   toNode.NodeKey,
			Kind: kind,
		})
	}

	// Boundary: one-hop edges from selected nodes to non-selected
	// topic/service/external nodes, same semantics as C3.
	type outKey struct{ toName, kind string }
	outSeen := make(map[outKey]bool)
	type inKey struct{ fromName, kind string }
	inSeen := make(map[inKey]bool)

	for _, e := range edges {
		if !selected[e.FromNodeID] || selected[e.ToNodeID] {
			continue
		}
		fromNode := nodeByID[e.FromNodeID]
		toNode := nodeByID[e.ToNodeID]
		if fromNode == nil || toNode == nil {
			continue
		}

		switch e.EdgeType {
		case "CALLS_SERVICE":
			if toNode.NodeType == "external_system" {
				k := outKey{toNode.Name, "http"}
				if !outSeen[k] {
					outSeen[k] = true
					sel.Boundary.Outgoing = append(sel.Boundary.Outgoing, BoundaryOut{
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
					sel.Boundary.Outgoing = append(sel.Boundary.Outgoing, BoundaryOut{
						ToName: toNode.Name,
						ToKind: "service",
						Via:    fromNode.Name,
						Kind:   "http",
					})
				}
			}
		case "PUBLISHES_TOPIC":
			k := outKey{toNode.Name, "async"}
			if !outSeen[k] {
				outSeen[k] = true
				sel.Boundary.Outgoing = append(sel.Boundary.Outgoing, BoundaryOut{
					ToName: toNode.Name,
					ToKind: "topic",
					Via:    fromNode.Name,
					Kind:   "async",
				})
			}
		case "CONSUMES_TOPIC":
			// Consuming a topic — incoming from the topic.
			k := inKey{toNode.Name, "async"}
			if !inSeen[k] {
				inSeen[k] = true
				sel.Boundary.Incoming = append(sel.Boundary.Incoming, BoundaryIn{
					FromName: toNode.Name,
					Kind:     "async",
				})
			}
		}
	}

	sortOutgoing(sel.Boundary.Outgoing)
	sortIncoming(sel.Boundary.Incoming)

	return sel, nil
}
