package admin

// C4 view-model API — three levels of abstraction.
//
// C1 /api/c4/c1?domain=X  — system context: services, externals, infra
// C2 /api/c4/c2?domain=X  — container diagram: services, topics, lifted edges
// C3 /api/c4/c3?domain=X&service=<key|name> — component diagram: one service
//
// Ownership rule: a code node belongs to the service whose root directory
// (= filepath.Dir(service.FilePath)) is a path prefix of the code node's FilePath.
// This is used to "lift" provider-level CALLS_SERVICE / PUBLISHES_TOPIC /
// CONSUMES_TOPIC edges to their owning service.

import (
	"encoding/json"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/alexdx2/chronicle-core/manifest"
	"github.com/alexdx2/chronicle-core/store"
)

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

// ---------- C1: system context ----------

type c1External struct {
	Key       string   `json:"key"`
	Name      string   `json:"name"`
	CallsFrom []string `json:"calls_from"`
}

type c1Infra struct {
	Key  string `json:"key"`
	Name string `json:"name"`
	Kind string `json:"kind"`
	Used bool   `json:"used"`
}

type c1Edge struct {
	From  string `json:"from"`
	To    string `json:"to"`
	Label string `json:"label"`
}

type c1Stats struct {
	Services  int `json:"services"`
	Endpoints int `json:"endpoints"`
	Models    int `json:"models"`
	Topics    int `json:"topics"`
	Flows     int `json:"flows"`
}

type c1Response struct {
	Domain    string       `json:"domain"`
	Name      string       `json:"name"`
	Stats     c1Stats      `json:"stats"`
	Externals []c1External `json:"externals"`
	Infra     []c1Infra    `json:"infra"`
	Edges     []c1Edge     `json:"edges"`
}

func (s *Server) handleC1(w http.ResponseWriter, r *http.Request) {
	domain := r.URL.Query().Get("domain")
	if domain == "" {
		domain = s.domainFromManifest()
	}

	st := s.getStore()
	allNodes, err := st.ListNodes(store.NodeFilter{Domain: domain})
	if err != nil {
		httpError(w, err, 500)
		return
	}
	nodes := activeOnly(allNodes)

	allEdges, err := st.ListEdges(store.EdgeFilter{})
	if err != nil {
		httpError(w, err, 500)
		return
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
	stats := c1Stats{}
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

	var externals []c1External
	for id, ext := range externalByID {
		callers := sortedKeys(externalCallers[id])
		externals = append(externals, c1External{
			Key:       ext.NodeKey,
			Name:      ext.Name,
			CallsFrom: callers,
		})
	}
	sortExternals(externals)

	// Infra: from manifest.
	var infra []c1Infra
	var c1Edges []c1Edge

	if m, err := manifest.LoadFile(s.manifestPath); err == nil {
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
			infra = append(infra, c1Infra{
				Key:  key,
				Name: inf.Name,
				Kind: inf.Type,
				Used: used,
			})
			// Edge: system → infra
			if used {
				c1Edges = append(c1Edges, c1Edge{From: "system", To: key, Label: "events"})
			}
		}
	}

	// Add edges for externals.
	for _, ext := range externals {
		c1Edges = append(c1Edges, c1Edge{From: "system", To: ext.Key, Label: "HTTP"})
	}

	name := domainDisplayName(s.manifestPath, domain)
	httpJSON(w, c1Response{
		Domain:    domain,
		Name:      name,
		Stats:     stats,
		Externals: externals,
		Infra:     infra,
		Edges:     c1Edges,
	})
}

// ---------- C2: container diagram ----------

type c2ServiceStats struct {
	Endpoints int `json:"endpoints"`
	Models    int `json:"models"`
	Providers int `json:"providers"`
	Modules   int `json:"modules"`
}

type c2Service struct {
	Key   string         `json:"key"`
	Name  string         `json:"name"`
	Stats c2ServiceStats `json:"stats"`
	Tech  string         `json:"tech,omitempty"`
}

type c2Topic struct {
	Key        string   `json:"key"`
	Name       string   `json:"name"`
	Transport  string   `json:"transport"`
	Publishers []string `json:"publishers"`
	Consumers  []string `json:"consumers"`
	Internal   bool     `json:"internal,omitempty"`
}

type c2Edge struct {
	From  string `json:"from"`
	To    string `json:"to"`
	Kind  string `json:"kind"`
	Label string `json:"label"`
}

type c2Response struct {
	Services  []c2Service  `json:"services"`
	Topics    []c2Topic    `json:"topics"`
	Externals []c1External `json:"externals"`
	Edges     []c2Edge     `json:"edges"`
}

func (s *Server) handleC2(w http.ResponseWriter, r *http.Request) {
	domain := r.URL.Query().Get("domain")
	if domain == "" {
		domain = s.domainFromManifest()
	}

	st := s.getStore()
	allNodes, err := st.ListNodes(store.NodeFilter{Domain: domain})
	if err != nil {
		httpError(w, err, 500)
		return
	}
	nodes := activeOnly(allNodes)

	allEdges, err := st.ListEdges(store.EdgeFilter{})
	if err != nil {
		httpError(w, err, 500)
		return
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
	if m, err := manifest.LoadFile(s.manifestPath); err == nil {
		if len(m.Tech) > 0 {
			svcTech = m.Tech[0]
		}
	}

	// Build services.
	svcByID := make(map[int64]*c2Service)
	svcByKey := make(map[string]*c2Service)
	var services []c2Service
	for _, n := range nodes {
		if n.Layer != "service" || n.NodeType != "service" {
			continue
		}
		svc := c2Service{
			Key:  n.NodeKey,
			Name: n.Name,
			Tech: svcTech,
		}
		svcByID[n.NodeID] = &svc
		svcByKey[n.NodeKey] = &svc
		services = append(services, svc)
	}

	// After building services slice, we need stable pointers — rebuild.
	svcByID = make(map[int64]*c2Service, len(services))
	svcByKey = make(map[string]*c2Service, len(services))
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

	svcEndpoints := make(map[int64]map[int64]bool)  // svcID → set of endpoint IDs
	svcModels := make(map[int64]map[int64]bool)      // svcID → set of model node IDs
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
		svc.Stats = c2ServiceStats{
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

	topicPubs := make(map[int64]map[string]bool)  // topicID → set of service names
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

	var topics []c2Topic
	for id, t := range topicByID {
		pubs := sortedKeys(topicPubs[id])
		subs := sortedKeys(topicSubs[id])
		internal := isInternalTopic(pubs, subs)
		topics = append(topics, c2Topic{
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

	var externals []c1External
	for id, ext := range externalByID {
		externals = append(externals, c1External{
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
	var c2Edges []c2Edge

	addEdge := func(from, to, kind, label string) {
		k := edgeKey{from, to, kind, label}
		if edgeSeen[k] {
			return
		}
		edgeSeen[k] = true
		c2Edges = append(c2Edges, c2Edge{From: from, To: to, Kind: kind, Label: label})
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

	httpJSON(w, c2Response{
		Services:  services,
		Topics:    topics,
		Externals: externals,
		Edges:     c2Edges,
	})
}

// ---------- C3: component diagram ----------

type c3Module struct {
	Key     string   `json:"key"`
	Name    string   `json:"name"`
	Members []string `json:"members"`
}

type c3Endpoint struct {
	Key  string `json:"key"`
	Name string `json:"name"`
}

type c3Component struct {
	Key        string       `json:"key"`
	Name       string       `json:"name"`
	Type       string       `json:"type"`
	Endpoints  []c3Endpoint `json:"endpoints,omitempty"`
	UsesModels []string     `json:"uses_models,omitempty"`
}

type c3InternalEdge struct {
	From string `json:"from"`
	To   string `json:"to"`
	Kind string `json:"kind"`
}

type c3BoundaryOut struct {
	ToName  string `json:"to_name"`
	ToKind  string `json:"to_kind"`
	Via     string `json:"via"`
	Kind    string `json:"kind"`
}

type c3BoundaryIn struct {
	FromName string `json:"from_name"`
	Kind     string `json:"kind"`
}

type c3Boundary struct {
	Outgoing []c3BoundaryOut `json:"outgoing"`
	Incoming []c3BoundaryIn  `json:"incoming"`
}

type c3Response struct {
	Service       map[string]string `json:"service"`
	Modules       []c3Module        `json:"modules"`
	Components    []c3Component     `json:"components"`
	InternalEdges []c3InternalEdge  `json:"internal_edges"`
	Boundary      c3Boundary        `json:"boundary"`
}

func (s *Server) handleC3(w http.ResponseWriter, r *http.Request) {
	domain := r.URL.Query().Get("domain")
	if domain == "" {
		domain = s.domainFromManifest()
	}
	svcParam := r.URL.Query().Get("service")
	if svcParam == "" {
		httpError(w, errorf("service parameter required"), 400)
		return
	}

	st := s.getStore()
	allNodes, err := st.ListNodes(store.NodeFilter{Domain: domain})
	if err != nil {
		httpError(w, err, 500)
		return
	}
	nodes := activeOnly(allNodes)

	allEdges, err := st.ListEdges(store.EdgeFilter{})
	if err != nil {
		httpError(w, err, 500)
		return
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
		if n.NodeKey == svcParam || n.Name == svcParam {
			targetSvc = n
			break
		}
	}
	if targetSvc == nil {
		httpError(w, errorf("service %q not found", svcParam), 404)
		return
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

	var modules []c3Module
	for mid, members := range moduleMembers {
		mn := nodeByID[mid]
		if mn == nil {
			continue
		}
		modules = append(modules, c3Module{
			Key:     mn.NodeKey,
			Name:    mn.Name,
			Members: members,
		})
	}
	sortModules(modules)

	// Components: owned controllers + providers (NOT modules).
	compEndpoints := make(map[int64][]c3Endpoint)
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
			compEndpoints[e.FromNodeID] = append(compEndpoints[e.FromNodeID], c3Endpoint{
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

	var components []c3Component
	for _, n := range nodes {
		if !ownedByTarget[n.NodeID] {
			continue
		}
		if n.NodeType != "controller" && n.NodeType != "provider" {
			continue
		}
		comp := c3Component{
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
	var internalEdges []c3InternalEdge
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
		internalEdges = append(internalEdges, c3InternalEdge{
			From: fromNode.NodeKey,
			To:   toNode.NodeKey,
			Kind: "injects",
		})
	}

	// Boundary edges.
	// Outgoing: from owned components → other services/topics/externals.
	type outKey struct{ toName, kind string }
	outSeen := make(map[outKey]bool)
	var outgoing []c3BoundaryOut

	// Incoming: from other services' components or topics → this service's components.
	type inKey struct{ fromName, kind string }
	inSeen := make(map[inKey]bool)
	var incoming []c3BoundaryIn

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
						outgoing = append(outgoing, c3BoundaryOut{
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
						outgoing = append(outgoing, c3BoundaryOut{
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
					outgoing = append(outgoing, c3BoundaryOut{
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
					incoming = append(incoming, c3BoundaryIn{
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
			incoming = append(incoming, c3BoundaryIn{
				FromName: callerSvc.Name,
				Kind:     "http",
			})
		}
	}

	sortOutgoing(outgoing)
	sortIncoming(incoming)

	httpJSON(w, c3Response{
		Service: map[string]string{
			"key":  targetSvc.NodeKey,
			"name": targetSvc.Name,
		},
		Modules:       modules,
		Components:    components,
		InternalEdges: internalEdges,
		Boundary: c3Boundary{
			Outgoing: outgoing,
			Incoming: incoming,
		},
	})
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

func sortExternals(e []c1External) {
	for i := 1; i < len(e); i++ {
		for j := i; j > 0 && e[j].Name < e[j-1].Name; j-- {
			e[j], e[j-1] = e[j-1], e[j]
		}
	}
}

func sortTopics(t []c2Topic) {
	for i := 1; i < len(t); i++ {
		for j := i; j > 0 && t[j].Name < t[j-1].Name; j-- {
			t[j], t[j-1] = t[j-1], t[j]
		}
	}
}

func sortModules(m []c3Module) {
	for i := 1; i < len(m); i++ {
		for j := i; j > 0 && m[j].Name < m[j-1].Name; j-- {
			m[j], m[j-1] = m[j-1], m[j]
		}
	}
}

func sortComponents(c []c3Component) {
	for i := 1; i < len(c); i++ {
		for j := i; j > 0 && c[j].Name < c[j-1].Name; j-- {
			c[j], c[j-1] = c[j-1], c[j]
		}
	}
}

func sortOutgoing(o []c3BoundaryOut) {
	for i := 1; i < len(o); i++ {
		for j := i; j > 0 && o[j].ToName < o[j-1].ToName; j-- {
			o[j], o[j-1] = o[j-1], o[j]
		}
	}
}

func sortIncoming(in []c3BoundaryIn) {
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

// errorf creates a simple error with a format string.
func errorf(format string, args ...any) error {
	if len(args) == 0 {
		return &simpleError{msg: format}
	}
	msg := format
	for _, a := range args {
		// Basic replacement: replace first %q with quoted string.
		s, ok := a.(string)
		if ok {
			msg = strings.Replace(msg, "%q", `"`+s+`"`, 1)
			msg = strings.Replace(msg, "%s", s, 1)
		}
	}
	return &simpleError{msg: msg}
}

type simpleError struct{ msg string }

func (e *simpleError) Error() string { return e.msg }
