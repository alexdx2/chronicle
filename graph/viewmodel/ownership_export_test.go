package viewmodel

import (
	"testing"

	"github.com/alexdx2/chronicle-core/store"
)

// setupOwnershipExportFixture builds a tiny store: one service (svc) owning
// one code node (comp) via path prefix, plus one wholly unrelated node
// (bare) that nothing attributes ownership to.
func setupOwnershipExportFixture(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(t.TempDir() + "/c.db")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	rev, _ := st.CreateRevision("d", "", "abc", "manual", "full", "{}")
	mk := func(key, layer, ntype, name, file string) {
		if _, err := st.UpsertNode(store.NodeRow{
			NodeKey: key, Layer: layer, NodeType: ntype, DomainKey: "d", Name: name,
			FilePath: file, Status: "active", Confidence: 1, Freshness: 1, TrustScore: 1,
			Metadata: "{}", LastSeenRevisionID: rev,
		}); err != nil {
			t.Fatalf("upsert %s: %v", key, err)
		}
	}
	mk("service:service:d:svc", "service", "service", "svc", "")
	mk("code:provider:d:svc/comp", "code", "provider", "Comp", "svc/comp.ts")
	mk("infra:broker:d:bus", "infra", "broker", "bus", "")
	return st
}

// TestOwningService_Owned: a code node under the service's own path prefix
// resolves to that service's node_key.
func TestOwningService_Owned(t *testing.T) {
	st := setupOwnershipExportFixture(t)
	key, ok := OwningService(st, "code:provider:d:svc/comp")
	if !ok {
		t.Fatal("OwningService ok = false, want true")
	}
	if key != "service:service:d:svc" {
		t.Errorf("OwningService key = %q, want %q", key, "service:service:d:svc")
	}
}

// TestOwningService_Unowned: a bare top-level node (no CONTAINS chain, no
// path-prefix match, not itself a service) is nobody's — ok=false, exactly
// the "domain verkhnego urovnya" case the brief's own doc comment names.
func TestOwningService_Unowned(t *testing.T) {
	st := setupOwnershipExportFixture(t)
	if key, ok := OwningService(st, "infra:broker:d:bus"); ok {
		t.Errorf("OwningService = (%q, true), want ok=false for an unowned top-level node", key)
	}
}

// TestOwningService_UnknownKey: a node_key absent from the graph entirely is
// also just "unowned" (ok=false), not a panic or an error return — matches
// OwnershipMap's own "missing entry" contract.
func TestOwningService_UnknownKey(t *testing.T) {
	st := setupOwnershipExportFixture(t)
	if _, ok := OwningService(st, "code:provider:d:does-not-exist"); ok {
		t.Error("OwningService(unknown key) ok = true, want false")
	}
}

// TestOwnershipMap_KeyedByOwnedNodeKey: OwnershipMap's map is keyed by the
// OWNED node's own key (not the owner's), values are the owner's full
// NodeRow (not just its key) — the shape CoreImpactQuerier needs to resolve
// Name/Repo/Domain without a second per-entry store round trip.
func TestOwnershipMap_KeyedByOwnedNodeKey(t *testing.T) {
	st := setupOwnershipExportFixture(t)
	m, err := OwnershipMap(st)
	if err != nil {
		t.Fatalf("OwnershipMap: %v", err)
	}
	svc, ok := m["code:provider:d:svc/comp"]
	if !ok {
		t.Fatal("OwnershipMap missing entry for code:provider:d:svc/comp")
	}
	if svc.NodeKey != "service:service:d:svc" || svc.Name != "svc" {
		t.Errorf("OwnershipMap entry = %+v, want the svc service's own NodeRow", svc)
	}
	if _, ok := m["infra:broker:d:bus"]; ok {
		t.Error("OwnershipMap has an entry for the unowned infra node, want none")
	}
}
