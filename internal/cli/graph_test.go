package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/alexdx2/chronicle-core/registry"
	"github.com/alexdx2/chronicle-core/store"
)

// seedGraphUpdateStore opens a fresh store at dbPath and gives it one active
// node — runGraphUpdate's own record mapping is exercised in depth by the
// snapshot package's own tests; this only needs enough content to tell
// "empty" from "has one more node" apart across calls.
func seedGraphUpdateStore(t *testing.T, dbPath string) *store.Store {
	t.Helper()
	s, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	if _, err := s.UpsertNode(store.NodeRow{
		NodeKey: "code:provider:d:a", Layer: "code", NodeType: "provider", DomainKey: "d",
		Name: "A", FilePath: "svc/a.ts", Status: "active",
		Confidence: 1, Freshness: 1, TrustScore: 1, Metadata: "{}",
	}); err != nil {
		t.Fatalf("seed UpsertNode: %v", err)
	}
	return s
}

// TestRunGraphUpdate_WriteThenCheck drives runGraphUpdate directly (no
// cobra, no process) through the exact sequence the brief's CLI test
// describes: update writes seq 1; a --check right after is clean (exit 0);
// mutating the store and checking again is not (exit 1); a second update
// picks up the change at seq 2; --check is clean again.
func TestRunGraphUpdate_WriteThenCheck(t *testing.T) {
	dir := t.TempDir()
	s := seedGraphUpdateStore(t, filepath.Join(dir, "chronicle.db"))
	defer s.Close()

	reg, err := registry.LoadDefaults()
	if err != nil {
		t.Fatalf("registry.LoadDefaults: %v", err)
	}
	graphPath := filepath.Join(dir, "graph.jsonl")

	// No graph.jsonl yet: --check must report a mismatch and write nothing.
	if _, mismatch, err := runGraphUpdate(s, reg, graphPath, true); err != nil {
		t.Fatalf("runGraphUpdate(check, no file): %v", err)
	} else if !mismatch {
		t.Fatal("check on a project with no graph.jsonl yet must report a mismatch")
	}
	if _, err := os.Stat(graphPath); err == nil {
		t.Fatal("--check must not create graph.jsonl")
	}

	// update: writes the file at seq 1, one node added relative to nothing.
	result, mismatch, err := runGraphUpdate(s, reg, graphPath, false)
	if err != nil {
		t.Fatalf("runGraphUpdate(update): %v", err)
	}
	if mismatch {
		t.Fatal("update itself must never report a mismatch")
	}
	if result.Seq != 1 {
		t.Fatalf("first update: Seq = %d, want 1", result.Seq)
	}
	if result.Added != 1 || result.Removed != 0 || result.Changed != 0 {
		t.Fatalf("first update summary = %+v, want Added=1 Removed=0 Changed=0", result)
	}
	written, err := os.ReadFile(graphPath)
	if err != nil {
		t.Fatalf("read graph.jsonl: %v", err)
	}
	if len(written) == 0 {
		t.Fatal("update wrote an empty graph.jsonl")
	}

	// check again, store unchanged: code 0 (no mismatch), file untouched.
	result2, mismatch2, err := runGraphUpdate(s, reg, graphPath, true)
	if err != nil {
		t.Fatalf("runGraphUpdate(check, unchanged): %v", err)
	}
	if mismatch2 {
		t.Fatal("check must report no mismatch when the store hasn't changed since update")
	}
	if result2.Seq != 1 {
		t.Fatalf("check on an unchanged store: Seq = %d, want 1 (must not bump seq)", result2.Seq)
	}
	if after, err := os.ReadFile(graphPath); err != nil {
		t.Fatalf("read graph.jsonl after check: %v", err)
	} else if !bytes.Equal(written, after) {
		t.Fatal("--check modified graph.jsonl")
	}

	// Mutate the store, then check again: code 1 (mismatch), file still untouched.
	if _, err := s.UpsertNode(store.NodeRow{
		NodeKey: "code:provider:d:b", Layer: "code", NodeType: "provider", DomainKey: "d",
		Name: "B", FilePath: "svc/b.ts", Status: "active",
		Confidence: 1, Freshness: 1, TrustScore: 1, Metadata: "{}",
	}); err != nil {
		t.Fatalf("UpsertNode b: %v", err)
	}
	_, mismatch3, err := runGraphUpdate(s, reg, graphPath, true)
	if err != nil {
		t.Fatalf("runGraphUpdate(check, after mutation): %v", err)
	}
	if !mismatch3 {
		t.Fatal("check must report a mismatch after the store changed")
	}
	if stillOld, err := os.ReadFile(graphPath); err != nil {
		t.Fatalf("read graph.jsonl after second check: %v", err)
	} else if !bytes.Equal(written, stillOld) {
		t.Fatal("--check wrote to graph.jsonl")
	}

	// A real update now bumps seq and picks up the new node.
	result3, _, err := runGraphUpdate(s, reg, graphPath, false)
	if err != nil {
		t.Fatalf("runGraphUpdate(second update): %v", err)
	}
	if result3.Seq != 2 {
		t.Fatalf("second update: Seq = %d, want 2", result3.Seq)
	}
	if result3.Added != 1 || result3.Removed != 0 || result3.Changed != 0 {
		t.Fatalf("second update summary = %+v, want Added=1 Removed=0 Changed=0", result3)
	}

	// Final check is clean again.
	if _, mismatch4, err := runGraphUpdate(s, reg, graphPath, true); err != nil {
		t.Fatalf("runGraphUpdate(final check): %v", err)
	} else if mismatch4 {
		t.Fatal("check must be clean immediately after an update")
	}
}

// TestNewGraphUpdateCmd_RegisteredUnderGraph is a thin wiring smoke test:
// `chronicle graph update` must resolve through the real root command and
// declare --check. It never calls Execute(), so it can't trip the
// mismatch-error exit path.
func TestNewGraphUpdateCmd_RegisteredUnderGraph(t *testing.T) {
	root := NewRootCmd()
	cmd, _, err := root.Find([]string{"graph", "update"})
	if err != nil {
		t.Fatalf("chronicle graph update not registered: %v", err)
	}
	if f := cmd.Flags().Lookup("check"); f == nil {
		t.Fatal("graph update is missing its --check flag")
	}
}
