package graph

import (
	"testing"

	"github.com/alexdx2/chronicle-core/validate"
)

// A retired endpoint (node + edge whose evidence is all stale/superseded) must
// stay inactive through RecalculateAllTrust: the trust pass must not set the
// edge active again, nor the node back to active.
func TestRecalculateAllTrust_KeepsRetiredFactsInactive(t *testing.T) {
	g := setupGraphDefaults(t)
	revID := makeRevision(t, g)
	const dom = "test-domain"
	svcKey := "code:controller:" + dom + ":svc"
	epKey := "contract:endpoint:" + dom + ":delete:/orders/{id}"
	if _, err := g.UpsertNode(validate.NodeInput{NodeKey: svcKey, Layer: "code", NodeType: "controller", DomainKey: dom, Name: "svc"}, revID); err != nil {
		t.Fatal(err)
	}
	epID, err := g.UpsertNode(validate.NodeInput{NodeKey: epKey, Layer: "contract", NodeType: "endpoint", DomainKey: dom, Name: "DELETE /orders/{id}"}, revID)
	if err != nil {
		t.Fatal(err)
	}
	ev := func(target string) validate.EvidenceInput {
		return validate.EvidenceInput{TargetKind: target, SourceKind: "ast", FilePath: "orders.ts", LineStart: 3, LineEnd: 3,
			ExtractorID: "x", ExtractorVersion: "1", Confidence: 0.9, RevisionID: revID}
	}
	if _, err := g.AddNodeEvidence(epKey, ev("node")); err != nil {
		t.Fatal(err)
	}
	edgeKey := validate.BuildEdgeKey(svcKey, epKey, "HANDLES_OPERATION")
	_, err = g.UpsertEdge(validate.EdgeInput{FromNodeKey: svcKey, ToNodeKey: epKey, EdgeType: "HANDLES_OPERATION",
		DerivationKind: "hard", FromLayer: "code", ToLayer: "contract"}, revID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := g.AddEdgeEvidence(edgeKey, ev("edge")); err != nil {
		t.Fatal(err)
	}
	if err := g.RecalculateAllTrust(); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := g.Store().MarkEvidenceStaleByFiles([]string{"orders.ts"}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := g.Store().RetireUnbackedByFiles(dom, []string{"orders.ts"}, revID); err != nil {
		t.Fatal(err)
	}
	check := func(when string) {
		t.Helper()
		e, err := g.Store().GetEdgeByKey(edgeKey)
		if err != nil {
			t.Fatal(err)
		}
		n, err := g.Store().GetNodeByID(epID)
		if err != nil {
			t.Fatal(err)
		}
		if e.Active || n.Status == "active" {
			t.Errorf("%s: retired endpoint came back (edge active=%v, node status=%q)", when, e.Active, n.Status)
		}
	}
	check("after retire")
	if err := g.RecalculateAllTrust(); err != nil {
		t.Fatal(err)
	}
	check("after RecalculateAllTrust")
}
