package graph

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/alexdx2/chronicle-core/registry"
	"github.com/alexdx2/chronicle-core/store"
)

// seedJournalSource builds a graph whose evidence justifies LESS than full
// trust, and flushes it to the journal. 0.8 is the point: replay writes a 1.0
// placeholder, so a missing recompute is visible rather than a rounding
// argument.
func seedJournalSource(t *testing.T) (eventsDir string) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), ".depbot")
	s, err := store.Open(filepath.Join(dir, "chronicle.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	revID, err := s.CreateRevision("dom", "", "abc123", "manual", "full", "{}")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.WithTx(func(tx *store.Store) error {
		id, err := tx.UpsertNode(store.NodeRow{
			NodeKey: "code:module:dom:a", Layer: "code", NodeType: "module", DomainKey: "dom",
			Name: "a", Status: "active", LastSeenRevisionID: revID, Metadata: "{}",
		})
		if err != nil {
			return err
		}
		_, err = tx.AddEvidence(store.EvidenceRow{
			TargetKind: "node", NodeID: id, SourceKind: "file", FilePath: "a.ts",
			LineStart: 1, ExtractorID: "x", ExtractorVersion: "1.0",
			Confidence: 0.8, AssertionKind: "import", Metadata: "{}",
			ValidFromRevisionID: revID,
		})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.FlushJournal(); err != nil {
		t.Fatal(err)
	}
	return filepath.Join(dir, "events")
}

// cloneFromJournal is the teammate's fresh checkout: the events are tracked in
// git, chronicle.db is not, so the graph arrives entirely through replay.
func cloneFromJournal(t *testing.T, eventsDir string) *store.Store {
	t.Helper()
	dir := filepath.Join(t.TempDir(), ".depbot")
	dst := filepath.Join(dir, "events")
	if err := os.MkdirAll(dst, 0o755); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(eventsDir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		b, err := os.ReadFile(filepath.Join(eventsDir, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dst, e.Name()), b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	s, err := store.Open(filepath.Join(dir, "chronicle.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func testRegistry(t *testing.T) *registry.Registry {
	t.Helper()
	reg, err := registry.LoadDefaults()
	if err != nil {
		t.Fatal(err)
	}
	return reg
}

func nodeTrust(t *testing.T, s *store.Store, key string) float64 {
	t.Helper()
	n, err := s.GetNodeByKey(key)
	if err != nil {
		t.Fatalf("GetNodeByKey %s: %v", key, err)
	}
	return n.TrustScore
}

// The opener that applies merged journal events owes the recompute, and the
// sync is consumed exactly once — so skipping it does not defer the work, it
// destroys it. Every later opener sees JournalSyncApplied() == 0 and correctly
// does nothing, leaving replay's placeholder trust in place for good.
func TestRecalculateTrustAfterJournalSyncSettlesReplayPlaceholders(t *testing.T) {
	s := cloneFromJournal(t, seedJournalSource(t))
	if s.JournalSyncApplied() <= 0 {
		t.Fatal("fixture: the clone did not materialise from the journal")
	}
	placeholder := nodeTrust(t, s, "code:module:dom:a")
	if placeholder != 1.0 {
		t.Fatalf("fixture: replay trust = %v, want the 1.0 placeholder", placeholder)
	}

	g := New(s, testRegistry(t))
	if err := g.RecalculateTrustAfterJournalSync(); err != nil {
		t.Fatalf("RecalculateTrustAfterJournalSync: %v", err)
	}

	got := nodeTrust(t, s, "code:module:dom:a")
	if got >= 1.0 {
		t.Errorf("trust = %v after the recompute; evidence of 0.8 cannot justify 1.0", got)
	}
}

// A store whose open applied nothing has no debt: the recompute would be pure
// latency on a hot path that runs before every tool call.
func TestRecalculateTrustAfterJournalSyncIsANoopWithoutASync(t *testing.T) {
	// No events directory, so nothing is replayed on open.
	dir := filepath.Join(t.TempDir(), ".depbot")
	s, err := store.Open(filepath.Join(dir, "chronicle.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	if s.JournalSyncApplied() != 0 {
		t.Fatalf("fixture: a store with no journal applied %d events", s.JournalSyncApplied())
	}

	revID, err := s.CreateRevision("dom", "", "abc123", "manual", "full", "{}")
	if err != nil {
		t.Fatal(err)
	}
	id, err := s.UpsertNode(store.NodeRow{
		NodeKey: "code:module:dom:a", Layer: "code", NodeType: "module", DomainKey: "dom",
		Name: "a", Status: "active", LastSeenRevisionID: revID, Metadata: "{}",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddEvidence(store.EvidenceRow{
		TargetKind: "node", NodeID: id, SourceKind: "file", FilePath: "a.ts",
		LineStart: 1, ExtractorID: "x", ExtractorVersion: "1.0",
		Confidence: 0.8, AssertionKind: "import", Metadata: "{}",
		ValidFromRevisionID: revID,
	}); err != nil {
		t.Fatal(err)
	}

	// A value the evidence does not justify. Nothing was synced, so this is
	// none of the method's business.
	if err := s.UpdateNodeTrust(id, 1, 1, 1, "active"); err != nil {
		t.Fatal(err)
	}
	if err := New(s, testRegistry(t)).RecalculateTrustAfterJournalSync(); err != nil {
		t.Fatal(err)
	}
	if got := nodeTrust(t, s, "code:module:dom:a"); got != 1.0 {
		t.Errorf("trust = %v; with no sync applied this must not recompute", got)
	}
}
