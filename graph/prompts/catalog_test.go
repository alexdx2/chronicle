package prompts

import (
	"os"
	"path/filepath"
	"testing"
)

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

func TestAddProjectPacks_IDIsLowercasedStem(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "Kafka-DotNet.md"), []byte("# KD\n\n## Match\n- *.cs\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	c := BuiltinCatalog()
	if err := c.AddProjectPacks(dir); err != nil {
		t.Fatal(err)
	}
	if p, ok := c.Get("kafka-dotnet"); !ok || p.Source != SourceProject {
		t.Fatalf("project pack not found under its lowercase id: %+v %v", p, ok)
	}
	if _, ok := c.Get("Kafka-DotNet"); ok {
		t.Error("the mixed-case id must not exist")
	}
}
