package graph

import (
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
