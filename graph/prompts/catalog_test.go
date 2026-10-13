package prompts

import "testing"

func TestCatalog_PriorityOnePerID(t *testing.T) {
	c := BuiltinCatalog()
	b, ok := c.Get("kafka")
	if !ok || b.Source != SourceBuiltin || b.Version == "" {
		t.Fatalf("builtin kafka: %+v", b)
	}
	c.Add(CatalogPack{ID: "kafka", Content: "# KAFKA (fleet)\n\n## Match\n- *.cs with Confluent.Kafka\n\n## Entry points (→ endpoint facts)\nx\n", Source: SourceFleet})
	f, _ := c.Get("kafka")
	if f.Source != SourceFleet || f.Version == b.Version || len(f.Exts) != 1 || f.Exts[0] != ".cs" {
		t.Fatalf("fleet pack did not replace builtin: %+v", f)
	}
	c.Add(CatalogPack{ID: "kafka", Content: "# builtin again", Source: SourceBuiltin})
	if g, _ := c.Get("kafka"); g.Source != SourceFleet {
		t.Fatal("lower source must not replace a higher one")
	}
	got := ComposeCatalog("CORE", c.ForTech([]string{"kafka", "kafka", "unknown"}))
	if got != "CORE\n\n---\n"+f.Content {
		t.Fatalf("compose = %q", got)
	}
}

func TestMatchExts(t *testing.T) {
	if e := MatchExts("## Match\n- files *.cs and .csproj\n- imports Confluent.Kafka"); len(e) != 2 || e[0] != ".cs" || e[1] != ".csproj" {
		t.Fatalf("%v", e)
	}
	if e := MatchExts(ExtractMatchSection(builtinTechPacks["kafka"].Content)); len(e) != 0 {
		t.Fatalf("builtin kafka has no extensions in Match: %v", e)
	}
}
