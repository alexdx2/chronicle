package graph

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alexdx2/chronicle-core/manifest"
)

func repoRootForTest(t *testing.T) string {
	t.Helper()
	dir, _ := os.Getwd()
	for i := 0; i < 6; i++ {
		if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
			return dir
		}
		dir = filepath.Dir(dir)
	}
	t.Skip("no .git found above test cwd")
	return ""
}

func TestDiscoverFilesOpts_ScopeFilter(t *testing.T) {
	g, _, revID := setupTestGraph(t)
	m := &manifest.Manifest{Domains: []manifest.DomainEntry{{
		Key:  "lab",
		Name: "lab",
		Scan: manifest.ScanConfig{Include: []string{"fixtures/tom-and-jerry/**"}, Exclude: []string{"**/node_modules/**"}},
	}}}
	rootDir := repoRootForTest(t)

	full, err := g.DiscoverFilesOpts(rootDir, "lab", revID, m, DiscoverOpts{VotesNeeded: 1})
	if err != nil {
		t.Fatalf("full discover: %v", err)
	}

	scoped, err := g.DiscoverFilesOpts(rootDir, "lab", revID, m, DiscoverOpts{
		VotesNeeded: 1,
		Scope:       []string{"fixtures/tom-and-jerry/arena-api/**"},
	})
	if err != nil {
		t.Fatalf("scoped discover: %v", err)
	}
	if scoped.TotalFiles == 0 {
		t.Fatal("scoped discover found nothing — scope intersect broken")
	}
	if scoped.TotalFiles >= full.TotalFiles {
		t.Errorf("scope did not narrow: scoped=%d full=%d", scoped.TotalFiles, full.TotalFiles)
	}
	for _, f := range scoped.Files {
		if !strings.HasPrefix(f, "fixtures/tom-and-jerry/arena-api/") {
			t.Errorf("out-of-scope file: %s", f)
		}
	}
}
