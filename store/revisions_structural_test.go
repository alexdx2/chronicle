package store

import (
	"errors"
	"testing"
)

// A structural pointer may only name a revision whose phase finished for the
// whole diff. An interrupted phase that still managed to write a row (or a row
// written by anything else) must not be readable as "structure is guaranteed
// up to here" — that is the one claim the pointer exists to make.
func TestLatestStructuralRevisionOnlyCountsCompletedPhases(t *testing.T) {
	s := openTestStore(t)

	if _, err := s.LatestStructuralRevision("d"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("no structural revision yet: %v", err)
	}

	// A refresh that is not structural at all.
	if _, err := s.CreateRevision("d", "", "sha-refresh", "git_hook", "incremental", `{"kind":"refresh"}`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.LatestStructuralRevision("d"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("a plain refresh is not a structural pointer: %v", err)
	}

	// A structural phase that did not finish.
	if _, err := s.CreateRevision("d", "", "sha-partial", "git_hook", "incremental",
		`{"kind":"structural","complete":false}`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.LatestStructuralRevision("d"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("an incomplete structural phase must not pin anything: %v", err)
	}

	// A finished one.
	id, err := s.CreateRevision("d", "", "sha-done", "git_hook", "incremental",
		`{"kind":"structural","complete":true}`)
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.LatestStructuralRevision("d")
	if err != nil {
		t.Fatalf("LatestStructuralRevision: %v", err)
	}
	if got.RevisionID != id || got.GitAfterSHA != "sha-done" {
		t.Fatalf("got %+v, want the completed structural revision", got)
	}

	// A later incomplete one does not move the pointer back off the good one.
	if _, err := s.CreateRevision("d", "", "sha-later-partial", "git_hook", "incremental",
		`{"kind":"structural","complete":false}`); err != nil {
		t.Fatal(err)
	}
	got, err = s.LatestStructuralRevision("d")
	if err != nil {
		t.Fatalf("LatestStructuralRevision after a failed run: %v", err)
	}
	if got.GitAfterSHA != "sha-done" {
		t.Fatalf("a failed phase moved the pointer to %q", got.GitAfterSHA)
	}
}

// Malformed metadata is one bad row, not a broken query: json_extract raises on
// invalid JSON, which without the json_valid guard would fail every call.
func TestLatestStructuralRevisionSurvivesMalformedMetadata(t *testing.T) {
	s := openTestStore(t)
	if _, err := s.CreateRevision("d", "", "sha-bad", "git_hook", "incremental", `not json at all`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateRevision("d", "", "sha-ok", "git_hook", "incremental",
		`{"kind":"structural","complete":true}`); err != nil {
		t.Fatal(err)
	}
	got, err := s.LatestStructuralRevision("d")
	if err != nil {
		t.Fatalf("one unparseable metadata broke the query: %v", err)
	}
	if got.GitAfterSHA != "sha-ok" {
		t.Fatalf("got %+v", got)
	}
}

func TestLatestStructuralRevisionIsDomainScoped(t *testing.T) {
	s := openTestStore(t)
	if _, err := s.CreateRevision("other", "", "sha-other", "git_hook", "incremental",
		`{"kind":"structural","complete":true}`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.LatestStructuralRevision("d"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("another domain's structural phase is not this domain's: %v", err)
	}
	// The empty domain means "any domain", as it does everywhere else here.
	if _, err := s.LatestStructuralRevision(""); err != nil {
		t.Fatalf("any-domain lookup: %v", err)
	}
}

// The rules-pack backlog: files whose deterministic rows were written by an
// older pack still need re-extraction even though nothing in them changed.
func TestCountFilesOnOtherExtractorVersion(t *testing.T) {
	s := openTestStore(t)
	revID, nodeID1, nodeID2 := seedNodes(t, s)

	add := func(nodeID int64, file, extractor, version, status string) {
		t.Helper()
		if _, err := s.AddEvidence(EvidenceRow{
			TargetKind: "node", NodeID: nodeID, SourceKind: "ast",
			FilePath: file, LineStart: 1, ExtractorID: extractor, ExtractorVersion: version,
			Confidence: 0.9, EvidenceStatus: status, EvidencePolarity: "positive",
			ValidFromRevisionID: revID, Metadata: "{}",
		}); err != nil {
			t.Fatal(err)
		}
	}

	add(nodeID1, "svc/a.ts", "chronicle-ast", "1", "valid")         // current pack
	add(nodeID1, "svc/b.ts", "chronicle-ast", "0.9", "valid")       // old pack
	add(nodeID2, "svc/b.ts", "chronicle-ast", "0.9", "revalidated") // same file again
	add(nodeID2, "svc/c.ts", "chronicle-ast", "0.8", "revalidated") // old pack, other file
	add(nodeID1, "svc/d.ts", "chronicle-ast", "0.9", "superseded")  // no longer counts
	add(nodeID1, "svc/e.ts", "chronicle-scan", "0.9", "valid")      // an agent's row, not the pack's

	n, err := s.CountFilesOnOtherExtractorVersion("orders", "chronicle-ast", "1")
	if err != nil {
		t.Fatalf("CountFilesOnOtherExtractorVersion: %v", err)
	}
	if n != 2 {
		t.Fatalf("files on an older pack = %d, want 2 (b.ts, c.ts)", n)
	}

	// Another domain's backlog is not this one's.
	n, err = s.CountFilesOnOtherExtractorVersion("elsewhere", "chronicle-ast", "1")
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("cross-domain leak: %d", n)
	}
}
