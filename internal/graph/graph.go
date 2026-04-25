package graph

import (
	"fmt"

	"github.com/anthropics/depbot/internal/registry"
	"github.com/anthropics/depbot/internal/store"
	"github.com/anthropics/depbot/internal/validate"
)

// Graph wraps the store with validation via the registry.
type Graph struct {
	store *store.Store
	reg   *registry.Registry
}

// New creates a new Graph.
func New(s *store.Store, r *registry.Registry) *Graph {
	return &Graph{store: s, reg: r}
}

// Store returns the underlying store.
func (g *Graph) Store() *store.Store {
	return g.store
}

// UpsertNode validates the input and upserts a node into the store.
func (g *Graph) UpsertNode(input validate.NodeInput, revisionID int64) (int64, error) {
	vn, err := validate.ValidateNodeInput(input, g.reg)
	if err != nil {
		return 0, fmt.Errorf("UpsertNode: %w", err)
	}

	row := store.NodeRow{
		NodeKey:             vn.NodeKey,
		Layer:               vn.Layer,
		NodeType:            vn.NodeType,
		DomainKey:           vn.DomainKey,
		Name:                vn.Name,
		QualifiedName:       vn.QualifiedName,
		RepoName:            vn.RepoName,
		FilePath:            vn.FilePath,
		Lang:                vn.Lang,
		OwnerKey:            vn.OwnerKey,
		Environment:         vn.Environment,
		Visibility:          vn.Visibility,
		Status:              vn.Status,
		FirstSeenRevisionID: revisionID,
		LastSeenRevisionID:  revisionID,
		Confidence:          vn.Confidence,
		Metadata:            vn.Metadata,
	}
	id, err := g.store.UpsertNode(row)
	if err != nil {
		return 0, fmt.Errorf("UpsertNode: %w", err)
	}
	return id, nil
}

// UpsertEdge validates the input and upserts an edge into the store.
func (g *Graph) UpsertEdge(input validate.EdgeInput, revisionID int64) (int64, error) {
	ve, err := validate.ValidateEdgeInput(input, g.reg)
	if err != nil {
		return 0, fmt.Errorf("UpsertEdge: %w", err)
	}

	fromID, err := g.store.GetNodeIDByKey(ve.FromNodeKey)
	if err != nil {
		return 0, fmt.Errorf("UpsertEdge: from_node_key %q: %w", ve.FromNodeKey, err)
	}
	toID, err := g.store.GetNodeIDByKey(ve.ToNodeKey)
	if err != nil {
		return 0, fmt.Errorf("UpsertEdge: to_node_key %q: %w", ve.ToNodeKey, err)
	}

	row := store.EdgeRow{
		EdgeKey:             ve.EdgeKey,
		FromNodeID:          fromID,
		ToNodeID:            toID,
		EdgeType:            ve.EdgeType,
		DerivationKind:      ve.DerivationKind,
		ContextKey:          ve.ContextKey,
		Active:              true,
		FirstSeenRevisionID: revisionID,
		LastSeenRevisionID:  revisionID,
		Confidence:          ve.Confidence,
		Metadata:            ve.Metadata,
	}
	id, err := g.store.UpsertEdge(row)
	if err != nil {
		return 0, fmt.Errorf("UpsertEdge: %w", err)
	}
	return id, nil
}

// AddNodeEvidence validates the input and adds evidence for a node.
func (g *Graph) AddNodeEvidence(nodeKey string, input validate.EvidenceInput) (int64, error) {
	input.TargetKind = "node"
	if err := validate.ValidateEvidenceInput(input, g.reg); err != nil {
		return 0, fmt.Errorf("AddNodeEvidence: %w", err)
	}

	nodeID, err := g.store.GetNodeIDByKey(nodeKey)
	if err != nil {
		return 0, fmt.Errorf("AddNodeEvidence: %w", err)
	}

	confidence := input.Confidence
	if confidence == 0 {
		confidence = 1.0
	}
	metadata := input.Metadata
	if metadata == "" {
		metadata = "{}"
	}

	row := store.EvidenceRow{
		TargetKind:       "node",
		NodeID:           nodeID,
		SourceKind:       input.SourceKind,
		RepoName:         input.RepoName,
		FilePath:         input.FilePath,
		LineStart:        input.LineStart,
		LineEnd:          input.LineEnd,
		ColumnStart:      input.ColumnStart,
		ColumnEnd:        input.ColumnEnd,
		Locator:          input.Locator,
		ExtractorID:      input.ExtractorID,
		ExtractorVersion: input.ExtractorVersion,
		ASTRule:          input.ASTRule,
		SnippetHash:      input.SnippetHash,
		CommitSHA:        input.CommitSHA,
		Confidence:       confidence,
		Metadata:         metadata,
	}
	id, err := g.store.AddEvidence(row)
	if err != nil {
		return 0, fmt.Errorf("AddNodeEvidence: %w", err)
	}
	return id, nil
}

// AddEdgeEvidence validates the input and adds evidence for an edge.
func (g *Graph) AddEdgeEvidence(edgeKey string, input validate.EvidenceInput) (int64, error) {
	input.TargetKind = "edge"
	if err := validate.ValidateEvidenceInput(input, g.reg); err != nil {
		return 0, fmt.Errorf("AddEdgeEvidence: %w", err)
	}

	edge, err := g.store.GetEdgeByKey(edgeKey)
	if err != nil {
		return 0, fmt.Errorf("AddEdgeEvidence: %w", err)
	}

	confidence := input.Confidence
	if confidence == 0 {
		confidence = 1.0
	}
	metadata := input.Metadata
	if metadata == "" {
		metadata = "{}"
	}

	row := store.EvidenceRow{
		TargetKind:       "edge",
		EdgeID:           edge.EdgeID,
		SourceKind:       input.SourceKind,
		RepoName:         input.RepoName,
		FilePath:         input.FilePath,
		LineStart:        input.LineStart,
		LineEnd:          input.LineEnd,
		ColumnStart:      input.ColumnStart,
		ColumnEnd:        input.ColumnEnd,
		Locator:          input.Locator,
		ExtractorID:      input.ExtractorID,
		ExtractorVersion: input.ExtractorVersion,
		ASTRule:          input.ASTRule,
		SnippetHash:      input.SnippetHash,
		CommitSHA:        input.CommitSHA,
		Confidence:       confidence,
		Metadata:         metadata,
	}
	id, err := g.store.AddEvidence(row)
	if err != nil {
		return 0, fmt.Errorf("AddEdgeEvidence: %w", err)
	}
	return id, nil
}
