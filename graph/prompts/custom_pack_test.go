package prompts

import (
	"strings"
	"testing"
)

// Tests for custom instruction pack scenarios:
// 1. Agent discovers unknown framework → creates pack → pack is usable
// 2. User adds pack via manifest → scan loads it
// 3. Pack registry doesn't have Django/Spring → available list is empty for .py files

func TestCustomPack_UnknownFrameworkNotMatched(t *testing.T) {
	// Python/Django file — no built-in pack
	ctx := FileContext{
		FilePath: "src/views.py",
		Imports:  []string{"django.http", "django.views"},
	}
	sel := MatchPacks(ctx, []string{})

	// Should NOT match any framework pack
	for _, p := range sel.Loaded {
		if strings.HasPrefix(p.ID, "framework/") {
			t.Errorf("unexpected framework pack loaded for Python: %s", p.ID)
		}
	}

	// Core should still be loaded
	assertPackLoaded(t, sel, "core/fact_schema")

	// TypeScript should NOT be loaded
	assertPackNotLoaded(t, sel, "language/typescript")
}

func TestCustomPack_JavaSpringNotMatched(t *testing.T) {
	ctx := FileContext{
		FilePath:   "src/OrderController.java",
		Imports:    []string{"org.springframework.web.bind.annotation"},
		Decorators: []string{"RestController", "GetMapping"},
	}
	sel := MatchPacks(ctx, []string{})

	// No Java/Spring pack exists
	for _, p := range sel.Loaded {
		if p.ID != "core/fact_schema" && p.ID != "core/enrichment" && p.ID != "core/flow_tracing" {
			t.Errorf("unexpected non-core pack loaded for Java: %s", p.ID)
		}
	}
}

func TestCustomPack_ManifestTechDoesntMatchNonexistent(t *testing.T) {
	// Manifest says "django" but no pack exists for it
	ctx := FileContext{
		FilePath: "src/views.py",
	}
	sel := MatchPacks(ctx, []string{"django", "python"})

	// Should not crash, should not load any framework pack
	for _, p := range sel.Loaded {
		if strings.HasPrefix(p.ID, "framework/django") || strings.HasPrefix(p.ID, "language/python") {
			t.Errorf("loaded nonexistent pack: %s", p.ID)
		}
	}
}

func TestCustomPack_RegistryHasNoGoSupport(t *testing.T) {
	ctx := FileContext{
		FilePath: "cmd/main.go",
		Imports:  []string{"net/http", "github.com/gorilla/mux"},
	}
	sel := MatchPacks(ctx, []string{"go"})

	// Only core packs should be loaded
	nonCore := 0
	for _, p := range sel.Loaded {
		if !strings.HasPrefix(p.ID, "core/") {
			nonCore++
		}
	}
	if nonCore > 0 {
		t.Errorf("expected only core packs for Go file, got %d non-core", nonCore)
	}
}

// This tests the scenario from CHECKPOINT 1:
// "If the project uses a framework NOT in the available packs,
//  offer to create a custom instruction pack."
func TestCustomPack_IdentifyGapForCreation(t *testing.T) {
	// Simulate: project uses Django + Celery, but we only have NestJS/Prisma/GraphQL packs
	projectTech := []string{"django", "celery", "postgresql"}

	ctx := FileContext{
		FilePath: "src/tasks.py",
		Imports:  []string{"celery", "django.db.models"},
	}
	sel := MatchPacks(ctx, projectTech)

	// Identify which tech from manifest has NO matching pack
	coveredTech := map[string]bool{}
	for _, p := range sel.Loaded {
		// Extract tech name from pack ID (e.g. "framework/nestjs" -> "nestjs")
		parts := strings.SplitN(p.ID, "/", 2)
		if len(parts) == 2 {
			coveredTech[parts[1]] = true
		}
	}

	var uncoveredTech []string
	for _, tech := range projectTech {
		if !coveredTech[strings.ToLower(tech)] {
			uncoveredTech = append(uncoveredTech, tech)
		}
	}

	// All project tech should be uncovered (no django/celery/postgresql packs exist)
	if len(uncoveredTech) != 3 {
		t.Errorf("expected 3 uncovered tech items, got %d: %v", len(uncoveredTech), uncoveredTech)
	}

	// This is the signal for Claude at CHECKPOINT 1 to offer creating custom packs
	for _, tech := range uncoveredTech {
		t.Logf("No instruction pack for '%s' — Claude should offer to create one", tech)
	}
}
