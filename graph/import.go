package graph

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/alexdx2/chronicle-core/registry"
	"github.com/alexdx2/chronicle-core/store"
	"github.com/alexdx2/chronicle-core/validate"
)

// ImportNode describes a node to import.
type ImportNode struct {
	NodeKey       string  `json:"node_key"`
	Layer         string  `json:"layer"`
	NodeType      string  `json:"node_type"`
	DomainKey     string  `json:"domain_key"`
	Domain        string  `json:"domain,omitempty"` // alias for domain_key — Claude often sends this
	Name          string  `json:"name"`
	QualifiedName string  `json:"qualified_name,omitempty"`
	RepoName      string  `json:"repo_name,omitempty"`
	FilePath      string  `json:"file_path,omitempty"`
	Lang          string  `json:"lang,omitempty"`
	OwnerKey      string  `json:"owner_key,omitempty"`
	Environment   string  `json:"environment,omitempty"`
	Visibility    string  `json:"visibility,omitempty"`
	Status        string  `json:"status,omitempty"`
	Confidence    float64 `json:"confidence,omitempty"`
	Metadata      FlexString `json:"metadata,omitempty"`
}

// ImportEdge describes an edge to import.
type ImportEdge struct {
	EdgeKey        string  `json:"edge_key,omitempty"`
	FromNodeKey    string  `json:"from_node_key"`
	ToNodeKey      string  `json:"to_node_key"`
	EdgeType       string  `json:"edge_type"`
	DerivationKind string  `json:"derivation_kind"`
	FromLayer      string  `json:"from_layer"`
	ToLayer        string  `json:"to_layer"`
	ContextKey     string  `json:"context_key,omitempty"`
	Confidence     float64 `json:"confidence,omitempty"`
	Metadata       FlexString `json:"metadata,omitempty"`
}

// FlexString accepts both JSON string and object for metadata fields.
// Claude sometimes sends {"key":"val"} instead of "{\"key\":\"val\"}".
type FlexString string

func (fs *FlexString) UnmarshalJSON(data []byte) error {
	// Try as string first
	var s string
	if err := json.Unmarshal(data, &s); err == nil {
		*fs = FlexString(s)
		return nil
	}
	// Accept as raw JSON (object/array) — re-serialize to string
	*fs = FlexString(string(data))
	return nil
}

// FlexInt accepts both JSON number and string for int fields.
// Claude sometimes sends "123" instead of 123.
type FlexInt int

func (fi *FlexInt) UnmarshalJSON(data []byte) error {
	// Try as number first
	var n int
	if err := json.Unmarshal(data, &n); err == nil {
		*fi = FlexInt(n)
		return nil
	}
	// Try as string
	var s string
	if err := json.Unmarshal(data, &s); err == nil {
		if s == "" {
			*fi = 0
			return nil
		}
		var parsed int
		if _, err := fmt.Sscanf(s, "%d", &parsed); err == nil {
			*fi = FlexInt(parsed)
			return nil
		}
	}
	*fi = 0
	return nil
}

// ImportEvidence describes an evidence record to import.
type ImportEvidence struct {
	TargetKind       string  `json:"target_kind"`
	NodeKey          string  `json:"node_key,omitempty"`
	EdgeKey          string  `json:"edge_key,omitempty"`
	SourceKind       string  `json:"source_kind"`
	RepoName         string  `json:"repo_name,omitempty"`
	FilePath         string  `json:"file_path,omitempty"`
	LineStart        FlexInt `json:"line_start,omitempty"`
	LineEnd          FlexInt `json:"line_end,omitempty"`
	ColumnStart      FlexInt `json:"column_start,omitempty"`
	ColumnEnd        FlexInt `json:"column_end,omitempty"`
	Locator          string  `json:"locator,omitempty"`
	ExtractorID      string  `json:"extractor_id"`
	ExtractorVersion string  `json:"extractor_version"`
	ASTRule          string  `json:"ast_rule,omitempty"`
	SnippetHash      string  `json:"snippet_hash,omitempty"`
	CommitSHA        string  `json:"commit_sha,omitempty"`
	Confidence       float64 `json:"confidence,omitempty"`
	Polarity         string  `json:"polarity,omitempty"`
	Metadata         string  `json:"metadata,omitempty"`
}

// ImportAlias describes an alias to import for a node.
type ImportAlias struct {
	NodeKey    string  `json:"node_key"`
	Alias      string  `json:"alias"`
	AliasKind  string  `json:"alias_kind"`
	Confidence float64 `json:"confidence,omitempty"`
}

// ImportPayload is the full bulk-import payload.
type ImportPayload struct {
	Nodes    []ImportNode     `json:"nodes"`
	Edges    []ImportEdge     `json:"edges"`
	Evidence []ImportEvidence `json:"evidence"`
	Aliases  []ImportAlias    `json:"aliases,omitempty"`
}

// ImportResult holds the result counts after an import.
// Valid items are written; invalid items are skipped and returned as errors with suggestions.
type ImportResult struct {
	NodesCreated    int              `json:"nodes_created"`
	EdgesCreated    int              `json:"edges_created"`
	EvidenceCreated int              `json:"evidence_created"`
	AliasesCreated  int              `json:"aliases_created"`
	Rejected        []RejectedItem   `json:"rejected,omitempty"`
}

// RejectedItem describes an item that failed validation with a suggested fix.
type RejectedItem struct {
	Index      int            `json:"index"`
	Kind       string         `json:"kind"` // "node", "edge"
	Error      string         `json:"error"`
	Suggestion *SuggestedFix  `json:"suggestion,omitempty"`
}

// DryRunResult holds the validation results from a dry-run import.
type DryRunResult struct {
	Valid             bool             `json:"valid"`
	NodesValidated    int              `json:"nodes_validated"`
	EdgesValidated    int              `json:"edges_validated"`
	EvidenceValidated int              `json:"evidence_validated"`
	Errors            []string         `json:"errors"`
	SuggestedFixes    []SuggestedFix   `json:"suggested_fixes"`
}

// SuggestedFix suggests a correction for an invalid import item.
type SuggestedFix struct {
	Index  int    `json:"index"`
	Kind   string `json:"kind"`
	Field  string `json:"field"`
	From   string `json:"from"`
	To     string `json:"to"`
	Reason string `json:"reason"`
}

// ImportAllDryRun validates the entire payload without writing anything.
// Returns validation results with suggested fixes for errors.
func (g *Graph) ImportAllDryRun(payload ImportPayload, revisionID int64) (*DryRunResult, error) {
	result := &DryRunResult{
		Valid:          true,
		Errors:         []string{},
		SuggestedFixes: []SuggestedFix{},
	}

	// Validate nodes
	for i, n := range payload.Nodes {
		input := validate.NodeInput{
			NodeKey:   n.NodeKey,
			Layer:     n.Layer,
			NodeType:  n.NodeType,
			DomainKey: normalizeDomainKey(&n),
			Name:      n.Name,
			Status:    n.Status,
			Confidence: n.Confidence,
			Metadata:  string(n.Metadata),
		}
		if _, err := validate.ValidateNodeInput(input, g.reg); err != nil {
			result.Valid = false
			result.Errors = append(result.Errors, fmt.Sprintf("node[%d]: %v", i, err))
		} else {
			result.NodesValidated++
		}
	}

	// Validate edges
	for i, e := range payload.Edges {
		fromLayer := e.FromLayer
		if fromLayer == "" {
			fromLayer = layerFromKey(e.FromNodeKey)
		}
		toLayer := e.ToLayer
		if toLayer == "" {
			toLayer = layerFromKey(e.ToNodeKey)
		}

		input := validate.EdgeInput{
			EdgeKey:        e.EdgeKey,
			FromNodeKey:    e.FromNodeKey,
			ToNodeKey:      e.ToNodeKey,
			EdgeType:       e.EdgeType,
			DerivationKind: e.DerivationKind,
			FromLayer:      fromLayer,
			ToLayer:        toLayer,
			Confidence:     e.Confidence,
			Metadata:       string(e.Metadata),
		}
		if _, err := validate.ValidateEdgeInput(input, g.reg); err != nil {
			result.Valid = false
			result.Errors = append(result.Errors, fmt.Sprintf("edge[%d]: %v", i, err))

			// Suggest fix for edge type issues
			if fromLayer != "" || toLayer != "" {
				// Prefer similarity-based suggestions first, then fall back to layer-based
				similar := g.reg.SuggestSimilarEdgeTypes(e.EdgeType)
				var bestSuggestion *registry.EdgeSuggestion
				// Try similarity matches that also fit the layers
				for i := range similar {
					s := &similar[i]
					fromOk := fromLayer == "" || containsStr(s.FromLayers, fromLayer)
					toOk := toLayer == "" || containsStr(s.ToLayers, toLayer)
					if fromOk && toOk {
						bestSuggestion = s
						break
					}
				}
				// Fall back to any matching edge type (skip CONTAINS as too generic)
				if bestSuggestion == nil {
					layerSuggestions := g.reg.SuggestEdgeTypes(fromLayer, toLayer)
					for i := range layerSuggestions {
						s := &layerSuggestions[i]
						if s.EdgeType != "CONTAINS" {
							bestSuggestion = s
							break
						}
					}
				}
				if bestSuggestion != nil {
					result.SuggestedFixes = append(result.SuggestedFixes, SuggestedFix{
						Index:  i,
						Kind:   "edge",
						Field:  "edge_type",
						From:   e.EdgeType,
						To:     bestSuggestion.EdgeType,
						Reason: fmt.Sprintf("%s allows from_layer %q to to_layer %q — %s", bestSuggestion.EdgeType, fromLayer, toLayer, bestSuggestion.Intent),
					})
				}
			}
		} else {
			result.EdgesValidated++
		}
	}

	// Validate evidence
	for i, ev := range payload.Evidence {
		evInput := validate.EvidenceInput{
			TargetKind:       ev.TargetKind,
			SourceKind:       ev.SourceKind,
			ExtractorID:      ev.ExtractorID,
			ExtractorVersion: ev.ExtractorVersion,
			Confidence:       ev.Confidence,
		}
		if err := validate.ValidateEvidenceInput(evInput, g.reg); err != nil {
			result.Valid = false
			result.Errors = append(result.Errors, fmt.Sprintf("evidence[%d]: %v", i, err))
		} else {
			result.EvidenceValidated++
		}
	}

	return result, nil
}

// layerFromKey extracts the layer from a node key (layer:type:domain:name).
func layerFromKey(key string) string {
	parts := strings.SplitN(key, ":", 2)
	if len(parts) >= 1 {
		return parts[0]
	}
	return ""
}

// normalizeDomainKey returns domain_key, falling back to domain alias.
func normalizeDomainKey(n *ImportNode) string {
	if n.DomainKey != "" {
		return n.DomainKey
	}
	return n.Domain
}

func containsStr(slice []string, item string) bool {
	for _, s := range slice {
		if s == item {
			return true
		}
	}
	return false
}

// ImportAll imports nodes, edges, and evidence in a single transaction.
// Valid items are written; invalid items are skipped and returned in Rejected with suggestions.
func (g *Graph) ImportAll(payload ImportPayload, revisionID int64) (*ImportResult, error) {
	var result ImportResult

	err := g.store.WithTx(func(tx *store.Store) error {
		txGraph := New(tx, g.reg)

		// Upsert nodes — skip invalid, collect rejections.
		for i, n := range payload.Nodes {
			input := validate.NodeInput{
				NodeKey:       n.NodeKey,
				Layer:         n.Layer,
				NodeType:      n.NodeType,
				DomainKey:     normalizeDomainKey(&n),
				Name:          n.Name,
				QualifiedName: n.QualifiedName,
				RepoName:      n.RepoName,
				FilePath:      n.FilePath,
				Lang:          n.Lang,
				OwnerKey:      n.OwnerKey,
				Environment:   n.Environment,
				Visibility:    n.Visibility,
				Status:        n.Status,
				Confidence:    n.Confidence,
				Metadata:      string(n.Metadata),
			}
			if _, err := txGraph.UpsertNode(input, revisionID); err != nil {
				result.Rejected = append(result.Rejected, RejectedItem{
					Index: i,
					Kind:  "node",
					Error: fmt.Sprintf("node[%d]: %v", i, err),
				})
				continue
			}
			result.NodesCreated++
		}

		// Upsert edges — skip invalid, collect rejections with suggestions.
		for i, e := range payload.Edges {
			fromLayer := e.FromLayer
			if fromLayer == "" {
				fromLayer = layerFromKey(e.FromNodeKey)
			}
			toLayer := e.ToLayer
			if toLayer == "" {
				toLayer = layerFromKey(e.ToNodeKey)
			}
			input := validate.EdgeInput{
				EdgeKey:        e.EdgeKey,
				FromNodeKey:    e.FromNodeKey,
				ToNodeKey:      e.ToNodeKey,
				EdgeType:       e.EdgeType,
				DerivationKind: e.DerivationKind,
				FromLayer:      fromLayer,
				ToLayer:        toLayer,
				ContextKey:     e.ContextKey,
				Confidence:     e.Confidence,
				Metadata:       string(e.Metadata),
			}
			if _, err := txGraph.UpsertEdge(input, revisionID); err != nil {
				rejected := RejectedItem{
					Index: i,
					Kind:  "edge",
					Error: fmt.Sprintf("edge[%d]: %v", i, err),
				}
				// Suggest fix for edge type issues
				if fromLayer != "" || toLayer != "" {
					similar := g.reg.SuggestSimilarEdgeTypes(e.EdgeType)
					var best *registry.EdgeSuggestion
					for j := range similar {
						s := &similar[j]
						fOk := fromLayer == "" || containsStr(s.FromLayers, fromLayer)
						tOk := toLayer == "" || containsStr(s.ToLayers, toLayer)
						if fOk && tOk {
							best = s
							break
						}
					}
					if best == nil {
						layerSugg := g.reg.SuggestEdgeTypes(fromLayer, toLayer)
						for j := range layerSugg {
							s := &layerSugg[j]
							if s.EdgeType != "CONTAINS" {
								best = s
								break
							}
						}
					}
					if best != nil {
						rejected.Suggestion = &SuggestedFix{
							Index:  i,
							Kind:   "edge",
							Field:  "edge_type",
							From:   e.EdgeType,
							To:     best.EdgeType,
							Reason: fmt.Sprintf("%s allows %s→%s — %s", best.EdgeType, fromLayer, toLayer, best.Intent),
						}
					}
				}
				result.Rejected = append(result.Rejected, rejected)
				continue
			}
			result.EdgesCreated++
		}

		// Add evidence — skip silently on missing targets (already lenient).
		for _, ev := range payload.Evidence {
			evInput := validate.EvidenceInput{
				TargetKind:       ev.TargetKind,
				SourceKind:       ev.SourceKind,
				RepoName:         ev.RepoName,
				FilePath:         ev.FilePath,
				LineStart:        int(ev.LineStart),
				LineEnd:          int(ev.LineEnd),
				ColumnStart:      int(ev.ColumnStart),
				ColumnEnd:        int(ev.ColumnEnd),
				Locator:          ev.Locator,
				ExtractorID:      ev.ExtractorID,
				ExtractorVersion: ev.ExtractorVersion,
				ASTRule:          ev.ASTRule,
				SnippetHash:      ev.SnippetHash,
				CommitSHA:        ev.CommitSHA,
				Confidence:       ev.Confidence,
				Polarity:         ev.Polarity,
				RevisionID:       revisionID,
				Metadata:         string(ev.Metadata),
			}

			switch ev.TargetKind {
			case "node":
				if ev.NodeKey == "" {
					continue
				}
				if _, err := txGraph.AddNodeEvidence(ev.NodeKey, evInput); err != nil {
					continue
				}
			case "edge":
				if ev.EdgeKey == "" {
					continue
				}
				if _, err := txGraph.AddEdgeEvidence(ev.EdgeKey, evInput); err != nil {
					continue
				}
			default:
				continue
			}
			result.EvidenceCreated++
		}

		// Auto-generate evidence for nodes with file_path that have no explicit evidence.
		// This ensures nodes imported with file context get baseline confidence.
		explicitNodeEvidence := make(map[string]bool)
		for _, ev := range payload.Evidence {
			if ev.TargetKind == "node" && ev.NodeKey != "" {
				explicitNodeEvidence[ev.NodeKey] = true
			}
		}
		for _, n := range payload.Nodes {
			if n.FilePath == "" || explicitNodeEvidence[n.NodeKey] {
				continue
			}
			evInput := validate.EvidenceInput{
				TargetKind:       "node",
				SourceKind:       "file",
				FilePath:         n.FilePath,
				ExtractorID:      "chronicle-auto",
				ExtractorVersion: "1.0",
				Confidence:       0.8,
				RevisionID:       revisionID,
			}
			if _, err := txGraph.AddNodeEvidence(n.NodeKey, evInput); err == nil {
				result.EvidenceCreated++
			}
		}

		// Auto-generate evidence for edges between nodes from the same file.
		explicitEdgeEvidence := make(map[string]bool)
		for _, ev := range payload.Evidence {
			if ev.TargetKind == "edge" && ev.EdgeKey != "" {
				explicitEdgeEvidence[ev.EdgeKey] = true
			}
		}
		// Build node_key → file_path map from payload
		nodeFiles := make(map[string]string)
		for _, n := range payload.Nodes {
			if n.FilePath != "" {
				nodeFiles[n.NodeKey] = n.FilePath
			}
		}
		for _, e := range payload.Edges {
			edgeKey := e.EdgeKey
			if edgeKey == "" {
				edgeKey = validate.BuildEdgeKey(e.FromNodeKey, e.ToNodeKey, e.EdgeType)
			}
			if explicitEdgeEvidence[edgeKey] {
				continue
			}
			// Use from_node's file as evidence source if available
			filePath := nodeFiles[e.FromNodeKey]
			if filePath == "" {
				filePath = nodeFiles[e.ToNodeKey]
			}
			if filePath == "" {
				continue
			}
			evInput := validate.EvidenceInput{
				TargetKind:       "edge",
				SourceKind:       "file",
				FilePath:         filePath,
				ExtractorID:      "chronicle-auto",
				ExtractorVersion: "1.0",
				Confidence:       0.75,
				RevisionID:       revisionID,
			}
			if _, err := txGraph.AddEdgeEvidence(edgeKey, evInput); err == nil {
				result.EvidenceCreated++
			}
		}

		// Import aliases.
		for _, a := range payload.Aliases {
			if a.NodeKey == "" || a.Alias == "" {
				continue
			}
			nodeID, err := tx.GetNodeIDByKey(a.NodeKey)
			if err != nil {
				continue
			}
			if _, err := tx.AddAlias(store.AliasRow{
				NodeID:     nodeID,
				Alias:      a.Alias,
				AliasKind:  a.AliasKind,
				Confidence: a.Confidence,
			}); err != nil {
				continue
			}
			result.AliasesCreated++
		}

		return nil
	})
	if err != nil {
		return nil, err
	}

	return &result, nil
}
