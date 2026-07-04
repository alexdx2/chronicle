package graph

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alexdx2/chronicle-core/store"
	"github.com/alexdx2/chronicle-core/validate"
)

// TestFinalizeScopesTierAToChangedFiles pins the incremental-finalize cost fix:
// when a revision carries verify_file obligations (= an incremental scan with a
// known changed set), the Tier-A file passes re-read ONLY the changed files.
// An unchanged-per-git file whose on-disk content drifted is not re-read — its
// metrics update on ITS next change, not on every unrelated 1-file scan.
func TestFinalizeScopesTierAToChangedFiles(t *testing.T) {
	g := setupGraphDefaults(t)
	revID := makeRevision(t, g)

	loopy := "class A { m(xs) { for (const x of xs) { doThing(); } } }\n"
	flat := "class B { m() { return 1; } }\n"
	dir := t.TempDir()
	write := func(name, content string) string {
		t.Helper()
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	pa := write("a.ts", flat)
	pb := write("b.ts", flat)
	for key, fp := range map[string]string{
		"code:provider:orders:a": pa,
		"code:provider:orders:b": pb,
	} {
		if _, err := g.UpsertNode(validate.NodeInput{
			NodeKey: key, Layer: "code", NodeType: "provider", DomainKey: "orders",
			Name: strings.ToUpper(key[len(key)-1:]), FilePath: fp,
		}, revID); err != nil {
			t.Fatalf("UpsertNode: %v", err)
		}
	}

	// Full finalize (no obligations): both measured as flat.
	if _, err := g.FinalizeIncrementalScan("orders", revID); err != nil {
		t.Fatalf("finalize 1: %v", err)
	}

	// Both files gain a loop on disk, but only a.ts is in the changed set.
	write("a.ts", loopy)
	write("b.ts", loopy)
	rev2, err := g.store.CreateRevision("test-domain", "abc123", "def456", "git_hook", "incremental", "{}")
	if err != nil {
		t.Fatalf("CreateRevision rev2: %v", err)
	}
	if _, err := g.store.CreateObligation(rev2, "orders", "verify_file", pa, "changed"); err != nil {
		t.Fatalf("CreateObligation: %v", err)
	}
	if _, err := g.FinalizeIncrementalScan("orders", rev2); err != nil {
		t.Fatalf("finalize 2: %v", err)
	}

	na, _ := g.store.GetNodeByKey("code:provider:orders:a")
	ma, _ := complexityFromMetadata(na.Metadata)
	if ma.LoopCount != 1 {
		t.Errorf("changed file a.ts must be re-measured: loop_count = %d, want 1", ma.LoopCount)
	}
	nb, _ := g.store.GetNodeByKey("code:provider:orders:b")
	mb, _ := complexityFromMetadata(nb.Metadata)
	if mb.LoopCount != 0 {
		t.Errorf("out-of-scope b.ts must NOT be re-read on an incremental finalize: loop_count = %d, want 0 (stale until its own change)", mb.LoopCount)
	}
}

// TestSimilarityUsesCachedFingerprints proves signatures are cached in node
// Metadata and reused for out-of-scope files: the twin edge survives a scoped
// re-run even when the unchanged twin's on-disk content is replaced, because
// its cached fingerprint — not the file — is compared.
func TestSimilarityUsesCachedFingerprints(t *testing.T) {
	g := setupGraphDefaults(t)
	revID := makeRevision(t, g)

	dir := t.TempDir()
	write := func(name, content string) string {
		t.Helper()
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	pa := write("order.service.ts", cloneA)
	pb := write("invoice.service.ts", cloneB)
	for key, fp := range map[string]string{
		"code:provider:orders:order-svc":   pa,
		"code:provider:orders:invoice-svc": pb,
	} {
		if _, err := g.UpsertNode(validate.NodeInput{
			NodeKey: key, Layer: "code", NodeType: "provider", DomainKey: "orders",
			Name: key, FilePath: fp,
		}, revID); err != nil {
			t.Fatalf("UpsertNode: %v", err)
		}
	}

	// Full pass: computes + caches fingerprints, creates the twin edge.
	if err := g.ComputeSimilarity(revID); err != nil {
		t.Fatalf("ComputeSimilarity: %v", err)
	}
	na, _ := g.store.GetNodeByKey("code:provider:orders:order-svc")
	if !strings.Contains(na.Metadata, `"fp":"`) {
		t.Fatalf("fingerprint must be cached in metadata: %q", na.Metadata)
	}

	// Scoped run: only invoice-svc changed (still a clone); order.service.ts is
	// REPLACED on disk with unrelated content but is out of scope — the cached
	// fingerprint must be used, so the twin edge persists.
	write("order.service.ts", unrelated)
	scope := fileScope{pb: true}
	if err := g.computeSimilarity(revID, scope); err != nil {
		t.Fatalf("computeSimilarity scoped: %v", err)
	}
	active := true
	edges, _ := g.store.ListEdges(store.EdgeFilter{EdgeType: "SIMILAR_TO", Active: &active})
	if len(edges) != 1 {
		t.Fatalf("cached fingerprint should keep the twin pair, got %d edges", len(edges))
	}
}
