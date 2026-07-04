package graph

import (
	"math"
	"strings"
	"testing"
)

func TestMergeGraphComplexity(t *testing.T) {
	in := `{"complexity":{"cyclomatic":5,"loop_count":1,"loop_depth":2,"metric_sources":{"cyclomatic":"ast","loop_count":"ast","loop_depth":"ast"}},"other":"keep"}`
	out, err := mergeGraphComplexity(in, graphComplexity{Recursive: true, TransitiveLoopDepth: 3})
	if err != nil {
		t.Fatalf("mergeGraphComplexity: %v", err)
	}
	// Tier-A metrics preserved, graph metrics set.
	m, ok := complexityFromMetadata(out)
	if !ok {
		t.Fatalf("complexity block missing in %q", out)
	}
	if m.Cyclomatic != 5 || m.LoopDepth != 2 {
		t.Fatalf("Tier-A metrics not preserved: %+v", m)
	}
	if !m.Recursive || m.TransitiveLoopDepth != 3 {
		t.Fatalf("graph metrics not set: %+v", m)
	}
	// Unrelated keys preserved; metric_sources tagged graph for derived metrics.
	if !strings.Contains(out, `"other":"keep"`) {
		t.Fatalf("unrelated key dropped: %q", out)
	}
	if !strings.Contains(out, `"transitive_loop_depth":"graph"`) {
		t.Fatalf("metric_sources not tagged for derived metrics: %q", out)
	}
}

func TestHotPathReason(t *testing.T) {
	// Untested (when actually examined) wins over everything.
	if got := hotPathReason(ComplexityMetrics{Smells: []string{"alloc_in_loop"}}, 0.1, 5, 99, true, false); !strings.Contains(got, "untested") {
		t.Fatalf("untested should win, got %q", got)
	}
	// A covered node must never be called untested; smell comes next.
	if got := hotPathReason(ComplexityMetrics{Smells: []string{"alloc_in_loop"}, Recursive: true}, 0.1, 5, 99, true, true); !strings.Contains(got, "alloc_in_loop") {
		t.Fatalf("smell should win for covered node, got %q", got)
	}
	// Unexamined node (test pass never looked): no untested claim, smell wins.
	if got := hotPathReason(ComplexityMetrics{Smells: []string{"alloc_in_loop"}, Recursive: true}, 0.1, 5, 99, false, false); !strings.Contains(got, "alloc_in_loop") {
		t.Fatalf("smell should win when tests unexamined, got %q", got)
	}
	// High churn wins over stale and structure.
	if got := hotPathReason(ComplexityMetrics{Recursive: true}, 0.1, 5, 15, false, false); !strings.Contains(got, "churn") {
		t.Fatalf("churn should win over stale, got %q", got)
	}
	// Stale wins over structural factors when churn is low.
	if got := hotPathReason(ComplexityMetrics{Recursive: true, TransitiveLoopDepth: 9}, 0.1, 5, 0, false, false); !strings.Contains(got, "stale") {
		t.Fatalf("stale should win, got %q", got)
	}
	// Fresh + recursive -> recursive tag.
	if got := hotPathReason(ComplexityMetrics{Recursive: true}, 1.0, 5, 0, false, false); !strings.Contains(got, "recursive=true") {
		t.Fatalf("recursive tag expected, got %q", got)
	}
	// Fresh + high transitive depth (no recursion) -> transitive tag.
	if got := hotPathReason(ComplexityMetrics{TransitiveLoopDepth: 4}, 1.0, 5, 0, false, false); !strings.Contains(got, "transitive_loop_depth=4") {
		t.Fatalf("transitive tag expected, got %q", got)
	}
	// Fresh, no structural recursion/depth -> highly-connected fallback.
	if got := hotPathReason(ComplexityMetrics{Cyclomatic: 25}, 1.0, 7, 0, false, false); !strings.Contains(got, "highly connected") {
		t.Fatalf("connected fallback expected, got %q", got)
	}
}

func TestComputeGraphComplexity(t *testing.T) {
	t.Run("chain accumulates transitive depth", func(t *testing.T) {
		ld := map[string]int{"A": 1, "B": 1}
		calls := []callEdge{{From: "A", To: "B", Confidence: 0.9}}
		got := computeGraphComplexity(ld, calls)
		if got["A"].TransitiveLoopDepth != 2 {
			t.Fatalf("A.transitive = %d, want 2", got["A"].TransitiveLoopDepth)
		}
		if got["B"].TransitiveLoopDepth != 1 {
			t.Fatalf("B.transitive = %d, want 1", got["B"].TransitiveLoopDepth)
		}
		if got["A"].Recursive || got["B"].Recursive {
			t.Fatalf("nothing should be recursive: A=%v B=%v", got["A"].Recursive, got["B"].Recursive)
		}
		if math.Abs(got["A"].BindingConfidence-0.9) > 1e-9 {
			t.Fatalf("A.binding = %v, want 0.9", got["A"].BindingConfidence)
		}
		if math.Abs(got["B"].BindingConfidence-1.0) > 1e-9 {
			t.Fatalf("B.binding = %v, want 1.0 (leaf)", got["B"].BindingConfidence)
		}
	})

	t.Run("self recursion flags recursive, depth = own loop_depth", func(t *testing.T) {
		ld := map[string]int{"A": 2}
		calls := []callEdge{{From: "A", To: "A", Confidence: 0.8}}
		got := computeGraphComplexity(ld, calls)
		if !got["A"].Recursive {
			t.Fatalf("A should be recursive")
		}
		if got["A"].TransitiveLoopDepth != 2 {
			t.Fatalf("A.transitive = %d, want 2", got["A"].TransitiveLoopDepth)
		}
		if math.Abs(got["A"].BindingConfidence-0.8) > 1e-9 {
			t.Fatalf("A.binding = %v, want 0.8 (cycle edge)", got["A"].BindingConfidence)
		}
	})

	t.Run("mutual recursion: both recursive, depth = max member loop_depth", func(t *testing.T) {
		ld := map[string]int{"A": 1, "B": 3}
		calls := []callEdge{{From: "A", To: "B", Confidence: 0.9}, {From: "B", To: "A", Confidence: 0.7}}
		got := computeGraphComplexity(ld, calls)
		if !got["A"].Recursive || !got["B"].Recursive {
			t.Fatalf("both should be recursive: A=%v B=%v", got["A"].Recursive, got["B"].Recursive)
		}
		if got["A"].TransitiveLoopDepth != 3 || got["B"].TransitiveLoopDepth != 3 {
			t.Fatalf("SCC depth: A=%d B=%d, want 3,3", got["A"].TransitiveLoopDepth, got["B"].TransitiveLoopDepth)
		}
		if math.Abs(got["A"].BindingConfidence-0.7) > 1e-9 {
			t.Fatalf("A.binding = %v, want 0.7 (min cycle edge)", got["A"].BindingConfidence)
		}
	})

	t.Run("diamond takes max over callees", func(t *testing.T) {
		ld := map[string]int{"A": 0, "B": 2, "C": 1}
		calls := []callEdge{{From: "A", To: "B", Confidence: 0.95}, {From: "A", To: "C", Confidence: 0.6}}
		got := computeGraphComplexity(ld, calls)
		if got["A"].TransitiveLoopDepth != 2 {
			t.Fatalf("A.transitive = %d, want 2 (0 + max(2,1))", got["A"].TransitiveLoopDepth)
		}
		if math.Abs(got["A"].BindingConfidence-0.95) > 1e-9 {
			t.Fatalf("A.binding = %v, want 0.95 (path to deeper callee B)", got["A"].BindingConfidence)
		}
	})
}

func TestNormComplexity(t *testing.T) {
	// New 4-term formula: 0.4·tld/5 + 0.25·cyc/20 + 0.2·cog/15 + 0.15·smells/3.
	cases := []struct {
		name                 string
		tld, cyc, cog, smell int
		want                 float64
	}{
		{"zero metrics give zero", 0, 0, 0, 0, 0},
		{"all four at caps -> 1", 5, 20, 15, 3, 1.0},
		{"over caps clamps to 1", 10, 40, 30, 6, 1.0},
		{"graph term only", 5, 0, 0, 0, 0.4},
		{"cyclomatic term only", 0, 20, 0, 0, 0.25},
		{"cognitive term only", 0, 0, 15, 0, 0.2},
		{"smells term only", 0, 0, 0, 3, 0.15},
		{"mixed partial", 2, 10, 0, 0, 0.285},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			smells := make([]string, c.smell)
			got := normComplexity(ComplexityMetrics{
				TransitiveLoopDepth: c.tld, Cyclomatic: c.cyc, Cognitive: c.cog, Smells: smells,
			})
			if math.Abs(got-c.want) > 1e-9 {
				t.Fatalf("normComplexity(tld=%d,cyc=%d,cog=%d,smells=%d) = %v, want %v",
					c.tld, c.cyc, c.cog, c.smell, got, c.want)
			}
		})
	}
}
