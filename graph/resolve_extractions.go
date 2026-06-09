package graph

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/alexdx2/chronicle-core/store"
	"github.com/alexdx2/chronicle-core/validate"
)

// Fact represents a single extracted observation from a source file.
type Fact struct {
	Kind       string   `json:"kind"`                  // import, call, decorator, http_call, dependency, model, enum, model_relation, endpoint, produces, consumes, flow, declares, parent
	FromFile   string   `json:"from_file,omitempty"`   // source file (usually implicit from extraction context)
	From       string   `json:"from,omitempty"`        // source entity name/identifier
	FromType   string   `json:"from_type,omitempty"`   // node type of source: controller, provider, module, repository, service
	To         string   `json:"to"`                    // target module/service/entity
	ToType     string   `json:"to_type,omitempty"`     // node type of target: controller, provider, module, model, enum, topic, endpoint
	Symbols    []string `json:"symbols,omitempty"`     // imported symbols
	Method     string   `json:"method,omitempty"`      // HTTP method or called method name
	Object     string   `json:"object,omitempty"`      // callee object
	Decorator  string   `json:"decorator,omitempty"`   // decorator name
	Target     string   `json:"target,omitempty"`      // URL or target identifier
	Transport  string   `json:"transport,omitempty"`   // transport mechanism: "queue", "local", "kafka", etc.
	Confidence float64  `json:"confidence,omitempty"`  // agent confidence [0,1]
	Note       string   `json:"note,omitempty"`        // agent uncertainty/note
	Reason     string   `json:"reason,omitempty"`      // reason for relationship (e.g. parent container justification)
	// Flow-specific fields
	FlowName   string   `json:"flow_name,omitempty"`   // use case name (e.g. "Tom attacks Jerry")
	Trigger    string   `json:"trigger,omitempty"`      // what triggers this flow (endpoint, event, cron)
	Steps      []string `json:"steps,omitempty"`        // ordered list of steps in the flow
	Requires   []string `json:"requires,omitempty"`     // services/models this flow depends on
}

// frameworkInjectDenylist contains NestJS/Node framework plumbing names that
// should never become provider nodes or INJECTS edges. These are runtime
// tokens injected via @InjectQueue(), EventEmitter2, etc. — not application
// services. Add entries in lowercase; matching is case-insensitive.
var frameworkInjectDenylist = map[string]bool{
	"queue":        true, // Bull/BullMQ @InjectQueue token
	"eventemitter2": true, // NestJS event emitter adapter
}

// ResolveOptions controls obligation gating before resolve.
type ResolveOptions struct {
	AllowDegraded bool `json:"allow_degraded"`
}

// ResolveExtractionsResult is returned by ResolveExtractions.
type ResolveExtractionsResult struct {
	FilesProcessed       int               `json:"files_processed"`
	ExtractionsResolved  int               `json:"extractions_resolved"`
	NodesCreated         int               `json:"nodes_created"`
	EdgesCreated         int               `json:"edges_created"`
	EvidenceCreated      int               `json:"evidence_created"`
	Hygiene              GraphHygieneStats `json:"hygiene"`
	QualityWarnings      []QualityWarning  `json:"quality_warnings,omitempty"`
	Unresolved           []UnresolvedRef   `json:"unresolved,omitempty"`
	Degraded             bool              `json:"degraded,omitempty"`
	DegradedFiles        []string          `json:"degraded_files,omitempty"`
}

// UnresolvedRef is a reference that couldn't be automatically resolved.
type UnresolvedRef struct {
	FromFile string `json:"from_file"`
	Kind     string `json:"kind"`
	Target   string `json:"target"`
	Reason   string `json:"reason"`
}

// UnmatchedHTTPCall represents an http_call that created a CALLS_SERVICE edge
// but couldn't be matched to a specific endpoint. Presented to the LLM
// in the endpoint_reconcile phase so it can emit calls_endpoint facts.
type UnmatchedHTTPCall struct {
	FromNodeKey string   `json:"from_node_key"`         // the client node
	FromName    string   `json:"from_name"`              // human-readable name
	FromFile    string   `json:"from_file"`              // source file
	TargetHost  string   `json:"target_host"`            // e.g. "tom-api"
	TargetURL   string   `json:"target_url"`             // full URL from fact
	Method      string   `json:"method"`                 // HTTP method
	Path        string   `json:"path"`                   // extracted path from URL
	Endpoints   []string `json:"known_endpoints"`        // endpoints exposed by the matched/similar service
}

// FindUnmatchedHTTPCalls identifies http_call edges that have CALLS_SERVICE
// but no corresponding CALLS_ENDPOINT. For each, it finds known endpoints
// on the target service so the LLM can match them.
func (g *Graph) FindUnmatchedHTTPCalls(domainKey string) []UnmatchedHTTPCall {
	// Get all CALLS_SERVICE edges (from http_call facts)
	callsServiceEdges, _ := g.store.ListEdges(store.EdgeFilter{EdgeType: "CALLS_SERVICE"})
	// Get all CALLS_ENDPOINT edges
	callsEndpointEdges, _ := g.store.ListEdges(store.EdgeFilter{EdgeType: "CALLS_ENDPOINT"})

	// Build set of from_node_keys that already have CALLS_ENDPOINT
	hasEndpoint := map[string]bool{}
	for _, e := range callsEndpointEdges {
		hasEndpoint[e.FromNodeKey] = true
	}

	// Get all endpoints in this domain
	endpointNodes, _ := g.store.ListNodes(store.NodeFilter{Domain: domainKey, NodeType: "endpoint"})

	// Group endpoints by service: find which service exposes which endpoints
	exposeEdges, _ := g.store.ListEdges(store.EdgeFilter{EdgeType: "EXPOSES_ENDPOINT"})
	// Map: service controller key → endpoint names
	controllerEndpoints := map[string][]string{}
	for _, e := range exposeEdges {
		epNode, _ := g.store.GetNodeByKey(e.ToNodeKey)
		if epNode != nil {
			controllerEndpoints[e.FromNodeKey] = append(controllerEndpoints[e.FromNodeKey], epNode.Name)
		}
	}

	// Build flat list of all endpoint names for fallback
	allEndpoints := make([]string, 0, len(endpointNodes))
	for _, ep := range endpointNodes {
		allEndpoints = append(allEndpoints, ep.Name)
	}

	var result []UnmatchedHTTPCall
	for _, e := range callsServiceEdges {
		if hasEndpoint[e.FromNodeKey] {
			continue // already matched
		}
		fromNode, _ := g.store.GetNodeByKey(e.FromNodeKey)
		if fromNode == nil {
			continue
		}
		// Extract host from the target service key
		parts := strings.SplitN(e.ToNodeKey, ":", 4)
		host := ""
		if len(parts) >= 4 {
			host = parts[3]
		}

		// Find evidence to get original URL
		evidence, _ := g.store.ListEvidenceByEdge(e.EdgeID)
		targetURL := ""
		method := "GET"
		for _, ev := range evidence {
			var assertion map[string]any
			if json.Unmarshal([]byte(ev.Assertion), &assertion) == nil {
				if sub, ok := assertion["substring"].(string); ok {
					targetURL = sub
				}
			}
		}
		path := extractPathFromURL(targetURL)

		result = append(result, UnmatchedHTTPCall{
			FromNodeKey: e.FromNodeKey,
			FromName:    fromNode.Name,
			FromFile:    fromNode.FilePath,
			TargetHost:  host,
			TargetURL:   targetURL,
			Method:      method,
			Path:        path,
			Endpoints:   allEndpoints,
		})
	}
	return result
}

// finalizeObligationsBeforeResolve enforces the obligation gate before graph resolve.
// When allowDegraded is true, open obligations for files that already have extractions
// are marked skipped so interactive scans can proceed to resolve.
func (g *Graph) finalizeObligationsBeforeResolve(domainKey string, revisionID int64, allowDegraded bool) ([]string, error) {
	openObligations, err := g.store.ListOpenObligations(revisionID)
	if err != nil {
		return nil, fmt.Errorf("finalize obligations: %w", err)
	}
	if len(openObligations) == 0 {
		return nil, nil
	}

	if !allowDegraded {
		total, _ := g.store.ListAllObligations(revisionID)
		open := 0
		satisfied := 0
		for _, o := range total {
			switch o.Status {
			case "satisfied":
				satisfied++
			case "open":
				open++
			}
		}
		return nil, fmt.Errorf("scan incomplete: %d/%d obligations completed (%d still open). Complete all obligations before resolving", satisfied, len(total), open)
	}

	extractions, err := g.store.ListUnresolvedExtractions(revisionID, domainKey)
	if err != nil {
		return nil, fmt.Errorf("finalize obligations: %w", err)
	}
	filesWithExtraction := make(map[string]bool)
	for _, ext := range extractions {
		filesWithExtraction[ext.FilePath] = true
	}

	var degradedFiles []string
	degradedSet := make(map[string]bool)
	for _, ob := range openObligations {
		if !filesWithExtraction[ob.TargetKey] {
			continue
		}
		if err := g.store.MarkObligationFailed(ob.ObligationID, "degraded_resolve: extraction present"); err != nil {
			return nil, err
		}
		if !degradedSet[ob.TargetKey] {
			degradedSet[ob.TargetKey] = true
			degradedFiles = append(degradedFiles, ob.TargetKey)
		}
	}

	remaining, _ := g.store.ListOpenObligations(revisionID)
	if len(remaining) > 0 {
		return nil, fmt.Errorf("scan incomplete: %d obligations still open with no extraction", len(remaining))
	}
	return degradedFiles, nil
}

// ResolveExtractions takes all pending extractions and builds the graph.
// Creates nodes, edges, and evidence from the collected facts.
func (g *Graph) ResolveExtractions(domainKey string, revisionID int64) (*ResolveExtractionsResult, error) {
	return g.ResolveExtractionsWithOptions(domainKey, revisionID, ResolveOptions{})
}

// ResolveExtractionsWithOptions builds the graph, optionally allowing degraded resolve
// when each file has at least one extraction (interactive agent mode).
func (g *Graph) ResolveExtractionsWithOptions(domainKey string, revisionID int64, opts ResolveOptions) (*ResolveExtractionsResult, error) {
	degradedFiles, err := g.finalizeObligationsBeforeResolve(domainKey, revisionID, opts.AllowDegraded)
	if err != nil {
		return nil, err
	}

	rawExtractions, err := g.store.ListUnresolvedExtractions(revisionID, domainKey)
	if err != nil {
		return nil, fmt.Errorf("ResolveExtractions: %w", err)
	}

	// Merge voted extractions before resolving
	extractions := mergeVotedExtractions(rawExtractions)

	result := &ResolveExtractionsResult{
		FilesProcessed: len(extractions),
		Degraded:       len(degradedFiles) > 0,
		DegradedFiles:  degradedFiles,
	}

	// Collect all facts across all files
	var allFiles []fileFacts

	for _, ext := range extractions {
		normalized := normalizeFacts(ext.FactsJSON)
		var facts []Fact
		if err := json.Unmarshal([]byte(normalized), &facts); err != nil {
			result.Unresolved = append(result.Unresolved, UnresolvedRef{
				FromFile: ext.FilePath,
				Kind:     "parse_error",
				Target:   "",
				Reason:   "invalid facts JSON: " + err.Error(),
			})
			continue
		}
		allFiles = append(allFiles, fileFacts{filePath: ext.FilePath, fromType: ext.FromType, facts: facts})
	}

	// Phase 1: Discover all entities mentioned across all files
	// Build a set of known entity names for resolution
	knownEntities := g.collectKnownEntities(allFiles)

	// Sort: files with endpoints first (they expose endpoints that others call)
	sort.SliceStable(allFiles, func(i, j int) bool {
		return fileTypeOrderFromFacts(allFiles[i].facts) < fileTypeOrderFromFacts(allFiles[j].facts)
	})

	// Index all scanned files (including type_only / empty facts) for class-name resolution.
	allScanned, _ := g.store.ListExtractions(revisionID, domainKey)
	g.scanFileIndex = buildScanFileIndex(allScanned)
	defer func() { g.scanFileIndex = scanFileIndex{} }()

	// Phase 2: Create nodes and edges from facts
	for _, ff := range allFiles {
		// File-level from_type: API param > first fact > default "provider"
		fileNodeType := ff.fromType
		if fileNodeType == "" {
			fileNodeType = fileNodeTypeFromFacts(ff.facts)
		}
		for _, fact := range ff.facts {
			if fact.FromType == "" {
				fact.FromType = fileNodeType
			}
			created, unresolved := g.resolveOneFact(domainKey, revisionID, ff.filePath, fact, knownEntities)
			result.NodesCreated += created.nodes
			result.EdgesCreated += created.edges
			result.EvidenceCreated += created.evidence
			if unresolved != nil {
				result.Unresolved = append(result.Unresolved, *unresolved)
			}
		}
	}

	// Post-resolve: detect module nodes from graph structure and fix edge types.
	// A module = node with ≥2 outbound import edges, 0 endpoints, 0 service actions.
	// This is generic (not framework-specific) — modules wire things, they don't DO things.
	g.fixModuleEdges(domainKey)
	g.detectContainsConflicts(domainKey, revisionID)
	g.detectContainsCycles(domainKey, revisionID)
	hygiene := g.applyGraphHygiene(domainKey)
	result.Hygiene = hygiene
	// Mark all extractions as resolved
	if err := g.store.MarkExtractionsResolved(revisionID, domainKey); err != nil {
		return nil, fmt.Errorf("ResolveExtractions mark resolved: %w", err)
	}
	if n, err := g.CountResolvedExtractions(revisionID, domainKey); err == nil {
		result.ExtractionsResolved = n
	}
	result.QualityWarnings = g.BuildScanQualityReport(domainKey)

	g.emitter.Emit(ScanEvent{
		Kind:  EventResolveComplete,
		Phase: "resolve",
		Data: map[string]any{
			"nodes_created":               result.NodesCreated,
			"edges_created":               result.EdgesCreated,
			"evidence_created":            result.EvidenceCreated,
			"files_processed":             result.FilesProcessed,
			"topic_endpoints_suppressed":  hygiene.TopicEndpointsSuppressed,
			"controller_contains_removed": hygiene.ControllerContainsRemoved,
			"service_duplicates_merged":   hygiene.ServiceDuplicatesMerged,
		},
	})

	return result, nil
}

type createdCounts struct {
	nodes    int
	edges    int
	evidence int
}

type fileFacts struct {
	filePath string
	fromType string // file-level from_type from API (not from individual facts)
	facts    []Fact
}

func (g *Graph) collectKnownEntities(allFiles []fileFacts) map[string]bool {
	// This is a simplified version — collects all "to" targets and "from" sources
	// In reality would also query existing graph nodes
	entities := make(map[string]bool)

	// Get existing nodes from graph
	nodes, _ := g.store.ListNodes(store.NodeFilter{})
	for _, n := range nodes {
		entities[n.Name] = true
		entities[n.NodeKey] = true
	}

	// Collect from extraction facts
	for _, ff := range allFiles {
		for _, f := range ff.facts {
			if f.From != "" {
				entities[f.From] = true
			}
			if f.To != "" {
				entities[f.To] = true
			}
		}
	}
	return entities
}

func (g *Graph) resolveOneFact(domainKey string, revisionID int64, filePath string, fact Fact, _ map[string]bool) (createdCounts, *UnresolvedRef) {
	var counts createdCounts

	switch fact.Kind {
	case "import":
		// Filter: skip infrastructure and architectural deps
		if !ShouldTrackDependency(fact.To) {
			return counts, nil
		}

		// Create evidence for an import relationship
		assertion := buildImportAssertion(fact)
		assertionJSON, _ := json.Marshal(assertion)

		// Try to find or create the edge — use fact-provided types
		fromNodeKey := typedNodeKeyFromFile(domainKey, filePath, fact.FromType)
		toName := inferNameFromImport(fact.To)

		// Check if target already exists as a different type (e.g. controller)
		// to avoid creating duplicate provider nodes for controllers
		toNodeKey := typedNodeKeyFromImport(domainKey, fact.To, fact.ToType)
		if fact.ToType == "" || fact.ToType == "provider" {
			// Check if a controller node already exists with this name
			ctrlKey := "code:controller:" + domainKey + ":" + strings.ToLower(toName)
			if _, err := g.store.GetNodeIDByKey(ctrlKey); err == nil {
				toNodeKey = ctrlKey
			}
			// Also try PascalCase→dot-case resolution
			dotCase := normalizePascalCase(toName)
			altKey := "code:provider:" + domainKey + ":" + strings.ToLower(dotCase)
			if _, err := g.store.GetNodeIDByKey(altKey); err == nil {
				toNodeKey = altKey
			}
		}

		// Ensure nodes exist
		fromID := g.ensureNodeID(domainKey, revisionID, fromNodeKey, inferNameFromPath(filePath), filePath)
		g.registerNodeAlias(fromID, inferNameFromPath(filePath), "file_stem")

		// Try to resolve target via alias before creating new node
		var toID int64
		if candidates, err := g.store.FindCodeNodesByAlias(domainKey, toName, ""); err == nil && len(candidates) == 1 {
			toID = candidates[0].NodeID
			toNodeKey = candidates[0].NodeKey
		} else {
			// Fallback: create as before
			toID = g.ensureNodeID(domainKey, revisionID, toNodeKey, toName, "")
		}

		// Determine edge type based on from/to node types
		edgeType := inferImportEdgeType(fromNodeKey, toNodeKey)
		edgeKey := fromNodeKey + "->" + toNodeKey + ":" + edgeType
		_, err := g.store.UpsertEdge(store.EdgeRow{
			EdgeKey:             edgeKey,
			FromNodeID:          fromID,
			ToNodeID:            toID,
			FromNodeKey:         fromNodeKey,
			ToNodeKey:           toNodeKey,
			EdgeType:            edgeType,
			DerivationKind:      "hard",
			Active:              true,
			LastSeenRevisionID:  revisionID,
			Confidence:          0.9,
			Freshness:           1.0,
			TrustScore:          0.9,
			Metadata:            "{}",
			ValidFromRevisionID: 0, // legacy mode: update in place, don't close+reopen on duplicate
		})
		if err != nil {
			// Edge might already exist — that's fine
			return counts, nil
		}
		counts.edges++

		// Add evidence with assertion
		confidence := fact.Confidence
		if confidence == 0 {
			confidence = 0.95
		}
		_, _ = g.AddEdgeEvidence(edgeKey, validate.EvidenceInput{
			TargetKind:       "edge",
			SourceKind:       "file",
			FilePath:         filePath,
			ExtractorID:      "chronicle-scan",
			ExtractorVersion: "1.0",
			Confidence:       confidence,
			RevisionID:       revisionID,
			AssertionKind:    "import_specifier",
			Assertion:        string(assertionJSON),
		})
		counts.evidence++

	case "dependency":
		// Filter: skip infrastructure and architectural deps
		if !ShouldTrackDependency(fact.To) {
			return counts, nil
		}

		// Pick assertion kind based on file type
		assertionKind, assertion := buildDependencyAssertion(filePath, fact)

		fromNodeKey := typedNodeKeyFromFile(domainKey, filePath, fact.FromType)
		toNodeKey := "code:module:" + domainKey + ":" + normalizePackageName(fact.To)

		fromID := g.ensureNodeID(domainKey, revisionID, fromNodeKey, inferNameFromPath(filePath), filePath)
		toID := g.ensureNodeID(domainKey, revisionID, toNodeKey, fact.To, "")

		edgeKey := fromNodeKey + "->" + toNodeKey + ":DEPENDS_ON"
		_, err := g.store.UpsertEdge(store.EdgeRow{
			EdgeKey: edgeKey, FromNodeKey: fromNodeKey, ToNodeKey: toNodeKey,
			FromNodeID: fromID, ToNodeID: toID,
			EdgeType: "DEPENDS_ON", DerivationKind: "hard", Active: true,
			LastSeenRevisionID: revisionID, Confidence: 0.95, Freshness: 1.0, TrustScore: 0.95,
			Metadata: "{}", ValidFromRevisionID: 0, // legacy mode: update in place, don't close+reopen on duplicate
		})
		if err == nil {
			counts.edges++
		}

		_, _ = g.AddEdgeEvidence(edgeKey, validate.EvidenceInput{
			TargetKind: "edge", SourceKind: "file", FilePath: filePath,
			ExtractorID: "chronicle-scan", ExtractorVersion: "1.0",
			Confidence: 0.95, RevisionID: revisionID,
			AssertionKind: assertionKind, Assertion: assertion,
		})
		counts.evidence++

	case "http_call":
		// External HTTP call — create service edge + evidence
		// First, try to resolve hostname against known service aliases
		fromNodeKey := typedNodeKeyFromFile(domainKey, filePath, fact.FromType)
		fromID := g.ensureNodeID(domainKey, revisionID, fromNodeKey, inferNameFromPath(filePath), filePath)

		targetName := inferExternalSystemName(fact.Target)

		// Check if hostname matches a known service via DNS alias
		var toNodeKey string
		var toID int64
		resolved := false
		if aliases, err := g.store.ListAliasesByNormalized(strings.ToLower(targetName), "dns"); err == nil && len(aliases) > 0 {
			// Found a matching alias — link to existing service node
			if node, err := g.store.GetNodeByID(aliases[0].NodeID); err == nil {
				toNodeKey = node.NodeKey
				toID = node.NodeID
				resolved = true
			}
		}
		if !resolved {
			// No alias match — create external_system node
			toNodeKey = "service:external_system:" + domainKey + ":" + strings.ToLower(targetName)
			toID = g.ensureNodeID(domainKey, revisionID, toNodeKey, targetName, "")
		}

		edgeKey := fromNodeKey + "->" + toNodeKey + ":CALLS_SERVICE"
		_, err := g.store.UpsertEdge(store.EdgeRow{
			EdgeKey: edgeKey, FromNodeKey: fromNodeKey, ToNodeKey: toNodeKey,
			FromNodeID:          fromID,
			ToNodeID:            toID,
			EdgeType: "CALLS_SERVICE", DerivationKind: "linked", Active: true,
			LastSeenRevisionID: revisionID, Confidence: 0.85, Freshness: 1.0, TrustScore: 0.85,
			Metadata: "{}", ValidFromRevisionID: 0, // legacy mode: update in place, don't close+reopen on duplicate
		})
		if err == nil {
			counts.edges++
		}

		// Evidence: text_contains assertion for the URL
		assertion, _ := json.Marshal(map[string]any{
			"substring": fact.Target,
			"context":   "fetch",
		})
		_, _ = g.AddEdgeEvidence(edgeKey, validate.EvidenceInput{
			TargetKind: "edge", SourceKind: "file", FilePath: filePath,
			ExtractorID: "chronicle-scan", ExtractorVersion: "1.0",
			Confidence: 0.85, RevisionID: revisionID,
			AssertionKind: "text_contains", Assertion: string(assertion),
		})
		counts.evidence++

		// Also create CALLS_ENDPOINT edge if we can extract a path from the URL
		if endpointPath := extractPathFromURL(fact.Target); endpointPath != "" {
			epNodeKey, _ := normalizeEndpointKey(domainKey, fact.Method, endpointPath)
			// Only create edge if the endpoint node already exists (was exposed by another file)
			if epID, err2 := g.store.GetNodeIDByKey(epNodeKey); err2 == nil {
				callEpEdgeKey := fromNodeKey + "->" + epNodeKey + ":CALLS_ENDPOINT"
				_, err3 := g.store.UpsertEdge(store.EdgeRow{
					EdgeKey: callEpEdgeKey, FromNodeID: fromID, ToNodeID: epID,
					FromNodeKey: fromNodeKey, ToNodeKey: epNodeKey,
					EdgeType: "CALLS_ENDPOINT", DerivationKind: "linked", Active: true,
					LastSeenRevisionID: revisionID, Confidence: 0.80, Freshness: 1.0, TrustScore: 0.80,
					Metadata: "{}", ValidFromRevisionID: 0, // legacy mode: update in place, don't close+reopen on duplicate
				})
				if err3 == nil {
					counts.edges++
				}
			}
		}

	case "call":
		// Method call — evidence for the call expression
		if fact.Method == "" && fact.Object == "" {
			return counts, nil
		}
		fromNodeKey := typedNodeKeyFromFile(domainKey, filePath, fact.FromType)
		g.ensureNode(domainKey, revisionID, fromNodeKey, inferNameFromPath(filePath), filePath)

		assertion, _ := json.Marshal(map[string]any{
			"callee_object": fact.Object,
			"callee_method": fact.Method,
		})

		// Add evidence to existing edge — don't create edges from call facts alone
		if fact.Object != "" {
			toNodeKey := "code:provider:" + domainKey + ":" + strings.ToLower(fact.Object)
			edgeKey := fromNodeKey + "->" + toNodeKey + ":INJECTS"
			// Only add evidence to existing edge — don't create edges from call facts alone
			_, _ = g.AddEdgeEvidence(edgeKey, validate.EvidenceInput{
				TargetKind: "edge", SourceKind: "file", FilePath: filePath,
				ExtractorID: "chronicle-scan", ExtractorVersion: "1.0",
				Confidence: 0.80, RevisionID: revisionID,
				AssertionKind: "call_expression", Assertion: string(assertion),
			})
			counts.evidence++
		}

	case "member_call":
		// 3-level member chain: this.X.Y.method() — promote Y to USES_MODEL if it matches a known model
		if fact.To == "" {
			return counts, nil
		}
		// Capitalize first letter to match model node naming
		candidateName := strings.ToUpper(fact.To[:1]) + fact.To[1:]
		modelKey := "data:model:" + domainKey + ":" + strings.ToLower(candidateName)
		if modelID, err := g.store.GetNodeIDByKey(modelKey); err == nil {
			fromNodeKey := typedNodeKeyFromFile(domainKey, filePath, fact.FromType)
			fromID := g.ensureNodeID(domainKey, revisionID, fromNodeKey, inferNameFromPath(filePath), filePath)
			edgeKey := fromNodeKey + "->" + modelKey + ":USES_MODEL"
			_, err := g.store.UpsertEdge(store.EdgeRow{
				EdgeKey: edgeKey, FromNodeID: fromID, ToNodeID: modelID,
				FromNodeKey: fromNodeKey, ToNodeKey: modelKey,
				EdgeType: "USES_MODEL", DerivationKind: "hard", Active: true,
				LastSeenRevisionID: revisionID, Confidence: 0.90, Freshness: 1.0, TrustScore: 0.90,
				Metadata: "{}", ValidFromRevisionID: 0, // legacy mode: update in place, don't close+reopen on duplicate
			})
			if err == nil {
				counts.edges++
			}
		}
		// If not a known model, fact is kept as raw evidence but doesn't create an edge

	case "calls_service":
		// Explicit service call — creates CALLS_SERVICE edge.
		// If the target resolves to an in-domain code-layer node (DI provider/controller),
		// this is a local intra-service call — skip the edge entirely. Only truly external
		// or unresolvable targets get a CALLS_SERVICE edge.
		if fact.To == "" {
			return counts, nil
		}
		fromNodeKey := typedNodeKeyFromFile(domainKey, filePath, fact.FromType)
		fromID := g.ensureNodeID(domainKey, revisionID, fromNodeKey, inferNameFromPath(filePath), filePath)
		g.registerNodeAlias(fromID, inferNameFromPath(filePath), "file_stem")

		toName := fact.To

		// R4: resolve target and drop if it is a local code-layer node.
		// We use the same resolution order as resolveClassNameTarget but only look up
		// existing nodes — we must NOT create new nodes here.
		if isLocalCodeNode(g, domainKey, toName) {
			return counts, nil
		}

		// Try service layer first (service:service:domain:name)
		svcKey := "service:service:" + domainKey + ":" + normalizePackageName(toName)
		toNodeKey := svcKey
		existingNode, _ := g.store.GetNodeByKey(svcKey)
		if existingNode == nil {
			// Fall back to code:provider
			toNodeKey = "code:provider:" + domainKey + ":" + strings.ToLower(toName)
		}
		toID := g.ensureNodeID(domainKey, revisionID, toNodeKey, toName, "")

		edgeKey := fromNodeKey + "->" + toNodeKey + ":CALLS_SERVICE"
		confidence := fact.Confidence
		if confidence == 0 {
			confidence = 0.85
		}
		_, err := g.store.UpsertEdge(store.EdgeRow{
			EdgeKey: edgeKey, FromNodeID: fromID, ToNodeID: toID,
			FromNodeKey: fromNodeKey, ToNodeKey: toNodeKey,
			EdgeType: "CALLS_SERVICE", DerivationKind: "hard", Active: true,
			LastSeenRevisionID: revisionID, Confidence: confidence, Freshness: 1.0, TrustScore: confidence,
			Metadata: "{}", ValidFromRevisionID: 0, // legacy mode: update in place, don't close+reopen on duplicate
		})
		if err == nil {
			counts.edges++
		}

	case "uses_model":
		// Explicit model usage — creates USES_MODEL edge
		if fact.To == "" {
			return counts, nil
		}
		fromNodeKey := typedNodeKeyFromFile(domainKey, filePath, fact.FromType)
		fromID := g.ensureNodeID(domainKey, revisionID, fromNodeKey, inferNameFromPath(filePath), filePath)

		// Capitalize model name
		modelName := strings.ToUpper(fact.To[:1]) + fact.To[1:]
		modelKey := "data:model:" + domainKey + ":" + strings.ToLower(modelName)
		modelID := g.ensureNodeID(domainKey, revisionID, modelKey, modelName, "")

		edgeKey := fromNodeKey + "->" + modelKey + ":USES_MODEL"
		confidence := fact.Confidence
		if confidence == 0 {
			confidence = 0.85
		}
		_, err := g.store.UpsertEdge(store.EdgeRow{
			EdgeKey: edgeKey, FromNodeID: fromID, ToNodeID: modelID,
			FromNodeKey: fromNodeKey, ToNodeKey: modelKey,
			EdgeType: "USES_MODEL", DerivationKind: "hard", Active: true,
			LastSeenRevisionID: revisionID, Confidence: confidence, Freshness: 1.0, TrustScore: confidence,
			Metadata: "{}", ValidFromRevisionID: 0, // legacy mode: update in place, don't close+reopen on duplicate
		})
		if err == nil {
			counts.edges++
		}

	case "calls_endpoint":
		// Internal API call — creates CALLS_ENDPOINT edge
		if fact.Target == "" {
			return counts, nil
		}
		fromNodeKey := typedNodeKeyFromFile(domainKey, filePath, fact.FromType)
		fromID := g.ensureNodeID(domainKey, revisionID, fromNodeKey, inferNameFromPath(filePath), filePath)
		g.registerNodeAlias(fromID, inferNameFromPath(filePath), "file_stem")

		epKey, epName := normalizeEndpointKey(domainKey, fact.Method, fact.Target)
		epID := g.ensureNodeID(domainKey, revisionID, epKey, epName, "")

		edgeKey := fromNodeKey + "->" + epKey + ":CALLS_ENDPOINT"
		_, err := g.store.UpsertEdge(store.EdgeRow{
			EdgeKey: edgeKey, FromNodeID: fromID, ToNodeID: epID,
			FromNodeKey: fromNodeKey, ToNodeKey: epKey,
			EdgeType: "CALLS_ENDPOINT", DerivationKind: "hard", Active: true,
			LastSeenRevisionID: revisionID, Confidence: 0.85, Freshness: 1.0, TrustScore: 0.85,
			Metadata: "{}", ValidFromRevisionID: 0, // legacy mode: update in place, don't close+reopen on duplicate
		})
		if err == nil {
			counts.edges++
		}

	case "provides":
		// Module registration — module CONTAINS controller/provider (fact schema contract).
		if fact.To == "" {
			return counts, nil
		}
		moduleType := fact.FromType
		if moduleType == "" {
			moduleType = "module"
		}
		fromNodeKey := typedNodeKeyFromFile(domainKey, filePath, moduleType)
		fromID := g.ensureNodeID(domainKey, revisionID, fromNodeKey, inferNameFromPath(filePath), filePath)
		if fromID == 0 {
			return counts, nil
		}
		g.registerNodeAlias(fromID, inferNameFromPath(filePath), "file_stem")

		toNodeKey, toID := g.resolveClassNameTarget(domainKey, revisionID, fact.To)
		if toID == 0 {
			return counts, &UnresolvedRef{
				FromFile: filePath,
				Kind:     "provides",
				Target:   fact.To,
				Reason:   fmt.Sprintf("provides target %q not resolved to a code node", fact.To),
			}
		}

		edgeKey := fromNodeKey + "->" + toNodeKey + ":CONTAINS"
		confidence := fact.Confidence
		if confidence == 0 {
			confidence = 0.90
		}
		_, err := g.store.UpsertEdge(store.EdgeRow{
			EdgeKey: edgeKey, FromNodeID: fromID, ToNodeID: toID,
			FromNodeKey: fromNodeKey, ToNodeKey: toNodeKey,
			EdgeType: "CONTAINS", DerivationKind: "hard", Active: true,
			LastSeenRevisionID: revisionID, Confidence: confidence, Freshness: 1.0, TrustScore: confidence,
			Metadata: "{}", ValidFromRevisionID: 0,
		})
		if err == nil {
			counts.edges++
		}
		assertion, _ := json.Marshal(map[string]any{
			"provided": fact.To,
		})
		_, _ = g.AddEdgeEvidence(edgeKey, validate.EvidenceInput{
			TargetKind: "edge", SourceKind: "file", FilePath: filePath,
			ExtractorID: "chronicle-scan", ExtractorVersion: "1.0",
			Confidence: confidence, RevisionID: revisionID,
			AssertionKind: "module_provides", Assertion: string(assertion),
		})
		counts.evidence++

	case "injects":
		// Constructor DI — creates INJECTS edge (stronger than import)
		if fact.To == "" {
			return counts, nil
		}
		// R5: skip framework plumbing tokens that are never real application nodes.
		if frameworkInjectDenylist[strings.ToLower(fact.To)] {
			return counts, nil
		}
		if !isPlausibleClassName(fact.To) {
			return counts, nil
		}
		fromNodeKey := typedNodeKeyFromFile(domainKey, filePath, fact.FromType)
		fromID := g.ensureNodeID(domainKey, revisionID, fromNodeKey, inferNameFromPath(filePath), filePath)
		g.registerNodeAlias(fromID, inferNameFromPath(filePath), "file_stem")
		// Resolve injection target — matches PascalCase class names to existing file nodes
		toNodeKey, toID := g.resolveClassNameTarget(domainKey, revisionID, fact.To)
		if toID == 0 {
			return counts, nil
		}

		edgeKey := fromNodeKey + "->" + toNodeKey + ":INJECTS"
		_, err := g.store.UpsertEdge(store.EdgeRow{
			EdgeKey: edgeKey, FromNodeID: fromID, ToNodeID: toID,
			FromNodeKey: fromNodeKey, ToNodeKey: toNodeKey,
			EdgeType: "INJECTS", DerivationKind: "hard", Active: true,
			LastSeenRevisionID: revisionID, Confidence: 0.95, Freshness: 1.0, TrustScore: 0.95,
			Metadata: "{}", ValidFromRevisionID: 0, // legacy mode: update in place, don't close+reopen on duplicate
		})
		if err == nil {
			counts.edges++
		}
		assertion, _ := json.Marshal(map[string]any{
			"injected": fact.To,
		})
		_, _ = g.AddEdgeEvidence(edgeKey, validate.EvidenceInput{
			TargetKind: "edge", SourceKind: "file", FilePath: filePath,
			ExtractorID: "chronicle-scan", ExtractorVersion: "1.0",
			Confidence: 0.95, RevisionID: revisionID,
			AssertionKind: "constructor_injection", Assertion: string(assertion),
		})
		counts.evidence++

	case "decorator":
		// Decorator — evidence for decorator on class/method
		if fact.Decorator == "" {
			return counts, nil
		}
		assertion, _ := json.Marshal(map[string]any{
			"decorator_name": fact.Decorator,
			"target_name":    fact.From,
		})

		// Add evidence to the node (not edge)
		nodeKey := typedNodeKeyFromFile(domainKey, filePath, fact.FromType)
		_, _ = g.AddNodeEvidence(nodeKey, validate.EvidenceInput{
			TargetKind: "node", SourceKind: "file", FilePath: filePath,
			ExtractorID: "chronicle-scan", ExtractorVersion: "1.0",
			Confidence: 0.90, RevisionID: revisionID,
			AssertionKind: "decorator", Assertion: string(assertion),
		})
		counts.evidence++

	case "produces":
		// Produces to topic/queue.
		// transport=local means in-process EventEmitter calls — no broker node, no graph edge.
		if strings.ToLower(fact.Transport) == "local" {
			return counts, nil
		}
		fromNodeKey := typedNodeKeyFromFile(domainKey, filePath, fact.FromType)
		fromID := g.ensureNodeID(domainKey, revisionID, fromNodeKey, inferNameFromPath(filePath), filePath)
		g.registerNodeAlias(fromID, inferNameFromPath(filePath), "file_stem")

		toNodeKey := g.resolveTopicKey(domainKey, fact.To, revisionID)
		toID := g.ensureNodeID(domainKey, revisionID, toNodeKey, fact.To, "")

		edgeKey := fromNodeKey + "->" + toNodeKey + ":PUBLISHES_TOPIC"
		_, err := g.store.UpsertEdge(store.EdgeRow{
			EdgeKey: edgeKey, FromNodeKey: fromNodeKey, ToNodeKey: toNodeKey,
			FromNodeID: fromID, ToNodeID: toID,
			EdgeType: "PUBLISHES_TOPIC", DerivationKind: "hard", Active: true,
			LastSeenRevisionID: revisionID, Confidence: 0.95, Freshness: 1.0, TrustScore: 0.95,
			Metadata: "{}", ValidFromRevisionID: 0, // legacy mode: update in place, don't close+reopen on duplicate
		})
		if err == nil {
			counts.edges++
		}

		assertion, _ := json.Marshal(map[string]any{
			"substring": fact.To,
		})
		_, _ = g.AddEdgeEvidence(edgeKey, validate.EvidenceInput{
			TargetKind: "edge", SourceKind: "file", FilePath: filePath,
			ExtractorID: "chronicle-scan", ExtractorVersion: "1.0",
			Confidence: 0.95, RevisionID: revisionID,
			AssertionKind: "text_contains", Assertion: string(assertion),
		})
		counts.evidence++

	case "consumes":
		// Consumes from topic/queue.
		// transport=local means in-process EventEmitter calls — no broker node, no graph edge.
		if strings.ToLower(fact.Transport) == "local" {
			return counts, nil
		}
		fromNodeKey := typedNodeKeyFromFile(domainKey, filePath, fact.FromType)
		fromID := g.ensureNodeID(domainKey, revisionID, fromNodeKey, inferNameFromPath(filePath), filePath)
		g.registerNodeAlias(fromID, inferNameFromPath(filePath), "file_stem")

		toNodeKey := g.resolveTopicKey(domainKey, fact.To, revisionID)
		toID := g.ensureNodeID(domainKey, revisionID, toNodeKey, fact.To, "")

		edgeKey := fromNodeKey + "->" + toNodeKey + ":CONSUMES_TOPIC"
		_, err := g.store.UpsertEdge(store.EdgeRow{
			EdgeKey: edgeKey, FromNodeKey: fromNodeKey, ToNodeKey: toNodeKey,
			FromNodeID:          fromID,
			ToNodeID:            toID,
			EdgeType: "CONSUMES_TOPIC", DerivationKind: "hard", Active: true,
			LastSeenRevisionID: revisionID, Confidence: 0.95, Freshness: 1.0, TrustScore: 0.95,
			Metadata: "{}", ValidFromRevisionID: 0, // legacy mode: update in place, don't close+reopen on duplicate
		})
		if err == nil {
			counts.edges++
		}

		assertion, _ := json.Marshal(map[string]any{
			"substring": fact.To,
		})
		_, _ = g.AddEdgeEvidence(edgeKey, validate.EvidenceInput{
			TargetKind: "edge", SourceKind: "file", FilePath: filePath,
			ExtractorID: "chronicle-scan", ExtractorVersion: "1.0",
			Confidence: 0.95, RevisionID: revisionID,
			AssertionKind: "text_contains", Assertion: string(assertion),
		})
		counts.evidence++

	case "endpoint":
		// Endpoint — contract node + evidence
		fromNodeKey := typedNodeKeyFromFile(domainKey, filePath, fact.FromType)
		fromID := g.ensureNodeID(domainKey, revisionID, fromNodeKey, inferNameFromPath(filePath), filePath)
		g.registerNodeAlias(fromID, inferNameFromPath(filePath), "file_stem")

		route := fact.Target
		if route == "" {
			route = fact.To
		}
		// Normalize the controller base path (fact.From).
		// When the LLM emits the class name ("TomController") instead of the route
		// prefix ("tom"), we strip the "Controller"/"Gateway" suffix and lowercase to
		// recover the conventional NestJS base path. This prevents phantom nodes like
		// "GET /TomController/status" when the canonical path is "GET /tom/status".
		basePath := normalizeControllerBase(fact.From)

		// Use joinRoutePath only when route is relative (no leading slash).
		// If route already starts with "/", it is already absolute — use it directly
		// (the basePath / class-name is not a meaningful prefix here).
		var path string
		if strings.HasPrefix(route, "/") {
			path = route
		} else {
			path = joinRoutePath(basePath, route)
		}
		toNodeKey, endpointName := normalizeEndpointKey(domainKey, fact.Method, path)
		toID := g.ensureNodeID(domainKey, revisionID, toNodeKey, endpointName, "")

		edgeKey := fromNodeKey + "->" + toNodeKey + ":EXPOSES_ENDPOINT"
		_, err := g.store.UpsertEdge(store.EdgeRow{
			EdgeKey: edgeKey, FromNodeKey: fromNodeKey, ToNodeKey: toNodeKey,
			FromNodeID:          fromID,
			ToNodeID:            toID,
			EdgeType: "EXPOSES_ENDPOINT", DerivationKind: "hard", Active: true,
			LastSeenRevisionID: revisionID, Confidence: 0.95, Freshness: 1.0, TrustScore: 0.95,
			Metadata: "{}", ValidFromRevisionID: 0, // legacy mode: update in place, don't close+reopen on duplicate
		})
		if err == nil {
			counts.edges++
		}

		assertion, _ := json.Marshal(map[string]any{
			"substring": path,
		})
		_, _ = g.AddEdgeEvidence(edgeKey, validate.EvidenceInput{
			TargetKind: "edge", SourceKind: "file", FilePath: filePath,
			ExtractorID: "chronicle-scan", ExtractorVersion: "1.0",
			Confidence: 0.90, RevisionID: revisionID,
			AssertionKind: "text_contains", Assertion: string(assertion),
		})
		counts.evidence++

	case "model":
		// Data model — node + USES_MODEL edge from source file
		nodeKey := "data:model:" + domainKey + ":" + strings.ToLower(fact.To)
		modelID := g.ensureNodeID(domainKey, revisionID, nodeKey, fact.To, "")

		assertion, _ := json.Marshal(map[string]any{
			"model": fact.To,
		})
		_, _ = g.AddNodeEvidence(nodeKey, validate.EvidenceInput{
			TargetKind: "node", SourceKind: "file", FilePath: filePath,
			ExtractorID: "chronicle-scan", ExtractorVersion: "1.0",
			Confidence: 0.95, RevisionID: revisionID,
			AssertionKind: "prisma_model", Assertion: string(assertion),
		})
		counts.nodes++
		counts.evidence++

		// Create USES_MODEL edge if source is a service/provider (not schema file)
		if !strings.HasSuffix(filePath, ".prisma") {
			fromNodeKey := typedNodeKeyFromFile(domainKey, filePath, fact.FromType)
			fromID := g.ensureNodeID(domainKey, revisionID, fromNodeKey, inferNameFromPath(filePath), filePath)
			edgeKey := fromNodeKey + "->" + nodeKey + ":USES_MODEL"
			_, err := g.store.UpsertEdge(store.EdgeRow{
				EdgeKey: edgeKey, FromNodeID: fromID, ToNodeID: modelID,
				FromNodeKey: fromNodeKey, ToNodeKey: nodeKey,
				EdgeType: "USES_MODEL", DerivationKind: "hard", Active: true,
				LastSeenRevisionID: revisionID, Confidence: 0.90, Freshness: 1.0, TrustScore: 0.90,
				Metadata: "{}", ValidFromRevisionID: 0, // legacy mode: update in place, don't close+reopen on duplicate
			})
			if err == nil {
				counts.edges++
			}
		}

	case "enum":
		// Enum/type defined in schema — data:enum node
		nodeKey := "data:enum:" + domainKey + ":" + strings.ToLower(fact.To)
		g.ensureNodeID(domainKey, revisionID, nodeKey, fact.To, "")
		assertion, _ := json.Marshal(map[string]any{"enum": fact.To})
		_, _ = g.AddNodeEvidence(nodeKey, validate.EvidenceInput{
			TargetKind: "node", SourceKind: "file", FilePath: filePath,
			ExtractorID: "chronicle-scan", ExtractorVersion: "1.0",
			Confidence: 0.95, RevisionID: revisionID,
			AssertionKind: "schema_enum", Assertion: string(assertion),
		})
		counts.nodes++
		counts.evidence++

	case "model_relation":
		// Relationship between models, or model→enum field reference
		if fact.From == "" || fact.To == "" {
			return counts, nil
		}
		fromNodeKey := "data:model:" + domainKey + ":" + strings.ToLower(fact.From)
		// Determine target type: check if To is an enum (by fact.ToType or existing node)
		var toNodeKey string
		if fact.ToType == "enum" {
			toNodeKey = "data:enum:" + domainKey + ":" + strings.ToLower(fact.To)
		} else {
			// Check if an enum node already exists with this name
			enumKey := "data:enum:" + domainKey + ":" + strings.ToLower(fact.To)
			if _, err := g.store.GetNodeIDByKey(enumKey); err == nil {
				toNodeKey = enumKey
			} else {
				toNodeKey = "data:model:" + domainKey + ":" + strings.ToLower(fact.To)
			}
		}
		fromID := g.ensureNodeID(domainKey, revisionID, fromNodeKey, fact.From, "")
		toID := g.ensureNodeID(domainKey, revisionID, toNodeKey, fact.To, "")
		edgeKey := fromNodeKey + "->" + toNodeKey + ":REFERENCES_MODEL"
		_, err := g.store.UpsertEdge(store.EdgeRow{
			EdgeKey: edgeKey, FromNodeID: fromID, ToNodeID: toID,
			FromNodeKey: fromNodeKey, ToNodeKey: toNodeKey,
			EdgeType: "REFERENCES_MODEL", DerivationKind: "hard", Active: true,
			LastSeenRevisionID: revisionID, Confidence: 0.95, Freshness: 1.0, TrustScore: 0.95,
			Metadata: "{}", ValidFromRevisionID: 0, // legacy mode: update in place, don't close+reopen on duplicate
		})
		if err == nil {
			counts.edges++
		}

	case "flow":
		// Business flow / use case — creates flow node + edges to triggers and requirements
		if fact.FlowName == "" {
			return counts, nil
		}
		// Key from trigger (deterministic) — not from flow_name (agent-dependent)
		// "POST /arena/attack" → "post__arena_attack", "battle-results" → "battle-results"
		triggerForKey := fact.Trigger
		if triggerForKey == "" {
			triggerForKey = fact.FlowName
		}
		flowKeyBase := strings.ToLower(strings.ReplaceAll(strings.ReplaceAll(triggerForKey, " ", "_"), "/", "_"))
		flowKey := "flow:use_case:" + domainKey + ":" + flowKeyBase
		g.ensureNode(domainKey, revisionID, flowKey, fact.FlowName, filePath)
		counts.nodes++

		// Evidence: the orchestrating method exists in the file
		// Use text_contains for the method name or flow name
		methodName := fact.Method
		if methodName == "" {
			methodName = fact.FlowName
		}
		assertion, _ := json.Marshal(map[string]any{
			"substring": methodName,
		})
		_, _ = g.AddNodeEvidence(flowKey, validate.EvidenceInput{
			TargetKind: "node", SourceKind: "file", FilePath: filePath,
			ExtractorID: "chronicle-scan", ExtractorVersion: "1.0",
			Confidence: 0.85, RevisionID: revisionID,
			AssertionKind: "text_contains", Assertion: string(assertion),
		})
		counts.evidence++

		// Trigger → TRIGGERS_FLOW edge
		if fact.Trigger != "" {
			// Parse trigger: "GET /arena/attack" → method="GET", path="/arena/attack"
			triggerMethod, triggerPath := "GET", fact.Trigger
			if parts := strings.SplitN(fact.Trigger, " ", 2); len(parts) == 2 {
				triggerMethod = parts[0]
				triggerPath = parts[1]
			}
			triggerKey, triggerName := normalizeEndpointKey(domainKey, triggerMethod, triggerPath)
			triggerID := g.ensureNodeID(domainKey, revisionID, triggerKey, triggerName, "")
			flowID, _ := g.store.GetNodeIDByKey(flowKey)
			edgeKey := triggerKey + "->" + flowKey + ":TRIGGERS_FLOW"
			_, err := g.store.UpsertEdge(store.EdgeRow{
				EdgeKey: edgeKey, FromNodeID: triggerID, ToNodeID: flowID,
				FromNodeKey: triggerKey, ToNodeKey: flowKey,
				EdgeType: "TRIGGERS_FLOW", DerivationKind: "hard", Active: true,
				LastSeenRevisionID: revisionID, Confidence: 0.85, Freshness: 1.0, TrustScore: 0.85,
				Metadata: "{}", ValidFromRevisionID: 0, // legacy mode: update in place, don't close+reopen on duplicate
			})
			if err == nil {
				counts.edges++
			}
		}

		// Requirements → REQUIRES edges
		flowID, _ := g.store.GetNodeIDByKey(flowKey)
		for _, req := range fact.Requires {
			reqKey := "code:provider:" + domainKey + ":" + strings.ToLower(req)
			reqID := g.ensureNodeID(domainKey, revisionID, reqKey, req, "")
			edgeKey := flowKey + "->" + reqKey + ":REQUIRES"
			_, err := g.store.UpsertEdge(store.EdgeRow{
				EdgeKey: edgeKey, FromNodeID: flowID, ToNodeID: reqID,
				FromNodeKey: flowKey, ToNodeKey: reqKey,
				EdgeType: "REQUIRES", DerivationKind: "hard", Active: true,
				LastSeenRevisionID: revisionID, Confidence: 0.85, Freshness: 1.0, TrustScore: 0.85,
				Metadata: "{}", ValidFromRevisionID: 0, // legacy mode: update in place, don't close+reopen on duplicate
			})
			if err == nil {
				counts.edges++
			}

			// Evidence: the required service is referenced in this file (use original case)
			reqAssertion, _ := json.Marshal(map[string]any{
				"substring": req,
			})
			_, _ = g.AddEdgeEvidence(edgeKey, validate.EvidenceInput{
				TargetKind: "edge", SourceKind: "file", FilePath: filePath,
				ExtractorID: "chronicle-scan", ExtractorVersion: "1.0",
				Confidence: 0.80, RevisionID: revisionID,
				AssertionKind: "text_contains", Assertion: string(reqAssertion),
			})
			counts.evidence++
		}
	case "delegates":
		// Record delegation — creates obligation for delegated file if not already scanned
		if fact.To == "" {
			return counts, nil
		}
		delegatedFile := fact.To
		if !strings.HasPrefix(delegatedFile, "/") {
			delegatedFile = filepath.Join(filepath.Dir(filePath), fact.To)
		}
		// Create obligation if not already satisfied
		if revisionID > 0 {
			g.store.CreateObligation(revisionID, domainKey, "scan_file", delegatedFile, "delegation from "+filePath)
		}
		return counts, &UnresolvedRef{
			FromFile: filePath,
			Kind:     "delegation",
			Target:   delegatedFile,
			Reason:   fmt.Sprintf("delegates to %s via %s — ensure this file is also scanned", delegatedFile, fact.Method),
		}

	case "parent":
		// Parent container relationship — creates CONTAINS edge from parent to child
		if fact.To == "" {
			return counts, nil
		}

		// Child = the file being scanned
		childNodeKey := typedNodeKeyFromFile(domainKey, filePath, fact.FromType)
		childID := g.ensureNodeID(domainKey, revisionID, childNodeKey, inferNameFromPath(filePath), filePath)

		// Find parent node by name in the domain
		parentNode := g.findNodeByNameInDomain(domainKey, fact.To)
		if parentNode == nil {
			// Parent not found — store a discovery warning
			g.store.AddDiscovery(store.Discovery{
				DomainKey:    domainKey,
				Category:     "missing_edge",
				Title:        "Parent node not found: " + fact.To,
				Description:  fmt.Sprintf("File %s declares parent %q but no node with that name exists in domain %s", filePath, fact.To, domainKey),
				Source:       "system",
				Confidence:   0.5,
				RelatedNodes: fmt.Sprintf("[%q]", childNodeKey),
			})
			return counts, &UnresolvedRef{
				FromFile: filePath,
				Kind:     "parent",
				Target:   fact.To,
				Reason:   fmt.Sprintf("parent node %q not found in domain %s", fact.To, domainKey),
			}
		}

		// Self-reference guard
		if parentNode.NodeKey == childNodeKey {
			g.store.AddDiscovery(store.Discovery{
				DomainKey:   domainKey,
				Category:    "correction",
				Title:       "Self-referencing parent: " + fact.To,
				Description: fmt.Sprintf("File %s declares itself as its own parent — skipping", filePath),
				Source:      "system",
				Confidence:  0.3,
			})
			return counts, nil
		}

		// Create CONTAINS edge: parent → child
		edgeKey := parentNode.NodeKey + "->" + childNodeKey + ":CONTAINS"
		confidence := fact.Confidence
		if confidence == 0 {
			confidence = 0.85
		}
		_, err := g.store.UpsertEdge(store.EdgeRow{
			EdgeKey: edgeKey, FromNodeID: parentNode.NodeID, ToNodeID: childID,
			FromNodeKey: parentNode.NodeKey, ToNodeKey: childNodeKey,
			EdgeType: "CONTAINS", DerivationKind: "hard", Active: true,
			LastSeenRevisionID: revisionID, Confidence: confidence, Freshness: 1.0, TrustScore: confidence,
			Metadata: "{}", ValidFromRevisionID: 0,
		})
		if err == nil {
			counts.edges++
		}

		// Store evidence with reason
		reason := fact.Reason
		if reason == "" {
			reason = "parent container"
		}
		assertion, _ := json.Marshal(map[string]any{
			"parent":  fact.To,
			"reason":  reason,
		})
		_, _ = g.AddEdgeEvidence(edgeKey, validate.EvidenceInput{
			TargetKind: "edge", SourceKind: "file", FilePath: filePath,
			ExtractorID: "chronicle-scan", ExtractorVersion: "1.0",
			Confidence: confidence, RevisionID: revisionID,
			AssertionKind: "parent_declaration", Assertion: string(assertion),
		})
		counts.evidence++

	case "declares_service":
		// Create a service-layer node for a deployable service (from package.json, Dockerfile, etc.)
		if fact.To == "" {
			return counts, nil
		}
		svcName := normalizePackageName(fact.To)
		nodeKey := "service:service:" + domainKey + ":" + svcName
		id, err := g.UpsertNode(validate.NodeInput{
			NodeKey:   nodeKey,
			Layer:     "service",
			NodeType:  "service",
			DomainKey: domainKey,
			Name:      fact.To,
			FilePath:  filePath,
		}, revisionID)
		if err == nil && id > 0 {
			counts.nodes++
		}
	}

	return counts, nil
}

// registerNodeAlias stores a name as an alias for a node.
func (g *Graph) registerNodeAlias(nodeID int64, alias, aliasKind string) {
	if alias == "" || nodeID == 0 {
		return
	}
	g.store.AddAlias(store.AliasRow{
		NodeID:     nodeID,
		Alias:      alias,
		AliasKind:  aliasKind,
		Confidence: 0.9,
	})
}

// resolveTopicKey finds an existing topic node by normalized name, or creates a canonical key.
// Handles common variations: "battle-results" vs "battle-result", "order.created" vs "order-created".
func (g *Graph) resolveTopicKey(domainKey, topicName string, revisionID int64) string {
	normalized := strings.ToLower(strings.ReplaceAll(topicName, " ", "-"))
	canonicalKey := "contract:topic:" + domainKey + ":" + normalized

	// Check if exact key exists
	if n, _ := g.store.GetNodeByKey(canonicalKey); n != nil {
		return canonicalKey
	}

	// Try only plural/singular variation (the most common agent inconsistency).
	// DO NOT try dot↔dash — order.created and order-created are different topics.
	// DO NOT try removing version suffixes — order.created.v2 is a different topic.
	variants := []string{normalized}
	if strings.HasSuffix(normalized, "s") {
		variants = append(variants, strings.TrimSuffix(normalized, "s"))
	} else {
		variants = append(variants, normalized+"s")
	}

	for _, v := range variants {
		key := "contract:topic:" + domainKey + ":" + v
		if n, _ := g.store.GetNodeByKey(key); n != nil {
			return key // Found existing topic — reuse it
		}
	}

	return canonicalKey // No match — create new
}

// findNodeByNameInDomain searches for an existing node by name within a domain.
// Tries exact match first, then normalized match (strip dots/dashes/underscores, case-insensitive).
// e.g. "arena.module" matches "ArenaModule", "tom-api" matches "TomApi".
func (g *Graph) findNodeByNameInDomain(domainKey, name string) *store.NodeRow {
	nodes, _ := g.store.ListNodes(store.NodeFilter{Domain: domainKey})
	lower := strings.ToLower(name)

	// Exact case-insensitive match first
	for _, n := range nodes {
		if strings.ToLower(n.Name) == lower {
			return &n
		}
	}

	// Normalized match: strip separators, compare
	normalize := func(s string) string {
		s = strings.ToLower(s)
		s = strings.ReplaceAll(s, ".", "")
		s = strings.ReplaceAll(s, "-", "")
		s = strings.ReplaceAll(s, "_", "")
		s = strings.ReplaceAll(s, " ", "")
		return s
	}
	normName := normalize(name)
	for _, n := range nodes {
		if normalize(n.Name) == normName {
			return &n
		}
	}

	return nil
}

// ensureNodeID ensures a node exists and returns its ID.
// If the exact key doesn't exist but a stem-based node with the same name does,
// merges by updating the old node's key to the new path-based key.
func (g *Graph) ensureNodeID(domainKey string, revisionID int64, nodeKey, name, filePath string) int64 {
	id, err := g.store.GetNodeIDByKey(nodeKey)
	if err == nil {
		// Patch FilePath if the existing node has none and we have one
		if filePath != "" {
			if existing, e2 := g.store.GetNodeByKey(nodeKey); e2 == nil && existing.FilePath == "" {
				existing.FilePath = filePath
				existing.LastSeenRevisionID = revisionID
				g.store.UpsertNode(*existing)
			}
		}
		return id
	}

	// Key doesn't exist — check if a stem-based node with the same name exists.
	// This happens when an import fact created a node from a class name (stem key)
	// before the actual file was scanned (path key).
	if filePath != "" && name != "" {
		existing := g.findNodeByNameInDomain(domainKey, name)
		if existing != nil && existing.Layer == "code" && existing.FilePath == "" {
			// Found a stem-based node — merge by updating its key and file path
			g.store.Exec("UPDATE graph_nodes SET node_key = ?, file_path = ?, last_seen_revision_id = ? WHERE node_id = ?",
				nodeKey, filePath, revisionID, existing.NodeID)
			// Also update edge keys that reference the old node key
			g.store.Exec("UPDATE graph_edges SET from_node_key = ? WHERE from_node_id = ?", nodeKey, existing.NodeID)
			g.store.Exec("UPDATE graph_edges SET to_node_key = ? WHERE to_node_id = ?", nodeKey, existing.NodeID)
			g.store.Exec("UPDATE graph_edges SET edge_key = from_node_key || '->' || to_node_key || ':' || edge_type WHERE from_node_id = ? OR to_node_id = ?", existing.NodeID, existing.NodeID)
			return existing.NodeID
		}
	}

	g.ensureNode(domainKey, revisionID, nodeKey, name, filePath)
	id, _ = g.store.GetNodeIDByKey(nodeKey)
	if id > 0 && filePath != "" {
		g.registerClassNameAliases(id, inferNameFromPath(filePath))
	}
	return id
}

func (g *Graph) ensureNode(domainKey string, revisionID int64, nodeKey, name, filePath string) {
	_, err := g.store.GetNodeIDByKey(nodeKey)
	if err != nil {
		parts := strings.SplitN(nodeKey, ":", 4)
		layer := "code"
		nodeType := "module"
		if len(parts) >= 2 {
			layer = parts[0]
			nodeType = parts[1]
		}
		supportKind := ""
		if nodeType == "provider" {
			supportKind = classifyProviderRole(name)
		}
		g.store.UpsertNode(store.NodeRow{
			NodeKey:            nodeKey,
			Layer:              layer,
			NodeType:           nodeType,
			DomainKey:          domainKey,
			Name:               name,
			FilePath:           filePath,
			Status:             "active",
			LastSeenRevisionID: revisionID,
			Confidence:         0.9,
			Freshness:          1.0,
			TrustScore:         0.9,
			Metadata:           "{}",
			SupportKind:        supportKind,
		})
	}
}

// classifyProviderRole returns a support kind string for NestJS support providers
// identified by well-known name suffixes (guard, interceptor, pipe, middleware,
// filter, task, cron, processor). Gateway is intentionally excluded — it is an
// API entrypoint, not a support provider.
func classifyProviderRole(name string) string {
	lower := strings.ToLower(name)
	switch {
	case strings.HasSuffix(lower, "guard"):
		return "guard"
	case strings.HasSuffix(lower, "interceptor"):
		return "interceptor"
	case strings.HasSuffix(lower, "pipe"):
		return "pipe"
	case strings.HasSuffix(lower, "middleware"):
		return "middleware"
	case strings.HasSuffix(lower, "filter"):
		return "filter"
	case strings.HasSuffix(lower, "task"):
		return "task"
	case strings.HasSuffix(lower, "cron"):
		return "cron"
	case strings.HasSuffix(lower, "processor"):
		return "processor"
	default:
		return ""
	}
}

// fixModuleEdges reclassifies edges for nodes that the agent explicitly typed as "module".
// Only touches nodes that are already code:module (agent set from_type="module").
// For those nodes, INJECTS/DEPENDS_ON → CONTAINS.
// Does NOT promote provider → module — that's the agent's job.
func (g *Graph) fixModuleEdges(domainKey string) {
	active := true
	allEdges, err := g.store.ListEdges(store.EdgeFilter{Active: &active})
	if err != nil {
		return
	}

	// Collect known module node keys
	moduleNodes := make(map[string]bool)
	nodes, _ := g.store.ListNodes(store.NodeFilter{Domain: domainKey})
	for _, n := range nodes {
		if n.NodeType == "module" {
			moduleNodes[n.NodeKey] = true
		}
	}

	// Reclassify edges FROM module nodes: INJECTS/DEPENDS_ON → CONTAINS
	for _, e := range allEdges {
		if !moduleNodes[e.FromNodeKey] {
			continue
		}
		if e.EdgeType == "INJECTS" || e.EdgeType == "DEPENDS_ON" {
			newKey := e.FromNodeKey + "->" + e.ToNodeKey + ":CONTAINS"
			g.store.Exec("UPDATE graph_edges SET edge_type='CONTAINS', edge_key=? WHERE edge_id=?", newKey, e.EdgeID)
		}
	}
}

// detectContainsConflicts finds nodes with multiple CONTAINS parents.
// If a node has CONTAINS edges from both parent and provides facts,
// keep the highest-confidence edge and deactivate others.
// Log conflicts as discoveries for diagnostics.
func (g *Graph) detectContainsConflicts(domainKey string, revisionID int64) {
	active := true
	allEdges, _ := g.store.ListEdges(store.EdgeFilter{Active: &active})

	// Group CONTAINS edges by child (to_node_id)
	childParents := map[int64][]store.EdgeRow{}
	for _, e := range allEdges {
		if e.EdgeType != "CONTAINS" {
			continue
		}
		childParents[e.ToNodeID] = append(childParents[e.ToNodeID], e)
	}

	for childID, parents := range childParents {
		if len(parents) <= 1 {
			continue
		}

		// Group by parent node — same parent = duplicate (merge), different parent = conflict
		byParent := map[int64][]store.EdgeRow{}
		for _, p := range parents {
			byParent[p.FromNodeID] = append(byParent[p.FromNodeID], p)
		}

		// Deduplicate: same parent, multiple edges → keep highest confidence, remove rest
		for _, edges := range byParent {
			if len(edges) <= 1 {
				continue
			}
			best := edges[0]
			for _, e := range edges[1:] {
				if e.Confidence > best.Confidence {
					best = e
				}
			}
			for _, e := range edges {
				if e.EdgeID == best.EdgeID {
					continue
				}
				// Silent dedup — same relationship, just remove the duplicate
				g.store.Exec("DELETE FROM graph_edges WHERE edge_id = ?", e.EdgeID)
			}
		}

		// True conflict: different parents for the same child
		if len(byParent) <= 1 {
			continue
		}

		// Keep the edge with highest confidence across all parents
		best := parents[0]
		for _, p := range parents[1:] {
			if p.Confidence > best.Confidence {
				best = p
			}
		}

		for _, p := range parents {
			if p.FromNodeID == best.FromNodeID {
				continue // keep all edges from the winning parent
			}
			g.store.Exec("UPDATE graph_edges SET active = 0 WHERE edge_id = ?", p.EdgeID)

			child, _ := g.store.GetNodeByID(childID)
			childName := ""
			if child != nil {
				childName = child.Name
			}
			g.store.AddDiscovery(store.Discovery{
				DomainKey:   domainKey,
				Category:    "contains_conflict",
				Title:       fmt.Sprintf("Multiple CONTAINS parents for %s", childName),
				Description: fmt.Sprintf("node %q has conflicting parents; kept %s, deactivated %s", childName, best.EdgeKey, p.EdgeKey),
				Source:      "system",
				Confidence:  0.9,
			})
		}
	}
}

// detectContainsCycles finds and breaks cycles in the CONTAINS hierarchy.
// Uses DFS to detect back-edges; deactivates the cycle-closing edge and
// logs a discovery for diagnostics.
func (g *Graph) detectContainsCycles(domainKey string, revisionID int64) {
	active := true
	allEdges, _ := g.store.ListEdges(store.EdgeFilter{Active: &active})

	// Build adjacency: parent → children
	children := map[int64][]int64{}
	edgeByPair := map[[2]int64]store.EdgeRow{}
	for _, e := range allEdges {
		if e.EdgeType != "CONTAINS" {
			continue
		}
		children[e.FromNodeID] = append(children[e.FromNodeID], e.ToNodeID)
		edgeByPair[[2]int64{e.FromNodeID, e.ToNodeID}] = e
	}

	// DFS cycle detection
	visited := map[int64]bool{}
	inStack := map[int64]bool{}

	var dfs func(node int64)
	dfs = func(node int64) {
		visited[node] = true
		inStack[node] = true
		for _, child := range children[node] {
			if !visited[child] {
				dfs(child)
			} else if inStack[child] {
				// Cycle — deactivate this edge
				edge := edgeByPair[[2]int64{node, child}]
				g.store.Exec("UPDATE graph_edges SET active = 0 WHERE edge_id = ?", edge.EdgeID)

				n1, _ := g.store.GetNodeByID(node)
				n2, _ := g.store.GetNodeByID(child)
				name1, name2 := "", ""
				if n1 != nil {
					name1 = n1.Name
				}
				if n2 != nil {
					name2 = n2.Name
				}
				g.store.AddDiscovery(store.Discovery{
					DomainKey:   domainKey,
					Category:    "contains_cycle",
					Title:       fmt.Sprintf("CONTAINS cycle: %s -> %s", name1, name2),
					Description: fmt.Sprintf("CONTAINS cycle detected: %s -> %s; edge deactivated", name1, name2),
					Source:      "system",
					Confidence:  0.9,
				})
			}
		}
		inStack[node] = false
	}

	for nodeID := range children {
		if !visited[nodeID] {
			dfs(nodeID)
		}
	}
}

// normalizeFacts rewrites common LLM field name variations to canonical names.
// LLMs (especially cheap models) invent field names like "from", "module", "source",
// "specifier", "url", "path", "items", "queue", "event_type" instead of the canonical
// "to", "target", "symbols". This normalizes them before JSON unmarshal.
func normalizeFacts(raw string) string {
	raw = strings.TrimSpace(raw)

	// Handle structured enrichment output: {"candidate_decisions":[], "additional_facts":[]}
	if strings.HasPrefix(raw, "{") {
		var structured map[string]any
		if err := json.Unmarshal([]byte(raw), &structured); err == nil {
			var combined []map[string]any
			if decisions, ok := structured["candidate_decisions"].([]any); ok {
				for _, d := range decisions {
					if dm, ok := d.(map[string]any); ok {
						combined = append(combined, dm)
					}
				}
			}
			if additional, ok := structured["additional_facts"].([]any); ok {
				for _, a := range additional {
					if am, ok := a.(map[string]any); ok {
						combined = append(combined, am)
					}
				}
			}
			if len(combined) > 0 || (structured["candidate_decisions"] != nil && structured["additional_facts"] != nil) {
				out, _ := json.Marshal(combined)
				raw = string(out)
			}
		}
	}

	var arr []map[string]any
	if err := json.Unmarshal([]byte(raw), &arr); err != nil {
		return raw // not an array of objects, return as-is
	}

	for i, fact := range arr {
		// Unwrap candidate wrapper format: {"candidate_id":"...","decision":"accept","fact":{...}}
		// Agents sometimes submit the wrapper as a top-level fact instead of the inner fact.
		if _, hasCandidateID := fact["candidate_id"]; hasCandidateID {
			if inner, ok := fact["fact"].(map[string]any); ok {
				decision, _ := fact["decision"].(string)
				if decision == "reject" {
					arr[i] = nil
					continue
				}
				// Preserve candidate_id on the inner fact for voting
				inner["candidate_id"] = fact["candidate_id"]
				arr[i] = inner
				fact = inner
			}
		}

		kind, _ := fact["kind"].(string)

		// Normalize "to" field — the primary target/path
		if _, ok := fact["to"]; !ok {
			for _, alias := range []string{"from", "module", "source", "specifier"} {
				if v, ok := fact[alias]; ok {
					// "from" for import means the module path (import X from './path')
					if s, ok := v.(string); ok && (kind == "import" || kind == "dependency") {
						fact["to"] = s
						delete(fact, alias)
						break
					}
				}
			}
		}

		// Normalize "target" field — URL or endpoint path
		if _, ok := fact["target"]; !ok {
			for _, alias := range []string{"url", "path", "endpoint"} {
				if v, ok := fact[alias]; ok {
					fact["target"] = v
					delete(fact, alias)
					break
				}
			}
		}

		// Normalize "to" for produces/consumes — topic/event/queue name
		if kind == "produces" || kind == "consumes" {
			if _, ok := fact["to"]; !ok {
				for _, alias := range []string{"queue", "event_type", "topic", "event", "channel"} {
					if v, ok := fact[alias]; ok {
						fact["to"] = v
						delete(fact, alias)
						break
					}
				}
			}
		}

		// Normalize "symbols" field — imported names
		if _, ok := fact["symbols"]; !ok {
			for _, alias := range []string{"items", "imported", "names"} {
				if v, ok := fact[alias]; ok {
					fact["symbols"] = v
					delete(fact, alias)
					break
				}
			}
			// Single imported name in "target" field (haiku puts symbol name there)
			if kind == "import" {
				if _, ok := fact["symbols"]; !ok {
					if t, ok := fact["target"].(string); ok && !strings.HasPrefix(t, "/") && !strings.HasPrefix(t, "http") {
						fact["symbols"] = []string{t}
						delete(fact, "target")
					}
				}
			}
		}

		// Normalize "method" for produces/consumes (haiku puts it in various fields)
		if kind == "produces" || kind == "consumes" {
			if _, ok := fact["method"]; !ok {
				for _, alias := range []string{"handler_method", "handler", "method_name"} {
					if v, ok := fact[alias]; ok {
						fact["method"] = v
						delete(fact, alias)
						break
					}
				}
			}
		}

		// Normalize kind aliases
		switch kind {
		case "dependency_injection", "constructor_injection", "di":
			// Normalize to "injects" — constructor DI
			fact["kind"] = "injects"
			if _, ok := fact["to"]; !ok {
				for _, alias := range []string{"injects", "injected_type", "target", "dependency"} {
					if v, ok := fact[alias]; ok {
						fact["to"] = v
						delete(fact, alias)
						break
					}
				}
			}
		case "dependency":
			// Normalize "dependency" to "import" — agents confuse npm deps with imports
			fact["kind"] = "import"
		case "depends_on":
			// Normalize "depends_on" to "injects" — agents mean constructor injection
			fact["kind"] = "injects"
			if _, ok := fact["to"]; !ok {
				if v, ok := fact["from"]; ok {
					// depends_on often has "from" as the source, but we need "to" for the target
					fact["to"] = v
					delete(fact, "from")
				}
			}
		case "service_call", "method_call", "member_call_service":
			// Normalize to "calls_service"
			fact["kind"] = "calls_service"
			if _, ok := fact["to"]; !ok {
				if v, ok := fact["target"]; ok {
					fact["to"] = v
					delete(fact, "target")
				}
			}
		case "model_access", "uses_modal", "model_usage":
			// Normalize to "uses_model"
			fact["kind"] = "uses_model"
			if _, ok := fact["to"]; !ok {
				if v, ok := fact["model"]; ok {
					fact["to"] = v
					delete(fact, "model")
				}
			}
		case "module_controller", "module_provider", "class_definition", "class",
			"uses_interceptor", "component", "uses_component", "hook_export",
			"hoc_export", "config_export", "hybrid_auth", "guard_strategy",
			"note", "config":
			// Drop non-standard kinds that don't map to edges
			arr[i] = nil
			continue
		}

		arr[i] = fact
	}

	// Remove nil entries
	var clean []map[string]any
	for _, f := range arr {
		if f != nil {
			clean = append(clean, f)
		}
	}

	out, err := json.Marshal(clean)
	if err != nil {
		return raw
	}
	return string(out)
}

// mergeVotedExtractions combines multiple LLM vote extractions into one per file.
// AST and single extractions pass through unchanged.
// LLM votes are grouped by file+vote_group, facts are voted on.
func mergeVotedExtractions(extractions []store.ExtractionRow) []store.ExtractionRow {
	// Separate vote rows from non-vote rows
	var result []store.ExtractionRow
	voteGroups := map[string][]store.ExtractionRow{} // key: file_path+vote_group

	for _, ext := range extractions {
		if ext.ExtractionRole == "llm_vote" && ext.VoteGroup != "" {
			key := ext.FilePath + "|" + ext.VoteGroup
			voteGroups[key] = append(voteGroups[key], ext)
		} else {
			result = append(result, ext)
		}
	}

	// Merge each vote group
	for _, votes := range voteGroups {
		if len(votes) == 0 {
			continue
		}
		merged := mergeVoteGroup(votes)
		result = append(result, merged)
	}

	return result
}

// isHardFact returns true for fact kinds that come from deterministic code evidence
// (syntax, decorators, schema definitions). These should be accepted even with 1 vote.
// Inferred/linked facts require majority.
func isHardFact(fact map[string]any) bool {
	kind, _ := fact["kind"].(string)
	switch kind {
	case "model", "enum", "model_relation", "endpoint", "provides", "injects",
		"declares_service", "produces", "consumes", "decorator", "import", "parent":
		return true
	}
	// Candidate decisions that accepted a hard fact
	if decision, ok := fact["decision"].(string); ok && decision == "accept" {
		if inner, ok := fact["fact"].(map[string]any); ok {
			return isHardFact(inner)
		}
	}
	return false
}

// voteConfidence returns the confidence for a voted fact.
// Hard facts: accept even 1 vote (confidence scales with vote count).
// Inferred facts: require 2/3 majority, otherwise dropped (returns 0).
func voteConfidence(count, totalVotes int, sampleFact map[string]any) float64 {
	if isHardFact(sampleFact) {
		// Union strategy for hard facts — accept any vote
		switch {
		case count >= totalVotes:
			return 0.95
		case count*2 >= totalVotes:
			return 0.80
		default:
			return 0.60 // Single vote — lower confidence but still accepted
		}
	}
	// Majority strategy for inferred facts
	switch {
	case count >= totalVotes:
		return 0.85
	case count*3 >= totalVotes*2:
		return 0.70
	default:
		return 0 // Drop — not enough votes
	}
}

func mergeVoteGroup(votes []store.ExtractionRow) store.ExtractionRow {
	totalVotes := len(votes)

	// Primary: vote by candidate_id (stable AST anchor)
	// Secondary: vote by canonical fact key (fallback for free-form facts)
	candidateVotes := map[string][]map[string]any{} // candidate_id → list of fact objects
	freeformVotes := map[string][]map[string]any{}   // canonical_key → list of fact objects

	for _, v := range votes {
		normalized := normalizeFacts(v.FactsJSON)
		var facts []map[string]any
		if err := json.Unmarshal([]byte(normalized), &facts); err != nil {
			continue
		}
		seenCandidate := map[string]bool{}
		seenFreeform := map[string]bool{}
		for _, f := range facts {
			cid, _ := f["candidate_id"].(string)
			if cid != "" {
				// Candidate-based: group by candidate_id
				if !seenCandidate[cid] {
					seenCandidate[cid] = true
					candidateVotes[cid] = append(candidateVotes[cid], f)
				}
			} else {
				// Free-form: group by canonical key
				key := canonicalFactKey(f)
				if !seenFreeform[key] {
					seenFreeform[key] = true
					freeformVotes[key] = append(freeformVotes[key], f)
				}
			}
		}
	}

	// Build merged facts
	var mergedFacts []json.RawMessage

	// Merge candidate votes — union strategy:
	// Hard facts (model, endpoint, declares_service, etc.): accept even 1 vote
	// Inferred facts (calls_service, etc.): require 2/3 majority
	for _, factList := range candidateVotes {
		count := len(factList)
		confidence := voteConfidence(count, totalVotes, factList[0])
		if confidence == 0 {
			continue
		}
		// Use first vote's fact, inject confidence
		fact := factList[0]
		// Extract the inner fact if it has a "fact" wrapper from candidate response
		if inner, ok := fact["fact"].(map[string]any); ok {
			inner["confidence"] = confidence
			b, _ := json.Marshal(inner)
			mergedFacts = append(mergedFacts, b)
		} else {
			fact["confidence"] = confidence
			delete(fact, "candidate_id")
			delete(fact, "decision")
			b, _ := json.Marshal(fact)
			mergedFacts = append(mergedFacts, b)
		}
	}

	// Merge free-form votes (fallback) — same union strategy
	for _, factList := range freeformVotes {
		count := len(factList)
		confidence := voteConfidence(count, totalVotes, factList[0])
		if confidence == 0 {
			continue
		}
		fact := factList[0]
		fact["confidence"] = confidence
		b, _ := json.Marshal(fact)
		mergedFacts = append(mergedFacts, b)
	}

	mergedJSON := "[]"
	if len(mergedFacts) > 0 {
		b, _ := json.Marshal(mergedFacts)
		mergedJSON = string(b)
	}

	merged := votes[0]
	merged.ExtractionRole = "llm_merged"
	merged.FactsJSON = mergedJSON
	merged.Status = "extracted"
	return merged
}

// canonicalFactKey produces a stable key for voting comparison.
// Normalizes kind prefixes and field positions to handle LLM format variance.
func canonicalFactKey(f map[string]any) string {
	kind, _ := f["kind"].(string)
	to, _ := f["to"].(string)
	from, _ := f["from"].(string)
	target, _ := f["target"].(string)
	method, _ := f["method"].(string)

	// Strip code:/contract:/data: prefixes from kind
	kind = strings.ToLower(kind)
	for _, prefix := range []string{"code:", "contract:", "data:", "service:"} {
		kind = strings.TrimPrefix(kind, prefix)
	}

	// Normalize field positions: some agents put module path in "target" instead of "to"
	to = strings.ToLower(to)
	target = strings.ToLower(target)
	method = strings.ToLower(method)
	from = strings.ToLower(from)

	// For imports: the module path might be in to, target, or from
	if kind == "import" {
		// Merge to+target — whichever has a path-like value
		if to == "" && target != "" {
			to = target
			target = ""
		}
		// Method might hold the symbol name, not a real method
		if method != "" && !strings.Contains(method, ".") && to == "" {
			// method is probably the module path
			to = method
			method = ""
		}
	}

	// For injects: to is the type name, might be in target
	if kind == "injects" || kind == "inject" {
		kind = "injects"
		if to == "" && target != "" {
			to = target
			target = ""
		}
	}

	// Strip node key prefixes from values (code:service:domain:Name → name)
	to = stripNodeKeyPrefix(to)
	from = stripNodeKeyPrefix(from)
	target = stripNodeKeyPrefix(target)

	to = strings.TrimRight(to, "/")
	target = strings.TrimRight(target, "/")

	return kind + "|" + to + "|" + target + "|" + method
}

func stripNodeKeyPrefix(s string) string {
	// "code:service:tom-and-jerry:ArenaService" → "arenaservice"
	parts := strings.Split(s, ":")
	if len(parts) > 1 {
		return strings.ToLower(parts[len(parts)-1])
	}
	return s
}

func buildImportAssertion(fact Fact) map[string]any {
	a := map[string]any{"module": fact.To}
	if len(fact.Symbols) > 0 {
		a["symbols"] = fact.Symbols
	}
	return a
}

func inferNodeKeyFromFile(domain, filePath string) string {
	name := inferNameFromPath(filePath)
	return "code:module:" + domain + ":" + strings.ToLower(name)
}

func inferNodeKeyFromImport(domain, module string) string {
	name := inferNameFromImport(module)
	return "code:module:" + domain + ":" + strings.ToLower(name)
}

// typedNodeKey creates a node key using the type provided by Claude in the fact.
// Falls back to "provider" if empty (most files are services/helpers).
func typedNodeKey(domain, name, nodeType string) string {
	if nodeType == "" {
		nodeType = "provider"
	}
	return "code:" + nodeType + ":" + domain + ":" + strings.ToLower(name)
}

func typedNodeKeyFromFile(domain, filePath, nodeType string) string {
	pathKey := inferPathKey(filePath)
	return typedNodeKey(domain, pathKey, nodeType)
}

func typedNodeKeyFromImport(domain, module, nodeType string) string {
	name := inferNameFromImport(module)
	return typedNodeKey(domain, name, nodeType)
}

func inferImportEdgeType(fromKey, toKey string) string {
	fromType := extractNodeType(fromKey)
	toType := extractNodeType(toKey)

	// Module importing controllers/providers → CONTAINS
	if fromType == "module" && (toType == "controller" || toType == "provider") {
		return "CONTAINS"
	}
	// Controller/provider importing a provider → INJECTS (DI)
	if (fromType == "controller" || fromType == "provider") && toType == "provider" {
		return "INJECTS"
	}
	return "DEPENDS_ON"
}

func extractNodeType(nodeKey string) string {
	// "code:controller:domain:name" → "controller"
	parts := strings.SplitN(nodeKey, ":", 4)
	if len(parts) >= 2 {
		return parts[1]
	}
	return ""
}

// inferPathKey derives a canonical key segment from a file path.
// Uses the relative path without extension to avoid collisions.
// e.g. "arena-api/src/arena/arena.service.ts" → "arena-api/src/arena/arena.service"
func inferPathKey(filePath string) string {
	p := filePath
	for _, ext := range []string{".ts", ".tsx", ".js", ".jsx", ".go", ".py", ".json", ".yaml", ".yml", ".prisma", ".graphql", ".proto"} {
		p = strings.TrimSuffix(p, ext)
	}
	p = strings.ReplaceAll(p, "\\", "/")
	p = strings.TrimPrefix(p, "./")
	p = strings.TrimPrefix(p, "/")
	return p
}

func inferNameFromPath(filePath string) string {
	// Get filename without extension
	parts := strings.Split(filePath, "/")
	filename := parts[len(parts)-1]
	// Remove extensions
	for _, ext := range []string{".ts", ".tsx", ".js", ".go", ".json", ".yaml", ".yml", ".prisma"} {
		filename = strings.TrimSuffix(filename, ext)
	}
	return filename
}

func inferNameFromImport(module string) string {
	// "@scope/package" → "package"
	// "./local" → "local"
	// "bare-module" → "bare-module"
	if strings.HasPrefix(module, "@") {
		parts := strings.SplitN(module, "/", 3)
		if len(parts) >= 2 {
			return parts[1]
		}
	}
	if strings.HasPrefix(module, "./") || strings.HasPrefix(module, "../") {
		parts := strings.Split(module, "/")
		return parts[len(parts)-1]
	}
	return module
}

// normalizePascalCase converts PascalCase class names to dot-case file names
// to match the file-based naming convention used by inferNameFromPath.
// e.g. "ArenaService" → "arena.service", "BattleResultProducer" → "battle-result.producer"
func normalizePascalCase(name string) string {
	if name == "" || !strings.ContainsAny(name[:1], "ABCDEFGHIJKLMNOPQRSTUVWXYZ") {
		return name // already lowercase or dot-case
	}
	var parts []string
	current := ""
	for i, r := range name {
		if i > 0 && r >= 'A' && r <= 'Z' {
			if current != "" {
				parts = append(parts, strings.ToLower(current))
			}
			current = string(r)
		} else {
			current += string(r)
		}
	}
	if current != "" {
		parts = append(parts, strings.ToLower(current))
	}
	return strings.Join(parts, ".")
}

// flattenName strips dots, dashes, underscores, slashes and lowercases for fuzzy comparison.
func flattenName(name string) string {
	r := strings.NewReplacer(".", "", "-", "", "_", "", "/", "", "\\", "", " ", "")
	return strings.ToLower(r.Replace(name))
}

type scanFileIndex struct {
	byClassName map[string]string // PascalCase class name → file_path
	byStem      map[string]string // normalized file stem → file_path
	byPath      map[string]string // file_path → from_type hint
}

func buildScanFileIndex(extractions []store.ExtractionRow) scanFileIndex {
	idx := scanFileIndex{
		byClassName: make(map[string]string),
		byStem:      make(map[string]string),
		byPath:      make(map[string]string),
	}
	for _, ext := range extractions {
		if ext.FilePath == "" {
			continue
		}
		idx.byPath[ext.FilePath] = ext.FromType
		stem := inferNameFromPath(ext.FilePath)
		idx.byStem[strings.ToLower(stem)] = ext.FilePath
		idx.byStem[flattenName(stem)] = ext.FilePath
		if pc := pascalCaseFromFileStem(stem); pc != "" {
			idx.byClassName[pc] = ext.FilePath
		}
	}
	return idx
}

func pascalCaseFromFileStem(stem string) string {
	if stem == "" {
		return ""
	}
	var parts []string
	for _, seg := range strings.Split(stem, ".") {
		for _, sub := range strings.Split(seg, "-") {
			if sub == "" {
				continue
			}
			parts = append(parts, strings.ToUpper(sub[:1])+sub[1:])
		}
	}
	return strings.Join(parts, "")
}

func isPlausibleClassName(name string) bool {
	if name == "" {
		return false
	}
	lower := strings.ToLower(name)
	if strings.Contains(name, "<") || strings.Contains(name, ">") {
		return false
	}
	if strings.HasPrefix(lower, "i.") || strings.HasPrefix(lower, "i_") {
		return false
	}
	if strings.Contains(lower, "service.scope") || strings.Contains(lower, "hub.context") {
		return false
	}
	return true
}

func inferNodeTypeFromClassName(className string) string {
	if strings.HasSuffix(className, "Controller") || strings.HasSuffix(className, "Gateway") {
		return "controller"
	}
	return "provider"
}

func (g *Graph) registerClassNameAliases(nodeID int64, fileStem string) {
	if pc := pascalCaseFromFileStem(fileStem); pc != "" {
		g.registerNodeAlias(nodeID, pc, "class_name")
	}
}

func (g *Graph) resolveClassNameTarget(domainKey string, revisionID int64, className string) (string, int64) {
	if !isPlausibleClassName(className) {
		return "", 0
	}

	// Scanned file index — covers zero-fact files referenced by module provides/injects.
	if fp := g.scanFileIndex.byClassName[className]; fp != "" {
		nodeType := g.scanFileIndex.byPath[fp]
		if nodeType == "" {
			nodeType = inferNodeTypeFromClassName(className)
		}
		nodeKey := typedNodeKeyFromFile(domainKey, fp, nodeType)
		id := g.ensureNodeID(domainKey, revisionID, nodeKey, inferNameFromPath(fp), fp)
		return nodeKey, id
	}

	dotCase := normalizePascalCase(className)
	flat := flattenName(className)

	if aliasNodes, err := g.store.FindCodeNodesByAlias(domainKey, className, ""); err == nil && len(aliasNodes) == 1 {
		return aliasNodes[0].NodeKey, aliasNodes[0].NodeID
	}
	if aliasNodes, err := g.store.FindCodeNodesByAlias(domainKey, dotCase, ""); err == nil && len(aliasNodes) == 1 {
		return aliasNodes[0].NodeKey, aliasNodes[0].NodeID
	}

	// Path-keyed nodes (canonical): match by flattened name on existing nodes.
	for _, nodeType := range []string{"provider", "controller", "module"} {
		nodes, _ := g.store.ListNodes(store.NodeFilter{Domain: domainKey, NodeType: nodeType})
		for _, n := range nodes {
			if flattenName(n.Name) == flat {
				return n.NodeKey, n.NodeID
			}
			keyParts := strings.Split(n.NodeKey, ":")
			if len(keyParts) > 0 {
				lastPart := keyParts[len(keyParts)-1]
				if flattenName(filepath.Base(lastPart)) == flat || flattenName(lastPart) == flat {
					return n.NodeKey, n.NodeID
				}
			}
		}
	}

	// Legacy stem-keyed nodes (pre-path merge).
	prefix := "code:provider:" + domainKey + ":"
	candidates := []string{
		strings.ToLower(dotCase),
		strings.ReplaceAll(strings.ToLower(dotCase), ".", "-"),
		strings.ToLower(className),
		flat,
	}
	for _, c := range candidates {
		key := prefix + c
		if id, err := g.store.GetNodeIDByKey(key); err == nil {
			return key, id
		}
		key = "code:controller:" + domainKey + ":" + c
		if id, err := g.store.GetNodeIDByKey(key); err == nil {
			return key, id
		}
	}

	// Stem from scan index without exact PascalCase hit.
	if fp := g.scanFileIndex.byStem[strings.ToLower(dotCase)]; fp != "" {
		nodeType := g.scanFileIndex.byPath[fp]
		if nodeType == "" {
			nodeType = inferNodeTypeFromClassName(className)
		}
		nodeKey := typedNodeKeyFromFile(domainKey, fp, nodeType)
		id := g.ensureNodeID(domainKey, revisionID, nodeKey, inferNameFromPath(fp), fp)
		return nodeKey, id
	}

	// Last resort: create stem-based provider (may merge when file is processed later).
	nodeType := inferNodeTypeFromClassName(className)
	key := typedNodeKey(domainKey, strings.ToLower(dotCase), nodeType)
	id := g.ensureNodeID(domainKey, revisionID, key, dotCase, "")
	return key, id
}

// resolveInjectTarget is kept for callers outside resolveOneFact.
func (g *Graph) resolveInjectTarget(domainKey string, revisionID int64, className string, _ string) (string, int64) {
	return g.resolveClassNameTarget(domainKey, revisionID, className)
}

// SaveFileExtraction stores extraction results from a scan agent.
func (g *Graph) SaveFileExtraction(revisionID int64, domain, filePath, status, fromType, factsJSON, errorMsg string) (int64, error) {
	return g.store.SaveExtraction(revisionID, domain, filePath, status, fromType, factsJSON, errorMsg)
}

func (g *Graph) SaveFileExtractionWithVote(revisionID int64, domain, filePath, status, fromType, factsJSON, errorMsg, role, voteGroup string, voteIndex int) (int64, error) {
	return g.store.SaveExtractionWithVote(revisionID, domain, filePath, status, fromType, factsJSON, errorMsg, role, voteGroup, voteIndex)
}

func fileNodeTypeFromFacts(facts []Fact) string {
	for _, f := range facts {
		if f.FromType != "" {
			return f.FromType
		}
	}
	// Default to "provider" — most files are services/helpers, not modules.
	// Modules (DI containers) are rare and agents should explicitly set from_type="module".
	return "provider"
}

func fileTypeOrderFromFacts(facts []Fact) int {
	for _, f := range facts {
		if f.Kind == "declares_service" || f.Kind == "model" || f.Kind == "enum" {
			return 0 // boundary + schema files first — creates service/model nodes before references
		}
	}
	for _, f := range facts {
		if f.Kind == "endpoint" {
			return 1 // files that expose endpoints second
		}
	}
	for _, f := range facts {
		if f.Kind == "produces" || f.Kind == "consumes" {
			return 2 // async handlers third
		}
	}
	return 3 // everything else last
}

func extractPathFromURL(url string) string {
	// "http://tom-api:3001/tom/status" → "/tom/status"
	// "http://api:3000/orders/stats?from=2024" → "/orders/stats"
	u := url
	for _, prefix := range []string{"https://", "http://"} {
		u = strings.TrimPrefix(u, prefix)
	}
	if idx := strings.Index(u, "/"); idx >= 0 {
		path := u[idx:]
		// Strip query string
		if qIdx := strings.Index(path, "?"); qIdx >= 0 {
			path = path[:qIdx]
		}
		// Strip fragment
		if fIdx := strings.Index(path, "#"); fIdx >= 0 {
			path = path[:fIdx]
		}
		// Strip trailing slash for consistent matching
		path = strings.TrimRight(path, "/")
		return path
	}
	return ""
}

// joinRoutePath joins a controller prefix and a route segment into a normalized path.
// Strips surrounding slashes from each part, then reassembles with a leading slash.
func joinRoutePath(prefix, route string) string {
	prefix = strings.Trim(prefix, "/")
	route = strings.Trim(route, "/")
	switch {
	case prefix == "" && route == "":
		return "/"
	case prefix == "":
		return "/" + route
	case route == "":
		return "/" + prefix
	default:
		return "/" + prefix + "/" + route
	}
}

// normalizeEndpointKey builds a consistent endpoint node key from method + path.
// Format: "contract:endpoint:{domain}:{method}:{path}" where path preserves slashes.
// This MUST be used by all code that creates or looks up endpoint nodes.
func normalizeEndpointKey(domainKey, method, path string) (nodeKey string, displayName string) {
	method = strings.ToUpper(method)
	if method == "" {
		method = "GET"
	}
	// Normalize path: ensure leading slash, strip trailing slash
	path = strings.TrimRight(path, "/")
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	displayName = method + " " + path
	nodeKey = "contract:endpoint:" + domainKey + ":" + strings.ToLower(method+":"+path)
	return
}

func inferExternalSystemName(url string) string {
	// "http://notifications:3005/push" → "notifications"
	// "https://hooks.example.com/battles" → "hooks.example.com"
	u := url
	for _, prefix := range []string{"https://", "http://"} {
		u = strings.TrimPrefix(u, prefix)
	}
	// Take host part
	if idx := strings.Index(u, "/"); idx > 0 {
		u = u[:idx]
	}
	// Remove port
	if idx := strings.Index(u, ":"); idx > 0 {
		u = u[:idx]
	}
	return u
}

// buildDependencyAssertion picks the right assertion kind based on file extension.
// Claude often reports "dependency" facts from .ts files (should be import_specifier)
// or .yml files (should be yaml_key_exists). We derive the correct kind from the file type.
func buildDependencyAssertion(filePath string, fact Fact) (assertionKind string, assertionJSON string) {
	ext := strings.ToLower(filePath)
	switch {
	case strings.HasSuffix(ext, ".json"):
		a, _ := json.Marshal(map[string]any{
			"package":  fact.To,
			"sections": []string{"dependencies", "devDependencies", "peerDependencies"},
		})
		return "manifest_dependency", string(a)
	case strings.HasSuffix(ext, ".ts") || strings.HasSuffix(ext, ".tsx") ||
		strings.HasSuffix(ext, ".js") || strings.HasSuffix(ext, ".jsx"):
		a, _ := json.Marshal(map[string]any{
			"module":  fact.To,
			"symbols": fact.Symbols,
		})
		return "import_specifier", string(a)
	case strings.HasSuffix(ext, ".yml") || strings.HasSuffix(ext, ".yaml"):
		a, _ := json.Marshal(map[string]any{
			"path": fact.To,
		})
		return "yaml_key_exists", string(a)
	case strings.HasSuffix(ext, ".mod"):
		a, _ := json.Marshal(map[string]any{
			"module": fact.To,
		})
		return "go_module_require", string(a)
	case strings.HasSuffix(ext, ".prisma"):
		a, _ := json.Marshal(map[string]any{
			"model": fact.To,
		})
		return "prisma_model", string(a)
	default:
		// Fallback: text search
		a, _ := json.Marshal(map[string]any{
			"substring": fact.To,
		})
		return "text_contains", string(a)
	}
}

func normalizePackageName(pkg string) string {
	// "@scope/name" → "name"
	// "some-package" → "some-package"
	name := inferNameFromImport(pkg)
	return strings.ToLower(strings.ReplaceAll(name, "/", "-"))
}

// normalizeControllerBase converts a controller identifier to a URL route prefix.
// When the LLM emits a class name ("TomController") instead of the NestJS @Controller
// argument ("tom"), we strip the "Controller"/"Gateway" suffix and lowercase to recover
// the conventional base path. If the value is already lowercase (e.g. "tom", "arena"),
// it is returned unchanged.
//   "tom"             → "tom"
//   "TomController"   → "tom"
//   "ArenaGateway"    → "arena"
//   "api/v1"          → "api/v1"
func normalizeControllerBase(base string) string {
	if base == "" {
		return ""
	}
	for _, suffix := range []string{"Controller", "Gateway"} {
		if strings.HasSuffix(base, suffix) {
			return strings.ToLower(strings.TrimSuffix(base, suffix))
		}
	}
	return base
}

// isLocalCodeNode returns true when the given name resolves to an established
// in-domain code-layer node — one that was pre-created or came from a scanned
// file (identified by a PascalCase display name matching the target). Stem nodes
// synthesized by resolveClassNameTarget as fallback placeholders use a dot-case
// name (e.g. "order.service") and are intentionally excluded. This is used by
// the calls_service handler (R4) to suppress CALLS_SERVICE edges for DI
// references that are really local intra-service method calls.
func isLocalCodeNode(g *Graph, domainKey, name string) bool {
	if name == "" {
		return false
	}
	flat := flattenName(name)
	for _, nodeType := range []string{"provider", "controller", "module"} {
		nodes, _ := g.store.ListNodes(store.NodeFilter{Domain: domainKey, NodeType: nodeType})
		for _, n := range nodes {
			if n.Layer != "code" {
				continue
			}
			// Exclude synthesized placeholder nodes: their Name is the dot-case
			// derivation (lower-case, contains dots). Real nodes have PascalCase or
			// path-keyed names (contain slashes). If the name starts with a lowercase
			// letter AND contains only dots/dashes/lowercase, it's a placeholder.
			if len(n.Name) > 0 && n.Name[0] >= 'a' && n.Name[0] <= 'z' && !strings.Contains(n.Name, "/") {
				continue
			}
			// Match by display name (case-insensitive flatten)
			if flattenName(n.Name) == flat {
				return true
			}
		}
	}
	return false
}
