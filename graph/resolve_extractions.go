package graph

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/alexdx2/chronicle-core/store"
	"github.com/alexdx2/chronicle-core/validate"
)

// Fact represents a single extracted observation from a source file.
type Fact struct {
	Kind       string   `json:"kind"`                  // import, call, decorator, http_call, dependency, model, endpoint, produces, consumes, flow
	FromFile   string   `json:"from_file,omitempty"`   // source file (usually implicit from extraction context)
	From       string   `json:"from,omitempty"`        // source entity name/identifier
	To         string   `json:"to"`                    // target module/service/entity
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
	extractions, err := g.store.ListUnresolvedExtractions(revisionID, domainKey)
	if err != nil {
		return nil, fmt.Errorf("ResolveExtractions: %w", err)
	}

	result := &ResolveExtractionsResult{
		FilesProcessed: len(extractions),
	}

	// Collect all facts across all files
	var allFiles []fileFacts

	for _, ext := range extractions {
		var facts []Fact
		if err := json.Unmarshal([]byte(ext.FactsJSON), &facts); err != nil {
			result.Unresolved = append(result.Unresolved, UnresolvedRef{
				FromFile: ext.FilePath,
				Kind:     "parse_error",
				Target:   "",
				Reason:   "invalid facts JSON: " + err.Error(),
			})
			continue
		}
		allFiles = append(allFiles, fileFacts{filePath: ext.FilePath, facts: facts})
	}

	// Phase 1: Discover all entities mentioned across all files
	// Build a set of known entity names for resolution
	knownEntities := g.collectKnownEntities(allFiles)

	// Phase 2: Create nodes and edges from facts
	for _, ff := range allFiles {
		for _, fact := range ff.facts {
			created, unresolved := g.resolveOneFact(domainKey, revisionID, ff.filePath, fact, knownEntities)
			result.NodesCreated += created.nodes
			result.EdgesCreated += created.edges
			result.EvidenceCreated += created.evidence
			if unresolved != nil {
				result.Unresolved = append(result.Unresolved, *unresolved)
			}
		}
	}

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

		// Try to find or create the edge
		fromNodeKey := inferNodeKeyFromFile(domainKey, filePath)
		toNodeKey := inferNodeKeyFromImport(domainKey, fact.To)

		// Ensure nodes exist
		fromID := g.ensureNodeID(domainKey, revisionID, fromNodeKey, inferNameFromPath(filePath), filePath)
		toID := g.ensureNodeID(domainKey, revisionID, toNodeKey, inferNameFromImport(fact.To), "")

		// Create edge
		edgeKey := fromNodeKey + "->" + toNodeKey + ":DEPENDS_ON"
		_, err := g.store.UpsertEdge(store.EdgeRow{
			EdgeKey:             edgeKey,
			FromNodeID:          fromID,
			ToNodeID:            toID,
			FromNodeKey:         fromNodeKey,
			ToNodeKey:           toNodeKey,
			EdgeType:            "DEPENDS_ON",
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

		// Package manifest dependency
		assertion, _ := json.Marshal(map[string]any{
			"package":  fact.To,
			"sections": []string{"dependencies", "devDependencies"},
		})

		fromNodeKey := inferNodeKeyFromFile(domainKey, filePath)
		toNodeKey := "code:module:" + domainKey + ":" + normalizePackageName(fact.To)

		fromID := g.ensureNodeID(domainKey, revisionID, fromNodeKey, inferNameFromPath(filePath), filePath)
		toID := g.ensureNodeID(domainKey, revisionID, toNodeKey, fact.To, "")

		edgeKey := fromNodeKey + "->" + toNodeKey + ":DEPENDS_ON"
		_, err := g.store.UpsertEdge(store.EdgeRow{
			EdgeKey: edgeKey, FromNodeKey: fromNodeKey, ToNodeKey: toNodeKey,
			FromNodeID:          fromID,
			ToNodeID:            toID,
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
			AssertionKind: "manifest_dependency", Assertion: string(assertion),
		})
		counts.evidence++

	case "http_call":
		// External HTTP call — create external system node + edge + evidence
		fromNodeKey := inferNodeKeyFromFile(domainKey, filePath)
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

	case "call":
		// Method call — evidence for the call expression
		if fact.Method == "" && fact.Object == "" {
			return counts, nil
		}
		fromNodeKey := inferNodeKeyFromFile(domainKey, filePath)
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
		nodeKey := inferNodeKeyFromFile(domainKey, filePath)
		_, _ = g.AddNodeEvidence(nodeKey, validate.EvidenceInput{
			TargetKind: "node", SourceKind: "file", FilePath: filePath,
			ExtractorID: "chronicle-scan", ExtractorVersion: "1.0",
			Confidence: 0.90, RevisionID: revisionID,
			AssertionKind: "decorator", Assertion: string(assertion),
		})
		counts.evidence++

	case "produces":
		// Produces to topic/queue
		fromNodeKey := inferNodeKeyFromFile(domainKey, filePath)
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
		fromNodeKey := inferNodeKeyFromFile(domainKey, filePath)
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
		// HTTP/WS/GraphQL endpoint — contract node + evidence
		fromNodeKey := inferNodeKeyFromFile(domainKey, filePath)
		fromID := g.ensureNodeID(domainKey, revisionID, fromNodeKey, inferNameFromPath(filePath), filePath)

		endpointName := fact.To
		if fact.Method != "" {
			endpointName = fact.Method + " " + fact.To
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
			"substring": fact.To,
		})
		_, _ = g.AddEdgeEvidence(edgeKey, validate.EvidenceInput{
			TargetKind: "edge", SourceKind: "file", FilePath: filePath,
			ExtractorID: "chronicle-scan", ExtractorVersion: "1.0",
			Confidence: 0.90, RevisionID: revisionID,
			AssertionKind: "text_contains", Assertion: string(assertion),
		})
		counts.evidence++

	case "model":
		// Data model — node + evidence
		nodeKey := "data:model:" + domainKey + ":" + strings.ToLower(fact.To)
		g.ensureNode(domainKey, revisionID, nodeKey, fact.To, filePath)

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

	case "flow":
		// Business flow / use case — creates flow node + edges to triggers and requirements
		if fact.FlowName == "" {
			return counts, nil
		}
		flowKey := "flow:use_case:" + domainKey + ":" + strings.ToLower(strings.ReplaceAll(fact.FlowName, " ", "_"))
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

func buildImportAssertion(fact Fact) map[string]any {
	a := map[string]any{"module": fact.To}
	if len(fact.Symbols) > 0 {
		a["symbols"] = fact.Symbols
	}
	return a
}

func inferNodeKeyFromFile(domain, filePath string) string {
	// Convert file path to a node key
	// e.g. "src/orders/order.service.ts" → "code:module:domain:orders"
	name := inferNameFromPath(filePath)
	return "code:module:" + domain + ":" + strings.ToLower(name)
}

func inferNodeKeyFromImport(domain, module string) string {
	name := inferNameFromImport(module)
	return "code:module:" + domain + ":" + strings.ToLower(name)
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
func (g *Graph) SaveFileExtraction(revisionID int64, domain, filePath, status, factsJSON, errorMsg string) (int64, error) {
	return g.store.SaveExtraction(revisionID, domain, filePath, status, factsJSON, errorMsg)
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

func normalizePackageName(pkg string) string {
	// "@scope/name" → "name"
	// "some-package" → "some-package"
	name := inferNameFromImport(pkg)
	return strings.ToLower(strings.ReplaceAll(name, "/", "-"))
}
