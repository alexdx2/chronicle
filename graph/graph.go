package graph

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"

	"github.com/alexdx2/chronicle-core/registry"
	"github.com/alexdx2/chronicle-core/store"
	"github.com/alexdx2/chronicle-core/validate"
	"github.com/alexdx2/chronicle-core/verify"
)

// Graph wraps the store with validation via the registry.
type Graph struct {
	store   *store.Store
	reg     *registry.Registry
	emitter EventEmitter

	// resolveInFlight guards ResolveExtractions per domain+revision: a resolve
	// can outlive the MCP client's tool timeout and agents retry while the
	// first run is still working — the retry must be rejected, not raced.
	// Pointer so the tx-scoped struct copies (g2 := *g) share the same guard.
	resolveInFlight *sync.Map

	// scanFileIndex is populated during ResolveExtractions for class-name → file_path lookup.
	scanFileIndex scanFileIndex

	// currentFileImportMap is set per-file during ResolveExtractions.
	// It maps each imported symbol to the resolved absolute file path derived from
	// the relative import specifier, allowing provides/parent resolution to consult
	// the module's own import declarations before falling back to class-name heuristics.
	// symbol → resolved file path (no extension, relative to module file dir)
	currentFileImportMap map[string]string

	// pendingExtEndpoints accumulates endpoint references from http_call facts
	// whose host stayed unresolved (external_system placeholder) during
	// ResolveExtractions. Evidence can't carry them — its dedup key collapses
	// multiple calls from one file. Materialized as status="external" endpoint
	// nodes in the post-pass if the target is still external after merging.
	pendingExtEndpoints []pendingExtEndpoint

	// evidenceErr records the first evidence-write failure on a void path
	// (ensureNodeID → ensureNode) during ResolveExtractions. Evidence writes
	// journal events; silently dropping a failure inside a tx that then
	// commits corrupts replay. resolveExtractionsInTx checks it before
	// returning so the transaction aborts instead.
	evidenceErr error
}

// noteEvidenceErr records the first evidence-write failure during a resolve pass.
func (g *Graph) noteEvidenceErr(err error) {
	if err != nil && g.evidenceErr == nil {
		g.evidenceErr = err
	}
}

// pendingExtEndpoint is one unresolved http_call endpoint reference.
type pendingExtEndpoint struct {
	FromNodeKey string
	FromNodeID  int64
	ToNodeKey   string // external_system placeholder the call targets
	Method      string
	Path        string
}

// defaultEvidenceConfidence returns the confidence for an evidence row.
// If the caller provided an explicit confidence, use it. Otherwise default to 0.95
// (high confidence — the evidence exists, it's the derivation that's uncertain).
func defaultEvidenceConfidence(explicit float64) float64 {
	if explicit > 0 {
		return explicit
	}
	return 0.95
}

// New creates a new Graph.
func New(s *store.Store, r *registry.Registry) *Graph {
	return &Graph{store: s, reg: r, emitter: noopEmitter{}, resolveInFlight: &sync.Map{}}
}

// SetEventEmitter attaches a listener for scan events.
func (g *Graph) SetEventEmitter(e EventEmitter) {
	if e != nil {
		g.emitter = e
	}
}

// Store returns the underlying store.
func (g *Graph) Store() *store.Store {
	return g.store
}

// Registry returns the underlying registry.
func (g *Graph) Registry() *registry.Registry {
	return g.reg
}

// ResolveNodeKey resolves a name-or-key string to a node_key.
// Tries in order: exact key match → case-insensitive name search.
// Returns the node_key and an error if not found or ambiguous.
func (g *Graph) ResolveNodeKey(nameOrKey string) (string, error) {
	// Try exact key first.
	if _, err := g.store.GetNodeByKey(nameOrKey); err == nil {
		return nameOrKey, nil
	}

	// Search by name.
	nodes, err := g.store.SearchNodesByName(nameOrKey)
	if err != nil {
		return "", fmt.Errorf("ResolveNodeKey: %w", err)
	}
	if len(nodes) == 0 {
		return "", fmt.Errorf("ResolveNodeKey: no node found matching %q", nameOrKey)
	}
	if len(nodes) == 1 {
		return nodes[0].NodeKey, nil
	}
	// Multiple matches — prefer exact name match.
	for _, n := range nodes {
		if strings.EqualFold(n.Name, nameOrKey) {
			return n.NodeKey, nil
		}
	}
	// Ambiguous — return first with a helpful error listing options.
	var keys []string
	for _, n := range nodes[:min(len(nodes), 5)] {
		keys = append(keys, n.NodeKey+" ("+n.Name+")")
	}
	return "", fmt.Errorf("ResolveNodeKey: ambiguous — %d matches for %q: %s", len(nodes), nameOrKey, strings.Join(keys, ", "))
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

	confidence := ConfidenceFromDerivation(ve.DerivationKind)
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
		Confidence:          confidence,
		Freshness:           1.0,
		TrustScore:          confidence,
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

	confidence := defaultEvidenceConfidence(input.Confidence)
	metadata := input.Metadata
	if metadata == "" {
		metadata = "{}"
	}
	polarity := input.Polarity
	if polarity == "" {
		polarity = "positive"
	}

	// Verify assertion at creation time if possible
	verificationStatus := "unverified"
	verificationReason := ""
	if input.AssertionKind != "" && input.Assertion != "" && input.FilePath != "" {
		vResult := verifyAssertionAtCreation(input.FilePath, input.AssertionKind, input.Assertion)
		verificationStatus = vResult.status
		verificationReason = vResult.reason
		if vResult.status == "verified" && vResult.locator != nil {
			input.LineStart = vResult.locator.LineStart
			input.LineEnd = vResult.locator.LineEnd
		}
		if vResult.confidence > 0 {
			confidence = vResult.confidence
		}
	}

	row := store.EvidenceRow{
		TargetKind:              "node",
		NodeID:                  nodeID,
		SourceKind:              input.SourceKind,
		RepoName:                input.RepoName,
		FilePath:                input.FilePath,
		LineStart:               input.LineStart,
		LineEnd:                 input.LineEnd,
		ColumnStart:             input.ColumnStart,
		ColumnEnd:               input.ColumnEnd,
		Locator:                 input.Locator,
		ExtractorID:             input.ExtractorID,
		ExtractorVersion:        input.ExtractorVersion,
		ASTRule:                 input.ASTRule,
		SnippetHash:             input.SnippetHash,
		CommitSHA:               input.CommitSHA,
		Confidence:              confidence,
		EvidencePolarity:        polarity,
		ValidFromRevisionID:     input.RevisionID,
		Assertion:               input.Assertion,
		AssertionKind:           input.AssertionKind,
		AssertionVersion:        input.AssertionVersion,
		VerificationStatus:      verificationStatus,
		VerificationReason:      verificationReason,
		Metadata:                metadata,
	}
	id, err := g.store.AddEvidence(row)
	if err != nil {
		return 0, fmt.Errorf("AddNodeEvidence: %w", err)
	}
	if err := g.RecalculateNodeTrust(nodeID); err != nil {
		return id, fmt.Errorf("AddNodeEvidence recalc: %w", err)
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

	confidence := defaultEvidenceConfidence(input.Confidence)
	metadata := input.Metadata
	if metadata == "" {
		metadata = "{}"
	}
	polarity := input.Polarity
	if polarity == "" {
		polarity = "positive"
	}

	// Verify assertion at creation time if possible
	verificationStatus := "unverified"
	verificationReason := ""
	if input.AssertionKind != "" && input.Assertion != "" && input.FilePath != "" {
		vResult := verifyAssertionAtCreation(input.FilePath, input.AssertionKind, input.Assertion)
		verificationStatus = vResult.status
		verificationReason = vResult.reason
		if vResult.status == "verified" && vResult.locator != nil {
			// Use verified locator instead of Claude's claimed location
			input.LineStart = vResult.locator.LineStart
			input.LineEnd = vResult.locator.LineEnd
		}
		if vResult.confidence > 0 {
			confidence = vResult.confidence
		}
	}

	row := store.EvidenceRow{
		TargetKind:              "edge",
		EdgeID:                  edge.EdgeID,
		SourceKind:              input.SourceKind,
		RepoName:                input.RepoName,
		FilePath:                input.FilePath,
		LineStart:               input.LineStart,
		LineEnd:                 input.LineEnd,
		ColumnStart:             input.ColumnStart,
		ColumnEnd:               input.ColumnEnd,
		Locator:                 input.Locator,
		ExtractorID:             input.ExtractorID,
		ExtractorVersion:        input.ExtractorVersion,
		ASTRule:                 input.ASTRule,
		SnippetHash:             input.SnippetHash,
		CommitSHA:               input.CommitSHA,
		Confidence:              confidence,
		EvidencePolarity:        polarity,
		ValidFromRevisionID:     input.RevisionID,
		Assertion:               input.Assertion,
		AssertionKind:           input.AssertionKind,
		AssertionVersion:        input.AssertionVersion,
		VerificationStatus:      verificationStatus,
		VerificationReason:      verificationReason,
		Metadata:                metadata,
	}
	id, err := g.store.AddEvidence(row)
	if err != nil {
		return 0, fmt.Errorf("AddEdgeEvidence: %w", err)
	}
	if err := g.RecalculateEdgeTrust(edge.EdgeID); err != nil {
		return id, fmt.Errorf("AddEdgeEvidence recalc: %w", err)
	}
	return id, nil
}

// creationVerifyResult holds the result of verifying an assertion at evidence creation time.
type creationVerifyResult struct {
	status     string // "verified", "unverified", "rejected"
	reason     string
	confidence float64
	locator    *verify.Locator
}

// verifyAssertionAtCreation checks an assertion against the actual file content
// at the time evidence is being created. This catches Claude hallucinations early.
func verifyAssertionAtCreation(filePath, assertionKind, assertion string) creationVerifyResult {
	if assertionKind == "" || assertion == "" || assertion == "{}" {
		return creationVerifyResult{status: "unverified", reason: "no assertion to verify"}
	}

	content, err := os.ReadFile(filePath)
	if err != nil {
		// File not readable — can't verify, but don't reject
		return creationVerifyResult{status: "unverified", reason: "file not readable: " + err.Error()}
	}

	reg := verify.DefaultRegistry()
	result, err := reg.Verify(assertionKind, content, json.RawMessage(assertion), nil)
	if err != nil {
		return creationVerifyResult{status: "unverified", reason: "verifier error: " + err.Error()}
	}

	switch result.Status {
	case "valid":
		return creationVerifyResult{
			status:     "verified",
			reason:     "verified at creation",
			confidence: result.Confidence,
			locator:    result.NewLocator,
		}
	case "missing":
		// Claude claimed something that doesn't exist — store but mark as rejected
		return creationVerifyResult{
			status:     "rejected",
			reason:     "assertion not found in file: " + result.Reason,
			confidence: 0.1,
		}
	case "ambiguous":
		return creationVerifyResult{
			status: "unverified",
			reason: "ambiguous match: " + result.Reason,
		}
	default: // "unsupported"
		return creationVerifyResult{
			status: "unverified",
			reason: "no verifier for " + assertionKind,
		}
	}
}
