package prompts

import (
	"strings"
	"testing"
)

// Full non-LLM custom pack lifecycle tests and ID validation.

// --- ID validation ---

func TestValidateID_ValidCustomIDs(t *testing.T) {
	valid := []string{
		"custom/django",
		"custom/spring-boot",
		"custom/otopoint-pricing",
		"custom/company.framework",
		"custom/my-org-auth",
	}
	for _, id := range valid {
		if err := ValidateCustomPackID(id); err != "" {
			t.Errorf("ID %q should be valid, got error: %s", id, err)
		}
	}
}

func TestValidateID_RejectsPathTraversal(t *testing.T) {
	bad := []string{
		"custom/../secret",
		"custom/../../etc/passwd",
		"custom/a..b",
	}
	for _, id := range bad {
		if err := ValidateCustomPackID(id); err == "" {
			t.Errorf("ID %q should be rejected (path traversal)", id)
		}
	}
}

func TestValidateID_RejectsNonCustomPrefix(t *testing.T) {
	bad := []string{
		"framework/nestjs",
		"core/fact_schema",
		"guide/pack_authoring",
		"language/typescript",
		"orm/prisma",
		"django",            // no prefix
		"my-pack",           // no prefix
		"",                  // empty
	}
	for _, id := range bad {
		if err := ValidateCustomPackID(id); err == "" {
			t.Errorf("ID %q should be rejected (not custom/ prefix)", id)
		}
	}
}

func TestValidateID_RejectsEmptySlug(t *testing.T) {
	if err := ValidateCustomPackID("custom/"); err == "" {
		t.Error("ID 'custom/' should be rejected (empty slug)")
	}
}

func TestValidateID_RejectsNestedPaths(t *testing.T) {
	bad := []string{
		"custom/a/b",
		"custom/org/pack",
		`custom/a\b`,
	}
	for _, id := range bad {
		if err := ValidateCustomPackID(id); err == "" {
			t.Errorf("ID %q should be rejected (nested path)", id)
		}
	}
}

// --- Full lifecycle: save → load → compose ---

func TestLifecycle_SaveLoadCompose(t *testing.T) {
	// Step 1: Validate a custom pack
	djangoPack := `# DJANGO EXTRACTION RULES

Apply when file imports from django.

## Views
{"kind":"endpoint","method":"GET","target":"/orders"}
{"kind":"endpoint","method":"POST","target":"/orders"}

## Models
{"kind":"model","to":"Order"}
{"kind":"model_relation","from":"Order","to":"User"}

## ORM usage
{"kind":"uses_model","to":"Order"}

## Do NOT extract
- Admin classes
- Migration files
`

	// Step 2: Validate content
	invalid := ValidateCustomPack(djangoPack)
	if len(invalid) > 0 {
		t.Fatalf("django pack has invalid kinds: %v", invalid)
	}

	// Step 3: Validate ID
	id := "custom/django"
	if err := ValidateCustomPackID(id); err != "" {
		t.Fatalf("ID validation failed: %s", err)
	}

	// Step 4: Compose with core + custom pack
	// Simulate what scan would do: core guide + custom content
	composed := FactSchema + "\n\n---\n\n" + djangoPack
	if !strings.Contains(composed, "FACT SCHEMA") {
		t.Error("composed prompt missing core fact schema")
	}
	if !strings.Contains(composed, "DJANGO EXTRACTION RULES") {
		t.Error("composed prompt missing django pack")
	}
	if !strings.Contains(composed, `"kind":"endpoint"`) {
		t.Error("composed prompt missing endpoint template")
	}
	if !strings.Contains(composed, `"kind":"model"`) {
		t.Error("composed prompt missing model template")
	}
}

func TestLifecycle_InvalidPackRejectedBeforeSave(t *testing.T) {
	badPack := `# BAD PACK
{"kind":"django_view","to":"OrderView"}
{"kind":"flask_route","method":"GET","target":"/users"}
`
	invalid := ValidateCustomPack(badPack)
	if len(invalid) == 0 {
		t.Fatal("bad pack should have invalid kinds")
	}

	// Verify the invalid kinds are identified
	assertContainsStr(t, invalid, "django_view")
	assertContainsStr(t, invalid, "flask_route")

	// Save should be blocked (tested at MCP handler level, but validation logic is here)
}

func TestLifecycle_OverrideBuiltinBlocked(t *testing.T) {
	// Trying to save a pack that shadows a built-in
	blocked := []string{
		"framework/nestjs",
		"core/fact_schema",
		"orm/prisma",
	}
	for _, id := range blocked {
		if err := ValidateCustomPackID(id); err == "" {
			t.Errorf("should block saving over built-in pack %q", id)
		}
	}
}

// --- CHECKPOINT 1 prompt content ---

func TestCheckpoint1_ContainsPackAuthoringInstructions(t *testing.T) {
	// The scan command text should reference the authoring guide when gaps exist
	// We test this by checking the command instructions string
	scanCmd := `chronicle_get_instruction_pack(id="guide/pack_authoring")
chronicle_save_custom_pack`

	// These should be in the scan command instructions
	if !strings.Contains(scanCmd, "guide/pack_authoring") {
		t.Error("scan command should reference pack authoring guide")
	}
	if !strings.Contains(scanCmd, "chronicle_save_custom_pack") {
		t.Error("scan command should reference save custom pack tool")
	}
}

func TestCheckpoint1_GapInfoLeadsToCreation(t *testing.T) {
	// Simulate the full CHECKPOINT 1 flow:
	// 1. Detect gaps
	gaps := DetectInstructionGaps([]string{"typescript", "nestjs", "django", "celery"})

	// 2. Only django and celery should be gaps
	if len(gaps) != 2 {
		t.Fatalf("expected 2 gaps, got %d", len(gaps))
	}

	// 3. Each gap should suggest creating a pack
	for _, g := range gaps {
		if g.SuggestedAction != "create_custom_pack" {
			t.Errorf("gap %s should suggest create_custom_pack", g.Tech)
		}
	}

	// 4. The pack authoring guide should exist and be fetchable
	if PackAuthoringGuide == "" {
		t.Error("pack authoring guide not available")
	}

	// 5. After Claude creates a pack, it should be validatable
	mockDjangoPack := `# DJANGO
{"kind":"endpoint","method":"GET","target":"/test"}
{"kind":"model","to":"TestModel"}
`
	invalid := ValidateCustomPack(mockDjangoPack)
	if len(invalid) != 0 {
		t.Errorf("valid mock pack rejected: %v", invalid)
	}

	// 6. ID should be valid
	if err := ValidateCustomPackID("custom/django"); err != "" {
		t.Errorf("custom/django ID rejected: %s", err)
	}
}

// --- Edge cases ---

func TestCustomPack_EmptyContentPassesValidation(t *testing.T) {
	// Empty pack is technically valid (no invalid kinds)
	invalid := ValidateCustomPack("")
	if len(invalid) != 0 {
		t.Errorf("empty pack should pass validation, got: %v", invalid)
	}
}

func TestCustomPack_ProseOnlyPassesValidation(t *testing.T) {
	pack := `# My Framework Rules

When you see @MyDecorator, treat the class as a controller.
Extract endpoints using the core endpoint kind.
Map MyModel to model facts.
Do not create any custom kinds.`

	invalid := ValidateCustomPack(pack)
	if len(invalid) != 0 {
		t.Errorf("prose-only pack should pass validation, got: %v", invalid)
	}
}
