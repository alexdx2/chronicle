package store

import "testing"

// TestAddEvidenceRefreshesAssertionOnDedup pins the fix for assertion drift:
// re-observing the same evidence identity (same dedup key) with a CHANGED
// assertion must refresh the stored assertion + verification fields, not
// freeze them at first observation. Changing metrics (churn, complexity)
// re-assert on every scan — a frozen assertion makes later mechanical
// re-verification compare stale claims against fresh source.
func TestAddEvidenceRefreshesAssertionOnDedup(t *testing.T) {
	s := openTestDB(t)
	rev, _ := s.CreateRevision("dom", "", "refresh-a", "manual", "full", "{}")
	nodeID, _ := s.UpsertNode(NodeRow{
		NodeKey: "code:provider:dom:svc", Layer: "code", NodeType: "provider",
		DomainKey: "dom", Name: "Svc", Status: "active",
		FirstSeenRevisionID: rev, LastSeenRevisionID: rev,
		Confidence: 1.0, Freshness: 1.0, TrustScore: 1.0, Metadata: "{}",
	})

	base := EvidenceRow{
		TargetKind: "node", NodeID: nodeID,
		SourceKind: "git", ExtractorID: "churn-git", ExtractorVersion: "1",
		Confidence: 1.0, EvidenceStatus: "valid", EvidencePolarity: "positive",
		ValidFromRevisionID: rev, Metadata: "{}",
		Assertion: `{"commits":4}`, AssertionKind: "churn",
		VerificationStatus: "unverified", VerificationReason: "no verifier for churn",
	}
	id1, err := s.AddEvidence(base)
	if err != nil {
		t.Fatalf("AddEvidence 1: %v", err)
	}

	// Next scan: same identity, new observed value + verification outcome.
	updated := base
	updated.Assertion = `{"commits":15}`
	updated.LineEnd = 42
	updated.VerificationStatus = "verified"
	updated.VerificationReason = "verified at creation"
	id2, err := s.AddEvidence(updated)
	if err != nil {
		t.Fatalf("AddEvidence 2: %v", err)
	}
	if id2 != id1 {
		t.Fatalf("same identity must update in place: id1=%d id2=%d", id1, id2)
	}

	rows, err := s.ListEvidenceByNode(nodeID)
	if err != nil {
		t.Fatalf("ListEvidenceByNode: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("want 1 row, got %d", len(rows))
	}
	got := rows[0]
	if got.Assertion != `{"commits":15}` {
		t.Errorf("assertion frozen at first observation: %q", got.Assertion)
	}
	if got.VerificationStatus != "verified" {
		t.Errorf("verification_status not refreshed: %q", got.VerificationStatus)
	}
	if got.LineEnd != 42 {
		t.Errorf("line_end not refreshed: %d", got.LineEnd)
	}
}
