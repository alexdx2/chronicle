package viewmodel

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/alexdx2/chronicle-core/store"
)

// Rev3 task 4: a frame whose members are all invisible is not drawn, and an
// arrow into it does not fold there either — it disappears with the node.

// synthFoldStore: component comp (service owner-svc) uses model Payload;
// Payload has two fields with no owner of their own (HAS_FIELD is not an
// ownership edge, and the fields carry no file path, so they fall into the
// synthetic "owner unknown" bucket — the exact battle.queue/BattleEvent
// shape). comp also calls an endpoint of a second, otherwise-untouched
// service collapsed-svc — an ordinary collapsed card, unrelated to the
// unowned bucket, to prove those are unaffected.
func synthFoldStore(t *testing.T) (st *store.Store, compKey, modelKey, collapsedSvcKey string) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "fold.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	rev, err := st.CreateRevision("fold", "", "fold-fixture", "manual", "full", "{}")
	if err != nil {
		t.Fatal(err)
	}
	mk := func(key, layer, ntype, name, file string) int64 {
		id, err := st.UpsertNode(store.NodeRow{NodeKey: key, Layer: layer, NodeType: ntype, DomainKey: "fold", Name: name,
			FilePath: file, Status: "active", Confidence: 0.9, Freshness: 1, TrustScore: 0.9, Metadata: "{}", LastSeenRevisionID: rev})
		if err != nil {
			t.Fatalf("upsert %s: %v", key, err)
		}
		return id
	}
	edge := func(from, to int64, fk, tk, typ string) {
		if _, err := st.UpsertEdge(store.EdgeRow{EdgeKey: fk + "->" + tk + ":" + typ, FromNodeID: from, ToNodeID: to, EdgeType: typ,
			DerivationKind: "hard", Active: true, Confidence: 0.9, Freshness: 1, TrustScore: 0.9, Metadata: "{}", LastSeenRevisionID: rev}); err != nil {
			t.Fatalf("edge %s: %v", typ, err)
		}
	}
	svcKey := "service:service:fold:owner-svc"
	compKey = "code:provider:fold:owner-svc/src/comp"
	modelKey = "data:model:fold:Payload"
	f1Key := "data:field:fold:Payload.a"
	f2Key := "data:field:fold:Payload.b"
	collapsedSvcKey = "service:service:fold:collapsed-svc"
	epKey := "contract:endpoint:fold:get:/x"
	ctlKey := "code:controller:fold:collapsed-svc/src/ctl"

	mk(svcKey, "service", "service", "owner-svc", "owner-svc/package.json")
	compID := mk(compKey, "code", "provider", "comp", "owner-svc/src/comp.ts")
	modelID := mk(modelKey, "data", "model", "Payload", "") // no file path: owner comes only from the single USES_MODEL user
	f1ID := mk(f1Key, "data", "field", "a", "")
	f2ID := mk(f2Key, "data", "field", "b", "")
	mk(collapsedSvcKey, "service", "service", "collapsed-svc", "collapsed-svc/package.json")
	ctlID := mk(ctlKey, "code", "controller", "ctl", "collapsed-svc/src/ctl.ts")
	epID := mk(epKey, "contract", "endpoint", "GET /x", "")

	edge(compID, modelID, compKey, modelKey, "USES_MODEL")
	edge(modelID, f1ID, modelKey, f1Key, "HAS_FIELD")
	edge(modelID, f2ID, modelKey, f2Key, "HAS_FIELD")
	edge(ctlID, epID, ctlKey, epKey, "EXPOSES_ENDPOINT")
	edge(compID, epID, compKey, epKey, "CALLS_ENDPOINT")
	return st, compKey, modelKey, collapsedSvcKey
}

func TestBuildViewDeps_EmptyOwnerUnknownFrameNotDrawn(t *testing.T) {
	st, compKey, modelKey, collapsedSvcKey := synthFoldStore(t)
	v, err := BuildPreset(st, "deps", "fold", compKey)
	if err != nil {
		t.Fatalf("deps: %v", err)
	}
	for _, g := range v.Groups {
		if g.Key == unownedGroupKey {
			t.Errorf("owner-unknown frame should not be drawn when every member folded into it is invisible: %+v", g)
		}
	}
	for _, e := range v.Edges {
		if e.Kind == "HAS_FIELD" {
			t.Errorf("has-field arrow into the hidden frame should not be drawn: %+v", e)
		}
		if e.To == unownedGroupKey || e.From == unownedGroupKey {
			t.Errorf("no arrow should reference the dropped frame: %+v", e)
		}
	}
	// The model itself is still a real, visible member of owner-svc's frame
	// (only the fields folded into the now-dropped bucket).
	foundModel := false
	for _, n := range v.Nodes {
		if n.Key == modelKey {
			foundModel = true
			if n.RenderMode == "hidden" {
				t.Errorf("the model itself should stay visible: %+v", n)
			}
		}
	}
	if !foundModel {
		t.Fatalf("expected the model as a visible node: nodes=%+v", v.Nodes)
	}
	// Collapsed cards are unaffected: collapsed-svc is reached only through
	// its endpoint (no individually drawn member either — mechanically the
	// same shape as the dropped frame) but it is a real, stat-bearing
	// service, not the synthetic owner-unknown bucket, so it stays.
	var collapsed *VGroup
	for i := range v.Groups {
		if v.Groups[i].Key == collapsedSvcKey {
			collapsed = &v.Groups[i]
		}
	}
	if collapsed == nil {
		t.Fatalf("collapsed-svc card should stay drawn: groups=%+v", v.Groups)
	}
	if len(collapsed.Stats) == 0 {
		t.Errorf("collapsed-svc should still show its endpoint stat: %+v", collapsed)
	}
	txt := RenderText(v)
	if strings.Contains(txt, "owner unknown") {
		t.Errorf("text should not print the empty owner-unknown frame:\n%s", txt)
	}
	if strings.Contains(txt, "has field") {
		t.Errorf("text should not print the folded has-field arrow:\n%s", txt)
	}
}
