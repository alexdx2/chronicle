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

// TestBuildViewAPI_UnresolvedServiceCall_Note reproduces the real fixture
// shape (arena-api/jerry.client): a caller component with BOTH a resolved
// CALLS_ENDPOINT and a direct, unresolved CALLS_SERVICE to the same callee
// service, plus a second caller component with only the unresolved
// CALLS_SERVICE — the note counts both (×2) and the resolved call still
// draws its own arrow.
func TestBuildViewAPI_UnresolvedServiceCall_Note(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	defer st.Close()
	rev, err := st.CreateRevision("d", "", "api-note", "manual", "full", "{}")
	if err != nil {
		t.Fatal(err)
	}

	callerSvcKey := "service:service:d:caller-api"
	calleeSvcKey := "service:service:d:callee-api"
	callerCompKey := "code:provider:d:caller-api/src/client"
	callerComp2Key := "code:provider:d:caller-api/src/worker"
	calleeCtlKey := "code:controller:d:callee-api/src/ctl"
	epKey := "contract:endpoint:d:get:/thing"

	mk := func(key, layer, ntype, name, file string) int64 {
		id, err := st.UpsertNode(store.NodeRow{NodeKey: key, Layer: layer, NodeType: ntype, DomainKey: "d", Name: name,
			FilePath: file, Status: "active", Confidence: 0.9, Freshness: 1, TrustScore: 0.9, Metadata: "{}", LastSeenRevisionID: rev})
		if err != nil {
			t.Fatalf("upsert %s: %v", key, err)
		}
		return id
	}
	callerSvcID := mk(callerSvcKey, "service", "service", "caller-api", "caller-api/package.json")
	calleeSvcID := mk(calleeSvcKey, "service", "service", "callee-api", "callee-api/package.json")
	callerCompID := mk(callerCompKey, "code", "provider", "client", "caller-api/src/client.ts")
	callerComp2ID := mk(callerComp2Key, "code", "provider", "worker", "caller-api/src/worker.ts")
	calleeCtlID := mk(calleeCtlKey, "code", "controller", "ctl", "callee-api/src/ctl.ts")
	epID := mk(epKey, "contract", "endpoint", "GET /thing", "")

	edge := func(from, to int64, fk, tk, typ string) {
		if _, err := st.UpsertEdge(store.EdgeRow{EdgeKey: fk + "->" + tk + ":" + typ, FromNodeID: from, ToNodeID: to, EdgeType: typ,
			DerivationKind: "hard", Active: true, Confidence: 0.9, Freshness: 1, TrustScore: 0.9, Metadata: "{}", LastSeenRevisionID: rev}); err != nil {
			t.Fatalf("edge %s: %v", typ, err)
		}
	}
	edge(calleeCtlID, epID, calleeCtlKey, epKey, "EXPOSES_ENDPOINT")
	edge(callerCompID, epID, callerCompKey, epKey, "CALLS_ENDPOINT")                // resolved
	edge(callerCompID, calleeSvcID, callerCompKey, calleeSvcKey, "CALLS_SERVICE")   // unresolved #1
	edge(callerComp2ID, calleeSvcID, callerComp2Key, calleeSvcKey, "CALLS_SERVICE") // unresolved #2
	_ = callerSvcID

	v, err := BuildPreset(st, "api", "d", "")
	if err != nil {
		t.Fatalf("api: %v", err)
	}
	want := "caller-api also calls callee-api ×2 without a resolved endpoint."
	if len(v.Notes) != 1 || v.Notes[0] != want {
		t.Fatalf("notes = %v, want [%q]", v.Notes, want)
	}
	foundResolved := false
	for _, e := range v.Edges {
		if e.Kind == "CALLS_ENDPOINT" && e.From == callerCompKey && e.To == epKey {
			foundResolved = true
		}
		if e.Kind == "CALLS_SERVICE" {
			t.Errorf("the api lens must not draw a raw CALLS_SERVICE arrow: %+v", e)
		}
	}
	if !foundResolved {
		t.Errorf("the resolved endpoint call should still be drawn: %+v", v.Edges)
	}
	if !strings.Contains(RenderText(v), "note: "+want) {
		t.Errorf("RenderText should carry the note:\n%s", RenderText(v))
	}
}
