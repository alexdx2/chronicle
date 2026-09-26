package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/alexdx2/chronicle-core/snapshot"
)

func writeSnapshotFile(t *testing.T, path string, snap *snapshot.Snapshot) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("create %s: %v", path, err)
	}
	defer f.Close()
	if err := snapshot.Encode(f, snap); err != nil {
		t.Fatalf("encode %s: %v", path, err)
	}
}

// TestRunGraphMerge_WritesResultToOursPath is the brief's Step-1 CLI test:
// `graph merge` on three files writes the result into the `ours` path (git's
// %A convention) — base and theirs (%O and %B) are read-only inputs and must
// come out untouched.
func TestRunGraphMerge_WritesResultToOursPath(t *testing.T) {
	dir := t.TempDir()
	basePath := filepath.Join(dir, "base.jsonl")
	oursPath := filepath.Join(dir, "ours.jsonl")
	theirsPath := filepath.Join(dir, "theirs.jsonl")

	base := &snapshot.Snapshot{Header: snapshot.Header{Kind: "header", Format: snapshot.Format, Version: snapshot.Version, FactSchema: snapshot.FactSchema}}
	ours := &snapshot.Snapshot{
		Header: snapshot.Header{Kind: "header", Format: snapshot.Format, Version: snapshot.Version, FactSchema: snapshot.FactSchema, Seq: 1},
		Nodes: []snapshot.Node{
			{Kind: "node", ID: "code:module:orders:ours", Layer: "code", Type: "module", Domain: "orders", Name: "Ours", Status: "active"},
		},
	}
	theirs := &snapshot.Snapshot{
		Header: snapshot.Header{Kind: "header", Format: snapshot.Format, Version: snapshot.Version, FactSchema: snapshot.FactSchema, Seq: 3},
		Nodes: []snapshot.Node{
			{Kind: "node", ID: "code:module:orders:theirs", Layer: "code", Type: "module", Domain: "orders", Name: "Theirs", Status: "active"},
		},
	}
	writeSnapshotFile(t, basePath, base)
	writeSnapshotFile(t, oursPath, ours)
	writeSnapshotFile(t, theirsPath, theirs)

	baseBefore, err := os.ReadFile(basePath)
	if err != nil {
		t.Fatal(err)
	}
	theirsBefore, err := os.ReadFile(theirsPath)
	if err != nil {
		t.Fatal(err)
	}

	var warn bytes.Buffer
	runGraphMerge(basePath, oursPath, theirsPath, &warn)

	data, err := os.ReadFile(oursPath)
	if err != nil {
		t.Fatalf("read ours path after merge: %v", err)
	}
	merged, warnings, err := snapshot.Decode(bytes.NewReader(data), snapshot.DefaultLimits())
	if err != nil {
		t.Fatalf("decode merged result at ours path: %v", err)
	}
	if len(warnings) != 0 {
		t.Errorf("unexpected decode warnings on merged output: %+v", warnings)
	}

	ids := map[string]bool{}
	for _, n := range merged.Nodes {
		ids[n.ID] = true
	}
	if !ids["code:module:orders:ours"] || !ids["code:module:orders:theirs"] {
		t.Fatalf("merged result at ours-path missing an independent addition: %+v", merged.Nodes)
	}
	if merged.Header.Seq != 4 { // max(ours.Seq=1, theirs.Seq=3) + 1
		t.Errorf("merged Header.Seq = %d, want 4", merged.Header.Seq)
	}

	baseAfter, err := os.ReadFile(basePath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(baseBefore, baseAfter) {
		t.Errorf("basePath (%%O) was modified — the driver must only write oursPath")
	}
	theirsAfter, err := os.ReadFile(theirsPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(theirsBefore, theirsAfter) {
		t.Errorf("theirsPath (%%B) was modified — the driver must only write oursPath")
	}
}

// TestRunGraphMerge_MissingInputsDegradeGracefully proves the git
// merge-driver contract this command exists to fulfil: it always produces
// SOME result rather than aborting, even when base/theirs can't be read at
// all (here: nonexistent paths, standing in for a corrupt or absent file).
// Warnings are reported, but ours' own content is preserved rather than
// losing everything to a hard failure.
func TestRunGraphMerge_MissingInputsDegradeGracefully(t *testing.T) {
	dir := t.TempDir()
	basePath := filepath.Join(dir, "does-not-exist-base.jsonl")
	theirsPath := filepath.Join(dir, "does-not-exist-theirs.jsonl")
	oursPath := filepath.Join(dir, "ours.jsonl")

	ours := &snapshot.Snapshot{
		Header: snapshot.Header{Kind: "header", Format: snapshot.Format, Version: snapshot.Version, FactSchema: snapshot.FactSchema, Seq: 5},
		Nodes: []snapshot.Node{
			{Kind: "node", ID: "code:module:orders:solo", Layer: "code", Type: "module", Domain: "orders", Name: "Solo", Status: "active"},
		},
	}
	writeSnapshotFile(t, oursPath, ours)

	var warn bytes.Buffer
	runGraphMerge(basePath, oursPath, theirsPath, &warn)

	if warn.Len() == 0 {
		t.Error("expected warnings about unreadable base/theirs, got none")
	}

	data, err := os.ReadFile(oursPath)
	if err != nil {
		t.Fatalf("read ours path: %v", err)
	}
	merged, _, err := snapshot.Decode(bytes.NewReader(data), snapshot.DefaultLimits())
	if err != nil {
		t.Fatalf("decode result written despite missing base/theirs: %v", err)
	}
	if len(merged.Nodes) != 1 || merged.Nodes[0].ID != "code:module:orders:solo" {
		t.Fatalf("merged result = %+v, want ours' single node preserved", merged.Nodes)
	}
}

// TestNewGraphMergeCmd_RequiresExactlyThreeArgs guards the git merge-driver
// argument contract (%O %A %B) at the cobra layer.
func TestNewGraphMergeCmd_RequiresExactlyThreeArgs(t *testing.T) {
	cmd := newGraphMergeCmd()
	if err := cmd.Args(cmd, []string{"only-two", "args"}); err == nil {
		t.Error("want an error for 2 args, got nil")
	}
	if err := cmd.Args(cmd, []string{"base", "ours", "theirs"}); err != nil {
		t.Errorf("want no error for exactly 3 args, got %v", err)
	}
}
