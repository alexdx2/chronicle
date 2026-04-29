package graph

import (
	"math"
	"testing"

	"github.com/alexdx2/chronicle-core/store"
)

func TestConfidenceFromDerivation(t *testing.T) {
	tests := []struct {
		kind string
		want float64
	}{
		{"hard", 0.50},
		{"linked", 0.40},
		{"inferred", 0.30},
		{"unknown", 0.15},
		{"bogus", 0.15},
	}
	for _, tt := range tests {
		got := ConfidenceFromDerivation(tt.kind)
		if got != tt.want {
			t.Errorf("ConfidenceFromDerivation(%q) = %v, want %v", tt.kind, got, tt.want)
		}
	}
}

func TestCombineConfidence(t *testing.T) {
	tests := []struct {
		name string
		vals []float64
		want float64
	}{
		{"empty", nil, 0.0},
		{"single", []float64{0.95}, 0.95},
		{"two hard", []float64{0.95, 0.95}, 0.9975},
		{"hard+linked", []float64{0.95, 0.80}, 0.99},
		{"three", []float64{0.6, 0.6, 0.6}, 0.936},
	}
	for _, tt := range tests {
		got := CombineConfidence(tt.vals)
		if math.Abs(got-tt.want) > 0.001 {
			t.Errorf("CombineConfidence(%s) = %v, want %v", tt.name, got, tt.want)
		}
	}
}

func TestPositiveNegativeConfidence(t *testing.T) {
	evidence := []store.EvidenceRow{
		{EvidencePolarity: "positive", EvidenceStatus: "valid", Confidence: 0.95},
		{EvidencePolarity: "positive", EvidenceStatus: "stale", Confidence: 0.80},    // stale positive doesn't count
		{EvidencePolarity: "negative", EvidenceStatus: "valid", Confidence: 0.92},
		{EvidencePolarity: "positive", EvidenceStatus: "invalidated", Confidence: 0.9}, // invalidated doesn't count
	}

	pos := PositiveConfidence(evidence)
	if math.Abs(pos-0.95) > 0.001 {
		t.Errorf("PositiveConfidence = %v, want 0.95", pos)
	}

	neg := NegativeConfidence(evidence)
	if math.Abs(neg-0.92) > 0.001 {
		t.Errorf("NegativeConfidence = %v, want 0.92", neg)
	}

	base := BaseConfidence(pos, neg)
	// 0.95 * (1 - 0.92) = 0.95 * 0.08 = 0.076
	if math.Abs(base-0.076) > 0.001 {
		t.Errorf("BaseConfidence = %v, want ~0.076", base)
	}
}

func TestFreshnessScore(t *testing.T) {
	t.Run("all valid", func(t *testing.T) {
		evidence := []store.EvidenceRow{
			{EvidencePolarity: "positive", EvidenceStatus: "valid", Confidence: 0.95},
			{EvidencePolarity: "positive", EvidenceStatus: "revalidated", Confidence: 0.80},
		}
		got := FreshnessScore(evidence)
		if math.Abs(got-1.0) > 0.001 {
			t.Errorf("all valid freshness = %v, want 1.0", got)
		}
	})

	t.Run("mixed valid and stale", func(t *testing.T) {
		evidence := []store.EvidenceRow{
			{EvidencePolarity: "positive", EvidenceStatus: "valid", Confidence: 0.95},
			{EvidencePolarity: "positive", EvidenceStatus: "stale", Confidence: 0.80},
		}
		got := FreshnessScore(evidence)
		// weighted: (1.0*0.95 + 0.5*0.80) / (0.95+0.80) = 1.35/1.75 = 0.771
		// but capped at 0.6 because has stale
		if math.Abs(got-0.6) > 0.001 {
			t.Errorf("mixed freshness = %v, want 0.6 (capped)", got)
		}
	})

	t.Run("all stale", func(t *testing.T) {
		evidence := []store.EvidenceRow{
			{EvidencePolarity: "positive", EvidenceStatus: "stale", Confidence: 0.95},
		}
		got := FreshnessScore(evidence)
		if math.Abs(got-0.5) > 0.001 {
			t.Errorf("all stale freshness = %v, want 0.5", got)
		}
	})

	t.Run("negative evidence ignored for freshness", func(t *testing.T) {
		evidence := []store.EvidenceRow{
			{EvidencePolarity: "positive", EvidenceStatus: "valid", Confidence: 0.95},
			{EvidencePolarity: "negative", EvidenceStatus: "valid", Confidence: 0.92},
		}
		got := FreshnessScore(evidence)
		if math.Abs(got-1.0) > 0.001 {
			t.Errorf("freshness with negative = %v, want 1.0", got)
		}
	})

	t.Run("empty", func(t *testing.T) {
		got := FreshnessScore(nil)
		if got != 1.0 {
			t.Errorf("empty freshness = %v, want 1.0", got)
		}
	})

	t.Run("only negative evidence", func(t *testing.T) {
		evidence := []store.EvidenceRow{
			{EvidencePolarity: "negative", EvidenceStatus: "valid", Confidence: 0.92},
		}
		got := FreshnessScore(evidence)
		// No positive evidence → totalWeight=0 → returns 0.0
		if got != 0.0 {
			t.Errorf("only negative freshness = %v, want 0.0", got)
		}
	})
}

func TestComputeEdgeStatus(t *testing.T) {
	tests := []struct {
		name     string
		evidence []store.EvidenceRow
		want     string
	}{
		{"empty", nil, "unknown"},
		{"valid positive", []store.EvidenceRow{
			{EvidencePolarity: "positive", EvidenceStatus: "valid", Confidence: 0.95},
		}, "active"},
		{"stale only", []store.EvidenceRow{
			{EvidencePolarity: "positive", EvidenceStatus: "stale", Confidence: 0.95},
		}, "stale"},
		{"all invalidated", []store.EvidenceRow{
			{EvidencePolarity: "positive", EvidenceStatus: "invalidated", Confidence: 0.95},
		}, "removed"},
		{"strong negative", []store.EvidenceRow{
			{EvidencePolarity: "positive", EvidenceStatus: "valid", Confidence: 0.95},
			{EvidencePolarity: "negative", EvidenceStatus: "valid", Confidence: 0.85},
		}, "contradicted"},
		{"weak negative keeps active", []store.EvidenceRow{
			{EvidencePolarity: "positive", EvidenceStatus: "valid", Confidence: 0.95},
			{EvidencePolarity: "negative", EvidenceStatus: "valid", Confidence: 0.50},
		}, "active"},
	}
	for _, tt := range tests {
		got := ComputeEdgeStatus(tt.evidence)
		if got != tt.want {
			t.Errorf("ComputeEdgeStatus(%s) = %q, want %q", tt.name, got, tt.want)
		}
	}
}

func TestConfidenceCap(t *testing.T) {
	tests := []struct {
		name     string
		evidence []store.EvidenceRow
		wantCap  float64
	}{
		{"no evidence", nil, 0.65},
		{"LLM-only (webhook source)", []store.EvidenceRow{
			{EvidencePolarity: "positive", EvidenceStatus: "valid", SourceKind: "webhook", Confidence: 0.5},
		}, 0.65},
		{"code evidence (file source)", []store.EvidenceRow{
			{EvidencePolarity: "positive", EvidenceStatus: "valid", SourceKind: "file", Confidence: 0.9},
		}, 0.85},
		{"runtime evidence (prisma)", []store.EvidenceRow{
			{EvidencePolarity: "positive", EvidenceStatus: "valid", SourceKind: "prisma", Confidence: 0.95},
		}, 0.92},
		{"stale evidence ignored for cap", []store.EvidenceRow{
			{EvidencePolarity: "positive", EvidenceStatus: "stale", SourceKind: "file", Confidence: 0.9},
		}, 0.65},
		{"negative evidence ignored for cap", []store.EvidenceRow{
			{EvidencePolarity: "negative", EvidenceStatus: "valid", SourceKind: "file", Confidence: 0.9},
		}, 0.65},
	}
	for _, tt := range tests {
		got := ConfidenceCap(tt.evidence)
		if math.Abs(got-tt.wantCap) > 0.001 {
			t.Errorf("ConfidenceCap(%s) = %v, want %v", tt.name, got, tt.wantCap)
		}
	}
}

func TestComputeTrust(t *testing.T) {
	t.Run("healthy edge with code evidence", func(t *testing.T) {
		evidence := []store.EvidenceRow{
			{EvidencePolarity: "positive", EvidenceStatus: "valid", Confidence: 0.95, SourceKind: "file"},
		}
		conf, fresh, trust, status := ComputeTrust(evidence)
		// 0.95 capped at 0.85 (code evidence cap)
		if math.Abs(conf-0.85) > 0.001 {
			t.Errorf("confidence = %v, want 0.85 (capped)", conf)
		}
		if math.Abs(fresh-1.0) > 0.001 {
			t.Errorf("freshness = %v, want 1.0", fresh)
		}
		if math.Abs(trust-0.85) > 0.001 {
			t.Errorf("trust = %v, want 0.85", trust)
		}
		if status != "active" {
			t.Errorf("status = %q, want active", status)
		}
	})

	t.Run("stale edge", func(t *testing.T) {
		evidence := []store.EvidenceRow{
			{EvidencePolarity: "positive", EvidenceStatus: "stale", Confidence: 0.95},
		}
		conf, fresh, trust, status := ComputeTrust(evidence)
		// stale positive doesn't contribute to PositiveConfidence → conf=0
		if conf != 0.0 {
			t.Errorf("confidence = %v, want 0.0 (stale positive not counted)", conf)
		}
		if math.Abs(fresh-0.5) > 0.001 {
			t.Errorf("freshness = %v, want 0.5", fresh)
		}
		if trust != 0.0 {
			t.Errorf("trust = %v, want 0.0 (0 * 0.5)", trust)
		}
		if status != "stale" {
			t.Errorf("status = %q, want stale", status)
		}
	})

	t.Run("contradicted edge", func(t *testing.T) {
		evidence := []store.EvidenceRow{
			{EvidencePolarity: "positive", EvidenceStatus: "valid", Confidence: 0.95},
			{EvidencePolarity: "negative", EvidenceStatus: "valid", Confidence: 0.92},
		}
		conf, fresh, trust, status := ComputeTrust(evidence)
		// base = 0.95 * (1 - 0.92) = 0.076
		if math.Abs(conf-0.076) > 0.001 {
			t.Errorf("confidence = %v, want ~0.076", conf)
		}
		// strong negative → fresh = 0
		if fresh != 0.0 {
			t.Errorf("freshness = %v, want 0.0 (strong negative)", fresh)
		}
		if trust != 0.0 {
			t.Errorf("trust = %v, want 0.0", trust)
		}
		if status != "contradicted" {
			t.Errorf("status = %q, want contradicted", status)
		}
	})
}
