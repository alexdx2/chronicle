package store

import (
	"strings"
	"testing"
)

func outboxKinds(t *testing.T, s *Store) map[string]int {
	t.Helper()
	rows, err := s.conn.Query(`SELECT kind, COUNT(*) FROM journal_outbox GROUP BY kind`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var k string
		var n int
		rows.Scan(&k, &n)
		out[k] = n
	}
	return out
}

func TestNodeEdgeChokepointsEmitEvents(t *testing.T) {
	s := openJournalTestStore(t)
	revID, _ := s.CreateRevision("dom", "", "abc", "manual", "full", "{}")

	var nodeID int64
	err := s.WithTx(func(tx *Store) error {
		var err error
		nodeID, err = tx.UpsertNode(NodeRow{
			NodeKey: "code:module:dom:a", Layer: "code", NodeType: "module", DomainKey: "dom",
			Name: "a", Status: "active", LastSeenRevisionID: revID, Metadata: "{}",
		})
		if err != nil {
			return err
		}
		_, err = tx.UpsertNode(NodeRow{
			NodeKey: "code:module:dom:b", Layer: "code", NodeType: "module", DomainKey: "dom",
			Name: "b", Status: "active", LastSeenRevisionID: revID, Metadata: "{}",
		})
		if err != nil {
			return err
		}
		bID, _ := tx.GetNodeIDByKey("code:module:dom:b")
		_, err = tx.UpsertEdge(EdgeRow{
			EdgeKey:    "code:module:dom:a->code:module:dom:b:IMPORTS",
			FromNodeID: nodeID, ToNodeID: bID, EdgeType: "IMPORTS", DerivationKind: "hard",
			Active: true, LastSeenRevisionID: revID, Metadata: "{}",
			FromNodeKey: "code:module:dom:a", ToNodeKey: "code:module:dom:b",
		})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteNode("code:module:dom:b"); err != nil {
		t.Fatal(err)
	}

	kinds := outboxKinds(t, s)
	if kinds[EvNodeUpsert] != 2 {
		t.Errorf("node_upsert = %d, want 2", kinds[EvNodeUpsert])
	}
	if kinds[EvEdgeUpsert] != 1 {
		t.Errorf("edge_upsert = %d, want 1", kinds[EvEdgeUpsert])
	}
	if kinds[EvNodeStatus] != 1 {
		t.Errorf("node_status = %d, want 1 (DeleteNode tombstone)", kinds[EvNodeStatus])
	}
}

func TestEvidenceChokepointsEmitEvents(t *testing.T) {
	s := openJournalTestStore(t)
	revID, _ := s.CreateRevision("dom", "", "abc", "manual", "full", "{}")
	var nodeID int64
	s.WithTx(func(tx *Store) error {
		var err error
		nodeID, err = tx.UpsertNode(NodeRow{
			NodeKey: "code:module:dom:a", Layer: "code", NodeType: "module", DomainKey: "dom",
			Name: "a", Status: "active", LastSeenRevisionID: revID, Metadata: "{}",
		})
		return err
	})

	_, err := s.AddEvidence(EvidenceRow{
		TargetKind: "node", NodeID: nodeID, SourceKind: "file", FilePath: "a.ts",
		LineStart: 3, ExtractorID: "x", ExtractorVersion: "1.0",
		Confidence: 0.8, AssertionKind: "import", Metadata: "{}",
		ValidFromRevisionID: revID,
	})
	if err != nil {
		t.Fatal(err)
	}
	// Re-observation (same dedup key) → evidence_status, not a second evidence_add.
	_, err = s.AddEvidence(EvidenceRow{
		TargetKind: "node", NodeID: nodeID, SourceKind: "file", FilePath: "a.ts",
		LineStart: 3, ExtractorID: "x", ExtractorVersion: "1.0",
		Confidence: 0.8, AssertionKind: "import", Metadata: "{}",
		ValidFromRevisionID: revID,
	})
	if err != nil {
		t.Fatal(err)
	}

	kinds := outboxKinds(t, s)
	if kinds[EvEvidenceAdd] != 1 {
		t.Errorf("evidence_add = %d, want 1", kinds[EvEvidenceAdd])
	}
	if kinds[EvEvidenceStatus] != 1 {
		t.Errorf("evidence_status = %d, want 1 (re-observation)", kinds[EvEvidenceStatus])
	}
	if kinds[EvRevisionOpen] != 1 {
		t.Errorf("revision_open = %d, want 1", kinds[EvRevisionOpen])
	}

	var subj, owner string
	s.QueryRowScan(`SELECT subject_key, COALESCE(owner_key,'') FROM journal_outbox WHERE kind='evidence_add'`, &subj, &owner)
	if !strings.HasPrefix(subj, "evidence:") {
		t.Errorf("evidence subject_key = %q, want evidence:<hash>", subj)
	}
	if owner != "code:module:dom:a" {
		t.Errorf("owner_key = %q, want node key", owner)
	}
	// evidence_uid persisted on the row
	var uid string
	s.QueryRowScan(`SELECT COALESCE(evidence_uid,'') FROM graph_evidence`, &uid)
	if uid != subj {
		t.Errorf("evidence_uid = %q, want %q (same as event subject)", uid, subj)
	}
}

func TestAliasGlossarySnapshotEmitEvents(t *testing.T) {
	s := openJournalTestStore(t)
	revID, _ := s.CreateRevision("dom", "", "abc", "manual", "full", "{}")
	var nodeID int64
	s.WithTx(func(tx *Store) error {
		var err error
		nodeID, err = tx.UpsertNode(NodeRow{
			NodeKey: "code:module:dom:a", Layer: "code", NodeType: "module", DomainKey: "dom",
			Name: "a", Status: "active", LastSeenRevisionID: revID, Metadata: "{}",
		})
		return err
	})
	aliasID, err := s.AddAlias(AliasRow{NodeID: nodeID, Alias: "alpha", AliasKind: "name", Confidence: 0.8})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.RemoveAlias(aliasID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.UpsertTerm(DomainTerm{DomainKey: "dom", Term: "battle"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateSnapshot(SnapshotRow{RevisionID: revID, DomainKey: "dom", Kind: "full"}); err != nil {
		t.Fatal(err)
	}

	kinds := outboxKinds(t, s)
	for kind, want := range map[string]int{
		EvAliasAdd: 1, EvAliasDel: 1, EvGlossarySet: 1, EvSnapshot: 1,
	} {
		if kinds[kind] != want {
			t.Errorf("%s = %d, want %d", kind, kinds[kind], want)
		}
	}
}

func TestMarkStaleEmitsTransitionOnlyEvents(t *testing.T) {
	s := openJournalTestStore(t)
	rev1, _ := s.CreateRevision("dom", "", "r1", "manual", "full", "{}")
	s.WithTx(func(tx *Store) error {
		_, err := tx.UpsertNode(NodeRow{
			NodeKey: "code:module:dom:a", Layer: "code", NodeType: "module", DomainKey: "dom",
			Name: "a", Status: "active", LastSeenRevisionID: rev1, Metadata: "{}",
		})
		return err
	})
	rev2, _ := s.CreateRevision("dom", "r1", "r2", "manual", "incremental", "{}")

	if _, err := s.MarkStaleNodes("dom", rev2); err != nil {
		t.Fatal(err)
	}
	// Second call: node already stale → no new event.
	if _, err := s.MarkStaleNodes("dom", rev2); err != nil {
		t.Fatal(err)
	}

	kinds := outboxKinds(t, s)
	if kinds[EvNodeStatus] != 1 {
		t.Errorf("node_status = %d, want exactly 1 (transition-only)", kinds[EvNodeStatus])
	}
}

// TestMarkStaleSkipsReSeenVersionedNodes: in versioned mode, re-upserting a
// node closes the rev1 row (valid_to set, status stays 'active') and inserts a
// current row with last_seen=rev2. MarkStaleNodes must not match the closed
// historical row — the node IS present in this revision — and must not touch
// the current row or emit any node_status:stale event.
func TestMarkStaleSkipsReSeenVersionedNodes(t *testing.T) {
	s := openJournalTestStore(t)
	rev1, _ := s.CreateRevision("dom", "", "r1", "manual", "full", "{}")
	if err := s.WithTx(func(tx *Store) error {
		_, err := tx.UpsertNode(NodeRow{
			NodeKey: "code:module:dom:a", Layer: "code", NodeType: "module", DomainKey: "dom",
			Name: "a", Status: "active", LastSeenRevisionID: rev1,
			ValidFromRevisionID: rev1, Metadata: "{}",
		})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	rev2, _ := s.CreateRevision("dom", "r1", "r2", "manual", "incremental", "{}")
	// Re-seen at rev2 — versioned close+insert.
	if err := s.WithTx(func(tx *Store) error {
		_, err := tx.UpsertNode(NodeRow{
			NodeKey: "code:module:dom:a", Layer: "code", NodeType: "module", DomainKey: "dom",
			Name: "a", Status: "active", LastSeenRevisionID: rev2,
			ValidFromRevisionID: rev2, Metadata: "{}",
		})
		return err
	}); err != nil {
		t.Fatal(err)
	}

	n, err := s.MarkStaleNodes("dom", rev2)
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("MarkStaleNodes marked %d nodes, want 0 (node was re-seen at rev2)", n)
	}
	var status string
	if err := s.QueryRowScan(`SELECT status FROM graph_nodes
		WHERE node_key='code:module:dom:a'
		  AND (valid_to_revision_id IS NULL OR valid_to_revision_id = 0)`, &status); err != nil {
		t.Fatal(err)
	}
	if status != "active" {
		t.Errorf("current version status = %q, want active", status)
	}
	kinds := outboxKinds(t, s)
	if kinds[EvNodeStatus] != 0 {
		t.Errorf("node_status events = %d, want 0 (no stale transition happened)", kinds[EvNodeStatus])
	}
}

func TestNodeEventFieldsExcludeDerived(t *testing.T) {
	s := openJournalTestStore(t)
	revID, _ := s.CreateRevision("dom", "", "abc", "manual", "full", "{}")
	s.WithTx(func(tx *Store) error {
		_, err := tx.UpsertNode(NodeRow{
			NodeKey: "code:module:dom:a", Layer: "code", NodeType: "module", DomainKey: "dom",
			Name: "a", Status: "active", LastSeenRevisionID: revID,
			Confidence: 0.9, Freshness: 1.0, TrustScore: 0.9, Metadata: "{}",
		})
		return err
	})
	var fields string
	s.QueryRowScan(`SELECT fields FROM journal_outbox WHERE kind='node_upsert'`, &fields)
	for _, banned := range []string{"confidence", "freshness", "trust_score"} {
		if strings.Contains(fields, banned) {
			t.Errorf("event fields contain derived value %q: %s", banned, fields)
		}
	}
}
