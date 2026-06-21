package graph

import (
	"testing"

	"github.com/alexdx2/chronicle-core/registry"
	"github.com/alexdx2/chronicle-core/store"
)

func newSeededGraph(t *testing.T) (*Graph, *store.Store) {
	t.Helper()
	st, err := store.Open(t.TempDir() + "/q.db")
	if err != nil {
		t.Fatal(err)
	}
	reg, _ := registry.LoadDefaults()
	g := New(st, reg)
	mk := func(key, layer, ntype, name string) {
		if _, err := st.UpsertNode(store.NodeRow{
			NodeKey: key, Layer: layer, NodeType: ntype, DomainKey: "d", Name: name,
			Status: "active", Confidence: 1, Freshness: 1, TrustScore: 1, Metadata: "{}",
		}); err != nil {
			t.Fatal(err)
		}
	}
	mk("service:service:d:svc", "service", "service", "svc")
	mk("code:provider:d:a", "code", "provider", "A")
	mk("code:provider:d:b", "code", "provider", "B")
	mk("code:provider:d:c", "code", "provider", "C")
	mk("code:provider:d:repl", "code", "provider", "Repl")
	return g, st
}

func TestBuildGraphStats(t *testing.T) {
	g, _ := newSeededGraph(t)
	stats := g.BuildGraphStats("d")
	if stats.NodesTotal != 5 {
		t.Fatalf("NodesTotal=%d want 5", stats.NodesTotal)
	}
	if stats.NodesByLayer["code"] != 4 || stats.NodesByLayer["service"] != 1 {
		t.Fatalf("NodesByLayer=%v", stats.NodesByLayer)
	}
	if stats.NodesByType["provider"] != 4 {
		t.Fatalf("NodesByType=%v", stats.NodesByType)
	}
}

func TestResolveReview_AllResolutions(t *testing.T) {
	g, st := newSeededGraph(t)
	rev, err := st.CreateRevision("d", "", "after", "manual", "full", "{}")
	if err != nil {
		t.Fatal(err)
	}

	// Invalid resolution → error.
	if _, err := g.ResolveReview("node", "code:provider:d:a", "bogus", "r", "", "", rev); err == nil {
		t.Error("invalid resolution should error")
	}
	// confirmed_valid — positive evidence required. Omit source_kind to exercise
	// the default (must be a valid registry source_kind: regression guard for the
	// "claude" bug where the default was an invalid source_kind).
	posEv := `{"assertion_kind":"call_expression","assertion":"{}","confidence":0.9}`
	if _, err := g.ResolveReview("node", "code:provider:d:a", "confirmed_valid", "looks right", posEv, "", rev); err != nil {
		t.Errorf("confirmed_valid: %v", err)
	}
	// deferred — no evidence needed.
	if _, err := g.ResolveReview("node", "code:provider:d:b", "deferred", "later", "", "", rev); err != nil {
		t.Errorf("deferred: %v", err)
	}
	// replaced_by — needs replacement key + evidence.
	replEv := `{"source_kind":"manual_resolution"}`
	if _, err := g.ResolveReview("node", "code:provider:d:c", "replaced_by", "merged", replEv, "code:provider:d:repl", rev); err != nil {
		t.Errorf("replaced_by: %v", err)
	}
	// replaced_by missing replacement key → error.
	if _, err := g.ResolveReview("node", "code:provider:d:c", "replaced_by", "x", replEv, "", rev); err == nil {
		t.Error("replaced_by without replacement key should error")
	}
	// confirmed_removed — negative evidence with checked_scope required.
	negEv := `{"source_kind":"manual_resolution","polarity":"negative","assertion":"{\"checked_scope\":\"searched file\"}"}`
	if _, err := g.ResolveReview("node", "code:provider:d:repl", "confirmed_removed", "gone", negEv, "", rev); err != nil {
		t.Errorf("confirmed_removed: %v", err)
	}
	// confirmed_removed with wrong polarity → error.
	if _, err := g.ResolveReview("node", "code:provider:d:a", "confirmed_removed", "x", `{"polarity":"positive"}`, "", rev); err == nil {
		t.Error("confirmed_removed with positive polarity should error")
	}
}

func TestScanReviewCandidates(t *testing.T) {
	g, st := newSeededGraph(t)
	rev, err := st.CreateRevision("d", "", "after", "manual", "full", "{}")
	if err != nil {
		t.Fatal(err)
	}
	// No low-trust nodes/edges seeded → candidates may be empty, but the call
	// must succeed and exercise the query path.
	if _, err := g.ScanReviewCandidates("d", rev); err != nil {
		t.Fatalf("ScanReviewCandidates: %v", err)
	}
}
