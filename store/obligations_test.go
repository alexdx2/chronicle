package store

import (
	"fmt"
	"path/filepath"
	"testing"
)

func TestClaimObligations_Basic(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	revID, _ := s.CreateRevision("myapp", "sha1", "", "manual", "full", "{}")

	// Create 5 obligations
	for i := 0; i < 5; i++ {
		s.CreateObligation(revID, "myapp", "scan_file", "file"+string(rune('A'+i))+".ts", "test")
	}

	// Claim 3
	batch, err := s.ClaimObligations(revID, "scan_file", 3)
	if err != nil {
		t.Fatalf("ClaimObligations: %v", err)
	}
	if len(batch) != 3 {
		t.Fatalf("expected 3 claimed, got %d", len(batch))
	}
	// Verify domain_key is returned
	for _, c := range batch {
		if c.DomainKey != "myapp" {
			t.Errorf("expected domain_key=myapp, got %s", c.DomainKey)
		}
	}
}

func TestClaimObligations_NoDuplicates(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	revID, _ := s.CreateRevision("myapp", "sha1", "", "manual", "full", "{}")

	for i := 0; i < 5; i++ {
		s.CreateObligation(revID, "myapp", "scan_file", "file"+string(rune('A'+i))+".ts", "test")
	}

	// First claim: 3 files
	batch1, _ := s.ClaimObligations(revID, "scan_file", 3)
	// Second claim: should get the remaining 2
	batch2, _ := s.ClaimObligations(revID, "scan_file", 3)

	if len(batch1) != 3 {
		t.Fatalf("batch1: expected 3, got %d", len(batch1))
	}
	if len(batch2) != 2 {
		t.Fatalf("batch2: expected 2, got %d", len(batch2))
	}

	// No overlap
	seen := make(map[string]bool)
	for _, c := range batch1 {
		seen[c.TargetKey] = true
	}
	for _, c := range batch2 {
		if seen[c.TargetKey] {
			t.Fatalf("duplicate file claimed: %s", c.TargetKey)
		}
	}
}

func TestClaimObligations_ReclaimsExpired(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	revID, _ := s.CreateRevision("myapp", "sha1", "", "manual", "full", "{}")
	s.CreateObligation(revID, "myapp", "scan_file", "stale.ts", "test")

	// Claim it
	batch1, _ := s.ClaimObligations(revID, "scan_file", 1)
	if len(batch1) != 1 {
		t.Fatal("expected 1 claimed")
	}

	// Manually expire the claim
	s.db.Exec(`UPDATE scan_obligations SET claim_expires_at = '2020-01-01T00:00:00Z' WHERE target_key = 'stale.ts'`)

	// Should be reclaimable
	batch2, _ := s.ClaimObligations(revID, "scan_file", 1)
	if len(batch2) != 1 {
		t.Fatalf("expected expired claim to be reclaimed, got %d", len(batch2))
	}
	if batch2[0].TargetKey != "stale.ts" {
		t.Fatalf("expected stale.ts, got %s", batch2[0].TargetKey)
	}
}

func TestSatisfyObligation_ClearsClaimFields(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	revID, _ := s.CreateRevision("myapp", "sha1", "", "manual", "full", "{}")
	s.CreateObligation(revID, "myapp", "scan_file", "file.ts", "test")

	// Claim then satisfy
	s.ClaimObligations(revID, "scan_file", 1)
	s.SatisfyObligation(revID, "scan_file", "file.ts")

	// Verify claim fields are cleared
	var claimedAt, expiresAt *string
	s.db.QueryRow(`SELECT claimed_at, claim_expires_at FROM scan_obligations WHERE target_key = 'file.ts'`).Scan(&claimedAt, &expiresAt)

	if claimedAt != nil {
		t.Error("claimed_at should be NULL after satisfy")
	}
	if expiresAt != nil {
		t.Error("claim_expires_at should be NULL after satisfy")
	}

	// Should not be reclaimable (status is satisfied)
	batch, _ := s.ClaimObligations(revID, "scan_file", 1)
	if len(batch) != 0 {
		t.Error("satisfied obligation should not be claimable")
	}
}

func TestObligationVoteColumns(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	revID, _ := s.CreateRevision("test", "sha1", "", "manual", "full", "{}")

	id, err := s.CreateObligation(revID, "test-domain", "scan_file", "src/foo.ts", "scan")
	if err != nil {
		t.Fatalf("CreateObligation: %v", err)
	}
	if id == 0 {
		t.Fatal("expected non-zero obligation_id")
	}

	// Verify vote columns exist and default to zero values
	var voteGroup string
	var voteIndex int
	err = s.db.QueryRow(`SELECT COALESCE(vote_group,''), COALESCE(vote_index,0) FROM scan_obligations WHERE obligation_id = ?`, id).Scan(&voteGroup, &voteIndex)
	if err != nil {
		t.Fatalf("QueryRow vote columns: %v", err)
	}
	if voteGroup != "" {
		t.Errorf("expected vote_group='', got %q", voteGroup)
	}
	if voteIndex != 0 {
		t.Errorf("expected vote_index=0, got %d", voteIndex)
	}
}

func TestCreateObligationWithVote(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	revID, _ := s.CreateRevision("test", "sha1", "", "manual", "full", "{}")

	id, err := s.CreateObligationWithVote(revID, "test-domain", "scan_file", "src/foo.ts", "scan", "scan_1:test:abc123", 2)
	if err != nil {
		t.Fatal(err)
	}
	if id == 0 {
		t.Fatal("expected non-zero id")
	}

	var voteGroup string
	var voteIndex int
	s.db.QueryRow(`SELECT vote_group, vote_index FROM scan_obligations WHERE obligation_id = ?`, id).Scan(&voteGroup, &voteIndex)
	if voteGroup != "scan_1:test:abc123" {
		t.Errorf("want scan_1:test:abc123, got %s", voteGroup)
	}
	if voteIndex != 2 {
		t.Errorf("want 2, got %d", voteIndex)
	}
}

func TestMultiplyObligationsByVotes(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	revID, _ := s.CreateRevision("test", "sha1", "", "manual", "full", "{}")

	for i := 1; i <= 3; i++ {
		s.CreateObligationWithVote(revID, "test-domain", "scan_file", "src/foo.ts", "scan", "scan_1:test:abc123", i)
	}

	var count int
	s.db.QueryRow(`SELECT COUNT(*) FROM scan_obligations WHERE revision_id = ? AND target_key = ?`, revID, "src/foo.ts").Scan(&count)
	if count != 3 {
		t.Errorf("want 3, got %d", count)
	}
}

func TestCountPendingObligations(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	revID, _ := s.CreateRevision("myapp", "sha1", "", "manual", "full", "{}")
	s.CreateObligation(revID, "myapp", "scan_file", "a.ts", "test")
	s.CreateObligation(revID, "myapp", "scan_file", "b.ts", "test")
	s.CreateObligation(revID, "myapp", "scan_file", "c.ts", "test")

	// All open
	count, _ := s.CountPendingObligations(revID, "scan_file")
	if count != 3 {
		t.Fatalf("expected 3 pending, got %d", count)
	}

	// Claim 2 — still pending (status is still 'open')
	s.ClaimObligations(revID, "scan_file", 2)
	count, _ = s.CountPendingObligations(revID, "scan_file")
	if count != 3 {
		t.Fatalf("claimed files should still count as pending, got %d", count)
	}

	// Satisfy 1
	s.SatisfyObligation(revID, "scan_file", "a.ts")
	count, _ = s.CountPendingObligations(revID, "scan_file")
	if count != 2 {
		t.Fatalf("expected 2 pending after satisfy, got %d", count)
	}
}

func TestClaimObligationsUniqueVoteGroup(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	revID, _ := s.CreateRevision("test", "sha1", "", "manual", "full", "{}")

	// Create 3 votes for file A, 3 votes for file B
	for i := 1; i <= 3; i++ {
		s.CreateObligationWithVote(revID, "d", "scan_file", "src/a.ts", "", "vg_a", i)
		s.CreateObligationWithVote(revID, "d", "scan_file", "src/b.ts", "", "vg_b", i)
	}

	// Claim batch of 4 — should get at most 1 per vote_group (2 total, not 4)
	claimed, err := s.ClaimObligations(revID, "scan_file", 4)
	if err != nil {
		t.Fatal(err)
	}
	if len(claimed) != 2 {
		t.Fatalf("want 2 claimed (one per vote_group), got %d", len(claimed))
	}

	voteGroups := map[string]bool{}
	for _, c := range claimed {
		if voteGroups[c.VoteGroup] {
			t.Errorf("duplicate vote_group in batch: %s", c.VoteGroup)
		}
		voteGroups[c.VoteGroup] = true
	}
}

func TestClaimObligationsNoVoteGroupStillWorksFull(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	revID, _ := s.CreateRevision("test", "sha1", "", "manual", "full", "{}")

	// Create 5 obligations with empty vote_group (votes=1 mode)
	for i := 0; i < 5; i++ {
		s.CreateObligation(revID, "d", "scan_file", fmt.Sprintf("src/f%d.ts", i), "")
	}

	// Should claim all 5 — empty vote_group means no dedup
	claimed, err := s.ClaimObligations(revID, "scan_file", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(claimed) != 5 {
		t.Fatalf("want 5, got %d", len(claimed))
	}
}

func TestObligationPoolStatus(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	revID, _ := s.CreateRevision("test", "sha1", "", "manual", "full", "{}")

	for i := 1; i <= 3; i++ {
		s.CreateObligationWithVote(revID, "d", "scan_file", "a.ts", "", "vg_a", i)
		s.CreateObligationWithVote(revID, "d", "scan_file", "b.ts", "", "vg_b", i)
	}

	status, err := s.ObligationPoolStatus(revID, "scan_file")
	if err != nil {
		t.Fatal(err)
	}
	if status.RemainingTotal != 6 {
		t.Errorf("remaining: want 6, got %d", status.RemainingTotal)
	}
	if status.ClaimableNow != 6 {
		t.Errorf("claimable: want 6, got %d", status.ClaimableNow)
	}

	s.ClaimObligations(revID, "scan_file", 2)
	status, _ = s.ObligationPoolStatus(revID, "scan_file")
	if status.ClaimableNow != 4 {
		t.Errorf("claimable after claim: want 4, got %d", status.ClaimableNow)
	}
	if status.InProgress != 2 {
		t.Errorf("in_progress: want 2, got %d", status.InProgress)
	}
}

func TestPoolStatusCountsExpiredAsClaimable(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	revID, _ := s.CreateRevision("test", "sha1", "", "manual", "full", "{}")

	s.CreateObligation(revID, "d", "scan_file", "a.ts", "")
	s.CreateObligation(revID, "d", "scan_file", "b.ts", "")
	s.ClaimObligations(revID, "scan_file", 2)

	// Expire one claim
	s.db.Exec(`UPDATE scan_obligations SET claim_expires_at = '2020-01-01T00:00:00Z' WHERE target_key = 'a.ts'`)

	status, _ := s.ObligationPoolStatus(revID, "scan_file")
	if status.RemainingTotal != 2 {
		t.Errorf("remaining: want 2, got %d", status.RemainingTotal)
	}
	if status.ClaimableNow != 1 {
		t.Errorf("claimable: want 1, got %d", status.ClaimableNow)
	}
	if status.InProgress != 1 {
		t.Errorf("in_progress: want 1, got %d", status.InProgress)
	}
	if status.Expired != 1 {
		t.Errorf("expired: want 1, got %d", status.Expired)
	}
}

func TestClaimObligationsReclaimsExpiredClaim(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	revID, _ := s.CreateRevision("test", "sha1", "", "manual", "full", "{}")
	s.CreateObligation(revID, "d", "scan_file", "a.ts", "")

	// Claim it
	claimed1, _ := s.ClaimObligations(revID, "scan_file", 1)
	if len(claimed1) != 1 {
		t.Fatal("expected 1 claimed")
	}

	// Nothing claimable now
	claimed2, _ := s.ClaimObligations(revID, "scan_file", 1)
	if len(claimed2) != 0 {
		t.Fatal("expected 0 claimed")
	}

	// Expire the claim manually
	s.db.Exec(`UPDATE scan_obligations SET claim_expires_at = '2020-01-01T00:00:00Z' WHERE obligation_id = ?`, claimed1[0].ObligationID)

	// Should be reclaimable now
	claimed3, _ := s.ClaimObligations(revID, "scan_file", 1)
	if len(claimed3) != 1 {
		t.Fatalf("expected 1 reclaimed, got %d", len(claimed3))
	}
	if claimed3[0].ObligationID != claimed1[0].ObligationID {
		t.Errorf("expected same obligation_id, got %d vs %d", claimed3[0].ObligationID, claimed1[0].ObligationID)
	}
}

func TestMarkObligationFailed(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	revID, _ := s.CreateRevision("myapp", "sha1", "", "manual", "full", "{}")
	id, _ := s.CreateObligation(revID, "myapp", "scan_file", "stuck.ts", "test")
	_, _ = s.ClaimObligations(revID, "scan_file", 1)

	if err := s.MarkObligationFailed(id, "test failure"); err != nil {
		t.Fatal(err)
	}

	status, err := s.ObligationPoolStatus(revID, "scan_file")
	if err != nil {
		t.Fatal(err)
	}
	if status.Failed != 1 {
		t.Errorf("failed: want 1, got %d", status.Failed)
	}
	if status.InProgress != 0 {
		t.Errorf("in_progress: want 0, got %d", status.InProgress)
	}
	if status.ClaimTTLMinutes <= 0 {
		t.Errorf("claim_ttl_minutes should be set")
	}
}

func TestSatisfyObligationByIDIsIdempotent(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	revID, _ := s.CreateRevision("test", "sha1", "", "manual", "full", "{}")
	id, err := s.CreateObligationWithVote(revID, "d", "scan_file", "a.ts", "", "vg_a", 1)
	if err != nil {
		t.Fatal(err)
	}

	err = s.SatisfyObligationByID(id)
	if err != nil {
		t.Fatal(err)
	}

	// Second call — idempotent, no error
	err = s.SatisfyObligationByID(id)
	if err != nil {
		t.Fatal(err)
	}

	status, err := s.ObligationPoolStatus(revID, "scan_file")
	if err != nil {
		t.Fatal(err)
	}
	if status.Completed != 1 {
		t.Errorf("completed: want 1, got %d", status.Completed)
	}
	if status.RemainingTotal != 0 {
		t.Errorf("remaining: want 0, got %d", status.RemainingTotal)
	}
}
