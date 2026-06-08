package prompts

import (
	"testing"
)

// Tests for custom pack scenarios and gap detection.

func TestCustomPack_NoBuiltinForUnknownTech(t *testing.T) {
	if HasBuiltinTechPack("django") {
		t.Error("django should not have a built-in pack")
	}
	if HasBuiltinTechPack("spring") {
		t.Error("spring should not have a built-in pack")
	}
	if HasBuiltinTechPack("go") {
		t.Error("go should not have a built-in pack")
	}
}

func TestCustomPack_GapDetectionForUnknownTech(t *testing.T) {
	gaps := DetectInstructionGaps([]string{"django", "celery", "postgresql"})
	if len(gaps) != 3 {
		t.Errorf("expected 3 gaps, got %d: %v", len(gaps), gapTechs(gaps))
	}
}

func TestCustomPack_NoGapForKnownTech(t *testing.T) {
	gaps := DetectInstructionGaps([]string{"nestjs", "typescript", "prisma"})
	if len(gaps) != 0 {
		t.Errorf("expected 0 gaps, got %d: %v", len(gaps), gapTechs(gaps))
	}
}
