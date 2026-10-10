package cli

import (
	"errors"
	"os"
	"os/exec"
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

func TestRunGraphRestore_LeavesStructuralPointer(t *testing.T) {
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
	domain := res.Domains[0]
	rev, err := st.LatestStructuralRevision(domain)
	if err != nil || rev == nil || rev.GitAfterSHA != res.Commit {
		t.Fatalf("restore must leave a structural pointer at %s, got %+v (err %v)", res.Commit, rev, err)
	}
	base, sweep, forget := structuralBase(st, repo, domain)
	if base != res.Commit || sweep || forget != 0 {
		t.Fatalf("structuralBase = (%q, %v, %d), want (%q, false, 0)", base, sweep, forget, res.Commit)
	}
}

func TestRunGraphRestore_MergeBaseWith(t *testing.T) {
	repo, dbPath, graphPath, reg := seedRepoWithSnapshot(t)
	gitRun(t, repo, "branch", "-M", "main")
	fork := strings.TrimSpace(gitCapture(t, repo, "rev-parse", "HEAD"))
	gitRun(t, repo, "branch", "feature")
	// main moves on with a newer snapshot commit.
	if err := os.WriteFile(filepath.Join(repo, "other.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitRun(t, repo, "add", "other.txt")
	gitRun(t, repo, "commit", "-q", "-m", "main moves")
	mainTip := strings.TrimSpace(gitCapture(t, repo, "rev-parse", "HEAD"))
	if mainTip == fork {
		t.Fatal("setup: main did not advance")
	}
	gitRun(t, repo, "checkout", "-q", "feature")
	os.Remove(dbPath)
	res, err := runGraphRestore(restoreInput{
		RepoRoot: repo, DBPath: dbPath, GraphPath: graphPath,
		At: "main", MergeBaseWith: "HEAD", Registry: reg,
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Commit != fork {
		t.Fatalf("commit = %q, want the fork commit %q (not main's tip %q)", res.Commit, fork, mainTip)
	}
}

func TestRunGraphRestore_ForceKeepsOldDBWhenLoadFails(t *testing.T) {
	repo, dbPath, graphPath, reg := seedRepoWithSnapshot(t)
	before, err := os.ReadFile(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	// Decode is lenient, so corrupt the one thing the store itself rejects:
	// a node status outside the CHECK constraint fails inside snapshot.Load.
	raw, err := os.ReadFile(graphPath)
	if err != nil {
		t.Fatal(err)
	}
	bad := strings.Replace(string(raw), `"status":"active"`, `"status":"bogus"`, 1)
	if bad == string(raw) {
		t.Fatalf("test setup: no node status to corrupt in %s", raw)
	}
	if err := os.WriteFile(graphPath, []byte(bad), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := runGraphRestore(restoreInput{RepoRoot: repo, DBPath: dbPath, GraphPath: graphPath, Force: true, Registry: reg}); err == nil {
		t.Fatal("corrupt snapshot must fail")
	} else if !strings.Contains(err.Error(), "snapshot: Load") {
		t.Fatalf("failure must come from snapshot.Load, got %v", err)
	}
	after, err := os.ReadFile(dbPath)
	if err != nil || string(after) != string(before) {
		t.Fatalf("old db must survive a failed restore (err %v)", err)
	}
	if _, err := os.Stat(dbPath + ".restore-tmp"); !os.IsNotExist(err) {
		t.Fatalf("temp db must be cleaned up, stat err %v", err)
	}
}

// publishChainT writes a minimal chain publication for codeSha on ref (tree:
// only .depbot/graph.jsonl + chronicle.domain.yaml) and returns its sha.
func publishChainT(t *testing.T, repo, ref, codeSha string, graph, man []byte) string {
	t.Helper()
	git := func(env []string, args ...string) string {
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		cmd.Env = append(os.Environ(), env...)
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("git %v: %v", args, err)
		}
		return strings.TrimSpace(string(out))
	}
	scratch := t.TempDir()
	env := []string{"GIT_INDEX_FILE=" + filepath.Join(t.TempDir(), "index")}
	for name, data := range map[string][]byte{".depbot/graph.jsonl": graph, ".depbot/chronicle.domain.yaml": man} {
		f := filepath.Join(scratch, filepath.Base(name))
		if err := os.WriteFile(f, data, 0o644); err != nil {
			t.Fatal(err)
		}
		blob := git(nil, "hash-object", "-w", f)
		git(env, "update-index", "--add", "--cacheinfo", "100644,"+blob+","+name)
	}
	tree := git(env, "write-tree")
	id, err := snapshot.ContentID(graph, man, nil)
	if err != nil {
		t.Fatal(err)
	}
	msg := "chronicle snapshot for " + codeSha + "\n\n" +
		snapshot.TrailerSnapshotID + ": " + id + "\n" +
		snapshot.TrailerCodeSha + ": " + codeSha + "\n" +
		snapshot.TrailerCodeRef + ": refs/heads/main\n" +
		snapshot.TrailerSchema + ": 1\n"
	c := git(nil, "commit-tree", tree, "-m", msg)
	git(nil, "update-ref", ref, c)
	return c
}

func TestRunGraphRestore_PublicationPinsCodeCommit(t *testing.T) {
	src, _, graphPath, reg := seedRepoWithSnapshot(t)
	graph, err := os.ReadFile(graphPath)
	if err != nil {
		t.Fatal(err)
	}
	_ = src
	repo := t.TempDir()
	gitRun(t, repo, "init", "-q")
	gitRun(t, repo, "config", "user.email", "t@example.com")
	gitRun(t, repo, "config", "user.name", "t")
	if err := os.WriteFile(filepath.Join(repo, "a.go"), []byte("package a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitRun(t, repo, "add", "a.go")
	gitRun(t, repo, "commit", "-q", "-m", "code")
	out, err := exec.Command("git", "-C", repo, "rev-parse", "HEAD").Output()
	if err != nil {
		t.Fatal(err)
	}
	code := strings.TrimSpace(string(out))
	pub := publishChainT(t, repo, "refs/heads/chronicle/graphs/main", code, graph, []byte("domains:\n  d: {name: D}\n"))

	db := filepath.Join(t.TempDir(), "chronicle.db")
	res, err := runGraphRestore(restoreInput{RepoRoot: repo, DBPath: db, At: code, Registry: reg})
	if err != nil {
		t.Fatal(err)
	}
	if res.Commit != code || res.Publication != pub {
		t.Fatalf("commit=%s publication=%s, want %s / %s", res.Commit, res.Publication, code, pub)
	}
	st, err := store.Open(db)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	rev, err := st.LatestScanRevision("")
	if err != nil || rev == nil || rev.GitAfterSHA != code {
		t.Fatalf("restore revision must pin code commit %s, got %+v (err %v)", code, rev, err)
	}
}
