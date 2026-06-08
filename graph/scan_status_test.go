package graph

import (
	"testing"

	"github.com/alexdx2/chronicle-core/store"
)

func TestBuildExtractionStats(t *testing.T) {
	rows := []store.ExtractionRow{
		{Status: "resolved"},
		{Status: "resolved"},
		{Status: "extracted"},
		{Status: "type_only"},
		{Status: "failed"},
	}
	s := BuildExtractionStats(rows)
	if s.Total != 5 || s.Resolved != 2 || s.Extracted != 1 || s.TypeOnly != 1 || s.Failed != 1 {
		t.Fatalf("unexpected stats: %+v", s)
	}
}

func TestEffectiveCheckoutLimit(t *testing.T) {
	if EffectiveCheckoutLimit(0) != DefaultBatchSize {
		t.Fatalf("zero should default to batch size")
	}
	if EffectiveCheckoutLimit(100) != ServerMaxWave {
		t.Fatalf("100 should cap at %d, got %d", ServerMaxWave, EffectiveCheckoutLimit(100))
	}
}
