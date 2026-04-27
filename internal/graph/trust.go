package graph

import (
	"github.com/anthropics/depbot/internal/store"
)

// DerivationConfidence maps derivation_kind to a fixed confidence value.
var DerivationConfidence = map[string]float64{
	"hard":     0.95,
	"linked":   0.80,
	"inferred": 0.60,
	"unknown":  0.40,
}

// ConfidenceFromDerivation returns the confidence for a derivation kind.
func ConfidenceFromDerivation(kind string) float64 {
	if c, ok := DerivationConfidence[kind]; ok {
		return c
	}
	return 0.40
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

// PositiveConfidence computes combined confidence from valid/revalidated positive evidence.
func PositiveConfidence(evidence []store.EvidenceRow) float64 {
	var confidences []float64
	for _, e := range evidence {
		if e.EvidencePolarity == "positive" && (e.EvidenceStatus == "valid" || e.EvidenceStatus == "revalidated") {
			confidences = append(confidences, e.Confidence)
		}
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

// ComputeTrust calculates all trust metrics from evidence for an edge or node.
func ComputeTrust(evidence []store.EvidenceRow) (confidence, freshness, trustScore float64, status string) {
	pos := PositiveConfidence(evidence)
	neg := NegativeConfidence(evidence)
	base := BaseConfidence(pos, neg)
	fresh := FreshnessScore(evidence)

	// If strong negative evidence, trust near 0.
	if neg >= 0.8 {
		fresh = 0.0
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

	confidence, freshness, trustScore, status := ComputeTrust(evidence)
	return g.store.UpdateEdgeTrust(edgeID, confidence, freshness, trustScore, status)
}

// RecalculateNodeTrust recomputes trust for a node from its evidence.
func (g *Graph) RecalculateNodeTrust(nodeID int64) error {
	evidence, err := g.store.ListEvidenceByNode(nodeID)
	if err != nil {
		return err
	}

	if len(evidence) == 0 {
		// Nodes without evidence keep defaults.
		return nil
	}

	confidence, freshness, trustScore, status := ComputeTrust(evidence)
	// Map edge-specific statuses to valid node statuses.
	switch status {
	case "contradicted", "removed":
		status = "deleted"
	}
	return g.store.UpdateNodeTrust(nodeID, confidence, freshness, trustScore, status)
}
