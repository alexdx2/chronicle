package scan

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/alexdx2/chronicle-core/store"
)

const (
	stShaA  = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	stShaB  = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	stBaseA = "cccccccccccccccccccccccccccccccccccccccc"
)

// stateWork is a working copy with an open database holding one node.
func stateWork(t *testing.T, manifest string) (wt string, st *store.Store) {
	t.Helper()
	wt = t.TempDir()
	s, err := store.Open(filepath.Join(wt, ".depbot", "chronicle.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	rev, err := s.CreateRevision("d", "", "sha1", "manual", "full", "{}")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.UpsertNode(store.NodeRow{NodeKey: "code:file:d:one", Layer: "code", NodeType: "file", DomainKey: "d", Name: "one",
		Status: "active", FirstSeenRevisionID: rev, LastSeenRevisionID: rev, Confidence: 1, Metadata: "{}"}); err != nil {
		t.Fatal(err)
	}
	if manifest != "" {
		if err := os.WriteFile(filepath.Join(wt, ".depbot", "chronicle.domain.yaml"), []byte(manifest), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return wt, s
}

func emptyDir(t *testing.T, dir string) bool {
	t.Helper()
	ents, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	return len(ents) == 0
}

func TestState_RoundTrip(t *testing.T) {
	wt, st := stateWork(t, "domains: {d: {}}\n")
	stateDir := filepath.Join(t.TempDir(), "state")
	if err := checkpointState(stateDir, wt, stShaA, stBaseA, st, true); err != nil {
		t.Fatal(err)
	}

	wt2 := t.TempDir()
	restored, err := restoreState(stateDir, wt2, stShaA, stBaseA)
	if err != nil || !restored {
		t.Fatalf("restored %v, err %v", restored, err)
	}
	s2, err := store.Open(filepath.Join(wt2, ".depbot", "chronicle.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	if n, err := s2.GetNodeByKey("code:file:d:one"); err != nil || n.Name != "one" {
		t.Fatalf("restored database lacks the node: %v %+v", err, n)
	}
	man, err := os.ReadFile(filepath.Join(wt2, ".depbot", "chronicle.domain.yaml"))
	if err != nil || string(man) != "domains: {d: {}}\n" {
		t.Fatalf("manifest = %q, %v", man, err)
	}
}

func TestState_ManifestFromTreeIsNotOverwritten(t *testing.T) {
	wt, st := stateWork(t, "domains: {d: {}}\n")
	stateDir := t.TempDir()
	if err := checkpointState(stateDir, wt, stShaA, "", st, true); err != nil {
		t.Fatal(err)
	}
	wt2 := t.TempDir()
	if err := os.MkdirAll(filepath.Join(wt2, ".depbot"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wt2, ".depbot", "chronicle.domain.yaml"), []byte("from the tree\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if ok, err := restoreState(stateDir, wt2, stShaA, ""); err != nil || !ok {
		t.Fatalf("restored %v, err %v", ok, err)
	}
	if man, _ := os.ReadFile(filepath.Join(wt2, ".depbot", "chronicle.domain.yaml")); string(man) != "from the tree\n" {
		t.Fatalf("manifest = %q", man)
	}
}

func TestState_MismatchedShaDiscards(t *testing.T) {
	wt, st := stateWork(t, "")
	stateDir := t.TempDir()
	if err := checkpointState(stateDir, wt, stShaA, stBaseA, st, false); err != nil {
		t.Fatal(err)
	}
	wt2 := t.TempDir()
	restored, err := restoreState(stateDir, wt2, stShaB, stBaseA)
	if err != nil || restored {
		t.Fatalf("restored %v, err %v", restored, err)
	}
	if !emptyDir(t, stateDir) {
		t.Fatal("state of another sha was not discarded")
	}
	if _, err := os.Stat(filepath.Join(wt2, ".depbot", "chronicle.db")); err == nil {
		t.Fatal("database copied into the working copy")
	}
}

func TestState_MismatchedBaseDiscards(t *testing.T) {
	wt, st := stateWork(t, "")
	stateDir := t.TempDir()
	if err := checkpointState(stateDir, wt, stShaA, stBaseA, st, false); err != nil {
		t.Fatal(err)
	}
	restored, err := restoreState(stateDir, t.TempDir(), stShaA, "")
	if err != nil || restored {
		t.Fatalf("restored %v, err %v", restored, err)
	}
	if !emptyDir(t, stateDir) {
		t.Fatal("state of another base was not discarded")
	}
}

func TestState_TruncatedDBDiscards(t *testing.T) {
	wt, st := stateWork(t, "")
	stateDir := t.TempDir()
	if err := checkpointState(stateDir, wt, stShaA, "", st, false); err != nil {
		t.Fatal(err)
	}
	db := filepath.Join(stateDir, "chronicle.db")
	fi, err := os.Stat(db)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Truncate(db, fi.Size()/2); err != nil {
		t.Fatal(err)
	}
	wt2 := t.TempDir()
	restored, err := restoreState(stateDir, wt2, stShaA, "")
	if err != nil || restored {
		t.Fatalf("restored %v, err %v", restored, err)
	}
	if !emptyDir(t, stateDir) {
		t.Fatal("truncated state was not discarded")
	}
	for _, suffix := range []string{"", "-wal", "-shm"} {
		if _, err := os.Stat(filepath.Join(wt2, ".depbot", "chronicle.db"+suffix)); err == nil {
			t.Fatalf("unusable database left in the working copy (%q)", suffix)
		}
	}
}

func TestState_AtomicCheckpoint(t *testing.T) {
	wt, st := stateWork(t, "")
	stateDir := t.TempDir()
	if err := checkpointState(stateDir, wt, stShaA, "", st, false); err != nil {
		t.Fatal(err)
	}
	rev, _ := st.CreateRevision("d", "", "sha2", "manual", "full", "{}")
	if _, err := st.UpsertNode(store.NodeRow{NodeKey: "code:file:d:two", Layer: "code", NodeType: "file", DomainKey: "d", Name: "two",
		Status: "active", FirstSeenRevisionID: rev, LastSeenRevisionID: rev, Confidence: 1, Metadata: "{}"}); err != nil {
		t.Fatal(err)
	}

	boom := errors.New("crash between copy and rename")
	renameFile = func(string, string) error { return boom }
	t.Cleanup(func() { renameFile = os.Rename })
	if err := checkpointState(stateDir, wt, stShaA, "", st, false); !errors.Is(err, boom) {
		t.Fatalf("checkpoint error = %v, want the rename failure", err)
	}
	renameFile = os.Rename

	// The previous checkpoint is whole: it restores and has the first node only.
	wt2 := t.TempDir()
	if ok, err := restoreState(stateDir, wt2, stShaA, ""); err != nil || !ok {
		t.Fatalf("restored %v, err %v", ok, err)
	}
	s2, err := store.Open(filepath.Join(wt2, ".depbot", "chronicle.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	if _, err := s2.GetNodeByKey("code:file:d:one"); err != nil {
		t.Fatalf("previous checkpoint lost its node: %v", err)
	}
	if _, err := s2.GetNodeByKey("code:file:d:two"); err == nil {
		t.Fatal("the failed checkpoint leaked into the saved database")
	}
}
