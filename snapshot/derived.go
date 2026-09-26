package snapshot

// derivedSourceKinds is the closed set of evidence source_kinds a later,
// deterministic pass over a checkout regenerates from scratch (complexity,
// git churn/co-change, test-link, near-duplicate detection — see
// CLAUDE.md's "Signal pipeline" table and the design spec's Global
// Constraints, §4.7/§2). None of it can be missing from a fresh clone: it
// is recomputed lazily on load (Task 4), so keeping it in the committed
// snapshot would only be redundant bytes that can also go stale relative to
// the checkout they were measured against.
var derivedSourceKinds = map[string]bool{
	"complexity-ast":     true,
	"complexity-smells":  true,
	"complexity-graph":   true,
	"churn-git":          true,
	"coupling-git":       true,
	"test-link":          true,
	"similarity-minhash": true,
}

// derivedFlowExtractor is DeriveFlows' extractor id. Flow evidence isn't
// distinguished by an unusual source_kind — it is distinguished by having
// been produced by this one deterministic pass over the graph itself
// (re-run on every Load, Task 4), so it is keyed by extractor_id here
// rather than added to derivedSourceKinds.
const derivedFlowExtractor = "chronicle:derive_flows"

// derivedEdgeTypes is the closed set of edge types that only ever exist as
// an analytical by-product of a checkout pass (git co-change, near-
// duplicate detection) and never as an extracted architectural fact — the
// same two types the registry's traversal policy already excludes from
// impact/path blast radius (structural_edge_types). Unlike derived
// evidence, which can leave its owning node or edge behind with less
// provenance, a derived-type edge is dropped whole: the edge itself, not
// just something attached to it, is the recomputable part.
var derivedEdgeTypes = map[string]bool{
	"CHANGES_WITH": true,
	"SIMILAR_TO":   true,
}

// IsDerivedEvidence reports whether one evidence row is derived — computed
// from a checkout and the graph rather than extracted from source — and so
// must be left out of the committed snapshot. Either closed criterion is
// sufficient on its own: a signal-pipeline source_kind, or DeriveFlows'
// extractor_id.
func IsDerivedEvidence(sourceKind, extractorID string) bool {
	return derivedSourceKinds[sourceKind] || extractorID == derivedFlowExtractor
}

// IsDerivedEdgeType reports whether edgeType is one of the analytical edge
// types recomputed from a checkout, never extracted — the whole edge is
// left out of the snapshot, not just its evidence.
func IsDerivedEdgeType(edgeType string) bool {
	return derivedEdgeTypes[edgeType]
}
