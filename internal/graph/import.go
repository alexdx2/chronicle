package graph

import (
	"encoding/json"
	"fmt"

	"github.com/anthropics/depbot/internal/store"
	"github.com/anthropics/depbot/internal/validate"
)

// ImportNode describes a node to import.
type ImportNode struct {
	NodeKey       string  `json:"node_key"`
	Layer         string  `json:"layer"`
	NodeType      string  `json:"node_type"`
	DomainKey     string  `json:"domain_key"`
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
	Metadata      string  `json:"metadata,omitempty"`
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
	Metadata       string  `json:"metadata,omitempty"`
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

// ImportPayload is the full bulk-import payload.
type ImportPayload struct {
	Nodes    []ImportNode     `json:"nodes"`
	Edges    []ImportEdge     `json:"edges"`
	Evidence []ImportEvidence `json:"evidence"`
}

// ImportResult holds the result counts after a successful import.
type ImportResult struct {
	NodesCreated    int `json:"nodes_created"`
	EdgesCreated    int `json:"edges_created"`
	EvidenceCreated int `json:"evidence_created"`
}

// ImportAll imports nodes, edges, and evidence in a single transaction.
// If any validation fails, the entire transaction is rolled back.
func (g *Graph) ImportAll(payload ImportPayload, revisionID int64) (*ImportResult, error) {
	var result ImportResult

	err := g.store.WithTx(func(tx *store.Store) error {
		txGraph := New(tx, g.reg)

		// Upsert nodes.
		for i, n := range payload.Nodes {
			input := validate.NodeInput{
				NodeKey:       n.NodeKey,
				Layer:         n.Layer,
				NodeType:      n.NodeType,
				DomainKey:     n.DomainKey,
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
				Metadata:      n.Metadata,
			}
			if _, err := txGraph.UpsertNode(input, revisionID); err != nil {
				return fmt.Errorf("ImportAll node[%d]: %w", i, err)
			}
			result.NodesCreated++
		}

		// Upsert edges.
		for i, e := range payload.Edges {
			input := validate.EdgeInput{
				EdgeKey:        e.EdgeKey,
				FromNodeKey:    e.FromNodeKey,
				ToNodeKey:      e.ToNodeKey,
				EdgeType:       e.EdgeType,
				DerivationKind: e.DerivationKind,
				FromLayer:      e.FromLayer,
				ToLayer:        e.ToLayer,
				ContextKey:     e.ContextKey,
				Confidence:     e.Confidence,
				Metadata:       e.Metadata,
			}
			if _, err := txGraph.UpsertEdge(input, revisionID); err != nil {
				return fmt.Errorf("ImportAll edge[%d]: %w", i, err)
			}
			result.EdgesCreated++
		}

		// Add evidence.
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
				Metadata:         ev.Metadata,
			}

			switch ev.TargetKind {
			case "node":
				if ev.NodeKey == "" {
					continue // skip evidence without node_key
				}
				if _, err := txGraph.AddNodeEvidence(ev.NodeKey, evInput); err != nil {
					continue // skip if node doesn't exist — non-fatal
				}
			case "edge":
				if ev.EdgeKey == "" {
					continue // skip evidence without edge_key
				}
				if _, err := txGraph.AddEdgeEvidence(ev.EdgeKey, evInput); err != nil {
					continue // skip if edge doesn't exist — non-fatal
				}
			default:
				continue // skip unknown target_kind
			}
			result.EvidenceCreated++
		}

		return nil
	})
	if err != nil {
		return nil, err
	}

	return &result, nil
}
