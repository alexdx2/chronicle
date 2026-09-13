package store

import (
	"errors"
	"testing"
)

// A structural pointer may only name a revision whose phase finished for the
// whole diff. An interrupted phase that still managed to write a marker (or a
// row written by anything else) must not be readable as "structure is
// guaranteed up to here" — that is the one claim the pointer exists to make.
func TestLatestStructuralRevisionOnlyCountsCompletedPhases(t *testing.T) {
	s := openTestStore(t)

	if _, err := s.LatestStructuralRevision("d"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("no structural revision yet: %v", err)
	}

	// A refresh that carries no structural stamp at all.
	if _, err := s.CreateRevision("d", "", "sha-refresh", "git_hook", "incremental", `{"kind":"refresh"}`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.LatestStructuralRevision("d"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("a plain refresh is not a structural pointer: %v", err)
	}

	// A structural phase that did not finish.
	if _, err := s.CreateRevision("d", "", "sha-partial", "git_hook", "incremental",
		`{"kind":"refresh","structural":{"complete":false,"pack":"1"}}`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.LatestStructuralRevision("d"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("an incomplete structural phase must not pin anything: %v", err)
	}

	// A finished one, stamped onto the revision that already sat at that
	// commit — the shape the phase actually writes, because
	// UNIQUE(domain_key, git_after_sha) forbids a second row there.
	id, err := s.CreateRevision("d", "", "sha-done", "git_hook", "incremental", `{"kind":"refresh"}`)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateRevisionMetadata(id, map[string]any{
		"structural": map[string]any{"complete": true, "pack": "1", "processed": 3},
	}); err != nil {
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
		`{"kind":"refresh","structural":{"complete":false}}`); err != nil {
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

// The stamp is the whole condition — not the trigger kind and not
// metadata.kind. A structural phase completes on whatever revision already
// sits at HEAD: the scan's own manual/full row when the graph was just
// scanned, the hook's refresh row on a normal commit, or a git_hook row of its
// own when nothing else had claimed that commit.
func TestLatestStructuralRevisionReadsTheStampOnAnyRevision(t *testing.T) {
	s := openTestStore(t)
	id, err := s.CreateRevision("d", "", "sha-scan", "manual", "full", `{}`)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateRevisionMetadata(id, map[string]any{
		"structural": map[string]any{"complete": true, "pack": "1"},
	}); err != nil {
		t.Fatal(err)
	}
	got, err := s.LatestStructuralRevision("d")
	if err != nil {
		t.Fatalf("a scan revision carrying the stamp is the structural pointer: %v", err)
	}
	if got.RevisionID != id {
		t.Fatalf("got %+v, want revision %d", got, id)
	}

	// A structure-only revision (nothing else had claimed that commit) too.
	sid, err := s.CreateRevision("d", "", "sha-own", "git_hook", "incremental",
		`{"kind":"structural","structural":{"complete":true,"pack":"1"}}`)
	if err != nil {
		t.Fatal(err)
	}
	got, err = s.LatestStructuralRevision("d")
	if err != nil {
		t.Fatal(err)
	}
	if got.RevisionID != sid {
		t.Fatalf("got %+v, want the structure-only revision %d", got, sid)
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
		`{"structural":{"complete":true}}`); err != nil {
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
		`{"structural":{"complete":true}}`); err != nil {
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
