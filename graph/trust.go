package graph

import (
	"strings"

	"github.com/alexdx2/chronicle-core/store"
)

// DerivationConfidence maps derivation_kind to a base confidence value.
// This is now the starting point, not the final confidence —
// evidence accumulation and caps determine the actual score.
var DerivationConfidence = map[string]float64{
	"hard":     0.50,
	"linked":   0.40,
	"inferred": 0.30,
	"unknown":  0.15,
}

// Asserter tier caps. The asserter (who produced the evidence) determines
// the maximum confidence a row can unlock — not the source_kind alone.
const (
	tierManual     = 0.95 // explicit human assertion
	tierRuntime    = 0.92 // observed at runtime / from live data
	tierStructural = 0.85 // deterministic structural extraction (AST, schemas, manifests)
	tierLLM        = 0.65 // LLM-asserted, unverified
	tierDerived    = 0.60 // system-derived (resolution, flow derivation, synthetic)
	tierDefault    = 0.65 // unknown asserter — treated like LLM
)

// Asserter classification tables.
var (
	// Manual/operator evidence: explicit human assertions
	manualSourceKinds = map[string]bool{
		"user_feedback": true, "manual": true,
	}
	// Runtime/data evidence: DB queries, HTTP calls, message bus
	runtimeSourceKinds = map[string]bool{
		"runtime": true, "prisma": true,
	}
	// Deterministic extractors: AST pass, manifest loader
	structuralExtractors = map[string]bool{
		"chronicle-ast": true, "chronicle:manifest": true,
	}
	// Source kinds that are structural by nature regardless of extractor:
	// declarative schemas and infra definitions.
	structuralSourceKinds = map[string]bool{
		"openapi": true, "graphql": true, "asyncapi": true,
		"avro": true, "proto": true, "schema_registry": true,
		"terraform": true, "k8s": true, "git": true, "ci": true,
	}
	// LLM extractors: agent-driven scan passes
	llmExtractors = map[string]bool{
		"chronicle-scan": true, "chronicle-auto": true,
	}
	// System-derived extractors: resolution and derivation pipelines
	derivedExtractors = map[string]bool{
		"chronicle:derive_flows": true, "chronicle:system": true,
	}
)

// asserterTier classifies a single evidence row by who asserted it and
// returns the confidence cap that row can unlock.
// Note: source_kind "file" alone says nothing about the asserter —
// LLM evidence is also file-anchored. Classification leans on extractor_id.
func asserterTier(e store.EvidenceRow) float64 {
	tier := tierDefault
	switch {
	case manualSourceKinds[e.SourceKind] || strings.HasPrefix(e.ExtractorID, "mcp:"):
		tier = tierManual
	case runtimeSourceKinds[e.SourceKind]:
		tier = tierRuntime
	case structuralExtractors[e.ExtractorID] || structuralSourceKinds[e.SourceKind]:
		tier = tierStructural
	case llmExtractors[e.ExtractorID]:
		tier = tierLLM
	case strings.HasPrefix(e.ExtractorID, "chronicle:resolve:") ||
		derivedExtractors[e.ExtractorID] || e.SourceKind == "synthetic":
		tier = tierDerived
	}
	// Verification promotion: a verified row counts as structural evidence,
	// regardless of who originally asserted it. Never demotes a higher tier.
	if e.VerificationStatus == "verified" && tier < tierStructural {
		tier = tierStructural
	}
	return tier
}

// ConfidenceCap returns the maximum confidence allowed given the evidence present.
// The cap is the best asserter tier among valid/revalidated positive,
// non-rejected evidence rows. Better asserters unlock higher caps.
func ConfidenceCap(evidence []store.EvidenceRow) float64 {
	best := 0.0
	for _, e := range evidence {
		if e.EvidencePolarity != "positive" {
			continue
		}
		if e.EvidenceStatus != "valid" && e.EvidenceStatus != "revalidated" {
			continue
		}
		if e.VerificationStatus == "rejected" {
			continue
		}
		if t := asserterTier(e); t > best {
			best = t
		}
	}
	if best == 0 {
		// No qualifying evidence — capped low.
		return tierDefault
	}
	return best
}

// ConfidenceFromDerivation returns the base confidence for a derivation kind.
func ConfidenceFromDerivation(kind string) float64 {
	if c, ok := DerivationConfidence[kind]; ok {
		return c
	}
	return 0.15
}

// CombineConfidence combines independent confidence values: 1 - Π(1 - ci).
func CombineConfidence(confidences []float64) float64 {
	if len(confidences) == 0 {
		return 0.0
	}
	product := 1.0
	for _, c := range confidences {
		product *= (1 - c)
	}
	return 1 - product
}

// verifiedConfidenceFloor is the minimum contribution of a mechanically
// verified evidence row. Verification replaces the asserter's self-reported
// uncertainty: once an assertion is confirmed against the source, the row is
// at least as trustworthy as a structural extraction.
const verifiedConfidenceFloor = tierStructural

// effectiveRowConfidence returns the confidence a single evidence row
// contributes to positive confidence. Verified rows are floored at the
// structural tier — a mechanically confirmed assertion is no longer the
// asserter's uncertainty.
func effectiveRowConfidence(e store.EvidenceRow) float64 {
	if e.VerificationStatus == "verified" && e.Confidence < verifiedConfidenceFloor {
		return verifiedConfidenceFloor
	}
	return e.Confidence
}

// EffectiveRowConfidence is the exported form of effectiveRowConfidence: the
// confidence a single evidence row is actually worth, floored at the
// structural tier once the row has been mechanically verified.
//
// It exists because the stored confidence column is the ASSERTER's number and
// verification never rewrites it — a reverified row can still read 0.1 on
// disk while every trust computation treats it as verifiedConfidenceFloor.
// Anything outside this package that derives a confidence from a raw evidence
// row (Pro's package tier, for one) must go through here, or the same edge
// gets two different confidences depending on who is asked.
func EffectiveRowConfidence(row store.EvidenceRow) float64 {
	return effectiveRowConfidence(row)
}

// PositiveConfidence computes combined confidence from valid/revalidated positive evidence.
// Rows whose verification_status is "rejected" or "missing" do not count as positive evidence.
// Verified rows contribute at least verifiedConfidenceFloor (see effectiveRowConfidence).
func PositiveConfidence(evidence []store.EvidenceRow) float64 {
	var confidences []float64
	for _, e := range evidence {
		if e.EvidencePolarity != "positive" {
			continue
		}
		if e.EvidenceStatus != "valid" && e.EvidenceStatus != "revalidated" {
			continue
		}
		if e.VerificationStatus == "rejected" || e.VerificationStatus == "missing" {
			continue
		}
		confidences = append(confidences, effectiveRowConfidence(e))
	}
	return CombineConfidence(confidences)
}

// NegativeConfidence computes combined confidence from valid negative evidence.
func NegativeConfidence(evidence []store.EvidenceRow) float64 {
	var confidences []float64
	for _, e := range evidence {
		if e.EvidencePolarity == "negative" && (e.EvidenceStatus == "valid" || e.EvidenceStatus == "revalidated") {
			confidences = append(confidences, e.Confidence)
		}
	}
	return CombineConfidence(confidences)
}

// BaseConfidence computes positive × (1 - negative).
func BaseConfidence(positive, negative float64) float64 {
	return positive * (1 - negative)
}

// FreshnessScore computes weighted average freshness from evidence,
// weighted by confidence. Caps at 0.6 if any evidence is stale.
func FreshnessScore(evidence []store.EvidenceRow) float64 {
	if len(evidence) == 0 {
		return 1.0
	}

	var weightedSum, totalWeight float64
	hasStale := false

	for _, e := range evidence {
		if e.EvidencePolarity != "positive" {
			continue
		}
		f := evidenceFreshness(e.EvidenceStatus)
		weightedSum += f * e.Confidence
		totalWeight += e.Confidence
		if e.EvidenceStatus == "stale" {
			hasStale = true
		}
	}

	if totalWeight == 0 {
		return 0.0
	}

	freshness := weightedSum / totalWeight

	if hasStale && freshness > 0.6 {
		freshness = 0.6
	}

	return freshness
}

// evidenceFreshness returns the freshness contribution for an evidence status.
func evidenceFreshness(status string) float64 {
	switch status {
	case "valid", "revalidated":
		return 1.0
	case "stale":
		return 0.5
	default: // invalidated, superseded
		return 0.0
	}
}

// TrustScore computes base_confidence × freshness.
func TrustScore(baseConfidence, freshness float64) float64 {
	return baseConfidence * freshness
}

// ComputeEdgeStatus determines edge status from its evidence.
func ComputeEdgeStatus(evidence []store.EvidenceRow) string {
	if len(evidence) == 0 {
		return "unknown"
	}

	hasValidPositive := false
	hasStalePositive := false
	allInvalidated := true
	negConf := NegativeConfidence(evidence)

	for _, e := range evidence {
		if e.EvidencePolarity == "positive" {
			switch e.EvidenceStatus {
			case "valid", "revalidated":
				hasValidPositive = true
				allInvalidated = false
			case "stale":
				hasStalePositive = true
				allInvalidated = false
			case "invalidated", "superseded":
				// counts toward allInvalidated
			}
		}
	}

	if negConf >= 0.8 {
		return "contradicted"
	}
	if hasValidPositive {
		return "active"
	}
	if hasStalePositive {
		return "stale"
	}
	if allInvalidated {
		return "removed"
	}
	return "unknown"
}

// ExistenceEvidence drops the rows that are not evidence that the thing
// exists. Today that is exactly source_kind "declared": a human ruling
// recorded beside a node (a verdict on a control, an owner for a field) says
// what SHOULD be true of it, never that it is there — nobody observed
// anything. Letting a declaration into the trust formula means writing an
// opinion about a scanned Prisma field moves that field's trust score, which
// is how a product decision quietly rewrites a code fact.
//
// Declarations are still stored, still queryable, still shown: they are just
// not counted as observations.
func ExistenceEvidence(evidence []store.EvidenceRow) []store.EvidenceRow {
	out := evidence[:0:0]
	for _, e := range evidence {
		if e.SourceKind == "declared" {
			continue
		}
		out = append(out, e)
	}
	return out
}

// ComputeTrust calculates all trust metrics from evidence for an edge or node.
// Confidence is capped based on the quality tier of available evidence.
// Declarations are filtered out first — see ExistenceEvidence.
func ComputeTrust(evidence []store.EvidenceRow) (confidence, freshness, trustScore float64, status string) {
	evidence = ExistenceEvidence(evidence)
	pos := PositiveConfidence(evidence)
	neg := NegativeConfidence(evidence)
	base := BaseConfidence(pos, neg)
	fresh := FreshnessScore(evidence)

	// If strong negative evidence, trust near 0.
	if neg >= 0.8 {
		fresh = 0.0
	}

	// Apply evidence-quality cap.
	cap := ConfidenceCap(evidence)
	if base > cap {
		base = cap
	}

	trust := TrustScore(base, fresh)
	st := ComputeEdgeStatus(evidence)
	return base, fresh, trust, st
}

// RecalculateEdgeTrust recomputes trust for an edge from its evidence.
func (g *Graph) RecalculateEdgeTrust(edgeID int64) error {
	evidence, err := g.store.ListEvidenceByEdge(edgeID)
	if err != nil {
		return err
	}

	// An edge backed by nothing but declarations has no observation to derive
	// trust from — leave what is there rather than deriving a number from
	// rows that were never observations.
	//
	// The len(evidence) > 0 half matters: an edge with NO evidence must still
	// be recomputed. Journal replay inserts 1.0/1.0/1.0 placeholders and
	// relies on RecalculateAllTrust to correct them, so skipping the
	// evidence-free case would leave a rebuilt graph claiming full trust in
	// edges nothing backs.
	if len(evidence) > 0 && len(ExistenceEvidence(evidence)) == 0 {
		return nil
	}

	confidence, freshness, trustScore, status := ComputeTrust(evidence)
	return g.store.UpdateEdgeTrust(edgeID, confidence, freshness, trustScore, status)
}

// RecalculateAllTrust recomputes trust for every current node and edge.
// Used after journal replay — trust is derived, never journaled.
func (g *Graph) RecalculateAllTrust() error {
	nodes, err := g.store.ListNodes(store.NodeFilter{})
	if err != nil {
		return err
	}
	for _, n := range nodes {
		if err := g.RecalculateNodeTrust(n.NodeID); err != nil {
			return err
		}
	}
	edges, err := g.store.ListEdges(store.EdgeFilter{})
	if err != nil {
		return err
	}
	for _, e := range edges {
		if err := g.RecalculateEdgeTrust(e.EdgeID); err != nil {
			return err
		}
	}
	return nil
}

// RecalculateNodeTrust recomputes trust for a node from its evidence.
func (g *Graph) RecalculateNodeTrust(nodeID int64) error {
	evidence, err := g.store.ListEvidenceByNode(nodeID)
	if err != nil {
		return err
	}

	if len(evidence) == 0 {
		// Nodes without evidence keep defaults (unchanged behaviour).
		return nil
	}
	// A node whose only evidence is a declaration is in the same position: a
	// ruling asserts nothing about existence, so there is nothing to derive
	// trust from. Keep what is stored rather than deriving from non-observations.
	if len(ExistenceEvidence(evidence)) == 0 {
		return nil
	}

	confidence, freshness, trustScore, status := ComputeTrust(evidence)
	// Map edge-specific statuses to valid node statuses.
	switch status {
	case "contradicted", "removed":
		status = "deleted"
	}
	// Preserve boundary markers: evidence supports an external node's existence,
	// not its locality. Only contradiction (→ deleted) may override "external".
	if status != "deleted" {
		if node, nerr := g.store.GetNodeByID(nodeID); nerr == nil && node.Status == "external" {
			status = "external"
		}
	}
	return g.store.UpdateNodeTrust(nodeID, confidence, freshness, trustScore, status)
}
