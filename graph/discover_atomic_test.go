package graph

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/alexdx2/chronicle-core/manifest"
	"github.com/alexdx2/chronicle-core/store"
)

// SQ-Contract 4: discover_files must validate fully, then write once. The
// field disaster: a missing/invalid scan config with >200 files wrote 22,870
// obligations plus infra/service nodes and evidence FIRST, discovered the
// config problem only afterward, and returned an error — the orphaned writes
// could only be cleared with chronicle_reset_db, and every re-run appended
// duplicate obligations (scan_obligations has no UNIQUE constraint, insert
// errors were discarded). These tests pin: (1) a refused discovery leaves
// every table discovery touches unchanged, (2) re-discovering the same
// revision is idempotent (delete-then-insert, no duplicate obligations), and
// (3) a valid discovery still writes obligations + infra + service nodes.

// makeTreeWithNFiles creates a git repo with n tracked dummy files and
// returns its root. DiscoverFilesOpts walks git-tracked files (git ls-files),
// so the >200-file gate needs a real tracked tree, not just files on disk.
func makeTreeWithNFiles(t *testing.T, n int) string {
	t.Helper()
	tmpDir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = tmpDir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-b", "main")
	for i := 0; i < n; i++ {
		p := filepath.Join(tmpDir, fmt.Sprintf("src/file_%d.txt", i))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	run("add", ".")
	run("commit", "-m", "c")
	return tmpDir
}

// snapshotCounts captures row counts across every table discovery touches.
// QueryRowScan takes a literal query string (no args param).
func snapshotCounts(t *testing.T, s *store.Store) map[string]int {
	t.Helper()
	out := map[string]int{}
	for _, q := range []struct{ k, sql string }{
		{"nodes", "SELECT COUNT(*) FROM graph_nodes"},
		{"edges", "SELECT COUNT(*) FROM graph_edges"},
		{"obligations", "SELECT COUNT(*) FROM scan_obligations"},
		{"evidence", "SELECT COUNT(*) FROM graph_evidence"},
		{"runs", "SELECT COUNT(*) FROM scan_runs"},
		{"outbox", "SELECT COUNT(*) FROM journal_outbox"},
	} {
		var n int
		if err := s.QueryRowScan(q.sql, &n); err != nil {
			t.Fatal(err)
		}
		out[q.k] = n
	}
	return out
}

// diffCounts compares two count snapshots manually (go-cmp is not a direct
// dependency of this module — no need to add one for a six-key map diff).
func diffCounts(before, after map[string]int) string {
	diff := ""
	for k := range before {
		if before[k] != after[k] {
			diff += fmt.Sprintf("%s: %d -> %d\n", k, before[k], after[k])
		}
	}
	return diff
}

func TestDiscoverFiles_InvalidConfigLeavesDBUnchanged(t *testing.T) {
	g, s, revID := setupTestGraph(t)
	before := snapshotCounts(t, s)

	// >200 files, nil manifest → must refuse BEFORE writing anything.
	_, err := g.DiscoverFilesOpts(makeTreeWithNFiles(t, 250), "testapp", revID, nil, DiscoverOpts{})
	if err == nil {
		t.Fatal("want config-refusal error")
	}
	var noCfg *ErrNoScanConfig
	if !errors.As(err, &noCfg) {
		t.Fatalf("want *ErrNoScanConfig, got %T: %v", err, err)
	}
	if noCfg.TotalFiles != 250 {
		t.Errorf("ErrNoScanConfig.TotalFiles = %d, want 250", noCfg.TotalFiles)
	}

	if diff := diffCounts(before, snapshotCounts(t, s)); diff != "" {
		t.Fatalf("DB changed on refused discovery (SQ-Contract 4):\n%s", diff)
	}
}

// A domain-scoped manifest with a real include pattern must never trip the
// gate, however many files git tracks outside that pattern — the >200 count
// is evaluated on the FILTERED result, not the raw git tree.
func TestDiscoverFiles_ValidScopedConfigIgnoresUnrelatedFileCount(t *testing.T) {
	g, _, revID := setupTestGraph(t)
	tmpDir := makeTreeWithNFiles(t, 250)

	m := &manifest.Manifest{Domains: []manifest.DomainEntry{
		{Name: "testapp", Scan: manifest.ScanConfig{Include: []string{"does-not-match/**"}}},
	}}

	res, err := g.DiscoverFilesOpts(tmpDir, "testapp", revID, m, DiscoverOpts{})
	if err != nil {
		t.Fatalf("DiscoverFilesOpts: %v", err)
	}
	if res.TotalFiles != 0 {
		t.Errorf("TotalFiles = %d, want 0 (scan config excludes everything)", res.TotalFiles)
	}
}

func TestDiscoverFiles_SameRevisionReDiscoveryIsIdempotent(t *testing.T) {
	g, s, revID := setupTestGraph(t)
	tmpDir := gitFixtureRepo(t)

	m := &manifest.Manifest{Domains: []manifest.DomainEntry{
		{Name: "testapp", Scan: manifest.ScanConfig{Include: []string{"src/**"}}},
	}}

	res1, err := g.DiscoverFilesOpts(tmpDir, "testapp", revID, m, DiscoverOpts{VotesNeeded: 1})
	if err != nil {
		t.Fatalf("first discover: %v", err)
	}
	obligations1, err := s.ListAllObligations(revID)
	if err != nil {
		t.Fatal(err)
	}

	res2, err := g.DiscoverFilesOpts(tmpDir, "testapp", revID, m, DiscoverOpts{VotesNeeded: 1})
	if err != nil {
		t.Fatalf("second discover: %v", err)
	}
	obligations2, err := s.ListAllObligations(revID)
	if err != nil {
		t.Fatal(err)
	}

	if res1.TotalFiles != res2.TotalFiles {
		t.Errorf("TotalFiles changed across re-discovery: %d vs %d", res1.TotalFiles, res2.TotalFiles)
	}
	if len(obligations2) != len(obligations1) {
		t.Errorf("obligation count changed on re-discovery (want idempotent, delete-then-insert): %d vs %d", len(obligations1), len(obligations2))
	}
	if len(obligations2) != res2.TotalFiles {
		t.Errorf("obligations = %d, want %d (one per file, no duplicates)", len(obligations2), res2.TotalFiles)
	}
}

// Regression: a valid discovery must still write one obligation per file plus
// the manifest's infra and service nodes, all inside the new transaction.
func TestDiscoverFiles_ValidConfigWritesObligationsInfraServices(t *testing.T) {
	g, s, revID := setupTestGraph(t)
	tmpDir := gitFixtureRepo(t)

	m := &manifest.Manifest{
		Domains:        []manifest.DomainEntry{{Name: "testapp", Scan: manifest.ScanConfig{Include: []string{"src/**"}}}},
		Infrastructure: []manifest.InfraEntry{{Name: "redis:6379", Type: "redis"}},
		Services:       []manifest.ServiceEntry{{Key: "orders-api", Path: "src"}},
	}

	res, err := g.DiscoverFilesOpts(tmpDir, "testapp", revID, m, DiscoverOpts{VotesNeeded: 1})
	if err != nil {
		t.Fatalf("DiscoverFilesOpts: %v", err)
	}
	if res.TotalFiles == 0 {
		t.Fatal("expected at least one file discovered")
	}

	obligations, err := s.ListAllObligations(revID)
	if err != nil {
		t.Fatal(err)
	}
	if len(obligations) != res.TotalFiles {
		t.Errorf("obligations = %d, want %d", len(obligations), res.TotalFiles)
	}

	nodes, err := s.ListNodes(store.NodeFilter{})
	if err != nil {
		t.Fatal(err)
	}
	var infraCount, serviceCount int
	for _, n := range nodes {
		switch n.Layer {
		case "infra":
			infraCount++
		case "service":
			serviceCount++
		}
	}
	if infraCount != 1 {
		t.Errorf("infra nodes = %d, want 1", infraCount)
	}
	if serviceCount != 1 {
		t.Errorf("service nodes = %d, want 1", serviceCount)
	}

	var evidenceCount int
	if err := s.QueryRowScan("SELECT COUNT(*) FROM graph_evidence", &evidenceCount); err != nil {
		t.Fatal(err)
	}
	if evidenceCount == 0 {
		t.Error("expected creation evidence for infra/service nodes")
	}
}
