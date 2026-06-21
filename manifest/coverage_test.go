package manifest

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoad_ListAndMapFormats(t *testing.T) {
	list := []byte("domains:\n  - name: shop\n    scan:\n      include: [\"shop/**\"]\ntech: [nestjs]\n")
	m, err := Load(list)
	if err != nil {
		t.Fatalf("list load: %v", err)
	}
	if len(m.Domains) != 1 || m.Domains[0].Name != "shop" {
		t.Fatalf("list domains: %+v", m.Domains)
	}

	mp := []byte("domains:\n  shop:\n    name: shop\n    scan:\n      include: [\"shop/**\"]\n")
	m2, err := Load(mp)
	if err != nil {
		t.Fatalf("map load: %v", err)
	}
	if len(m2.Domains) != 1 {
		t.Fatalf("map domains: %+v", m2.Domains)
	}
}

func TestLoad_Errors(t *testing.T) {
	if _, err := Load([]byte("tech: [x]\n")); err == nil {
		t.Error("missing domains should error")
	}
	if _, err := Load([]byte("domains:\n  - scan: {include: [a]}\n")); err == nil {
		t.Error("domain without name should error")
	}
	if _, err := Load([]byte("domains: [::bad")); err == nil {
		t.Error("invalid yaml should error")
	}
}

func TestLoadFile(t *testing.T) {
	if _, err := LoadFile(filepath.Join(t.TempDir(), "none.yaml")); err == nil {
		t.Error("missing file should error")
	}
	p := filepath.Join(t.TempDir(), "m.yaml")
	os.WriteFile(p, []byte("domains:\n  - name: d\n    scan: {include: [\"d/**\"]}\n"), 0o644)
	if _, err := LoadFile(p); err != nil {
		t.Errorf("valid file: %v", err)
	}
}

func TestInferServices(t *testing.T) {
	// Explicit services win.
	m := &Manifest{Services: []ServiceEntry{{Key: "api", Path: "api"}}}
	if got := m.InferServices(); len(got) != 1 || got[0].Key != "api" {
		t.Fatalf("explicit services: %+v", got)
	}
	// Derived from include patterns; ** / * skipped; deduped.
	m2 := &Manifest{Domains: []DomainEntry{{
		Name: "d",
		Scan: ScanConfig{Include: []string{"arena/**", "tom/src/**", "arena/again/**", "**/x", "*"}},
	}}}
	got := m2.InferServices()
	keys := map[string]bool{}
	for _, s := range got {
		keys[s.Key] = true
	}
	if !keys["arena"] || !keys["tom"] {
		t.Fatalf("derived services missing arena/tom: %+v", got)
	}
	if keys["**"] || keys["*"] {
		t.Fatalf("glob wildcards should not become services: %+v", got)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 deduped services, got %d: %+v", len(got), got)
	}
}

func TestDomainForFile_Globs(t *testing.T) {
	m := &Manifest{Domains: []DomainEntry{
		{Key: "shop", Name: "Shop", Scan: ScanConfig{Include: []string{"shop/**"}, Exclude: []string{"shop/test/**"}}},
		{Name: "noinclude"}, // no include → never matches
	}}
	if got := m.DomainForFile("shop/src/a.ts"); got != "shop" {
		t.Errorf("include match: got %q", got)
	}
	if got := m.DomainForFile("shop/test/a.spec.ts"); got != "_unassigned" {
		t.Errorf("excluded file should be unassigned, got %q", got)
	}
	if got := m.DomainForFile("other/x.ts"); got != "_unassigned" {
		t.Errorf("no match should be unassigned, got %q", got)
	}

	// Key empty → falls back to Name.
	m2 := &Manifest{Domains: []DomainEntry{{Name: "OnlyName", Scan: ScanConfig{Include: []string{"x/**"}}}}}
	if got := m2.DomainForFile("x/a.ts"); got != "OnlyName" {
		t.Errorf("name fallback: got %q", got)
	}
}

func TestMatchGlob(t *testing.T) {
	cases := []struct {
		file, pat string
		want      bool
	}{
		{"a/b/c.ts", "a/**", true},
		{"a/b/c.ts", "a/**/c.ts", true},
		{"a/b/c.ts", "**/c.ts", true},
		{"a/b/c.ts", "**/*.go", false},
		{"x/y.ts", "a/**", false},
		{"a/b.ts", "a/b.ts", true},
		{"a/b.ts", "a/*.ts", true},
	}
	for _, c := range cases {
		if got := matchGlob(c.file, c.pat); got != c.want {
			t.Errorf("matchGlob(%q, %q)=%v want %v", c.file, c.pat, got, c.want)
		}
	}
}

func TestReplaceDomainsWithClone_and_MergedScanConfig(t *testing.T) {
	m := &Manifest{Domains: []DomainEntry{
		{Key: "base", Name: "Base", Scan: ScanConfig{Include: []string{"a/**"}, Exclude: []string{"a/t/**"}}},
		{Key: "other", Name: "Other", Scan: ScanConfig{Include: []string{"b/**"}}},
	}}
	merged := m.MergedScanConfig()
	if len(merged.Include) != 2 || len(merged.Exclude) != 1 {
		t.Fatalf("merged: %+v", merged)
	}
	if !m.ReplaceDomainsWithClone("base", "lab") {
		t.Fatal("clone of existing base should succeed")
	}
	if len(m.Domains) != 1 || m.Domains[0].Key != "lab" {
		t.Fatalf("after clone: %+v", m.Domains)
	}
	if m.ReplaceDomainsWithClone("absent", "x") {
		t.Error("clone of absent base should return false")
	}
}
