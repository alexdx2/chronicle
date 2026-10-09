package cli

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alexdx2/chronicle-core/graph"
	"github.com/alexdx2/chronicle-core/registry"
	"github.com/alexdx2/chronicle-core/snapshot"
	"github.com/alexdx2/chronicle-core/store"
)

// seedRepoWithSnapshot builds a git repo whose HEAD commit contains a
// graph.jsonl exported from a seeded store, and returns the (still existing)
// database path next to it.
func seedRepoWithSnapshot(t *testing.T) (repo, dbPath, graphPath string, reg *registry.Registry) {
	t.Helper()
	repo = t.TempDir()
	gitRun(t, repo, "init", "-q")
	gitRun(t, repo, "config", "user.email", "t@example.com")
	gitRun(t, repo, "config", "user.name", "t")
	depbot := filepath.Join(repo, ".depbot")
	dbPath = filepath.Join(depbot, "chronicle.db")
	graphPath = filepath.Join(depbot, "graph.jsonl")

	s := seedGraphUpdateStore(t, dbPath)
	defer s.Close()
	var err error
	if reg, err = registry.LoadDefaults(); err != nil {
		t.Fatal(err)
	}
	if _, _, err := runGraphUpdate(s, reg, graphPath, false); err != nil {
		t.Fatal(err)
	}
	gitRun(t, repo, "add", ".depbot/graph.jsonl")
	gitRun(t, repo, "commit", "-q", "-m", "snapshot")
	return repo, dbPath, graphPath, reg
}

func TestRunGraphRestore_FromFile_RoundTrip(t *testing.T) {
	repo, dbPath, graphPath, reg := seedRepoWithSnapshot(t)
	os.Remove(dbPath)
	res, err := runGraphRestore(restoreInput{RepoRoot: repo, DBPath: dbPath, GraphPath: graphPath, Registry: reg})
	if err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	_, mismatch, err := runGraphUpdate(st, reg, graphPath, true)
	if err != nil || mismatch {
		t.Fatalf("restored db does not reproduce snapshot: mismatch=%v err=%v", mismatch, err)
	}
	if res.Seq == 0 || len(res.Revisions) == 0 {
		t.Fatalf("bad result %+v", res)
	}
	rev, err := st.LatestScanRevision("")
	if err != nil || rev == nil || rev.GitAfterSHA != res.Commit {
		t.Fatalf("restore must register a scan revision at %s, got %+v (err %v)", res.Commit, rev, err)
	}
}

func TestRunGraphRestore_At_UsesHistory(t *testing.T) {
	repo, dbPath, graphPath, reg := seedRepoWithSnapshot(t)
	os.Remove(dbPath)
	if err := os.WriteFile(graphPath, []byte("garbage\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := runGraphRestore(restoreInput{RepoRoot: repo, DBPath: dbPath, GraphPath: graphPath, At: "HEAD", Registry: reg})
	if err != nil {
		t.Fatal(err)
	}
	if want := strings.TrimSpace(gitCapture(t, repo, "rev-parse", "HEAD")); res.Commit != want {
		t.Fatalf("commit = %q, want %q", res.Commit, want)
	}
	if res.Counts.Node != 1 {
		t.Fatalf("expected the committed snapshot (1 node), got %+v", res.Counts)
	}
}

func TestRunGraphRestore_NoVersion(t *testing.T) {
	repo := t.TempDir()
	gitRun(t, repo, "init", "-q")
	gitRun(t, repo, "config", "user.email", "t@example.com")
	gitRun(t, repo, "config", "user.name", "t")
	if err := os.WriteFile(filepath.Join(repo, "README"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitRun(t, repo, "add", "README")
	gitRun(t, repo, "commit", "-q", "-m", "init")
	reg, err := registry.LoadDefaults()
	if err != nil {
		t.Fatal(err)
	}
	_, err = runGraphRestore(restoreInput{
		RepoRoot: repo, DBPath: filepath.Join(repo, ".depbot", "chronicle.db"),
		GraphPath: filepath.Join(repo, ".depbot", "graph.jsonl"), At: "HEAD", Registry: reg,
	})
	if !errors.Is(err, snapshot.ErrNoVersion) {
		t.Fatalf("want ErrNoVersion, got %v", err)
	}
}

func TestRunGraphRestore_RefusesWithoutForce(t *testing.T) {
	repo, dbPath, graphPath, reg := seedRepoWithSnapshot(t)
	in := restoreInput{RepoRoot: repo, DBPath: dbPath, GraphPath: graphPath, Registry: reg}
	if _, err := runGraphRestore(in); !errors.Is(err, errDBExists) {
		t.Fatalf("want errDBExists, got %v", err)
	}
	// Leave stale sidecars behind; --force must remove them with the db.
	for _, suf := range []string{"-wal", "-shm"} {
		if err := os.WriteFile(dbPath+suf, []byte("stale"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	in.Force = true
	if _, err := runGraphRestore(in); err != nil {
		t.Fatal(err)
	}
	for _, suf := range []string{"-wal", "-shm"} {
		if b, err := os.ReadFile(dbPath + suf); err == nil && string(b) == "stale" {
			t.Fatalf("stale %s survived --force", suf)
		}
	}
}

func TestRunGraphRestore_JournalNotReplayed(t *testing.T) {
	repo, dbPath, graphPath, reg := seedRepoWithSnapshot(t)

	// After the snapshot was exported, another node enters the journal only.
	s, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.UpsertNode(store.NodeRow{
		NodeKey: "code:provider:d:stale", Layer: "code", NodeType: "provider", DomainKey: "d",
		Name: "Stale", FilePath: "svc/stale.ts", Status: "active",
		Confidence: 1, Freshness: 1, TrustScore: 1, Metadata: "{}",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.FlushJournal(); err != nil {
		t.Fatal(err)
	}
	s.Close()
	events := filepath.Join(filepath.Dir(dbPath), "events", "d.jsonl")
	if b, err := os.ReadFile(events); err != nil || !strings.Contains(string(b), "stale") {
		t.Fatalf("test setup: journal must hold the stale node (err %v)", err)
	}

	if _, err := runGraphRestore(restoreInput{RepoRoot: repo, DBPath: dbPath, GraphPath: graphPath, Force: true, Registry: reg}); err != nil {
		t.Fatal(err)
	}

	// Open the way openGraph does: Open syncs the journal.
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	stats, err := graph.New(st, reg).QueryStats("")
	if err != nil {
		t.Fatal(err)
	}
	if stats.NodeCount != 1 {
		t.Fatalf("journal events were replayed over the snapshot: %d nodes, want 1", stats.NodeCount)
	}
}
