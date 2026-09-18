package store

import (
	"encoding/json"
	"testing"
)

// A commit has one revision row, and four writers can reach it: a scan, the
// refresh's verification, the structural phase and a surface import. These
// tests pin the rule that lets them share it — trigger_kind carries the
// strongest claim, everybody else describes itself in metadata — and the
// failures that rule exists to remove.

func claimedMetadata(t *testing.T, s *Store, id int64) map[string]any {
	t.Helper()
	rev, err := s.GetRevision(id)
	if err != nil {
		t.Fatalf("GetRevision(%d): %v", id, err)
	}
	md := map[string]any{}
	if err := json.Unmarshal([]byte(rev.Metadata), &md); err != nil {
		t.Fatalf("revision %d metadata %q: %v", id, rev.Metadata, err)
	}
	return md
}

func TestClaimRevisionCreatesOnceThenJoins(t *testing.T) {
	s := openTestStore(t)

	first, created, err := s.ClaimRevision(RevisionClaim{
		DomainKey: "d", AfterSHA: "abc", TriggerKind: "git_hook", Mode: "incremental",
		Metadata: `{"kind":"refresh"}`,
	})
	if err != nil || !created {
		t.Fatalf("first claim: id=%d created=%v err=%v", first, created, err)
	}

	second, created, err := s.ClaimRevision(RevisionClaim{
		DomainKey: "d", AfterSHA: "abc", TriggerKind: "git_hook", Mode: "incremental",
		Metadata: `{"kind":"structural"}`,
	})
	if err != nil {
		t.Fatalf("second claim: %v", err)
	}
	if created {
		t.Fatal("second claim created a second row for one commit")
	}
	if second != first {
		t.Fatalf("second claim got revision %d, want the existing %d", second, first)
	}
	// kind names the row's primary writer; the structural phase joining must
	// not rename the refresh's row out from under LatestRefreshRevision.
	if got := claimedMetadata(t, s, first)["kind"]; got != "refresh" {
		t.Fatalf("kind = %v after a structural claim joined, want refresh", got)
	}
}

// The crash this replaces: a scan is the first call of every scan run, and the
// post-commit hook has usually already named HEAD.
func TestClaimRevisionScanJoinsAHookRowAndTakesItOver(t *testing.T) {
	s := openTestStore(t)

	hookID, _, err := s.ClaimRevision(RevisionClaim{
		DomainKey: "d", AfterSHA: "abc", TriggerKind: "git_hook", Mode: "incremental",
		Metadata: `{"kind":"refresh"}`,
	})
	if err != nil {
		t.Fatalf("hook claim: %v", err)
	}
	if _, err := s.LatestScanRevision("d"); err == nil {
		t.Fatal("a hook row must not read as a scan")
	}

	scanID, created, err := s.ClaimRevision(RevisionClaim{
		DomainKey: "d", AfterSHA: "abc", TriggerKind: "manual", Mode: "full",
	})
	if err != nil {
		t.Fatalf("scan claim after a hook row: %v", err)
	}
	if created || scanID != hookID {
		t.Fatalf("scan claim: id=%d created=%v, want the hook's row %d reused", scanID, created, hookID)
	}

	scan, err := s.LatestScanRevision("d")
	if err != nil {
		t.Fatalf("LatestScanRevision after the scan claimed the commit: %v", err)
	}
	if scan.RevisionID != scanID {
		t.Fatalf("LatestScanRevision = %d, want %d", scan.RevisionID, scanID)
	}
	if scan.TriggerKind != "manual" || scan.Mode != "full" {
		t.Fatalf("promoted row is %s/%s, want manual/full", scan.TriggerKind, scan.Mode)
	}
}

// metadata.layer hides a row from LatestScanRevision, so a scan landing on a
// surface import's commit has to move the marker aside — without losing the
// import, which LatestSurfaceRevision must still find.
func TestClaimRevisionScanDemotesALayerImportWithoutLosingIt(t *testing.T) {
	s := openTestStore(t)

	uiID, _, err := s.ClaimRevision(RevisionClaim{
		DomainKey: "d", AfterSHA: "abc", TriggerKind: "manual", Mode: "incremental",
		Layer:    "ui",
		Metadata: `{"layer":"ui","source":"s.json","product":"p"}`,
	})
	if err != nil {
		t.Fatalf("ui claim: %v", err)
	}

	scanID, _, err := s.ClaimRevision(RevisionClaim{
		DomainKey: "d", AfterSHA: "abc", TriggerKind: "manual", Mode: "full",
	})
	if err != nil {
		t.Fatalf("scan claim onto a ui row: %v", err)
	}
	if scanID != uiID {
		t.Fatalf("scan claim created a second row (%d) for the ui commit %d", scanID, uiID)
	}

	if _, err := s.LatestScanRevision("d"); err != nil {
		t.Fatalf("the scanned commit is still invisible to LatestScanRevision: %v", err)
	}
	md := claimedMetadata(t, s, scanID)
	if md["layer"] != nil {
		t.Fatalf("layer = %v, want cleared so the scan is visible", md["layer"])
	}
	if md["layer_extra"] != "ui" {
		t.Fatalf("layer_extra = %v, want ui so the import is not forgotten", md["layer_extra"])
	}
	if _, err := s.LatestSurfaceRevision("d"); err != nil {
		t.Fatalf("the ui import was lost by the promotion: %v", err)
	}
}

// A layer import speaks for one slice of the graph. Promoting it would make a
// ui import read as the commit's code scan.
func TestClaimRevisionLayerImportNeverOutranksACodeWriter(t *testing.T) {
	s := openTestStore(t)

	hookID, _, err := s.ClaimRevision(RevisionClaim{
		DomainKey: "d", AfterSHA: "abc", TriggerKind: "git_hook", Mode: "incremental",
		Metadata: `{"kind":"refresh"}`,
	})
	if err != nil {
		t.Fatalf("hook claim: %v", err)
	}

	if _, _, err := s.ClaimRevision(RevisionClaim{
		DomainKey: "d", AfterSHA: "abc", TriggerKind: "manual", Mode: "incremental",
		Layer: "ui",
		Merge: map[string]any{"layer_extra": "ui", "surface": map[string]any{"product": "p"}},
	}); err != nil {
		t.Fatalf("ui claim onto a hook row: %v", err)
	}

	rev, err := s.GetRevision(hookID)
	if err != nil {
		t.Fatalf("GetRevision: %v", err)
	}
	if rev.TriggerKind != "git_hook" {
		t.Fatalf("a ui import promoted the row to %s", rev.TriggerKind)
	}
	if _, err := s.LatestScanRevision("d"); err == nil {
		t.Fatal("a ui import made the commit read as scanned")
	}
	if _, err := s.LatestSurfaceRevision("d"); err != nil {
		t.Fatalf("the ui import is not findable: %v", err)
	}
}

// A refresh that cannot own the row still has to record the check, or the
// graph reports a commit it did re-verify as merely stale.
func TestLatestRefreshRevisionFindsARefreshThatJoinedSomebodyElsesRow(t *testing.T) {
	s := openTestStore(t)

	uiID, _, err := s.ClaimRevision(RevisionClaim{
		DomainKey: "d", AfterSHA: "abc", TriggerKind: "manual", Mode: "incremental",
		Layer: "ui", Metadata: `{"layer":"ui"}`,
	})
	if err != nil {
		t.Fatalf("ui claim: %v", err)
	}
	if _, err := s.LatestRefreshRevision("d"); err == nil {
		t.Fatal("a ui row must not read as a re-verification on its own")
	}

	if _, _, err := s.ClaimRevision(RevisionClaim{
		DomainKey: "d", AfterSHA: "abc", TriggerKind: "git_hook", Mode: "incremental",
		Metadata: `{"kind":"refresh","noop":true}`,
		Merge:    map[string]any{"refresh": map[string]any{"noop": true}},
	}); err != nil {
		t.Fatalf("refresh claim onto a ui row: %v", err)
	}

	got, err := s.LatestRefreshRevision("d")
	if err != nil {
		t.Fatalf("the refresh's verification was lost: %v", err)
	}
	if got.RevisionID != uiID {
		t.Fatalf("LatestRefreshRevision = %d, want %d", got.RevisionID, uiID)
	}
}

// The structural phase is hook-driven too but advances a different pointer, so
// its own rows stay invisible to LatestRefreshRevision. Joining must not change
// that by accident.
func TestLatestRefreshRevisionStillSkipsAStructuralOnlyRow(t *testing.T) {
	s := openTestStore(t)

	if _, _, err := s.ClaimRevision(RevisionClaim{
		DomainKey: "d", AfterSHA: "abc", TriggerKind: "git_hook", Mode: "incremental",
		Metadata: `{"kind":"structural"}`,
	}); err != nil {
		t.Fatalf("structural claim: %v", err)
	}
	if _, err := s.LatestRefreshRevision("d"); err == nil {
		t.Fatal("a structural row read as a re-verification")
	}
}

func TestMergeableRevisionMetadataDropsKindOnly(t *testing.T) {
	got := MergeableRevisionMetadata(`{"kind":"refresh","lab":{"autopilot":true}}`)
	if _, ok := got["kind"]; ok {
		t.Fatal("kind would rename a row this writer did not create")
	}
	if _, ok := got["lab"]; !ok {
		t.Fatal("lab config was dropped")
	}
	if MergeableRevisionMetadata("") != nil || MergeableRevisionMetadata("not json") != nil {
		t.Fatal("empty and unparseable metadata must merge nothing")
	}
}

func TestClaimRevisionRefusesAnEmptySHA(t *testing.T) {
	s := openTestStore(t)
	if _, _, err := s.ClaimRevision(RevisionClaim{DomainKey: "d", TriggerKind: "manual", Mode: "full"}); err == nil {
		t.Fatal("an empty SHA names no commit and must not create a row")
	}
}
