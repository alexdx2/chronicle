package prompts

import (
	"strings"
	"testing"
)

// L0/L1 — Instruction pack matching tests.
// Tests that the right packs are auto-selected for different file types.

func TestMatchPacks_TypeScriptController(t *testing.T) {
	ctx := FileContext{
		FilePath:   "src/orders/order.controller.ts",
		Imports:    []string{"@nestjs/common", "@nestjs/graphql"},
		Decorators: []string{"Controller", "Post", "Get"},
	}
	sel := MatchPacks(ctx, []string{"nestjs", "typescript"})

	assertPackLoaded(t, sel, "core/fact_schema")
	assertPackLoaded(t, sel, "core/enrichment")
	assertPackLoaded(t, sel, "language/typescript")
	assertPackLoaded(t, sel, "framework/nestjs")
}

func TestMatchPacks_PrismaSchema(t *testing.T) {
	ctx := FileContext{
		FilePath: "prisma/schema.prisma",
	}
	sel := MatchPacks(ctx, []string{"prisma"})

	assertPackLoaded(t, sel, "core/fact_schema")
	assertPackLoaded(t, sel, "orm/prisma")
}

func TestMatchPacks_GraphQLResolver(t *testing.T) {
	ctx := FileContext{
		FilePath:   "src/resolvers/user.resolver.ts",
		Imports:    []string{"@nestjs/graphql"},
		Decorators: []string{"Resolver", "Query", "Mutation"},
	}
	sel := MatchPacks(ctx, []string{"nestjs", "graphql"})

	assertPackLoaded(t, sel, "framework/nestjs")
	assertPackLoaded(t, sel, "framework/graphql")
	assertPackLoaded(t, sel, "language/typescript")
}

func TestMatchPacks_PlainUtilFile(t *testing.T) {
	ctx := FileContext{
		FilePath: "src/utils/helpers.ts",
		Imports:  []string{"lodash"},
	}
	sel := MatchPacks(ctx, []string{})

	assertPackLoaded(t, sel, "core/fact_schema")
	assertPackLoaded(t, sel, "language/typescript")
	// Should NOT load framework packs for plain util
	assertPackNotLoaded(t, sel, "framework/nestjs")
	assertPackNotLoaded(t, sel, "orm/prisma")
	assertPackNotLoaded(t, sel, "framework/graphql")
}

func TestMatchPacks_NoUnnecessaryPacks(t *testing.T) {
	ctx := FileContext{
		FilePath: "src/config.ts",
	}
	sel := MatchPacks(ctx, []string{})

	// Only core + language
	loaded := map[string]bool{}
	for _, p := range sel.Loaded {
		loaded[p.ID] = true
	}

	for id := range loaded {
		if strings.HasPrefix(id, "framework/") || strings.HasPrefix(id, "orm/") {
			t.Errorf("unnecessary pack loaded for config.ts: %s", id)
		}
	}
}

func TestMatchPacks_ProjectTechLoadsFramework(t *testing.T) {
	// Even without file-level triggers, manifest tech should load packs
	ctx := FileContext{
		FilePath: "src/app.module.ts",
	}
	sel := MatchPacks(ctx, []string{"nestjs", "prisma"})

	assertPackLoaded(t, sel, "framework/nestjs")
	assertPackLoaded(t, sel, "orm/prisma")
}

func TestMatchPacks_GraphQLSchemaFile(t *testing.T) {
	ctx := FileContext{
		FilePath: "schema.graphql",
	}
	sel := MatchPacks(ctx, []string{})

	assertPackLoaded(t, sel, "framework/graphql")
	// Should NOT load TypeScript for .graphql files
	assertPackNotLoaded(t, sel, "language/typescript")
}

func TestMatchPacks_DecoratorTrigger(t *testing.T) {
	ctx := FileContext{
		FilePath:   "src/gateway.ts",
		Decorators: []string{"WebSocketGateway", "SubscribeMessage"},
	}
	sel := MatchPacks(ctx, []string{})

	assertPackLoaded(t, sel, "framework/nestjs")
}

func TestMatchPacks_ImportTrigger(t *testing.T) {
	ctx := FileContext{
		FilePath: "src/service.ts",
		Imports:  []string{"@prisma/client"},
	}
	sel := MatchPacks(ctx, []string{})

	assertPackLoaded(t, sel, "orm/prisma")
}

func TestMatchPacks_AvailablePacksListed(t *testing.T) {
	ctx := FileContext{
		FilePath: "src/service.ts",
	}
	sel := MatchPacks(ctx, []string{})

	// Non-loaded packs with triggers should appear in available
	if len(sel.Available) == 0 {
		t.Error("expected some available packs to be listed")
	}

	// Check that available packs have AppliesWhen descriptions
	for _, p := range sel.Available {
		if p.AppliesWhen == "" {
			t.Errorf("available pack %s missing AppliesWhen", p.ID)
		}
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
	result := Compose("CORE", []string{"nestjs", "nestjs", "nest"})
	count := strings.Count(result, "NESTJS EXTRACTION")
	if count > 1 {
		t.Errorf("expected 1 NestJS adapter, got %d", count)
	}
}

func TestComposeForFile_FullComposition(t *testing.T) {
	ctx := FileContext{
		FilePath:   "src/order.controller.ts",
		Imports:    []string{"@nestjs/common"},
		Decorators: []string{"Controller"},
	}
	result := ComposeForFile(ctx, []string{"prisma"})

	// Should contain core + typescript + nestjs + prisma
	if !strings.Contains(result, "FACT SCHEMA") {
		t.Error("missing fact schema")
	}
	if !strings.Contains(result, "TYPESCRIPT") {
		t.Error("missing typescript adapter")
	}
	if !strings.Contains(result, "NESTJS") {
		t.Error("missing nestjs adapter")
	}
	if !strings.Contains(result, "PRISMA") {
		t.Error("missing prisma adapter")
	}
}

func TestGetPack_ReturnsContent(t *testing.T) {
	pack, ok := GetPack("framework/nestjs")
	if !ok {
		t.Fatal("nestjs pack not found")
	}
	if pack.Content == "" {
		t.Error("nestjs pack has empty content")
	}
	if !strings.Contains(pack.Content, "NESTJS") {
		t.Error("nestjs pack content doesn't mention NestJS")
	}
}

func TestGetPack_UnknownReturnsNotFound(t *testing.T) {
	_, ok := GetPack("framework/django")
	if ok {
		t.Error("django pack should not exist")
	}
}

func TestAllPacks_HasExpectedCount(t *testing.T) {
	packs := AllPacks()
	if len(packs) < 7 {
		t.Errorf("expected at least 7 packs, got %d", len(packs))
	}

	// All packs should have content
	for _, p := range packs {
		if p.Content == "" {
			t.Errorf("pack %s has empty content", p.ID)
		}
	}
}

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
