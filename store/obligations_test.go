package store

import (
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
	for _, f := range batch1 {
		seen[f] = true
	}
	for _, f := range batch2 {
		if seen[f] {
			t.Fatalf("duplicate file claimed: %s", f)
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
	if batch2[0] != "stale.ts" {
		t.Fatalf("expected stale.ts, got %s", batch2[0])
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
