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
	Kind       string   `json:"kind"`                  // import, call, decorator, http_call, dependency, model, enum, model_relation, endpoint, produces, consumes, flow, declares
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
	Confidence float64  `json:"confidence,omitempty"`  // agent confidence [0,1]
	Note       string   `json:"note,omitempty"`        // agent uncertainty/note
	// Flow-specific fields
	FlowName   string   `json:"flow_name,omitempty"`   // use case name (e.g. "Tom attacks Jerry")
	Trigger    string   `json:"trigger,omitempty"`      // what triggers this flow (endpoint, event, cron)
	Steps      []string `json:"steps,omitempty"`        // ordered list of steps in the flow
	Requires   []string `json:"requires,omitempty"`     // services/models this flow depends on
}

// ResolveExtractionsResult is returned by ResolveExtractions.
type ResolveExtractionsResult struct {
	FilesProcessed  int            `json:"files_processed"`
	NodesCreated    int            `json:"nodes_created"`
	EdgesCreated    int            `json:"edges_created"`
	EvidenceCreated int            `json:"evidence_created"`
	Unresolved      []UnresolvedRef `json:"unresolved,omitempty"`
}

// UnresolvedRef is a reference that couldn't be automatically resolved.
type UnresolvedRef struct {
	FromFile string `json:"from_file"`
	Kind     string `json:"kind"`
	Target   string `json:"target"`
	Reason   string `json:"reason"`
}

// ResolveExtractions takes all pending extractions and builds the graph.
// Creates nodes, edges, and evidence from the collected facts.
func (g *Graph) ResolveExtractions(domainKey string, revisionID int64) (*ResolveExtractionsResult, error) {
	rawExtractions, err := g.store.ListUnresolvedExtractions(revisionID, domainKey)
	if err != nil {
		return nil, fmt.Errorf("ResolveExtractions: %w", err)
	}

	// Merge voted extractions before resolving
	extractions := mergeVotedExtractions(rawExtractions)

	result := &ResolveExtractionsResult{
		FilesProcessed: len(extractions),
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

	// Mark all extractions as resolved
	if err := g.store.MarkExtractionsResolved(revisionID, domainKey); err != nil {
		return nil, fmt.Errorf("ResolveExtractions mark resolved: %w", err)
	}

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
		toNodeKey := typedNodeKeyFromImport(domainKey, fact.To, fact.ToType)

		// Ensure nodes exist
		fromID := g.ensureNodeID(domainKey, revisionID, fromNodeKey, inferNameFromPath(filePath), filePath)
		toID := g.ensureNodeID(domainKey, revisionID, toNodeKey, inferNameFromImport(fact.To), "")

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
			ValidFromRevisionID: revisionID,
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
			Metadata: "{}", ValidFromRevisionID: revisionID,
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
		// External HTTP call — create external system node + edge + evidence
		fromNodeKey := typedNodeKeyFromFile(domainKey, filePath, fact.FromType)
		fromID := g.ensureNodeID(domainKey, revisionID, fromNodeKey, inferNameFromPath(filePath), filePath)

		targetName := inferExternalSystemName(fact.Target)
		toNodeKey := "service:external_system:" + domainKey + ":" + strings.ToLower(targetName)
		toID := g.ensureNodeID(domainKey, revisionID, toNodeKey, targetName, "")

		edgeKey := fromNodeKey + "->" + toNodeKey + ":CALLS_SERVICE"
		_, err := g.store.UpsertEdge(store.EdgeRow{
			EdgeKey: edgeKey, FromNodeKey: fromNodeKey, ToNodeKey: toNodeKey,
			FromNodeID:          fromID,
			ToNodeID:            toID,
			EdgeType: "CALLS_SERVICE", DerivationKind: "linked", Active: true,
			LastSeenRevisionID: revisionID, Confidence: 0.85, Freshness: 1.0, TrustScore: 0.85,
			Metadata: "{}", ValidFromRevisionID: revisionID,
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
			method := strings.ToUpper(fact.Method)
			if method == "" {
				method = "GET"
			}
			endpointName := method + " " + endpointPath
			epNodeKey := "contract:endpoint:" + domainKey + ":" + strings.ToLower(strings.ReplaceAll(strings.ReplaceAll(endpointName, " ", "_"), "/", "_"))
			// Only create edge if the endpoint node already exists (was exposed by another file)
			if epID, err2 := g.store.GetNodeIDByKey(epNodeKey); err2 == nil {
				callEpEdgeKey := fromNodeKey + "->" + epNodeKey + ":CALLS_ENDPOINT"
				_, err3 := g.store.UpsertEdge(store.EdgeRow{
					EdgeKey: callEpEdgeKey, FromNodeID: fromID, ToNodeID: epID,
					FromNodeKey: fromNodeKey, ToNodeKey: epNodeKey,
					EdgeType: "CALLS_ENDPOINT", DerivationKind: "linked", Active: true,
					LastSeenRevisionID: revisionID, Confidence: 0.80, Freshness: 1.0, TrustScore: 0.80,
					Metadata: "{}", ValidFromRevisionID: revisionID,
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
				Metadata: "{}", ValidFromRevisionID: revisionID,
			})
			if err == nil {
				counts.edges++
			}
		}
		// If not a known model, fact is kept as raw evidence but doesn't create an edge

	case "calls_service":
		// Explicit service call — creates CALLS_SERVICE edge
		if fact.To == "" {
			return counts, nil
		}
		fromNodeKey := typedNodeKeyFromFile(domainKey, filePath, fact.FromType)
		fromID := g.ensureNodeID(domainKey, revisionID, fromNodeKey, inferNameFromPath(filePath), filePath)

		toName := fact.To
		toNodeKey := "code:provider:" + domainKey + ":" + strings.ToLower(toName)
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
			Metadata: "{}", ValidFromRevisionID: revisionID,
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
			Metadata: "{}", ValidFromRevisionID: revisionID,
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

		method := strings.ToUpper(fact.Method)
		if method == "" {
			method = "GET"
		}
		epName := method + " " + fact.Target
		epKey := "contract:endpoint:" + domainKey + ":" + strings.ToLower(strings.ReplaceAll(epName, " ", "_"))
		epID := g.ensureNodeID(domainKey, revisionID, epKey, epName, "")

		edgeKey := fromNodeKey + "->" + epKey + ":CALLS_ENDPOINT"
		_, err := g.store.UpsertEdge(store.EdgeRow{
			EdgeKey: edgeKey, FromNodeID: fromID, ToNodeID: epID,
			FromNodeKey: fromNodeKey, ToNodeKey: epKey,
			EdgeType: "CALLS_ENDPOINT", DerivationKind: "hard", Active: true,
			LastSeenRevisionID: revisionID, Confidence: 0.85, Freshness: 1.0, TrustScore: 0.85,
			Metadata: "{}", ValidFromRevisionID: revisionID,
		})
		if err == nil {
			counts.edges++
		}

	case "injects":
		// Constructor DI — creates INJECTS edge (stronger than import)
		if fact.To == "" {
			return counts, nil
		}
		fromNodeKey := typedNodeKeyFromFile(domainKey, filePath, fact.FromType)
		fromID := g.ensureNodeID(domainKey, revisionID, fromNodeKey, inferNameFromPath(filePath), filePath)
		toNodeKey := "code:provider:" + domainKey + ":" + strings.ToLower(fact.To)
		toID := g.ensureNodeID(domainKey, revisionID, toNodeKey, fact.To, "")

		edgeKey := fromNodeKey + "->" + toNodeKey + ":INJECTS"
		_, err := g.store.UpsertEdge(store.EdgeRow{
			EdgeKey: edgeKey, FromNodeID: fromID, ToNodeID: toID,
			FromNodeKey: fromNodeKey, ToNodeKey: toNodeKey,
			EdgeType: "INJECTS", DerivationKind: "hard", Active: true,
			LastSeenRevisionID: revisionID, Confidence: 0.95, Freshness: 1.0, TrustScore: 0.95,
			Metadata: "{}", ValidFromRevisionID: revisionID,
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
		// Produces to topic/queue
		fromNodeKey := typedNodeKeyFromFile(domainKey, filePath, fact.FromType)
		fromID := g.ensureNodeID(domainKey, revisionID, fromNodeKey, inferNameFromPath(filePath), filePath)

		toNodeKey := "contract:topic:" + domainKey + ":" + strings.ToLower(strings.ReplaceAll(fact.To, " ", "-"))
		toID := g.ensureNodeID(domainKey, revisionID, toNodeKey, fact.To, "")

		edgeKey := fromNodeKey + "->" + toNodeKey + ":PUBLISHES_TOPIC"
		_, err := g.store.UpsertEdge(store.EdgeRow{
			EdgeKey: edgeKey, FromNodeKey: fromNodeKey, ToNodeKey: toNodeKey,
			FromNodeID: fromID, ToNodeID: toID,
			EdgeType: "PUBLISHES_TOPIC", DerivationKind: "hard", Active: true,
			LastSeenRevisionID: revisionID, Confidence: 0.95, Freshness: 1.0, TrustScore: 0.95,
			Metadata: "{}", ValidFromRevisionID: revisionID,
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
		// Consumes from topic/queue
		fromNodeKey := typedNodeKeyFromFile(domainKey, filePath, fact.FromType)
		fromID := g.ensureNodeID(domainKey, revisionID, fromNodeKey, inferNameFromPath(filePath), filePath)

		toNodeKey := "contract:topic:" + domainKey + ":" + strings.ToLower(strings.ReplaceAll(fact.To, " ", "-"))
		toID := g.ensureNodeID(domainKey, revisionID, toNodeKey, fact.To, "")

		edgeKey := fromNodeKey + "->" + toNodeKey + ":CONSUMES_TOPIC"
		_, err := g.store.UpsertEdge(store.EdgeRow{
			EdgeKey: edgeKey, FromNodeKey: fromNodeKey, ToNodeKey: toNodeKey,
			FromNodeID:          fromID,
			ToNodeID:            toID,
			EdgeType: "CONSUMES_TOPIC", DerivationKind: "hard", Active: true,
			LastSeenRevisionID: revisionID, Confidence: 0.95, Freshness: 1.0, TrustScore: 0.95,
			Metadata: "{}", ValidFromRevisionID: revisionID,
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

		path := fact.Target
		if path == "" {
			path = fact.To
		}
		// If AST provided a prefix (e.g. from @Controller('prefix')), combine it
		if fact.From != "" && path != "" {
			path = "/" + fact.From + "/" + path
		} else if fact.From != "" {
			path = "/" + fact.From
		}
		endpointName := path
		if fact.Method != "" {
			endpointName = fact.Method + " " + path
		}
		toNodeKey := "contract:endpoint:" + domainKey + ":" + strings.ToLower(strings.ReplaceAll(strings.ReplaceAll(endpointName, " ", "_"), "/", "_"))
		toID := g.ensureNodeID(domainKey, revisionID, toNodeKey, endpointName, "")

		edgeKey := fromNodeKey + "->" + toNodeKey + ":EXPOSES_ENDPOINT"
		_, err := g.store.UpsertEdge(store.EdgeRow{
			EdgeKey: edgeKey, FromNodeKey: fromNodeKey, ToNodeKey: toNodeKey,
			FromNodeID:          fromID,
			ToNodeID:            toID,
			EdgeType: "EXPOSES_ENDPOINT", DerivationKind: "hard", Active: true,
			LastSeenRevisionID: revisionID, Confidence: 0.95, Freshness: 1.0, TrustScore: 0.95,
			Metadata: "{}", ValidFromRevisionID: revisionID,
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
				Metadata: "{}", ValidFromRevisionID: revisionID,
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
		// FK relationship between models — REFERENCES_MODEL edge
		if fact.From == "" || fact.To == "" {
			return counts, nil
		}
		fromNodeKey := "data:model:" + domainKey + ":" + strings.ToLower(fact.From)
		toNodeKey := "data:model:" + domainKey + ":" + strings.ToLower(fact.To)
		fromID := g.ensureNodeID(domainKey, revisionID, fromNodeKey, fact.From, "")
		toID := g.ensureNodeID(domainKey, revisionID, toNodeKey, fact.To, "")
		edgeKey := fromNodeKey + "->" + toNodeKey + ":REFERENCES_MODEL"
		_, err := g.store.UpsertEdge(store.EdgeRow{
			EdgeKey: edgeKey, FromNodeID: fromID, ToNodeID: toID,
			FromNodeKey: fromNodeKey, ToNodeKey: toNodeKey,
			EdgeType: "REFERENCES_MODEL", DerivationKind: "hard", Active: true,
			LastSeenRevisionID: revisionID, Confidence: 0.95, Freshness: 1.0, TrustScore: 0.95,
			Metadata: "{}", ValidFromRevisionID: revisionID,
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
			triggerKey := "contract:endpoint:" + domainKey + ":" + strings.ToLower(strings.ReplaceAll(strings.ReplaceAll(fact.Trigger, " ", "_"), "/", "_"))
			triggerID := g.ensureNodeID(domainKey, revisionID, triggerKey, fact.Trigger, "")
			flowID, _ := g.store.GetNodeIDByKey(flowKey)
			edgeKey := triggerKey + "->" + flowKey + ":TRIGGERS_FLOW"
			_, err := g.store.UpsertEdge(store.EdgeRow{
				EdgeKey: edgeKey, FromNodeID: triggerID, ToNodeID: flowID,
				FromNodeKey: triggerKey, ToNodeKey: flowKey,
				EdgeType: "TRIGGERS_FLOW", DerivationKind: "hard", Active: true,
				LastSeenRevisionID: revisionID, Confidence: 0.85, Freshness: 1.0, TrustScore: 0.85,
				Metadata: "{}", ValidFromRevisionID: revisionID,
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
				Metadata: "{}", ValidFromRevisionID: revisionID,
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

// ensureNodeID ensures a node exists and returns its ID.
func (g *Graph) ensureNodeID(domainKey string, revisionID int64, nodeKey, name, filePath string) int64 {
	id, err := g.store.GetNodeIDByKey(nodeKey)
	if err == nil {
		return id
	}
	g.ensureNode(domainKey, revisionID, nodeKey, name, filePath)
	id, _ = g.store.GetNodeIDByKey(nodeKey)
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
		})
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

	// Merge candidate votes
	for _, factList := range candidateVotes {
		count := len(factList)
		var confidence float64
		switch {
		case count >= totalVotes:
			confidence = 0.85
		case count*3 >= totalVotes*2:
			confidence = 0.70
		default:
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

	// Merge free-form votes (fallback)
	for _, factList := range freeformVotes {
		count := len(factList)
		var confidence float64
		switch {
		case count >= totalVotes:
			confidence = 0.85
		case count*3 >= totalVotes*2:
			confidence = 0.70
		default:
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
	name := inferNameFromPath(filePath)
	return typedNodeKey(domain, name, nodeType)
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
		if f.Kind == "endpoint" {
			return 0 // files that expose endpoints first
		}
	}
	for _, f := range facts {
		if f.Kind == "produces" || f.Kind == "consumes" {
			return 1 // async handlers second
		}
	}
	return 2 // everything else last
}

func extractPathFromURL(url string) string {
	// "http://tom-api:3001/tom/status" → "/tom/status"
	u := url
	for _, prefix := range []string{"https://", "http://"} {
		u = strings.TrimPrefix(u, prefix)
	}
	if idx := strings.Index(u, "/"); idx >= 0 {
		return u[idx:]
	}
	return ""
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
