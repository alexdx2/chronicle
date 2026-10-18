package graph

import (
	"github.com/alexdx2/chronicle-core/store"
	"testing"

	"github.com/alexdx2/chronicle-core/validate"
)

// A node whose own (structural) evidence was superseded stays active while an
// active edge with live evidence still points at it; it dies once that edge
// is gone too. Contradiction is not overridden.
func TestRecalculateNodeTrust_ActiveEdgeKeepsNodeAlive(t *testing.T) {
	g := setupGraphDefaults(t)
	revID := makeRevision(t, g)

	mk := func(key, typ, name string) int64 {
		id, err := g.UpsertNode(validate.NodeInput{
			NodeKey: key, Layer: "code", NodeType: typ, DomainKey: "test-domain", Name: name,
		}, revID)
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	topicKey := "code:provider:test-domain:topic"
	srcKey := "code:controller:test-domain:src"
	topicID := mk(topicKey, "provider", "topic")
	mk(srcKey, "controller", "src")

	if _, err := g.AddNodeEvidence(topicKey, validate.EvidenceInput{
		TargetKind: "node", SourceKind: "ast", FilePath: "consumer.ts", LineStart: 1, LineEnd: 1,
		ExtractorID: "chronicle-structural", ExtractorVersion: "1", Confidence: 0.9, RevisionID: revID,
	}); err != nil {
		t.Fatal(err)
	}
	edgeKey := validate.BuildEdgeKey(srcKey, topicKey, "INJECTS")
	edgeID, err := g.UpsertEdge(validate.EdgeInput{
		FromNodeKey: srcKey, ToNodeKey: topicKey, EdgeType: "INJECTS",
		DerivationKind: "hard", FromLayer: "code", ToLayer: "code",
	}, revID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := g.AddEdgeEvidence(edgeKey, validate.EvidenceInput{
		TargetKind: "edge", SourceKind: "ast", FilePath: "producer.ts", LineStart: 3, LineEnd: 3,
		ExtractorID: "chronicle-ast", ExtractorVersion: "1", Confidence: 0.9, RevisionID: revID,
	}); err != nil {
		t.Fatal(err)
	}

	// The consumer file stops asserting the topic: its row is superseded.
	if _, err := g.Store().SupersedeEvidenceNotIn("consumer.ts", "chronicle-structural", nil, revID); err != nil {
		t.Fatal(err)
	}
	status := func() string {
		t.Helper()
		if err := g.RecalculateNodeTrust(topicID); err != nil {
			t.Fatal(err)
		}
		n, err := g.Store().GetNodeByID(topicID)
		if err != nil {
			t.Fatal(err)
		}
		return n.Status
	}
	if got := status(); got != "active" {
		t.Fatalf("topic with an active, evidenced edge: status = %q, want active", got)
	}

	// Edge deactivated -> nothing keeps the node alive.
	if err := g.Store().UpdateEdgeTrust(edgeID, 0, 0, 0, "removed"); err != nil {
		t.Fatal(err)
	}
	if got := status(); got != "deleted" {
		t.Fatalf("topic with no active edge: status = %q, want deleted", got)
	}
}

// Rejected-only evidence asserts nothing: the edge computes "removed".
func TestComputeEdgeStatus_RejectedOnlyIsNotPositive(t *testing.T) {
	rejected := store.EvidenceRow{EvidencePolarity: "positive", EvidenceStatus: "valid", VerificationStatus: "rejected", Confidence: 0.1}
	if got := ComputeEdgeStatus([]store.EvidenceRow{rejected}); got != "removed" {
		t.Fatalf("rejected-only = %q, want removed", got)
	}
	ok := store.EvidenceRow{EvidencePolarity: "positive", EvidenceStatus: "valid", VerificationStatus: "verified", Confidence: 0.9}
	if got := ComputeEdgeStatus([]store.EvidenceRow{rejected, ok}); got != "active" {
		t.Fatalf("rejected + verified = %q, want active", got)
	}
}

// An edge whose only evidence was rejected at creation is deactivated by the
// recalculation (it used to stay active at confidence 0).
func TestRecalculateEdgeTrust_RejectedOnlyDeactivates(t *testing.T) {
	g := setupGraphDefaults(t)
	revID := makeRevision(t, g)
	from, to := "code:controller:test-domain:rf", "code:provider:test-domain:rt"
	for _, n := range []struct{ key, typ, name string }{{from, "controller", "rf"}, {to, "provider", "rt"}} {
		if _, err := g.UpsertNode(validate.NodeInput{NodeKey: n.key, Layer: "code", NodeType: n.typ, DomainKey: "test-domain", Name: n.name}, revID); err != nil {
			t.Fatal(err)
		}
	}
	edgeID, err := g.UpsertEdge(validate.EdgeInput{FromNodeKey: from, ToNodeKey: to, EdgeType: "INJECTS", DerivationKind: "hard", FromLayer: "code", ToLayer: "code"}, revID)
	if err != nil {
		t.Fatal(err)
	}
	evID, err := g.AddEdgeEvidence(validate.BuildEdgeKey(from, to, "INJECTS"), validate.EvidenceInput{
		TargetKind: "edge", SourceKind: "ast", FilePath: "a.ts", LineStart: 1, LineEnd: 1,
		ExtractorID: "x", ExtractorVersion: "1", Confidence: 0.1, RevisionID: revID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := g.Store().UpdateEvidenceVerification(evID, "valid", "rejected", "not in file", 0, 0, revID); err != nil {
		t.Fatal(err)
	}
	if err := g.RecalculateEdgeTrust(edgeID); err != nil {
		t.Fatal(err)
	}
	active := true
	edges, err := g.Store().ListEdges(store.EdgeFilter{FromNodeID: 0, Active: &active})
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range edges {
		if e.EdgeID == edgeID {
			t.Fatalf("rejected-only edge is still active: %+v", e)
		}
	}
}
