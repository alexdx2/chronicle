package prompts

import (
	"strings"
	"testing"
)

// L0/L1 — Gap detection tests.
// Verifies that manifest tech without matching packs produces actionable gaps.

func TestDetectGaps_ManifestTechMissing(t *testing.T) {
	gaps := DetectInstructionGaps([]string{"typescript", "nestjs", "django", "celery"})

	// typescript and nestjs have packs → no gap
	// django and celery don't → gaps
	assertGapExists(t, gaps, "django")
	assertGapExists(t, gaps, "celery")
	assertNoGap(t, gaps, "typescript")
	assertNoGap(t, gaps, "nestjs")
}

func TestDetectGaps_AllCovered(t *testing.T) {
	gaps := DetectInstructionGaps([]string{"typescript", "nestjs", "prisma", "graphql"})
	if len(gaps) != 0 {
		t.Errorf("expected 0 gaps, got %d: %v", len(gaps), gapTechs(gaps))
	}
}

func TestDetectGaps_AllMissing(t *testing.T) {
	gaps := DetectInstructionGaps([]string{"django", "flask", "sqlalchemy", "celery"})
	if len(gaps) != 4 {
		t.Errorf("expected 4 gaps, got %d", len(gaps))
	}
	for _, g := range gaps {
		if g.SuggestedAction != "create_pack" {
			t.Errorf("gap %s should suggest create_pack, got %s", g.Tech, g.SuggestedAction)
		}
		if g.Reason == "" {
			t.Errorf("gap %s should have a reason", g.Tech)
		}
	}
}

func TestDetectGaps_EmptyManifest(t *testing.T) {
	gaps := DetectInstructionGaps([]string{})
	if len(gaps) != 0 {
		t.Errorf("expected 0 gaps for empty manifest, got %d", len(gaps))
	}
}

func TestDetectGaps_CaseInsensitive(t *testing.T) {
	gaps := DetectInstructionGaps([]string{"NestJS", "PRISMA", "Django"})
	assertNoGap(t, gaps, "NestJS")
	assertNoGap(t, gaps, "PRISMA")
	assertGapExists(t, gaps, "Django")
}

func TestDetectGaps_AliasesAreGaps(t *testing.T) {
	// "nest" is NOT a canonical pack ID — agent uses match sections to find nestjs
	gaps := DetectInstructionGaps([]string{"nest"})
	assertGapExists(t, gaps, "nest")
}

func TestDetectGaps_JavascriptIsGap(t *testing.T) {
	// "javascript" is NOT a canonical pack ID — agent uses match sections to find typescript
	gaps := DetectInstructionGaps([]string{"javascript"})
	assertGapExists(t, gaps, "javascript")
}

// --- Custom pack validation ---

func TestValidateCustomPack_ValidPack(t *testing.T) {
	pack := `# Django Extraction Rules
Use for Django views and models.
{"kind":"endpoint","method":"GET","target":"/orders"}
{"kind":"model","to":"Order"}
{"kind":"uses_model","to":"User"}
{"kind":"calls_service","to":"PaymentService","method":"charge"}
`
	invalid := ValidateCustomPack(pack)
	if len(invalid) != 0 {
		t.Errorf("expected 0 invalid kinds, got %v", invalid)
	}
}

func TestValidateCustomPack_InvalidKinds(t *testing.T) {
	pack := `# Bad Custom Pack
{"kind":"django_view","to":"CreateOrderView"}
{"kind":"flask_route","method":"GET","target":"/users"}
{"kind":"endpoint","method":"GET","target":"/orders"}
{"kind":"orm_query","to":"User"}
`
	invalid := ValidateCustomPack(pack)
	if len(invalid) != 3 {
		t.Errorf("expected 3 invalid kinds, got %d: %v", len(invalid), invalid)
	}
	assertContainsStr(t, invalid, "django_view")
	assertContainsStr(t, invalid, "flask_route")
	assertContainsStr(t, invalid, "orm_query")
}

func TestValidateCustomPack_EmptyPack(t *testing.T) {
	invalid := ValidateCustomPack("")
	if len(invalid) != 0 {
		t.Errorf("expected 0 invalid kinds for empty pack, got %v", invalid)
	}
}

func TestValidateCustomPack_NoFalsePositives(t *testing.T) {
	// Text that mentions kinds in prose but not in JSON format
	pack := `This pack handles django_view patterns.
Use the endpoint kind for URL routes.
The model kind maps to Django ORM models.`
	invalid := ValidateCustomPack(pack)
	if len(invalid) != 0 {
		t.Errorf("expected 0 invalid kinds from prose text, got %v", invalid)
	}
}

// --- Authoring guide ---

func TestPackAuthoringGuide_Exists(t *testing.T) {
	if PackAuthoringGuide == "" {
		t.Fatal("pack authoring guide is empty")
	}
	// Should contain key instructions
	mustContain := []string{
		"HOW TO CREATE",
		"allowed", // mentions allowed kinds
		"endpoint", "injects", "model", "produces", "consumes",
		"Never introduce new kinds",
		"Django", "Spring", // has examples
	}
	for _, s := range mustContain {
		if !strings.Contains(PackAuthoringGuide, s) {
			t.Errorf("authoring guide missing %q", s)
		}
	}
}

func TestPackAuthoringGuide_ExamplesPassValidation(t *testing.T) {
	// The Django and Spring examples in the guide should use only valid kinds
	invalid := ValidateCustomPack(PackAuthoringGuide)
	if len(invalid) > 0 {
		t.Errorf("authoring guide examples use invalid kinds: %v", invalid)
	}
}

// --- Built-in packs should pass validation ---

func TestBuiltinPacks_PassValidation(t *testing.T) {
	for _, pack := range AllPacks() {
		invalid := ValidateCustomPack(pack.Content)
		if len(invalid) > 0 {
			t.Errorf("built-in pack %s has invalid kinds: %v", pack.ID, invalid)
		}
	}
}

// --- Checkpoint prompt assembly ---

func TestCheckpointGapInfo_FormatsForAgent(t *testing.T) {
	gaps := DetectInstructionGaps([]string{"nestjs", "django", "celery"})

	// Build the gap info that would go into CHECKPOINT 1
	var gapLines []string
	for _, g := range gaps {
		gapLines = append(gapLines, "- "+g.Tech+": "+g.Reason+" ("+g.SuggestedAction+")")
	}
	gapText := strings.Join(gapLines, "\n")

	if !strings.Contains(gapText, "django") {
		t.Error("gap text should mention django")
	}
	if !strings.Contains(gapText, "celery") {
		t.Error("gap text should mention celery")
	}
	if !strings.Contains(gapText, "create_pack") {
		t.Error("gap text should suggest create_pack")
	}
	if strings.Contains(gapText, "nestjs") {
		t.Error("gap text should NOT mention nestjs (it has a pack)")
	}
}

// --- Helpers ---

func assertGapExists(t *testing.T, gaps []InstructionGap, tech string) {
	t.Helper()
	for _, g := range gaps {
		if strings.EqualFold(g.Tech, tech) {
			return
		}
	}
	t.Errorf("expected gap for %s, not found. Gaps: %v", tech, gapTechs(gaps))
}

func assertNoGap(t *testing.T, gaps []InstructionGap, tech string) {
	t.Helper()
	for _, g := range gaps {
		if strings.EqualFold(g.Tech, tech) {
			t.Errorf("unexpected gap for %s", tech)
			return
		}
	}
}

func gapTechs(gaps []InstructionGap) []string {
	var techs []string
	for _, g := range gaps {
		techs = append(techs, g.Tech)
	}
	return techs
}

func assertContainsStr(t *testing.T, list []string, s string) {
	t.Helper()
	for _, item := range list {
		if item == s {
			return
		}
	}
	t.Errorf("expected %q in %v", s, list)
}
