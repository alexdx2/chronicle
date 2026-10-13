package prompts

import (
	"os"
	"path/filepath"
	"strings"
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

func TestParseMatchLine(t *testing.T) {
	cases := []struct {
		name, in               string
		exts, always, contains []string
		ok                     bool
	}{
		{"all three", "Load this pack when:\n- x\nmatch: ext=.prisma,.ts; always=.prisma; contains=PrismaClient, Prisma\n", []string{".prisma", ".ts"}, []string{".prisma"}, []string{"PrismaClient", "Prisma"}, true},
		{"both", "match: ext=.cs,.csproj; contains=Confluent.Kafka, IProducer, IConsumer\n", []string{".cs", ".csproj"}, nil, []string{"Confluent.Kafka", "IProducer", "IConsumer"}, true},
		{"ext only", "match: ext=.prisma,.ts", []string{".prisma", ".ts"}, nil, nil, true},
		{"contains only", "match: contains=kafka, Kafka", nil, nil, []string{"kafka", "Kafka"}, true},
		{"absent", "Load this pack when:\n- Files have .cs extension\n", nil, nil, nil, false},
		{"no recognised key", "match: C# projects\nmatch: foo=bar; baz", nil, nil, nil, false},
		{"odd spacing and case", "  MATCH :  EXT = .TS , .Tsx ;CONTAINS=  @nestjs/ ;", []string{".ts", ".tsx"}, nil, []string{"@nestjs/"}, true},
		{"extension without dot", "match: ext=cs", []string{".cs"}, nil, nil, true},
		// what models actually write when the guide says "the `match:` line"
		{"inline code", "`match: ext=.cs,.csproj; contains=Confluent.Kafka`", []string{".cs", ".csproj"}, nil, []string{"Confluent.Kafka"}, true},
		{"bullet and bold", "- **match:** ext=.py; contains=boto3.resource('dynamodb'), DynamoDB", []string{".py"}, nil, []string{"boto3.resource('dynamodb')", "DynamoDB"}, true},
		{"bracketed template copied", "match: ext=[.cs,.csproj]; always=[.csproj]; contains=[Confluent.Kafka, IProducer]", []string{".cs", ".csproj"}, []string{".csproj"}, []string{"Confluent.Kafka", "IProducer"}, true},
		{"quoted tokens", "match: ext=\".ts\"; contains='@aws-cdk/core', \"aws-cdk-lib\"", []string{".ts"}, nil, []string{"@aws-cdk/core", "aws-cdk-lib"}, true},
		{"unknown key next to a known one", "match: ext=.cs; regex=.*", []string{".cs"}, nil, nil, true},
	}
	for _, c := range cases {
		exts, always, contains, ok := ParseMatchLine(c.in)
		j := func(a []string) string { return strings.Join(a, "|") }
		if ok != c.ok || j(exts) != j(c.exts) || j(always) != j(c.always) || j(contains) != j(c.contains) {
			t.Errorf("%s: got %q %q %q %v want %q %q %q %v", c.name, exts, always, contains, ok, c.exts, c.always, c.contains, c.ok)
		}
	}
}

func TestCatalog_ProseFallbackWhenTheLineHasNoKnownKey(t *testing.T) {
	c := NewCatalog()
	c.Add(CatalogPack{ID: "p", Content: "# P\n\n## Match\nmatch: C# projects\n- Files have .go extension\n", Source: SourceFleet})
	p, _ := c.Get("p")
	if p.Structured || strings.Join(p.Exts, ",") != ".go" {
		t.Fatalf("pack = %+v", p)
	}
}

func TestCatalog_MatchLineFillsExtsAndContains(t *testing.T) {
	c := NewCatalog()
	c.Add(CatalogPack{ID: "k", Content: "# K\n\n## Match\nLoad when .java files:\nmatch: ext=.cs; contains=Confluent.Kafka\n\n## Rules\nx\n", Source: SourceFleet})
	p, _ := c.Get("k")
	if strings.Join(p.Exts, ",") != ".cs" || strings.Join(p.Contains, ",") != "Confluent.Kafka" || !p.Structured {
		t.Fatalf("pack = %+v", p)
	}
	c.Add(CatalogPack{ID: "prose", Content: "# P\n\n## Match\n- Files have .go extension\n", Source: SourceFleet})
	p, _ = c.Get("prose")
	if strings.Join(p.Exts, ",") != ".go" || len(p.Contains) != 0 || p.Structured {
		t.Fatalf("prose pack = %+v", p)
	}
}

func TestBuiltinPacksCarryAMatchLine(t *testing.T) {
	for id := range builtinTechPacks {
		p, _ := BuiltinCatalog().Get(id)
		if !p.Structured {
			t.Errorf("builtin pack %s has no match: line", id)
		}
	}
	if p, _ := BuiltinCatalog().Get("nestjs"); len(p.Always) != 0 || len(p.Contains) == 0 {
		t.Errorf("nestjs must attach by token only: %+v", p)
	}
	if p, _ := BuiltinCatalog().Get("prisma"); strings.Join(p.Always, ",") != ".prisma" {
		t.Errorf("prisma always = %v", p.Always)
	}
}
