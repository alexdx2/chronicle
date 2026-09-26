package snapshot

import (
	"path/filepath"
	"testing"

	"github.com/alexdx2/chronicle-core/store"
)

// newMigrateTestStore opens a fresh on-disk store for a BackfillBlobs test —
// same convention as export_test.go's own store.Open(filepath.Join(t.TempDir(),
// "test.db")) call sites.
func newMigrateTestStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

// seedMigrateNode gives domainKey one active, current node so GetDomains
// (which reads graph_nodes, not graph_revisions) actually discovers the
// domain — without this, BackfillBlobs' outer domain loop would silently
// see zero domains and do nothing. Returns the node id.
func seedMigrateNode(t *testing.T, st *store.Store, domainKey, filePath string) int64 {
	t.Helper()
	nodeID, err := st.UpsertNode(store.NodeRow{
		NodeKey: "code:controller:" + domainKey + ":a", Layer: "code", NodeType: "controller",
		DomainKey: domainKey, Name: "A", FilePath: filePath, Status: "active",
		Confidence: 1, Freshness: 1, TrustScore: 1, Metadata: "{}",
	})
	if err != nil {
		t.Fatalf("UpsertNode: %v", err)
	}
	return nodeID
}

// TestBackfillBlobs_FromScanExtractions is the brief's Step 1 synthetic-DB
// case: a revision with a real git_after_sha, a scan_extractions row on a
// file, and evidence of that SAME file with no blob of its own. After
// BackfillBlobs, file_coverage must carry a blob for that file, and it must
// equal the file's REAL git blob at that commit (git's own rev-parse answer,
// not a value recomputed through the same gitutil.BlobSHA the code under
// test also calls — an independent oracle).
//
// The evidence row (already "served" by the scan_extractions row on the same
// path) also pins that the fallback pass does not re-process — and does not
// double-count — a file the main pass already settled: Updated must be
// exactly 1, not 2.
func TestBackfillBlobs_FromScanExtractions(t *testing.T) {
	dir := initGitRepoT(t)
	writeFileT(t, dir, "src/a.ts", "export class A {}\n")
	sha := commitAllT(t, dir, "add a.ts")
	wantBlob := captureGitT(t, dir, "rev-parse", sha+":src/a.ts")

	st := newMigrateTestStore(t)
	nodeID := seedMigrateNode(t, st, "d", "src/a.ts")

	revID, err := st.CreateRevision("d", "", sha, "full_scan", "full", "{}")
	if err != nil {
		t.Fatalf("CreateRevision: %v", err)
	}
	if _, err := st.SaveExtraction(revID, "d", "src/a.ts", "extracted", "controller", "[]", ""); err != nil {
		t.Fatalf("SaveExtraction: %v", err)
	}
	if _, err := st.AddEvidence(store.EvidenceRow{
		TargetKind: "node", NodeID: nodeID, SourceKind: "structural",
		FilePath: "src/a.ts", ExtractorID: "structural", ExtractorVersion: "1",
		Confidence: 1, EvidenceStatus: "valid", EvidencePolarity: "positive",
		ValidFromRevisionID: revID,
	}); err != nil {
		t.Fatalf("AddEvidence: %v", err)
	}

	report, err := BackfillBlobs(st, dir)
	if err != nil {
		t.Fatalf("BackfillBlobs: %v", err)
	}
	if report.Updated != 1 {
		t.Errorf("report.Updated = %d, want 1 (one file, resolved once — the evidence fallback must not re-process it)", report.Updated)
	}
	if report.Unresolved != 0 {
		t.Errorf("report.Unresolved = %d, want 0", report.Unresolved)
	}

	coverage, err := st.ListCoverage("d")
	if err != nil {
		t.Fatalf("ListCoverage: %v", err)
	}
	var got *store.CoverageRow
	for i := range coverage {
		if coverage[i].FilePath == "src/a.ts" {
			got = &coverage[i]
		}
	}
	if got == nil {
		t.Fatalf("file_coverage has no row for src/a.ts after backfill (rows: %+v)", coverage)
	}
	if got.BlobSHA != wantBlob {
		t.Errorf("BlobSHA = %s, want %s (git's own blob id for src/a.ts at %s)", got.BlobSHA, wantBlob, sha)
	}
}

// TestBackfillBlobs_UnresolvedWhenRevisionMissing is the brief's Step 1
// missing-revision case: a revision's git_after_sha names a commit that was
// never made in repoRoot (a shallow or recreated repository, in the brief's
// terms) — BackfillBlobs must report it as Unresolved rather than failing
// the whole run.
func TestBackfillBlobs_UnresolvedWhenRevisionMissing(t *testing.T) {
	dir := initGitRepoT(t)
	writeFileT(t, dir, "README.md", "hello\n")
	commitAllT(t, dir, "init")

	st := newMigrateTestStore(t)
	seedMigrateNode(t, st, "d", "src/a.ts")

	const missingSHA = "deadbeefdeadbeefdeadbeefdeadbeefdeadbeef"
	revID, err := st.CreateRevision("d", "", missingSHA, "full_scan", "full", "{}")
	if err != nil {
		t.Fatalf("CreateRevision: %v", err)
	}
	if _, err := st.SaveExtraction(revID, "d", "src/a.ts", "extracted", "controller", "[]", ""); err != nil {
		t.Fatalf("SaveExtraction: %v", err)
	}

	report, err := BackfillBlobs(st, dir)
	if err != nil {
		t.Fatalf("BackfillBlobs: %v, want no error even though the revision's commit does not exist", err)
	}
	if report.Unresolved == 0 {
		t.Error("report.Unresolved = 0, want > 0 (git_after_sha does not exist in repoRoot's history)")
	}
	if report.Updated != 0 {
		t.Errorf("report.Updated = %d, want 0", report.Updated)
	}

	coverage, err := st.ListCoverage("d")
	if err != nil {
		t.Fatalf("ListCoverage: %v", err)
	}
	for _, c := range coverage {
		if c.FilePath == "src/a.ts" {
			t.Errorf("file_coverage has a row for src/a.ts (%+v) — an unresolved file must be left without one", c)
		}
	}
}

// TestBackfillBlobs_NoDomains proves the zero-domains case (a brand new,
// empty store) is a clean no-op, not an error — GetDomains returning nothing
// must not make the rest of BackfillBlobs panic on a nil/empty slice.
func TestBackfillBlobs_NoDomains(t *testing.T) {
	st := newMigrateTestStore(t)
	dir := initGitRepoT(t)

	report, err := BackfillBlobs(st, dir)
	if err != nil {
		t.Fatalf("BackfillBlobs: %v", err)
	}
	if report.Updated != 0 || report.Unresolved != 0 {
		t.Errorf("report = %+v, want zero value on an empty store", report)
	}
}

// TestBackfillBlobs_EvidenceFallbackWhenNoScanExtractions covers context
// item 4's own case: a file captured only by evidence (its scan_extractions
// row uses the structural role, which ListScanExtractions deliberately
// excludes — see that method's doc comment) still gets a blob, reconstructed
// from the evidence's OWN valid_from_revision_id rather than from any
// scan_extractions row.
func TestBackfillBlobs_EvidenceFallbackWhenNoScanExtractions(t *testing.T) {
	dir := initGitRepoT(t)
	writeFileT(t, dir, "src/b.ts", "export class B {}\n")
	sha := commitAllT(t, dir, "add b.ts")
	wantBlob := captureGitT(t, dir, "rev-parse", sha+":src/b.ts")

	st := newMigrateTestStore(t)
	nodeID := seedMigrateNode(t, st, "d", "src/b.ts")

	revID, err := st.CreateRevision("d", "", sha, "full_scan", "full", "{}")
	if err != nil {
		t.Fatalf("CreateRevision: %v", err)
	}
	// A structural-role extraction: present in scan_extractions, but
	// deliberately invisible to ListScanExtractions (and so to BackfillBlobs'
	// main pass) — exactly the "served by no SCAN extraction" case.
	if _, err := st.SaveStructuralExtraction(revID, "d", "src/b.ts", "extracted", "controller", "[]", "", ""); err != nil {
		t.Fatalf("SaveStructuralExtraction: %v", err)
	}
	if _, err := st.AddEvidence(store.EvidenceRow{
		TargetKind: "node", NodeID: nodeID, SourceKind: "structural",
		FilePath: "src/b.ts", ExtractorID: "structural", ExtractorVersion: "1",
		Confidence: 1, EvidenceStatus: "valid", EvidencePolarity: "positive",
		ValidFromRevisionID: revID,
	}); err != nil {
		t.Fatalf("AddEvidence: %v", err)
	}

	report, err := BackfillBlobs(st, dir)
	if err != nil {
		t.Fatalf("BackfillBlobs: %v", err)
	}
	if report.Updated != 1 {
		t.Errorf("report.Updated = %d, want 1 (the evidence fallback resolving src/b.ts)", report.Updated)
	}

	coverage, err := st.ListCoverage("d")
	if err != nil {
		t.Fatalf("ListCoverage: %v", err)
	}
	var got *store.CoverageRow
	for i := range coverage {
		if coverage[i].FilePath == "src/b.ts" {
			got = &coverage[i]
		}
	}
	if got == nil {
		t.Fatalf("file_coverage has no row for src/b.ts after backfill (rows: %+v)", coverage)
	}
	if got.BlobSHA != wantBlob {
		t.Errorf("BlobSHA = %s, want %s (git's own blob id for src/b.ts at %s)", got.BlobSHA, wantBlob, sha)
	}
}
