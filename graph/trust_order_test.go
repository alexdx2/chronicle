package graph

import (
	"testing"

	"github.com/alexdx2/chronicle-core/validate"
)

// RecalculateAllTrust must not depend on node iteration order (ListNodes
// orders by node_key). The "dependent" node has no live own evidence and is
// alive only through an active edge to the "anchor", whose stored status is
// stale ("deleted") and flips to active in the same recompute. Both key
// orders are exercised.
func TestRecalculateAllTrust_OrderIndependentLiveness(t *testing.T) {
	for _, dependentFirst := range []bool{true, false} {
		name := "dependent-recomputed-last"
		depKey, anchorKey := "code:provider:test-domain:zzz-dep", "code:provider:test-domain:aaa-anchor"
		if dependentFirst {
			name = "dependent-recomputed-first"
			depKey, anchorKey = "code:provider:test-domain:aaa-dep", "code:provider:test-domain:zzz-anchor"
		}
		t.Run(name, func(t *testing.T) {
			g := setupGraphDefaults(t)
			revID := makeRevision(t, g)
			mk := func(key, typ string) int64 {
				id, err := g.UpsertNode(validate.NodeInput{NodeKey: key, Layer: "code", NodeType: typ, DomainKey: "test-domain", Name: key}, revID)
				if err != nil {
					t.Fatal(err)
				}
				return id
			}
			depID, anchorID := mk(depKey, "provider"), mk(anchorKey, "provider")
			ev := func(key, file string) {
				if _, err := g.AddNodeEvidence(key, validate.EvidenceInput{
					TargetKind: "node", SourceKind: "ast", FilePath: file, LineStart: 1, LineEnd: 1,
					ExtractorID: "chronicle-structural", ExtractorVersion: "1", Confidence: 0.9, RevisionID: revID,
				}); err != nil {
					t.Fatal(err)
				}
			}
			ev(depKey, "dep.ts")
			ev(anchorKey, "anchor.ts")
			if _, err := g.UpsertEdge(validate.EdgeInput{FromNodeKey: anchorKey, ToNodeKey: depKey, EdgeType: "INJECTS", DerivationKind: "hard", FromLayer: "code", ToLayer: "code"}, revID); err != nil {
				t.Fatal(err)
			}
			if _, err := g.AddEdgeEvidence(validate.BuildEdgeKey(anchorKey, depKey, "INJECTS"), validate.EvidenceInput{
				TargetKind: "edge", SourceKind: "ast", FilePath: "anchor.ts", LineStart: 2, LineEnd: 2,
				ExtractorID: "chronicle-ast", ExtractorVersion: "1", Confidence: 0.9, RevisionID: revID,
			}); err != nil {
				t.Fatal(err)
			}
			if _, err := g.Store().SupersedeEvidenceNotIn("dep.ts", "chronicle-structural", nil, revID); err != nil {
				t.Fatal(err)
			}
			if err := g.Store().UpdateNodeTrust(anchorID, 0, 0, 0, "deleted"); err != nil {
				t.Fatal(err)
			}
			if err := g.RecalculateAllTrust(); err != nil {
				t.Fatal(err)
			}
			for id, want := range map[int64]string{depID: "active", anchorID: "active"} {
				n, err := g.Store().GetNodeByID(id)
				if err != nil {
					t.Fatal(err)
				}
				if n.Status != want {
					t.Errorf("%s status = %q, want %q", n.NodeKey, n.Status, want)
				}
			}
		})
	}
}
