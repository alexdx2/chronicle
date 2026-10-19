package graph

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alexdx2/chronicle-core/store"
)

func configRowsOf(t *testing.T, g *Graph, hostSuffix string) int {
	t.Helper()
	edges, _ := g.Store().ListEdges(store.EdgeFilter{EdgeType: "CALLS_SERVICE"})
	n := 0
	for _, e := range edges {
		if !e.Active || !strings.HasSuffix(e.ToNodeKey, hostSuffix) {
			continue
		}
		evs, _ := g.Store().ListEvidenceByEdge(e.EdgeID)
		for _, ev := range evs {
			if ev.ASTRule == "config_value" {
				n++
			}
		}
	}
	return n
}

// The source is read under the scan's repo root, not the process cwd, and at
// most 256 KB of it.
func TestHostFromConfigSymbol_ReadsUnderRepoRootWithCap(t *testing.T) {
	root := t.TempDir()
	write := func(p, body string) {
		full := filepath.Join(root, p)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("svc/src/pricing.client.ts", "fetch(`${process.env.PRICING_API_URL}/prices`);\n")
	write("svc/src/late.client.ts", strings.Repeat("// pad\n", 50000)+"fetch(`${process.env.PRICING_API_URL}/late`);\n")
	t.Chdir(t.TempDir()) // the cwd is not the repo
	c := &ConfigHosts{Root: root}
	c.Add("PRICING_API_URL", "http://pricing-api:8080", "svc/.env")
	g, _, _ := resolveFiles(t, c, srcFacts{"svc/src/pricing.client.ts", []Fact{
		{Kind: "http_call", From: "pricing", FromType: "provider", Target: "pricing-api/prices", Method: "GET"},
	}})
	if n := configRowsOf(t, g, "pricing-api"); n != 1 {
		t.Errorf("config rows = %d, want 1 (source read under Root)", n)
	}
	g2, _, _ := resolveFiles(t, c, srcFacts{"svc/src/late.client.ts", []Fact{
		{Kind: "http_call", From: "late", FromType: "provider", Target: "pricing-api/late", Method: "GET"},
	}})
	if n := configRowsOf(t, g2, "pricing-api"); n != 0 {
		t.Errorf("symbol beyond 256 KB was read: %d config rows", n)
	}
}

func TestContainsWord(t *testing.T) {
	for _, c := range []struct {
		src, sym string
		want     bool
	}{
		{"x = process.env.PRICING_API_URL;", "PRICING_API_URL", true},
		{"x = process.env.PRICING_API_URL_V2;", "PRICING_API_URL", false},
		{"x = MY_PRICING_API_URL", "PRICING_API_URL", false},
		{`cfg["Services:Pricing"]`, "Services:Pricing", true},
		{"xServices:Pricing", "Services:Pricing", false},
		{"a PRICING_API_URLx PRICING_API_URL", "PRICING_API_URL", true},
	} {
		if got := containsWord(c.src, c.sym); got != c.want {
			t.Errorf("containsWord(%q,%q)=%v want %v", c.src, c.sym, got, c.want)
		}
	}
}

func TestHostFromConfigSymbol_WeakSymbols(t *testing.T) {
	root := t.TempDir()
	write := func(p, body string) {
		full := filepath.Join(root, p)
		_ = os.MkdirAll(filepath.Dir(full), 0o755)
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// Whole-word: the env name is only a prefix of another variable.
	write("svc/src/a.client.ts", "fetch(`${process.env.PRICING_API_URL_OLD}/p`);\n")
	// Last segment of the key under a different service's directory.
	write("other/src/b.client.ts", "const Pricing = 1; fetch(`${Pricing}/p`);\n")
	// Last segment too short.
	write("svc/src/c.client.ts", "const Api = 1; fetch(`${Api}/p`);\n")
	// Last segment, long enough, same tree: kept.
	write("svc/src/d.client.ts", "const Pricing = cfg.Pricing; fetch(`${Pricing}/p`);\n")
	t.Chdir(t.TempDir())
	c := &ConfigHosts{Root: root}
	c.Add("PRICING_API_URL", "http://pricing-api:8080", "svc/.env")
	c.Add("Services:Pricing", "http://pricing-api:8080", "svc/appsettings.json")
	c.Add("Services:Api", "http://pricing-api:8080", "svc/appsettings.json")
	for file, want := range map[string]int{
		"svc/src/a.client.ts":   0,
		"other/src/b.client.ts": 0,
		"svc/src/c.client.ts":   0,
		"svc/src/d.client.ts":   1,
	} {
		g, _, _ := resolveFiles(t, c, srcFacts{file, []Fact{
			{Kind: "http_call", From: "x", FromType: "provider", Target: "pricing-api/p", Method: "GET"},
		}})
		if n := configRowsOf(t, g, "pricing-api"); n != want {
			t.Errorf("%s: config rows = %d, want %d", file, n, want)
		}
	}
}
