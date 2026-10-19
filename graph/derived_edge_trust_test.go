package graph

import (
	"testing"

	"github.com/alexdx2/chronicle-core/validate"
)

// A derived edge (no evidence) retired by the cascade stays inactive through
// RecalculateAllTrust; a fresh derived edge stays active.
func TestRecalculateAllTrust_DerivedEdgeRetiredByCascadeStaysOff(t *testing.T) {
	g := setupGraphDefaults(t)
	revID := makeRevision(t, g)
	const dom = "test-domain"
	a := "code:controller:" + dom + ":a"
	b := "code:provider:" + dom + ":b"
	c := "code:provider:" + dom + ":c"
	for _, k := range []struct{ key, typ string }{{a, "controller"}, {b, "provider"}, {c, "provider"}} {
		if _, err := g.UpsertNode(validate.NodeInput{NodeKey: k.key, Layer: "code", NodeType: k.typ, DomainKey: dom, Name: k.key}, revID); err != nil {
			t.Fatal(err)
		}
	}
	ev := func(target string) validate.EvidenceInput {
		return validate.EvidenceInput{TargetKind: target, SourceKind: "ast", FilePath: "b.ts", LineStart: 1, LineEnd: 1,
			ExtractorID: "x", ExtractorVersion: "1", Confidence: 0.9, RevisionID: revID}
	}
	if _, err := g.AddNodeEvidence(b, ev("node")); err != nil {
		t.Fatal(err)
	}
	abKey := validate.BuildEdgeKey(a, b, "DEPENDS_ON")
	if _, err := g.UpsertEdge(validate.EdgeInput{FromNodeKey: a, ToNodeKey: b, EdgeType: "DEPENDS_ON", DerivationKind: "hard", FromLayer: "code", ToLayer: "code"}, revID); err != nil {
		t.Fatal(err)
	}
	if _, err := g.AddEdgeEvidence(abKey, ev("edge")); err != nil {
		t.Fatal(err)
	}
	bcKey := validate.BuildEdgeKey(b, c, "DEPENDS_ON")
	if _, err := g.UpsertEdge(validate.EdgeInput{FromNodeKey: b, ToNodeKey: c, EdgeType: "DEPENDS_ON", DerivationKind: "inferred", FromLayer: "code", ToLayer: "code"}, revID); err != nil {
		t.Fatal(err)
	}
	if err := g.RecalculateAllTrust(); err != nil {
		t.Fatal(err)
	}
	active := func(key string) bool {
		e, err := g.Store().GetEdgeByKey(key)
		if err != nil {
			t.Fatal(err)
		}
		return e.Active
	}
	if !active(bcKey) {
		t.Fatal("fresh derived edge is not active after RecalculateAllTrust")
	}
	if _, _, _, err := g.Store().MarkEvidenceStaleByFiles([]string{"b.ts"}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := g.Store().RetireUnbackedByFiles(dom, []string{"b.ts"}, revID); err != nil {
		t.Fatal(err)
	}
	if active(bcKey) {
		t.Fatal("cascade did not retire the derived edge")
	}
	if err := g.RecalculateAllTrust(); err != nil {
		t.Fatal(err)
	}
	if active(bcKey) {
		t.Error("derived edge retired by the cascade came back after RecalculateAllTrust")
	}
}
