package viewmodel

import (
	"github.com/alexdx2/chronicle-core/graph/salience"
	"github.com/alexdx2/chronicle-core/manifest"
	"github.com/alexdx2/chronicle-core/store"
)

// ---------- C2: container diagram ----------

// C2ServiceStats are per-service code counts.
type C2ServiceStats struct {
	Endpoints int `json:"endpoints"`
	Models    int `json:"models"`
	Providers int `json:"providers"`
	Modules   int `json:"modules"`
}

// C2Service is one service container.
type C2Service struct {
	Key   string         `json:"key"`
	Name  string         `json:"name"`
	Stats C2ServiceStats `json:"stats"`
	Tech  string         `json:"tech,omitempty"`
	// Salience annotation (registry-driven, level c2). Services are structural
	// at container level — always shown — so this is uniformly box/primary; it
	// exists so the frontend can treat every level's nodes uniformly.
	Tier       string `json:"tier,omitempty"`
	RenderMode string `json:"render_mode,omitempty"`
}

// Topic is a message topic/queue with its publishers and consumers.
type Topic struct {
	Key        string   `json:"key"`
	Name       string   `json:"name"`
	Transport  string   `json:"transport"`
	Publishers []string `json:"publishers"`
	Consumers  []string `json:"consumers"`
	Internal   bool     `json:"internal,omitempty"`
}

// C2Edge is a lifted service-level edge.
type C2Edge struct {
	From  string `json:"from"`
	To    string `json:"to"`
	Kind  string `json:"kind"`
	Label string `json:"label"`
}

// C2 is the container-diagram view model (level 2).
type C2 struct {
	Services  []C2Service `json:"services"`
	Topics    []Topic     `json:"topics"`
	Externals []External  `json:"externals"`
	Edges     []C2Edge    `json:"edges"`
}

// BuildC2 builds the C2 container view model for a domain. The manifest (for
// service tech) is resolved relative to the store directory; use
// BuildC2Manifest to pass an explicit manifest path.
func BuildC2(st *store.Store, domain string) (*C2, error) {
	return BuildC2Manifest(st, domain, DefaultManifestPath(st))
}

// BuildC2Manifest is BuildC2 with an explicit manifest path.
func BuildC2Manifest(st *store.Store, domain, manifestPath string) (*C2, error) {
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

	owned := ownershipMap(nodes)

	// Also need a module→service mapping (CONTAINS edge: module→controller/provider).
	// Determine service tech from manifest.
	svcTech := ""
	if m, err := manifest.LoadFile(manifestPath); err == nil {
		if len(m.Tech) > 0 {
			svcTech = m.Tech[0]
		}
	}

	// Build services.
	pol := saliencePolicyFor(st)
	svcByID := make(map[int64]*C2Service)
	svcByKey := make(map[string]*C2Service)
	var services []C2Service
	for _, n := range nodes {
		if n.Layer != "service" || n.NodeType != "service" {
			continue
		}
		role, roleConf := nodeRoleClaim(&n)
		sal := salience.Resolve(pol, salience.Input{
			NodeType:       n.NodeType,
			Layer:          n.Layer,
			Role:           role,
			RoleConfidence: roleConf,
			Level:          "c2",
		})
		svc := C2Service{
			Key:        n.NodeKey,
			Name:       n.Name,
			Tech:       svcTech,
			Tier:       string(sal.Tier),
			RenderMode: string(sal.RenderMode),
		}
		svcByID[n.NodeID] = &svc
		svcByKey[n.NodeKey] = &svc
		services = append(services, svc)
	}

	// After building services slice, we need stable pointers — rebuild.
	svcByID = make(map[int64]*C2Service, len(services))
	svcByKey = make(map[string]*C2Service, len(services))
	for i := range services {
		for _, n := range nodes {
			if n.Layer == "service" && n.NodeType == "service" && n.Name == services[i].Name {
				svcByID[n.NodeID] = &services[i]
				svcByKey[n.NodeKey] = &services[i]
				break
			}
		}
	}

	// Per-service stats.
	// Endpoints: count distinct endpoint nodes exposed by owned controllers.
	// Models: count distinct USES_MODEL targets from owned providers.
	// Providers/Modules: owned code counts.

	svcEndpoints := make(map[int64]map[int64]bool) // svcID → set of endpoint IDs
	svcModels := make(map[int64]map[int64]bool)    // svcID → set of model node IDs
	svcProviderCount := make(map[int64]int)
	svcModuleCount := make(map[int64]int)

	for _, n := range nodes {
		svc := owned[n.NodeID]
		if svc == nil {
			continue
		}
		switch n.NodeType {
		case "provider":
			svcProviderCount[svc.NodeID]++
		case "module":
			svcModuleCount[svc.NodeID]++
		}
	}

	for _, e := range edges {
		from := nodeByID[e.FromNodeID]
		if from == nil {
			continue
		}
		svc := owned[e.FromNodeID]
		if svc == nil {
			continue
		}
		switch e.EdgeType {
		case "EXPOSES_ENDPOINT":
			if svcEndpoints[svc.NodeID] == nil {
				svcEndpoints[svc.NodeID] = make(map[int64]bool)
			}
			svcEndpoints[svc.NodeID][e.ToNodeID] = true
		case "USES_MODEL":
			if svcModels[svc.NodeID] == nil {
				svcModels[svc.NodeID] = make(map[int64]bool)
			}
			svcModels[svc.NodeID][e.ToNodeID] = true
		}
	}

	// Apply stats.
	for i := range services {
		svc := &services[i]
		// Find NodeID for this service.
		var nid int64
		for _, n := range nodes {
			if n.Layer == "service" && n.NodeType == "service" && n.Name == svc.Name {
				nid = n.NodeID
				break
			}
		}
		svc.Stats = C2ServiceStats{
			Endpoints: len(svcEndpoints[nid]),
			Models:    len(svcModels[nid]),
			Providers: svcProviderCount[nid],
			Modules:   svcModuleCount[nid],
		}
	}

	// Topics.
	topicByID := make(map[int64]*store.NodeRow)
	for i := range nodes {
		if nodes[i].NodeType == "topic" {
			topicByID[nodes[i].NodeID] = &nodes[i]
		}
	}

	topicPubs := make(map[int64]map[string]bool) // topicID → set of service names
	topicSubs := make(map[int64]map[string]bool)

	for _, e := range edges {
		topic := topicByID[e.ToNodeID]
		if topic == nil {
			topic = topicByID[e.FromNodeID]
		}
		if topic == nil {
			continue
		}
		var svcNode *store.NodeRow
		switch e.EdgeType {
		case "PUBLISHES_TOPIC":
			topic = topicByID[e.ToNodeID]
			if topic == nil {
				continue
			}
			svcNode = owned[e.FromNodeID]
			if svcNode == nil {
				continue
			}
			if topicPubs[topic.NodeID] == nil {
				topicPubs[topic.NodeID] = make(map[string]bool)
			}
			topicPubs[topic.NodeID][svcNode.Name] = true
		case "CONSUMES_TOPIC":
			topic = topicByID[e.ToNodeID]
			if topic == nil {
				continue
			}
			svcNode = owned[e.FromNodeID]
			if svcNode == nil {
				continue
			}
			if topicSubs[topic.NodeID] == nil {
				topicSubs[topic.NodeID] = make(map[string]bool)
			}
			topicSubs[topic.NodeID][svcNode.Name] = true
		}
	}

	var topics []Topic
	for id, t := range topicByID {
		pubs := sortedKeys(topicPubs[id])
		subs := sortedKeys(topicSubs[id])
		internal := isInternalTopic(pubs, subs)
		topics = append(topics, Topic{
			Key:        t.NodeKey,
			Name:       t.Name,
			Transport:  topicTransport(*t),
			Publishers: pubs,
			Consumers:  subs,
			Internal:   internal,
		})
	}
	sortTopics(topics)

	// Externals (same as C1).
	externalByID := make(map[int64]*store.NodeRow)
	for i := range nodes {
		if nodes[i].NodeType == "external_system" {
			externalByID[nodes[i].NodeID] = &nodes[i]
		}
	}
	externalCallers := make(map[int64]map[string]bool)
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
		externals = append(externals, External{
			Key:       ext.NodeKey,
			Name:      ext.Name,
			CallsFrom: sortedKeys(externalCallers[id]),
		})
	}
	sortExternals(externals)

	// Build service lookup by name for dedup.
	svcKeyByName := make(map[string]string)
	for _, n := range nodes {
		if n.Layer == "service" && n.NodeType == "service" {
			svcKeyByName[n.Name] = n.NodeKey
		}
	}
	topicKeyByName := make(map[string]string)
	for _, t := range topics {
		topicKeyByName[t.Name] = t.Key
	}
	extKeyByName := make(map[string]string)
	for _, ext := range externals {
		extKeyByName[ext.Name] = ext.Key
	}

	// Lifted edges: provider-level → service level, deduplicated.
	type edgeKey struct{ from, to, kind, label string }
	edgeSeen := make(map[edgeKey]bool)
	var c2Edges []C2Edge

	addEdge := func(from, to, kind, label string) {
		k := edgeKey{from, to, kind, label}
		if edgeSeen[k] {
			return
		}
		edgeSeen[k] = true
		c2Edges = append(c2Edges, C2Edge{From: from, To: to, Kind: kind, Label: label})
	}

	for _, e := range edges {
		fromSvc := owned[e.FromNodeID]
		if fromSvc == nil {
			continue
		}
		switch e.EdgeType {
		case "CALLS_SERVICE":
			toNode := nodeByID[e.ToNodeID]
			if toNode == nil {
				continue
			}
			if toNode.NodeType == "external_system" {
				addEdge(fromSvc.NodeKey, toNode.NodeKey, "http", "HTTP")
			} else if toNode.Layer == "service" && toNode.NodeType == "service" {
				if toNode.NodeKey != fromSvc.NodeKey {
					addEdge(fromSvc.NodeKey, toNode.NodeKey, "http", "HTTP")
				}
			}
		case "PUBLISHES_TOPIC":
			toNode := nodeByID[e.ToNodeID]
			if toNode == nil {
				continue
			}
			addEdge(fromSvc.NodeKey, toNode.NodeKey, "async", "publishes")
		case "CONSUMES_TOPIC":
			// rendered as topic→service below — consumption flows from the
			// channel to the consumer, not the other way around
		}
	}

	// Add topic→service consumer edges.
	for _, e := range edges {
		if e.EdgeType != "CONSUMES_TOPIC" {
			continue
		}
		toNode := nodeByID[e.ToNodeID]
		if toNode == nil || toNode.NodeType != "topic" {
			continue
		}
		consumerSvc := owned[e.FromNodeID]
		if consumerSvc == nil {
			continue
		}
		addEdge(toNode.NodeKey, consumerSvc.NodeKey, "async", "consumes")
	}

	return &C2{
		Services:  services,
		Topics:    topics,
		Externals: externals,
		Edges:     c2Edges,
	}, nil
}
