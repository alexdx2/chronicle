package cli

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alexdx2/chronicle-core/registry"
	"github.com/alexdx2/chronicle-core/snapshot"
	"github.com/alexdx2/chronicle-core/store"
)

// copyTomAndJerryFixtureDB copies the tracked tom-and-jerry demo database
// into a fresh temp dir and opens the copy — same convention as (and same
// fixture as) snapshot/export_test.go's copyFixtureStore: tests must never
// open the checked-in fixture in place, since store.Open runs migrations
// and creates -wal/-shm siblings next to whatever file it's given.
//
// The fixture's .depbot dir is known to be dirty in the working tree (see
// task-7-report.md) as a side effect of other test suites' e2e runs earlier
// on this branch — copying whatever state chronicle.db is actually in is
// fine for this test, which only exercises `graph migrate`'s own logic
// against real scan history, not any particular fixture content.
func copyTomAndJerryFixtureDB(t *testing.T) *store.Store {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "fixtures", "tom-and-jerry", ".depbot", "chronicle.db"))
	if err != nil {
		t.Fatalf("read fixture db: %v", err)
	}
	dst := filepath.Join(t.TempDir(), "chronicle.db")
	if err := os.WriteFile(dst, data, 0644); err != nil {
		t.Fatalf("copy fixture db: %v", err)
	}
	s, err := store.Open(dst)
	if err != nil {
		t.Fatalf("open copied fixture db: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

// TestRunGraphMigrate_TomAndJerryFixture drives runGraphMigrate directly (no
// cobra) against a copy of the real tom-and-jerry scan history — the brief's
// Step 1 CLI test. The temp repoRoot is a real, freshly initialized git repo
// with no commits reachable from the fixture's recorded git_after_shas (this
// is a copy of the DATABASE only, not of whatever repo state chronicle.db
// itself was originally scanned against), so BackfillBlobs is expected to
// resolve few or no blobs here — that is fine and expected: this test's job
// is the brief's own bar, "a valid graph.jsonl, Decode with no warnings",
// end to end through the real migrate path (BackfillBlobs -> runGraphUpdate
// -> ensureGitWiring), not a specific backfill yield. The missing-history
// and found-history backfill cases themselves are already pinned by
// snapshot's own TestBackfillBlobs_* tests.
func TestRunGraphMigrate_TomAndJerryFixture(t *testing.T) {
	repoRoot := t.TempDir()
	gitRun(t, repoRoot, "init", "-q", "-b", "main")

	st := copyTomAndJerryFixtureDB(t)
	reg, err := registry.LoadDefaults()
	if err != nil {
		t.Fatalf("registry.LoadDefaults: %v", err)
	}
	graphPath := filepath.Join(repoRoot, ".depbot", "graph.jsonl")

	result, err := runGraphMigrate(st, reg, repoRoot, graphPath)
	if err != nil {
		t.Fatalf("runGraphMigrate: %v", err)
	}
	if !result.Migrated {
		t.Fatal("result.Migrated = false, want true (the tom-and-jerry fixture has real scan history)")
	}
	if result.Backfill.Unresolved < 0 || result.Backfill.Updated < 0 {
		t.Fatalf("result.Backfill = %+v, want non-negative counts", result.Backfill)
	}

	written, err := os.ReadFile(graphPath)
	if err != nil {
		t.Fatalf("read %s: %v", graphPath, err)
	}
	if len(written) == 0 {
		t.Fatal("runGraphMigrate wrote an empty graph.jsonl")
	}

	snap, warnings, err := snapshot.Decode(bytes.NewReader(written), snapshot.DefaultLimits())
	if err != nil {
		t.Fatalf("snapshot.Decode: %v", err)
	}
	// The checked-in tom-and-jerry fixture already carries one legacy node
	// (node_id 1, "infra:broker:battle-result") whose key predates the
	// layer:type:domain:qualified_name format, plus the counts_mismatch that
	// node's resulting drop causes — confirmed by running plain
	// runGraphUpdate (no BackfillBlobs, no migrate at all) against the same
	// fixture and seeing the identical pair of warnings. That is pre-existing
	// fixture data debt this task must not touch (see task-7-report.md), not
	// something BackfillBlobs/runGraphMigrate introduces, so it is tolerated
	// here by name; any OTHER warning fails the test — that would mean
	// migrate's own output is malformed, which is exactly the brief's bar.
	for _, w := range warnings {
		known := (w.Code == "bad_key" && strings.Contains(w.Msg, "infra:broker:battle-result")) ||
			(w.Code == "counts_mismatch" && strings.Contains(w.Msg, "node:"))
		if !known {
			t.Errorf("unexpected Decode warning = %+v, want only the fixture's known pre-existing bad_key/counts_mismatch pair", w)
		}
	}
	if snap.Header.Seq != 1 {
		t.Errorf("Header.Seq = %d, want 1 (first graph.jsonl this project ever wrote)", snap.Header.Seq)
	}

	// ensureGitWiring must have brought .gitignore/.gitattributes up to
	// Task 6's shape (reused, not reimplemented — see runGraphMigrate).
	gitattributes, err := os.ReadFile(filepath.Join(repoRoot, ".gitattributes"))
	if err != nil {
		t.Fatalf("read .gitattributes: %v", err)
	}
	if !bytes.Contains(gitattributes, []byte("graph.jsonl merge=chronicle-graph")) {
		t.Errorf(".gitattributes = %q, want a graph.jsonl merge=chronicle-graph line", gitattributes)
	}
	gitignore, err := os.ReadFile(filepath.Join(repoRoot, ".gitignore"))
	if err != nil {
		t.Fatalf("read .gitignore: %v", err)
	}
	if !bytes.Contains(gitignore, []byte("!.depbot/graph.jsonl")) {
		t.Errorf(".gitignore = %q, want a !.depbot/graph.jsonl exception", gitignore)
	}
}

// TestRunGraphMigrate_NoScanHistory_FallsBackToGraphUpdate is the brief's
// "fresh project" case: a store with zero scan revisions has nothing to
// migrate from, so runGraphMigrate must do exactly what `chronicle graph
// update` does (Migrated=false, graph.jsonl still written) rather than
// running BackfillBlobs or touching git wiring at all.
func TestRunGraphMigrate_NoScanHistory_FallsBackToGraphUpdate(t *testing.T) {
	repoRoot := t.TempDir()
	// Deliberately NOT a git repo at all — the fallback path must never
	// reach BackfillBlobs or ensureGitWiring's git subprocess call, so this
	// must still succeed.

	st, err := store.Open(filepath.Join(t.TempDir(), "chronicle.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	defer st.Close()
	if _, err := st.UpsertNode(store.NodeRow{
		NodeKey: "code:provider:d:a", Layer: "code", NodeType: "provider", DomainKey: "d",
		Name: "A", FilePath: "svc/a.ts", Status: "active",
		Confidence: 1, Freshness: 1, TrustScore: 1, Metadata: "{}",
	}); err != nil {
		t.Fatalf("UpsertNode: %v", err)
	}

	reg, err := registry.LoadDefaults()
	if err != nil {
		t.Fatalf("registry.LoadDefaults: %v", err)
	}
	graphPath := filepath.Join(repoRoot, "graph.jsonl")

	result, err := runGraphMigrate(st, reg, repoRoot, graphPath)
	if err != nil {
		t.Fatalf("runGraphMigrate: %v", err)
	}
	if result.Migrated {
		t.Fatal("result.Migrated = true, want false (store has no scan revisions at all)")
	}
	if result.Update.Seq != 1 {
		t.Errorf("result.Update.Seq = %d, want 1", result.Update.Seq)
	}
	if _, err := os.Stat(graphPath); err != nil {
		t.Errorf("graph.jsonl not written by the graph-update fallback: %v", err)
	}
	if _, err := os.Stat(filepath.Join(repoRoot, ".gitignore")); err == nil {
		t.Error(".gitignore was written by the no-history fallback — it must behave exactly like `graph update`, nothing more")
	}
}

// TestPrintMigrateReport_ContainsNextSteps pins the exact next-step command
// this task's brief requires be printed verbatim (this command deliberately
// never runs git rm or commits itself).
func TestPrintMigrateReport_ContainsNextSteps(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	realStdout := os.Stdout
	os.Stdout = w
	printMigrateReport(migrateResult{
		Migrated: true,
		Backfill: snapshot.BackfillReport{Updated: 3, Unresolved: 1},
		Update:   graphUpdateResult{Seq: 2},
	}, ".depbot/graph.jsonl")
	w.Close()
	os.Stdout = realStdout
	out, _ := io.ReadAll(r)

	got := string(out)
	if !bytes.Contains([]byte(got), []byte(migrateNextSteps)) {
		t.Errorf("printMigrateReport output = %q, want it to contain the exact next-step command %q", got, migrateNextSteps)
	}
	if !bytes.Contains([]byte(got), []byte("3")) || !bytes.Contains([]byte(got), []byte("1")) {
		t.Errorf("printMigrateReport output = %q, want it to mention Updated=3 and Unresolved=1", got)
	}
}

// TestNewGraphMigrateCmd_RegisteredUnderGraph is a thin wiring smoke test,
// the same shape as TestNewGraphUpdateCmd_RegisteredUnderGraph: `chronicle
// graph migrate` must resolve through the real root command. It never calls
// Execute(), so it can't trip runGraphMigrate's own error paths.
func TestNewGraphMigrateCmd_RegisteredUnderGraph(t *testing.T) {
	root := NewRootCmd()
	if _, _, err := root.Find([]string{"graph", "migrate"}); err != nil {
		t.Fatalf("chronicle graph migrate not registered: %v", err)
	}
}
