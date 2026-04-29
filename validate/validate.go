package validate

import (
	"fmt"

	"github.com/alexdx2/chronicle-core/registry"
)

type NodeInput struct {
	NodeKey       string
	Layer         string
	NodeType      string
	DomainKey     string
	Name          string
	QualifiedName string
	RepoName      string
	FilePath      string
	Lang          string
	OwnerKey      string
	Environment   string
	Visibility    string
	Status        string
	Confidence    float64
	Metadata      string
}

type ValidatedNode struct {
	NodeKey       string
	Layer         string
	NodeType      string
	DomainKey     string
	Name          string
	QualifiedName string
	RepoName      string
	FilePath      string
	Lang          string
	OwnerKey      string
	Environment   string
	Visibility    string
	Status        string
	Confidence    float64
	Metadata      string
}

type EdgeInput struct {
	EdgeKey        string
	FromNodeKey    string
	ToNodeKey      string
	EdgeType       string
	DerivationKind string
	FromLayer      string
	ToLayer        string
	ContextKey     string
	Confidence     float64
	Metadata       string
}

type ValidatedEdge struct {
	EdgeKey        string
	FromNodeKey    string
	ToNodeKey      string
	EdgeType       string
	DerivationKind string
	ContextKey     string
	Confidence     float64
	Metadata       string
}

type EvidenceInput struct {
	TargetKind       string
	SourceKind       string
	RepoName         string
	FilePath         string
	LineStart        int
	LineEnd          int
	ColumnStart      int
	ColumnEnd        int
	Locator          string
	ExtractorID      string
	ExtractorVersion string
	ASTRule          string
	SnippetHash      string
	CommitSHA        string
	Confidence       float64
	Polarity         string
	RevisionID       int64
	Metadata         string
}

func ValidateNodeInput(input NodeInput, reg *registry.Registry) (*ValidatedNode, error) {
	if input.Name == "" {
		return nil, fmt.Errorf("validation: name is required")
	}
	if input.DomainKey == "" {
		return nil, fmt.Errorf("validation: domain_key is required")
	}

	normalizedKey, err := NormalizeNodeKey(input.NodeKey)
	if err != nil {
		return nil, fmt.Errorf("validation: %w", err)
	}

	if !reg.IsValidLayer(input.Layer) {
		return nil, fmt.Errorf("validation: invalid layer %q", input.Layer)
	}
	if !reg.IsValidNodeType(input.Layer, input.NodeType) {
		return nil, fmt.Errorf("validation: invalid node_type %q for layer %q", input.NodeType, input.Layer)
	}

	confidence := input.Confidence
	if confidence == 0 {
		confidence = 1.0
	}
	if confidence < 0 || confidence > 1 {
		return nil, fmt.Errorf("validation: confidence %f out of range [0, 1]", confidence)
	}

	status := input.Status
	if status == "" {
		status = "active"
	}
	if !reg.IsValidStatus(status) {
		return nil, fmt.Errorf("validation: invalid status %q", status)
	}

	metadata := input.Metadata
	if metadata == "" {
		metadata = "{}"
	}

	return &ValidatedNode{
		NodeKey:       normalizedKey,
		Layer:         input.Layer,
		NodeType:      input.NodeType,
		DomainKey:     input.DomainKey,
		Name:          input.Name,
		QualifiedName: input.QualifiedName,
		RepoName:      input.RepoName,
		FilePath:      input.FilePath,
		Lang:          input.Lang,
		OwnerKey:      input.OwnerKey,
		Environment:   input.Environment,
		Visibility:    input.Visibility,
		Status:        status,
		Confidence:    confidence,
		Metadata:      metadata,
	}, nil
}

func ValidateEdgeInput(input EdgeInput, reg *registry.Registry) (*ValidatedEdge, error) {
	if input.FromNodeKey == "" {
		return nil, fmt.Errorf("validation: from_node_key is required")
	}
	if input.ToNodeKey == "" {
		return nil, fmt.Errorf("validation: to_node_key is required")
	}
	if input.EdgeType == "" {
		return nil, fmt.Errorf("validation: edge_type is required")
	}
	if input.DerivationKind == "" {
		input.DerivationKind = "hard" // default
	}

	if !reg.IsValidEdgeType(input.EdgeType) {
		return nil, fmt.Errorf("validation: invalid edge_type %q", input.EdgeType)
	}
	if !reg.IsValidDerivation(input.DerivationKind) {
		return nil, fmt.Errorf("validation: invalid derivation_kind %q", input.DerivationKind)
	}

	if err := reg.ValidateEdgeLayers(input.EdgeType, input.FromLayer, input.ToLayer); err != nil {
		return nil, fmt.Errorf("validation: %w", err)
	}

	confidence := input.Confidence
	if confidence == 0 {
		confidence = 1.0
	}
	if confidence < 0 || confidence > 1 {
		return nil, fmt.Errorf("validation: confidence %f out of range [0, 1]", confidence)
	}

	metadata := input.Metadata
	if metadata == "" {
		metadata = "{}"
	}

	edgeKey := input.EdgeKey
	if edgeKey == "" {
		edgeKey = BuildEdgeKey(input.FromNodeKey, input.ToNodeKey, input.EdgeType)
	} else {
		var err error
		edgeKey, err = NormalizeEdgeKey(edgeKey)
		if err != nil {
			return nil, fmt.Errorf("validation: %w", err)
		}
	}

	return &ValidatedEdge{
		EdgeKey:        edgeKey,
		FromNodeKey:    input.FromNodeKey,
		ToNodeKey:      input.ToNodeKey,
		EdgeType:       input.EdgeType,
		DerivationKind: input.DerivationKind,
		ContextKey:     input.ContextKey,
		Confidence:     confidence,
		Metadata:       metadata,
	}, nil
}

func ValidateEvidenceInput(input EvidenceInput, reg *registry.Registry) error {
	if input.TargetKind != "node" && input.TargetKind != "edge" {
		return fmt.Errorf("validation: target_kind must be 'node' or 'edge', got %q", input.TargetKind)
	}
	if !reg.IsValidSourceKind(input.SourceKind) {
		return fmt.Errorf("validation: invalid source_kind %q", input.SourceKind)
	}
	if input.ExtractorID == "" {
		return fmt.Errorf("validation: extractor_id is required")
	}
	if input.ExtractorVersion == "" {
		return fmt.Errorf("validation: extractor_version is required")
	}
	if input.Confidence != 0 && (input.Confidence < 0 || input.Confidence > 1) {
		return fmt.Errorf("validation: confidence %f out of range [0, 1]", input.Confidence)
	}
	return nil
}
