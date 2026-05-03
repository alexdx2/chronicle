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
	Kind       string `json:"kind"`                  // import, call, decorator, http_call, dependency, model, endpoint, produces, consumes
	FromFile   string `json:"from_file,omitempty"`   // source file (usually implicit from extraction context)
	From       string `json:"from,omitempty"`        // source entity name/identifier
	To         string `json:"to"`                    // target module/service/entity
	Symbols    []string `json:"symbols,omitempty"`   // imported symbols
	Method     string `json:"method,omitempty"`      // HTTP method or called method name
	Object     string `json:"object,omitempty"`      // callee object
	Decorator  string `json:"decorator,omitempty"`   // decorator name
	Target     string `json:"target,omitempty"`      // URL or target identifier
	Confidence float64 `json:"confidence,omitempty"` // agent confidence [0,1]
	Note       string `json:"note,omitempty"`        // agent uncertainty/note
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
		g.ensureNode(domainKey, revisionID, fromNodeKey, inferNameFromPath(filePath), filePath)
		g.ensureNode(domainKey, revisionID, toNodeKey, inferNameFromImport(fact.To), "")

		// Create edge
		edgeKey := fromNodeKey + "->" + toNodeKey + ":DEPENDS_ON"
		_, err := g.store.UpsertEdge(store.EdgeRow{
			EdgeKey:             edgeKey,
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

		g.ensureNode(domainKey, revisionID, fromNodeKey, inferNameFromPath(filePath), filePath)
		g.ensureNode(domainKey, revisionID, toNodeKey, fact.To, "")

		edgeKey := fromNodeKey + "->" + toNodeKey + ":DEPENDS_ON"
		_, err := g.store.UpsertEdge(store.EdgeRow{
			EdgeKey: edgeKey, FromNodeKey: fromNodeKey, ToNodeKey: toNodeKey,
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
		// External HTTP dependency — may be unresolved
		return counts, &UnresolvedRef{
			FromFile: filePath,
			Kind:     "http_call",
			Target:   fact.Target,
			Reason:   "external HTTP dependency — needs service mapping",
		}

	case "call", "decorator", "model", "endpoint", "produces", "consumes":
		// These create evidence but resolution is more complex
		// For now, store as-is and let Claude handle during review
		return counts, nil
	}

	return counts, nil
}

func (g *Graph) ensureNode(domainKey string, revisionID int64, nodeKey, name, filePath string) {
	// Try to get existing — if not found, create
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

func normalizePackageName(pkg string) string {
	// "@scope/name" → "name"
	// "some-package" → "some-package"
	name := inferNameFromImport(pkg)
	return strings.ToLower(strings.ReplaceAll(name, "/", "-"))
}
