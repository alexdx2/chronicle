package llm

import (
	"math"
	"testing"
)

func TestCost_KnownAndUnknown(t *testing.T) {
	u := Usage{InputTokens: 1_000_000, OutputTokens: 1_000_000, CacheReadTokens: 1_000_000}
	usd, known := Cost("claude-sonnet-5-5", u)
	if !known || math.Abs(usd-12.2) > 1e-9 {
		t.Fatalf("sonnet = %v %v, want 12.2 true", usd, known)
	}
	if usd, known := Cost("claude-haiku-5-5", u); !known || math.Abs(usd-0.61) > 1e-9 {
		t.Fatalf("haiku = %v %v", usd, known)
	}
	if usd, known := Cost("claude-opus-5-5", u); !known || math.Abs(usd-24.2) > 1e-9 {
		t.Fatalf("opus = %v %v", usd, known)
	}
	if usd, known := Cost("gemini-2.5-flash", u); known || usd != 0 {
		t.Fatalf("gemini = %v %v, want 0 false", usd, known)
	}
}

func TestSetPrice(t *testing.T) {
	SetPrice("test-model", Price{InputPerM: 1, OutputPerM: 2, CacheReadPerM: 0.5})
	usd, known := Cost("test-model", Usage{InputTokens: 2_000_000, OutputTokens: 1_000_000, CacheReadTokens: 2_000_000})
	if !known || math.Abs(usd-5) > 1e-9 {
		t.Fatalf("got %v %v, want 5 true", usd, known)
	}
}
