package graph

import (
	"math"
	"testing"

	"github.com/alexdx2/chronicle-core/store"
	"github.com/alexdx2/chronicle-core/validate"
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
			{EvidencePolarity: "positive", EvidenceStatus: "valid", Confidence: 0.80},
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
		{"manual tier (user_feedback source)", []store.EvidenceRow{
			{EvidencePolarity: "positive", EvidenceStatus: "valid", SourceKind: "user_feedback", Confidence: 0.9},
		}, 0.95},
		{"manual tier (mcp: extractor prefix)", []store.EvidenceRow{
			{EvidencePolarity: "positive", EvidenceStatus: "valid", SourceKind: "file", ExtractorID: "mcp:claude", Confidence: 0.9},
		}, 0.95},
		{"runtime tier (prisma source)", []store.EvidenceRow{
			{EvidencePolarity: "positive", EvidenceStatus: "valid", SourceKind: "prisma", Confidence: 0.95},
		}, 0.92},
		{"structural tier (chronicle-ast extractor on file)", []store.EvidenceRow{
			{EvidencePolarity: "positive", EvidenceStatus: "valid", SourceKind: "file", ExtractorID: "chronicle-ast", Confidence: 0.9},
		}, 0.85},
		{"structural tier (openapi source, any extractor)", []store.EvidenceRow{
			{EvidencePolarity: "positive", EvidenceStatus: "valid", SourceKind: "openapi", ExtractorID: "chronicle-scan", Confidence: 0.9},
		}, 0.85},
		{"llm tier (chronicle-scan on file — not structural)", []store.EvidenceRow{
			{EvidencePolarity: "positive", EvidenceStatus: "valid", SourceKind: "file", ExtractorID: "chronicle-scan", Confidence: 0.9},
		}, 0.65},
		{"derived tier (chronicle:resolve: prefix)", []store.EvidenceRow{
			{EvidencePolarity: "positive", EvidenceStatus: "valid", SourceKind: "file", ExtractorID: "chronicle:resolve:endpoint", Confidence: 0.9},
		}, 0.60},
		{"derived tier (synthetic source)", []store.EvidenceRow{
			{EvidencePolarity: "positive", EvidenceStatus: "valid", SourceKind: "synthetic", Confidence: 0.9},
		}, 0.60},
		{"unknown asserter defaults to 0.65 (file source alone is not structural)", []store.EvidenceRow{
			{EvidencePolarity: "positive", EvidenceStatus: "valid", SourceKind: "file", ExtractorID: "claude-code", Confidence: 0.9},
		}, 0.65},
		{"webhook source defaults to 0.65", []store.EvidenceRow{
			{EvidencePolarity: "positive", EvidenceStatus: "valid", SourceKind: "webhook", Confidence: 0.5},
		}, 0.65},
		{"best tier wins (llm + structural)", []store.EvidenceRow{
			{EvidencePolarity: "positive", EvidenceStatus: "valid", SourceKind: "file", ExtractorID: "chronicle-scan", Confidence: 0.6},
			{EvidencePolarity: "positive", EvidenceStatus: "valid", SourceKind: "file", ExtractorID: "chronicle-ast", Confidence: 0.6},
		}, 0.85},
		{"stale evidence ignored for cap", []store.EvidenceRow{
			{EvidencePolarity: "positive", EvidenceStatus: "stale", SourceKind: "file", ExtractorID: "chronicle-ast", Confidence: 0.9},
		}, 0.65},
		{"negative evidence ignored for cap", []store.EvidenceRow{
			{EvidencePolarity: "negative", EvidenceStatus: "valid", SourceKind: "file", ExtractorID: "chronicle-ast", Confidence: 0.9},
		}, 0.65},
	}
	for _, tt := range tests {
		got := ConfidenceCap(tt.evidence)
		if math.Abs(got-tt.wantCap) > 0.001 {
			t.Errorf("ConfidenceCap(%s) = %v, want %v", tt.name, got, tt.wantCap)
		}
	}
}

func TestVerificationPromotion(t *testing.T) {
	t.Run("verified LLM row promotes to structural cap", func(t *testing.T) {
		evidence := []store.EvidenceRow{
			{EvidencePolarity: "positive", EvidenceStatus: "valid", SourceKind: "file",
				ExtractorID: "chronicle-scan", VerificationStatus: "verified", Confidence: 0.9},
		}
		got := ConfidenceCap(evidence)
		if math.Abs(got-0.85) > 0.001 {
			t.Errorf("verified LLM cap = %v, want 0.85", got)
		}
	})

	t.Run("verified derived row promotes to structural cap", func(t *testing.T) {
		evidence := []store.EvidenceRow{
			{EvidencePolarity: "positive", EvidenceStatus: "valid", SourceKind: "file",
				ExtractorID: "chronicle:resolve:endpoint", VerificationStatus: "verified", Confidence: 0.9},
		}
		got := ConfidenceCap(evidence)
		if math.Abs(got-0.85) > 0.001 {
			t.Errorf("verified derived cap = %v, want 0.85", got)
		}
	})

	t.Run("verification never demotes a higher tier", func(t *testing.T) {
		evidence := []store.EvidenceRow{
			{EvidencePolarity: "positive", EvidenceStatus: "valid", SourceKind: "user_feedback",
				VerificationStatus: "verified", Confidence: 0.9},
		}
		got := ConfidenceCap(evidence)
		if math.Abs(got-0.95) > 0.001 {
			t.Errorf("verified manual cap = %v, want 0.95", got)
		}
	})
}

func TestVerificationConfidenceFloor(t *testing.T) {
	t.Run("verified low-confidence row floors at structural tier", func(t *testing.T) {
		// A mechanically confirmed assertion is no longer the asserter's
		// uncertainty: a verified 0.44 row contributes 0.85, not 0.44.
		evidence := []store.EvidenceRow{
			{EvidencePolarity: "positive", EvidenceStatus: "valid", SourceKind: "file",
				ExtractorID: "chronicle-scan", VerificationStatus: "verified", Confidence: 0.44},
		}
		pos := PositiveConfidence(evidence)
		if math.Abs(pos-0.85) > 0.001 {
			t.Errorf("PositiveConfidence(verified 0.44) = %v, want 0.85", pos)
		}
		conf, _, trust, _ := ComputeTrust(evidence)
		if math.Abs(conf-0.85) > 0.001 {
			t.Errorf("base confidence = %v, want 0.85 (floor meets promoted cap)", conf)
		}
		if math.Abs(trust-0.85) > 0.001 {
			t.Errorf("trust = %v, want 0.85", trust)
		}
	})

	t.Run("unverified low-confidence row keeps its own confidence", func(t *testing.T) {
		evidence := []store.EvidenceRow{
			{EvidencePolarity: "positive", EvidenceStatus: "valid", SourceKind: "file",
				ExtractorID: "chronicle-scan", Confidence: 0.44},
		}
		pos := PositiveConfidence(evidence)
		if math.Abs(pos-0.44) > 0.001 {
			t.Errorf("PositiveConfidence(unverified 0.44) = %v, want 0.44", pos)
		}
		conf, _, trust, _ := ComputeTrust(evidence)
		if math.Abs(conf-0.44) > 0.001 {
			t.Errorf("base confidence = %v, want 0.44", conf)
		}
		if math.Abs(trust-0.44) > 0.001 {
			t.Errorf("trust = %v, want 0.44", trust)
		}
	})

	t.Run("verified row above the floor is unchanged", func(t *testing.T) {
		evidence := []store.EvidenceRow{
			{EvidencePolarity: "positive", EvidenceStatus: "valid", SourceKind: "user_feedback",
				VerificationStatus: "verified", Confidence: 0.9},
		}
		pos := PositiveConfidence(evidence)
		if math.Abs(pos-0.9) > 0.001 {
			t.Errorf("PositiveConfidence(verified 0.9) = %v, want 0.9", pos)
		}
	})
}

func TestRejectionExclusion(t *testing.T) {
	t.Run("rejected row excluded from cap scan", func(t *testing.T) {
		evidence := []store.EvidenceRow{
			// rejected structural row must not unlock the structural cap
			{EvidencePolarity: "positive", EvidenceStatus: "valid", SourceKind: "file",
				ExtractorID: "chronicle-ast", VerificationStatus: "rejected", Confidence: 0.9},
			{EvidencePolarity: "positive", EvidenceStatus: "valid", SourceKind: "file",
				ExtractorID: "chronicle-scan", Confidence: 0.6},
		}
		got := ConfidenceCap(evidence)
		if math.Abs(got-0.65) > 0.001 {
			t.Errorf("cap with rejected structural row = %v, want 0.65", got)
		}
	})

	t.Run("rejected row excluded from PositiveConfidence", func(t *testing.T) {
		evidence := []store.EvidenceRow{
			{EvidencePolarity: "positive", EvidenceStatus: "valid", VerificationStatus: "rejected", Confidence: 0.9},
			{EvidencePolarity: "positive", EvidenceStatus: "valid", Confidence: 0.6},
		}
		got := PositiveConfidence(evidence)
		if math.Abs(got-0.6) > 0.001 {
			t.Errorf("PositiveConfidence with rejected row = %v, want 0.6", got)
		}
	})

	t.Run("all rows rejected gives zero positive confidence", func(t *testing.T) {
		evidence := []store.EvidenceRow{
			{EvidencePolarity: "positive", EvidenceStatus: "valid", VerificationStatus: "rejected", Confidence: 0.9},
		}
		got := PositiveConfidence(evidence)
		if got != 0.0 {
			t.Errorf("PositiveConfidence all rejected = %v, want 0.0", got)
		}
	})
}

func TestPositiveConfidence_SkipsMissing(t *testing.T) {
	rows := []store.EvidenceRow{{
		EvidencePolarity:   "positive",
		EvidenceStatus:     "valid",
		VerificationStatus: "missing",
		Confidence:         0.9,
	}}
	if got := PositiveConfidence(rows); got != 0 {
		t.Fatalf("missing assertion must not add positive confidence; got %f", got)
	}
}

func TestCorroborationCrossesLLMCap(t *testing.T) {
	// Two LLM rows at 0.6 plus one AST row at 0.6:
	// combined = 1 - (1-0.6)^3 = 0.936, structural tier present → capped at 0.85.
	evidence := []store.EvidenceRow{
		{EvidencePolarity: "positive", EvidenceStatus: "valid", SourceKind: "file", ExtractorID: "chronicle-scan", Confidence: 0.6},
		{EvidencePolarity: "positive", EvidenceStatus: "valid", SourceKind: "file", ExtractorID: "chronicle-scan", Confidence: 0.6},
		{EvidencePolarity: "positive", EvidenceStatus: "valid", SourceKind: "file", ExtractorID: "chronicle-ast", Confidence: 0.6},
	}

	pos := PositiveConfidence(evidence)
	if math.Abs(pos-0.936) > 0.001 {
		t.Fatalf("PositiveConfidence = %v, want 0.936", pos)
	}

	conf, _, _, _ := ComputeTrust(evidence)
	if math.Abs(conf-0.85) > 0.001 {
		t.Errorf("corroborated confidence = %v, want 0.85 (crosses 0.65, capped by structural)", conf)
	}

	// Without the AST row, the same combined evidence stays at the LLM cap.
	llmOnly := evidence[:2]
	conf, _, _, _ = ComputeTrust(llmOnly)
	if math.Abs(conf-0.65) > 0.001 {
		t.Errorf("llm-only confidence = %v, want 0.65 (LLM cap)", conf)
	}
}

func TestComputeTrust(t *testing.T) {
	t.Run("healthy edge with code evidence", func(t *testing.T) {
		evidence := []store.EvidenceRow{
			{EvidencePolarity: "positive", EvidenceStatus: "valid", Confidence: 0.95, SourceKind: "file", ExtractorID: "chronicle-ast"},
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

// TestDeclaredEvidenceIsNotExistenceEvidence pins the rule: a recorded human
// ruling ("this control asks the user, the owner is the shop") never moves the
// trust of the thing it is about. Declarations say what should be true; only
// observations say what is there.
func TestDeclaredEvidenceIsNotExistenceEvidence(t *testing.T) {
	observed := store.EvidenceRow{
		SourceKind: "prisma", ExtractorID: "chronicle-ast",
		EvidencePolarity: "positive", EvidenceStatus: "valid", Confidence: 0.9,
	}
	declared := store.EvidenceRow{
		SourceKind: "declared", ExtractorID: "okeep-surface",
		EvidencePolarity: "positive", EvidenceStatus: "valid", Confidence: 0.95,
	}

	wantC, wantF, wantT, wantS := ComputeTrust([]store.EvidenceRow{observed})
	gotC, gotF, gotT, gotS := ComputeTrust([]store.EvidenceRow{observed, declared})
	if gotC != wantC || gotF != wantF || gotT != wantT || gotS != wantS {
		t.Fatalf("declaration moved trust: %v/%v/%v/%q → %v/%v/%v/%q",
			wantC, wantF, wantT, wantS, gotC, gotF, gotT, gotS)
	}

	if n := len(ExistenceEvidence([]store.EvidenceRow{observed, declared})); n != 1 {
		t.Fatalf("ExistenceEvidence kept %d rows, want 1", n)
	}
	if n := len(ExistenceEvidence([]store.EvidenceRow{declared})); n != 0 {
		t.Fatalf("a declaration alone is %d rows of existence evidence, want 0", n)
	}
}

// TestRecalculateEdgeTrustHandlesEmptyAndDeclaredOnly separates the two cases
// the declaration rule must not conflate.
//
// An edge with NO evidence must still be recomputed: journal replay inserts
// 1.0/1.0/1.0 placeholders and leans on RecalculateAllTrust to correct them,
// so a skip here would leave a rebuilt graph claiming full trust in an edge
// nothing backs. An edge whose only evidence is a DECLARATION must be left
// alone: a ruling is not an observation, and deriving a number from it is the
// bug the rule exists to prevent.
func TestRecalculateEdgeTrustHandlesEmptyAndDeclaredOnly(t *testing.T) {
	g := setupGraphDefaults(t)
	revID := makeRevision(t, g)

	mkEdge := func(suffix string) (int64, string) {
		t.Helper()
		from := "code:controller:test-domain:a" + suffix
		to := "code:provider:test-domain:b" + suffix
		for _, n := range []struct{ key, nodeType, name string }{
			{from, "controller", "A" + suffix},
			{to, "provider", "B" + suffix},
		} {
			if _, err := g.UpsertNode(validate.NodeInput{
				NodeKey: n.key, Layer: "code", NodeType: n.nodeType,
				DomainKey: "test-domain", Name: n.name,
			}, revID); err != nil {
				t.Fatalf("UpsertNode %s: %v", n.key, err)
			}
		}
		key := validate.BuildEdgeKey(from, to, "INJECTS")
		id, err := g.UpsertEdge(validate.EdgeInput{
			FromNodeKey: from, ToNodeKey: to, EdgeType: "INJECTS",
			DerivationKind: "hard", FromLayer: "code", ToLayer: "code",
		}, revID)
		if err != nil {
			t.Fatalf("UpsertEdge: %v", err)
		}
		return id, key
	}

	read := func(key string) *store.EdgeRow {
		t.Helper()
		row, err := g.Store().GetEdgeByKey(key)
		if err != nil {
			t.Fatalf("GetEdgeByKey %s: %v", key, err)
		}
		return row
	}

	// --- no evidence at all: recomputed, exactly as before the rule ------
	emptyID, emptyKey := mkEdge("empty")
	// Stand in for what journal replay writes before RecalculateAllTrust runs.
	if err := g.Store().UpdateEdgeTrust(emptyID, 1.0, 1.0, 1.0, "active"); err != nil {
		t.Fatal(err)
	}
	if err := g.RecalculateEdgeTrust(emptyID); err != nil {
		t.Fatalf("RecalculateEdgeTrust: %v", err)
	}
	// The pre-change formula on an empty evidence list: positive 0, negative 0,
	// base 0 (under the 0.65 default cap), freshness 1.0, trust 0, status unknown.
	wantC, wantF, wantT, wantS := ComputeTrust(nil)
	if wantC != 0 || wantF != 1 || wantT != 0 || wantS != "unknown" {
		t.Fatalf("ComputeTrust(nil) drifted: %v/%v/%v/%q", wantC, wantF, wantT, wantS)
	}
	got := read(emptyKey)
	if got.Confidence != wantC || got.Freshness != wantF || got.TrustScore != wantT {
		t.Fatalf("evidence-free edge kept placeholders: conf=%v fresh=%v trust=%v, want %v/%v/%v",
			got.Confidence, got.Freshness, got.TrustScore, wantC, wantF, wantT)
	}

	// --- only a declaration: left exactly as stored ----------------------
	declaredID, declaredKey := mkEdge("declared")
	if _, err := g.AddEdgeEvidence(declaredKey, validate.EvidenceInput{
		TargetKind: "edge", SourceKind: "declared",
		ExtractorID: "okeep-surface", ExtractorVersion: "1",
		Assertion: `{"verdict":"ask"}`, AssertionKind: "decision",
		Confidence: 0.95, RevisionID: revID,
	}); err != nil {
		t.Fatalf("AddEdgeEvidence: %v", err)
	}
	if err := g.Store().UpdateEdgeTrust(declaredID, 0.5, 0.5, 0.25, "active"); err != nil {
		t.Fatal(err)
	}
	if err := g.RecalculateEdgeTrust(declaredID); err != nil {
		t.Fatalf("RecalculateEdgeTrust: %v", err)
	}
	got = read(declaredKey)
	if got.Confidence != 0.5 || got.Freshness != 0.5 || got.TrustScore != 0.25 {
		t.Fatalf("a declaration moved edge trust: conf=%v fresh=%v trust=%v, want 0.5/0.5/0.25",
			got.Confidence, got.Freshness, got.TrustScore)
	}
}

// TestRecalculateNodeTrustHandlesEmptyAndDeclaredOnly: a node with no evidence
// kept its defaults before the declaration rule and must keep them still; a
// node whose only evidence is a declaration keeps them too.
func TestRecalculateNodeTrustHandlesEmptyAndDeclaredOnly(t *testing.T) {
	g := setupGraphDefaults(t)
	revID := makeRevision(t, g)

	mkNode := func(suffix string) (int64, string) {
		t.Helper()
		key := "code:controller:test-domain:n" + suffix
		id, err := g.UpsertNode(validate.NodeInput{
			NodeKey: key, Layer: "code", NodeType: "controller",
			DomainKey: "test-domain", Name: "N" + suffix,
		}, revID)
		if err != nil {
			t.Fatalf("UpsertNode: %v", err)
		}
		if err := g.Store().UpdateNodeTrust(id, 1.0, 1.0, 1.0, "active"); err != nil {
			t.Fatal(err)
		}
		return id, key
	}

	emptyID, emptyKey := mkNode("empty")
	if err := g.RecalculateNodeTrust(emptyID); err != nil {
		t.Fatalf("RecalculateNodeTrust: %v", err)
	}
	row, err := g.Store().GetNodeByKey(emptyKey)
	if err != nil {
		t.Fatal(err)
	}
	if row.TrustScore != 1.0 {
		t.Fatalf("evidence-free node trust = %v, want the stored 1.0 (unchanged behaviour)", row.TrustScore)
	}

	declaredID, declaredKey := mkNode("declared")
	if _, err := g.AddNodeEvidence(declaredKey, validate.EvidenceInput{
		TargetKind: "node", SourceKind: "declared",
		ExtractorID: "okeep-surface", ExtractorVersion: "1",
		Assertion: `{"owner":"Shop"}`, AssertionKind: "decision",
		Confidence: 0.95, RevisionID: revID,
	}); err != nil {
		t.Fatalf("AddNodeEvidence: %v", err)
	}
	if err := g.RecalculateNodeTrust(declaredID); err != nil {
		t.Fatalf("RecalculateNodeTrust: %v", err)
	}
	row, err = g.Store().GetNodeByKey(declaredKey)
	if err != nil {
		t.Fatal(err)
	}
	if row.TrustScore != 1.0 {
		t.Fatalf("a declaration moved node trust to %v", row.TrustScore)
	}
}
