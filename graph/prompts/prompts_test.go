package prompts

import (
	"strings"
	"testing"
)

// Tests for the pack system.

func TestCorePacks_AlwaysLoaded(t *testing.T) {
	core := CorePacks()
	if len(core) < 3 {
		t.Errorf("expected at least 3 core packs, got %d", len(core))
	}
	ids := map[string]bool{}
	for _, p := range core {
		ids[p.ID] = true
		if !p.AlwaysLoad {
			t.Errorf("core pack %s should have always_load=true", p.ID)
		}
	}
	for _, expected := range []string{"core/fact_schema", "core/enrichment", "core/flow_tracing"} {
		if !ids[expected] {
			t.Errorf("missing core pack: %s", expected)
		}
	}
}

func TestAllPacks_OnlyCore(t *testing.T) {
	packs := AllPacks()
	if len(packs) != 3 {
		t.Errorf("expected 3 core packs, got %d", len(packs))
	}
	for _, p := range packs {
		if !p.AlwaysLoad {
			t.Errorf("non-core pack %s in AllPacks", p.ID)
		}
		if p.Content == "" {
			t.Errorf("pack %s has empty content", p.ID)
		}
	}
}

func TestListBuiltinTechPacks_HasMatchSections(t *testing.T) {
	packs := ListBuiltinTechPacks()
	if len(packs) < 5 {
		t.Errorf("expected at least 5 built-in tech packs, got %d", len(packs))
	}
	for _, tp := range packs {
		if tp.Match == "" {
			t.Errorf("tech pack %s has empty match section", tp.ID)
		}
		if tp.Content != "" {
			t.Error("ListBuiltinTechPacks should NOT include content")
		}
	}
}

func TestListBuiltinTechPacks_HasExpectedPacks(t *testing.T) {
	packs := ListBuiltinTechPacks()
	ids := map[string]bool{}
	for _, tp := range packs {
		ids[tp.ID] = true
	}
	for _, expected := range []string{"typescript", "nestjs", "prisma", "graphql", "dotnet"} {
		if !ids[expected] {
			t.Errorf("missing built-in tech pack: %s", expected)
		}
	}
}

func TestGetTechPackContent_Found(t *testing.T) {
	content, ok := GetTechPackContent("nestjs")
	if !ok {
		t.Fatal("nestjs pack not found")
	}
	if !strings.Contains(content, "NESTJS") {
		t.Error("nestjs pack content doesn't mention NestJS")
	}
}

func TestGetTechPackContent_NotFound(t *testing.T) {
	_, ok := GetTechPackContent("django")
	if ok {
		t.Error("django pack should not exist as built-in")
	}
}

func TestGetTechPackContent_Dotnet(t *testing.T) {
	content, ok := GetTechPackContent("dotnet")
	if !ok {
		t.Fatal("dotnet pack not found")
	}
	if !strings.Contains(content, "C#") {
		t.Error("dotnet pack content doesn't mention C#")
	}
}

func TestExtractMatchSection(t *testing.T) {
	content := `# MY PACK

## Match
Load this pack when:
- Files have .foo extension
- project.json exists

## Rules

Some extraction rules here.`

	match := ExtractMatchSection(content)
	if !strings.Contains(match, ".foo extension") {
		t.Error("match section should contain .foo extension")
	}
	if strings.Contains(match, "extraction rules") {
		t.Error("match section should NOT contain content from other sections")
	}
}

func TestExtractMatchSection_NoMatchSection(t *testing.T) {
	content := `# MY PACK

## Rules
Some rules.`

	match := ExtractMatchSection(content)
	if match != "" {
		t.Errorf("expected empty match section, got: %s", match)
	}
}

func TestCompose_AppendsAdapters(t *testing.T) {
	result := Compose("CORE GUIDE", []string{"nestjs", "prisma"})
	if !strings.Contains(result, "CORE GUIDE") {
		t.Error("composed guide missing core")
	}
	if !strings.Contains(result, "NESTJS") {
		t.Error("composed guide missing NestJS adapter")
	}
	if !strings.Contains(result, "PRISMA") {
		t.Error("composed guide missing Prisma adapter")
	}
}

func TestCompose_NoDuplicates(t *testing.T) {
	result := Compose("CORE", []string{"nestjs", "nestjs"})
	count := strings.Count(result, "NESTJS EXTRACTION")
	if count > 1 {
		t.Errorf("expected 1 NestJS adapter, got %d", count)
	}
}

// --- Helpers ---

func assertPackLoaded(t *testing.T, sel PackSelection, id string) {
	t.Helper()
	for _, p := range sel.Loaded {
		if p.ID == id {
			return
		}
	}
	t.Errorf("expected pack %s to be loaded, but it wasn't. Loaded: %v", id, packIDs(sel.Loaded))
}

func assertPackNotLoaded(t *testing.T, sel PackSelection, id string) {
	t.Helper()
	for _, p := range sel.Loaded {
		if p.ID == id {
			t.Errorf("pack %s should NOT be loaded, but it was", id)
			return
		}
	}
}

func packIDs(packs []PackMeta) []string {
	var ids []string
	for _, p := range packs {
		ids = append(ids, p.ID)
	}
	return ids
}
