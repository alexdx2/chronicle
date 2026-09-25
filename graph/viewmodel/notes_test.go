package viewmodel

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/alexdx2/chronicle-core/store"
)

// rev3 task 2 ("Честные пустые ответы" — honest empty answers): an empty or
// partial answer says so in View.Notes, decided from the view's own content
// (or, for the API lens, the raw domain edges a preset structurally
// excludes from its own arrows) — never a guess.

// TestBuildViewImpact_NoDependents_Note: "redis" is wired into nothing in
// synthStore — impact must say nothing depends on it.
func TestBuildViewImpact_NoDependents_Note(t *testing.T) {
	st := synthStore(t)
	v, err := BuildPreset(st, "impact", "d", "infra:cache:d:redis")
	if err != nil {
		t.Fatalf("impact: %v", err)
	}
	want := "Nothing in the graph depends on redis."
	if len(v.Notes) != 1 || v.Notes[0] != want {
		t.Fatalf("notes = %v, want [%q]", v.Notes, want)
	}
	if !strings.Contains(RenderText(v), "note: "+want) {
		t.Errorf("RenderText should carry the note:\n%s", RenderText(v))
	}
}

// TestBuildViewImpact_WithDependents_NoNote: mail-api DOES have a dependent
// (shop.service calls it) — no note.
func TestBuildViewImpact_WithDependents_NoNote(t *testing.T) {
	st := synthStore(t)
	v, err := BuildPreset(st, "impact", "d", "service:service:d:mail-api")
	if err != nil {
		t.Fatalf("impact: %v", err)
	}
	if len(v.Nodes) == 0 {
		t.Fatalf("expected dependents in the view, got none: %+v", v)
	}
	if len(v.Notes) != 0 {
		t.Errorf("mail-api has a dependent, expected no note, got %v", v.Notes)
	}
}

// TestBuildViewDeps_NoDependencies_Note: "redis" depends on nothing either.
func TestBuildViewDeps_NoDependencies_Note(t *testing.T) {
	st := synthStore(t)
	v, err := BuildPreset(st, "deps", "d", "infra:cache:d:redis")
	if err != nil {
		t.Fatalf("deps: %v", err)
	}
	want := "redis depends on nothing in the graph."
	if len(v.Notes) != 1 || v.Notes[0] != want {
		t.Fatalf("notes = %v, want [%q]", v.Notes, want)
	}
}

// TestBuildViewDeps_WithDependencies_NoNote: shop.service depends on the
// topic and mail-api — no note.
func TestBuildViewDeps_WithDependencies_NoNote(t *testing.T) {
	st := synthStore(t)
	v, err := BuildPreset(st, "deps", "d", "code:provider:d:shop-api/src/shop.service")
	if err != nil {
		t.Fatalf("deps: %v", err)
	}
	if len(v.Notes) != 0 {
		t.Errorf("shop.service has dependencies, expected no note, got %v", v.Notes)
	}
}

// TestBuildViewC3_EmptyService_Note: a service that owns no code, no
// endpoints, no models — c3 renders an empty view AND says why.
func TestBuildViewC3_EmptyService_Note(t *testing.T) {
	st := synthStore(t)
	rev, err := st.CreateRevision("d", "", "empty-svc", "manual", "full", "{}")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.UpsertNode(store.NodeRow{NodeKey: "service:service:d:shared-lib", Layer: "service", NodeType: "service",
		DomainKey: "d", Name: "shared-lib", Status: "active", Confidence: 1, Freshness: 1, TrustScore: 1, Metadata: "{}", LastSeenRevisionID: rev}); err != nil {
		t.Fatal(err)
	}

	v, err := BuildPreset(st, "c3", "d", "shared-lib")
	if err != nil {
		t.Fatalf("c3: %v", err)
	}
	if len(v.Nodes) != 0 {
		t.Fatalf("expected an empty view, got %d nodes: %+v", len(v.Nodes), v.Nodes)
	}
	want := "shared-lib owns no components in the graph."
	if len(v.Notes) != 1 || v.Notes[0] != want {
		t.Fatalf("notes = %v, want [%q]", v.Notes, want)
	}
	if !strings.Contains(RenderText(v), "note: "+want) {
		t.Errorf("RenderText should carry the note:\n%s", RenderText(v))
	}
}

// TestBuildViewC3_ServiceWithComponents_NoNote: shop-api owns components —
// no note.
func TestBuildViewC3_ServiceWithComponents_NoNote(t *testing.T) {
	st := synthStore(t)
	v, err := BuildPreset(st, "c3", "d", "shop-api")
	if err != nil {
		t.Fatalf("c3: %v", err)
	}
	if len(v.Nodes) == 0 {
		t.Fatalf("expected shop-api's components in the view, got none")
	}
	if len(v.Notes) != 0 {
		t.Errorf("shop-api owns components, expected no note, got %v", v.Notes)
	}
}

// TestBuildViewAPI_NoEndpointsAnywhere_NoNote: synthStore's lone
// CALLS_SERVICE edge (shop.service -> mail-api) is real, but neither
// service owns anything the api lens draws (no endpoints at all) — the
// pair is not "of the view", so no note invents one.
func TestBuildViewAPI_NoEndpointsAnywhere_NoNote(t *testing.T) {
	st := synthStore(t)
	v, err := BuildPreset(st, "api", "d", "")
	if err != nil {
		t.Fatalf("api: %v", err)
	}
	if len(v.Notes) != 0 {
		t.Errorf("neither service is drawn in the api lens, expected no note, got %v", v.Notes)
	}
}

// newAPINoteStore builds the shared shape for the API-lens covered/uncovered
// call tests below: two services, one endpoint on callee-api exposed by its
// controller, and a caller component ("client") that resolves it — the
// exact real-fixture pattern (arena-api/jerry.client → jerry-api). Returns
// the store plus the ids/keys each test needs to add its own extra edges.
func newAPINoteStore(t *testing.T) (st *store.Store, rev int64, calleeSvcID, epID, callerCompID int64, callerCompKey, calleeSvcKey, epKey string) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	rev, err = st.CreateRevision("d", "", "api-note", "manual", "full", "{}")
	if err != nil {
		t.Fatal(err)
	}

	callerSvcKey := "service:service:d:caller-api"
	calleeSvcKey = "service:service:d:callee-api"
	callerCompKey = "code:provider:d:caller-api/src/client"
	calleeCtlKey := "code:controller:d:callee-api/src/ctl"
	epKey = "contract:endpoint:d:get:/thing"

	mk := func(key, layer, ntype, name, file string) int64 {
		id, err := st.UpsertNode(store.NodeRow{NodeKey: key, Layer: layer, NodeType: ntype, DomainKey: "d", Name: name,
			FilePath: file, Status: "active", Confidence: 0.9, Freshness: 1, TrustScore: 0.9, Metadata: "{}", LastSeenRevisionID: rev})
		if err != nil {
			t.Fatalf("upsert %s: %v", key, err)
		}
		return id
	}
	mk(callerSvcKey, "service", "service", "caller-api", "caller-api/package.json")
	calleeSvcID = mk(calleeSvcKey, "service", "service", "callee-api", "callee-api/package.json")
	calleeCtlID := mk(calleeCtlKey, "code", "controller", "ctl", "callee-api/src/ctl.ts")
	epID = mk(epKey, "contract", "endpoint", "GET /thing", "")
	callerCompID = mk(callerCompKey, "code", "provider", "client", "caller-api/src/client.ts")

	edge := func(from, to int64, fk, tk, typ string) {
		if _, err := st.UpsertEdge(store.EdgeRow{EdgeKey: fk + "->" + tk + ":" + typ, FromNodeID: from, ToNodeID: to, EdgeType: typ,
			DerivationKind: "hard", Active: true, Confidence: 0.9, Freshness: 1, TrustScore: 0.9, Metadata: "{}", LastSeenRevisionID: rev}); err != nil {
			t.Fatalf("edge %s: %v", typ, err)
		}
	}
	edge(calleeCtlID, epID, calleeCtlKey, epKey, "EXPOSES_ENDPOINT")
	edge(callerCompID, epID, callerCompKey, epKey, "CALLS_ENDPOINT") // resolved
	return st, rev, calleeSvcID, epID, callerCompID, callerCompKey, calleeSvcKey, epKey
}

// TestBuildViewAPI_CoveredServiceCall_NoNote (fix round 1): the caller
// component's CALLS_SERVICE to callee-api twins its own resolved
// CALLS_ENDPOINT to callee-api's endpoint — the real fixture shape
// (arena-api/jerry.client → jerry-api). Covered: no note.
func TestBuildViewAPI_CoveredServiceCall_NoNote(t *testing.T) {
	st, rev, calleeSvcID, _, callerCompID, callerCompKey, calleeSvcKey, epKey := newAPINoteStore(t)
	if _, err := st.UpsertEdge(store.EdgeRow{EdgeKey: callerCompKey + "->" + calleeSvcKey + ":CALLS_SERVICE", FromNodeID: callerCompID, ToNodeID: calleeSvcID,
		EdgeType: "CALLS_SERVICE", DerivationKind: "hard", Active: true, Confidence: 0.9, Freshness: 1, TrustScore: 0.9, Metadata: "{}", LastSeenRevisionID: rev}); err != nil {
		t.Fatal(err)
	}

	v, err := BuildPreset(st, "api", "d", "")
	if err != nil {
		t.Fatalf("api: %v", err)
	}
	if len(v.Notes) != 0 {
		t.Fatalf("the CALLS_SERVICE edge twins a resolved call, expected no note, got %v", v.Notes)
	}
	foundResolved, foundRaw := false, false
	for _, e := range v.Edges {
		if e.Kind == "CALLS_ENDPOINT" && e.From == callerCompKey && e.To == epKey {
			foundResolved = true
		}
		if e.Kind == "CALLS_SERVICE" {
			foundRaw = true
		}
	}
	if !foundResolved {
		t.Errorf("the resolved endpoint call should still be drawn: %+v", v.Edges)
	}
	if foundRaw {
		t.Errorf("the api lens must not draw a raw CALLS_SERVICE arrow: %+v", v.Edges)
	}
}

// TestBuildViewAPI_UncoveredServiceCall_Note (fix round 1): a SECOND caller
// component ("worker") calls callee-api with only a CALLS_SERVICE edge — no
// CALLS_ENDPOINT of its own, so nothing twins it. The covered client edge
// added alongside it must stay silent; only the uncovered worker edge is
// noted (×1, not ×2) — proving the note discriminates per caller, not just
// per service pair.
func TestBuildViewAPI_UncoveredServiceCall_Note(t *testing.T) {
	st, rev, calleeSvcID, _, callerCompID, callerCompKey, calleeSvcKey, _ := newAPINoteStore(t)
	if _, err := st.UpsertEdge(store.EdgeRow{EdgeKey: callerCompKey + "->" + calleeSvcKey + ":CALLS_SERVICE", FromNodeID: callerCompID, ToNodeID: calleeSvcID,
		EdgeType: "CALLS_SERVICE", DerivationKind: "hard", Active: true, Confidence: 0.9, Freshness: 1, TrustScore: 0.9, Metadata: "{}", LastSeenRevisionID: rev}); err != nil {
		t.Fatal(err) // covered — must not add to the count below
	}
	workerKey := "code:provider:d:caller-api/src/worker"
	workerID, err := st.UpsertNode(store.NodeRow{NodeKey: workerKey, Layer: "code", NodeType: "provider", DomainKey: "d", Name: "worker",
		FilePath: "caller-api/src/worker.ts", Status: "active", Confidence: 0.9, Freshness: 1, TrustScore: 0.9, Metadata: "{}", LastSeenRevisionID: rev})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.UpsertEdge(store.EdgeRow{EdgeKey: workerKey + "->" + calleeSvcKey + ":CALLS_SERVICE", FromNodeID: workerID, ToNodeID: calleeSvcID,
		EdgeType: "CALLS_SERVICE", DerivationKind: "hard", Active: true, Confidence: 0.9, Freshness: 1, TrustScore: 0.9, Metadata: "{}", LastSeenRevisionID: rev}); err != nil {
		t.Fatal(err) // uncovered — worker has no CALLS_ENDPOINT at all
	}

	v, err := BuildPreset(st, "api", "d", "")
	if err != nil {
		t.Fatalf("api: %v", err)
	}
	want := "caller-api also calls callee-api ×1 without a resolved endpoint."
	if len(v.Notes) != 1 || v.Notes[0] != want {
		t.Fatalf("notes = %v, want [%q]", v.Notes, want)
	}
	if !strings.Contains(RenderText(v), "note: "+want) {
		t.Errorf("RenderText should carry the note:\n%s", RenderText(v))
	}
}

// newTwinCallStore builds one caller component that calls two of the
// callee's endpoints (resolved) plus the service directly (the coarse
// twin) — the exact real-fixture shape (arena-api/jerry.client → jerry-api:
// 2 CALLS_ENDPOINT + 1 CALLS_SERVICE, C2 counted all three as "HTTP ×3").
// Shared by the C2 and C3-boundary weight tests below (fix round 1).
func newTwinCallStore(t *testing.T) (st *store.Store, callerSvcKey, calleeSvcKey, callerCompKey string) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	rev, err := st.CreateRevision("d", "", "twin-call", "manual", "full", "{}")
	if err != nil {
		t.Fatal(err)
	}

	callerSvcKey = "service:service:d:caller-api"
	calleeSvcKey = "service:service:d:callee-api"
	callerCompKey = "code:provider:d:caller-api/src/client"
	calleeCtlKey := "code:controller:d:callee-api/src/ctl"
	ep1Key := "contract:endpoint:d:get:/a"
	ep2Key := "contract:endpoint:d:get:/b"

	mk := func(key, layer, ntype, name, file string) int64 {
		id, err := st.UpsertNode(store.NodeRow{NodeKey: key, Layer: layer, NodeType: ntype, DomainKey: "d", Name: name,
			FilePath: file, Status: "active", Confidence: 0.9, Freshness: 1, TrustScore: 0.9, Metadata: "{}", LastSeenRevisionID: rev})
		if err != nil {
			t.Fatalf("upsert %s: %v", key, err)
		}
		return id
	}
	mk(callerSvcKey, "service", "service", "caller-api", "caller-api/package.json")
	calleeSvcID := mk(calleeSvcKey, "service", "service", "callee-api", "callee-api/package.json")
	calleeCtlID := mk(calleeCtlKey, "code", "controller", "ctl", "callee-api/src/ctl.ts")
	ep1ID := mk(ep1Key, "contract", "endpoint", "GET /a", "")
	ep2ID := mk(ep2Key, "contract", "endpoint", "GET /b", "")
	callerCompID := mk(callerCompKey, "code", "provider", "client", "caller-api/src/client.ts")

	edge := func(from, to int64, fk, tk, typ string) {
		if _, err := st.UpsertEdge(store.EdgeRow{EdgeKey: fk + "->" + tk + ":" + typ, FromNodeID: from, ToNodeID: to, EdgeType: typ,
			DerivationKind: "hard", Active: true, Confidence: 0.9, Freshness: 1, TrustScore: 0.9, Metadata: "{}", LastSeenRevisionID: rev}); err != nil {
			t.Fatalf("edge %s: %v", typ, err)
		}
	}
	edge(calleeCtlID, ep1ID, calleeCtlKey, ep1Key, "EXPOSES_ENDPOINT")
	edge(calleeCtlID, ep2ID, calleeCtlKey, ep2Key, "EXPOSES_ENDPOINT")
	edge(callerCompID, ep1ID, callerCompKey, ep1Key, "CALLS_ENDPOINT")
	edge(callerCompID, ep2ID, callerCompKey, ep2Key, "CALLS_ENDPOINT")
	edge(callerCompID, calleeSvcID, callerCompKey, calleeSvcKey, "CALLS_SERVICE") // covered twin
	return st, callerSvcKey, calleeSvcKey, callerCompKey
}

// TestBuildViewC2_CoveredServiceCallDoesNotInflateWeight (fix round 1): two
// resolved CALLS_ENDPOINT calls plus the caller's coarse CALLS_SERVICE twin
// collapse into ONE C2 arrow — its weight counts the two real calls, not
// three (this is the exact bug reported against the fixture: C2 printed
// "arena-api → jerry-api: HTTP ×3" for two endpoint calls).
func TestBuildViewC2_CoveredServiceCallDoesNotInflateWeight(t *testing.T) {
	st, callerSvcKey, calleeSvcKey, _ := newTwinCallStore(t)

	v, err := BuildPreset(st, "c2", "d", "")
	if err != nil {
		t.Fatalf("c2: %v", err)
	}
	var got *VEdge
	for i := range v.Edges {
		if v.Edges[i].Kind == "CALLS_SERVICE" && v.Edges[i].From == callerSvcKey && v.Edges[i].To == calleeSvcKey {
			got = &v.Edges[i]
		}
	}
	if got == nil {
		t.Fatalf("expected a caller-api -> callee-api arrow: %+v", v.Edges)
	}
	if got.Weight != 2 {
		t.Errorf("weight = %d, want 2 (two resolved endpoint calls; the covered CALLS_SERVICE must not add a third)", got.Weight)
	}
	if len(got.CollapsedFrom) != 3 {
		t.Errorf("collapsed_from = %v, want all three underlying edges (still evidence, just not weight)", got.CollapsedFrom)
	}
	if strings.Contains(RenderText(v), "HTTP ×3") {
		t.Errorf("text should not inflate the call count:\n%s", RenderText(v))
	}
}

// TestBuildViewC3_CoveredServiceCallDoesNotInflateBoundaryWeight (fix round
// 1): the same twin shape, seen from caller-api's own C3 view — callee-api
// is outside that scope, so the arrow is a materialized BOUNDARY edge
// (addBoundaryEdge), a different code path from C2's main edge-folding
// loop. It must not inflate weight either.
func TestBuildViewC3_CoveredServiceCallDoesNotInflateBoundaryWeight(t *testing.T) {
	st, _, calleeSvcKey, callerCompKey := newTwinCallStore(t)

	v, err := BuildPreset(st, "c3", "d", "caller-api")
	if err != nil {
		t.Fatalf("c3: %v", err)
	}
	var got *VEdge
	for i := range v.Edges {
		if v.Edges[i].Kind == "CALLS_SERVICE" && v.Edges[i].From == callerCompKey && v.Edges[i].To == calleeSvcKey {
			got = &v.Edges[i]
		}
	}
	if got == nil {
		t.Fatalf("expected a boundary client -> callee-api arrow: %+v", v.Edges)
	}
	if got.Weight != 2 {
		t.Errorf("boundary weight = %d, want 2 (two resolved endpoint calls; the covered CALLS_SERVICE must not add a third)", got.Weight)
	}
	if len(got.CollapsedFrom) != 3 {
		t.Errorf("collapsed_from = %v, want all three underlying edges", got.CollapsedFrom)
	}
}
