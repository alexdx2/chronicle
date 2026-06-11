package store

import (
	"encoding/json"
	"errors"
	"path/filepath"
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

// seedNodesAndEdge creates nodes a, b and an a->b IMPORTS edge; returns ids.
func seedNodesAndEdge(t *testing.T, s *Store, revID int64) (aID, bID, edgeID int64) {
	t.Helper()
	err := s.WithTx(func(tx *Store) error {
		var err error
		aID, err = tx.UpsertNode(NodeRow{
			NodeKey: "code:module:dom:a", Layer: "code", NodeType: "module", DomainKey: "dom",
			Name: "a", Status: "active", LastSeenRevisionID: revID, Metadata: "{}",
		})
		if err != nil {
			return err
		}
		bID, err = tx.UpsertNode(NodeRow{
			NodeKey: "code:module:dom:b", Layer: "code", NodeType: "module", DomainKey: "dom",
			Name: "b", Status: "active", LastSeenRevisionID: revID, Metadata: "{}",
		})
		if err != nil {
			return err
		}
		edgeID, err = tx.UpsertEdge(EdgeRow{
			EdgeKey:    "code:module:dom:a->code:module:dom:b:IMPORTS",
			FromNodeID: aID, ToNodeID: bID, EdgeType: "IMPORTS", DerivationKind: "hard",
			Active: true, LastSeenRevisionID: revID, Metadata: "{}",
			FromNodeKey: "code:module:dom:a", ToNodeKey: "code:module:dom:b",
		})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return aID, bID, edgeID
}

// replayInto flushes src's journal and replays it into a fresh store.
func replayInto(t *testing.T, src *Store) *Store {
	t.Helper()
	if _, err := src.FlushJournal(); err != nil {
		t.Fatal(err)
	}
	dst := openJournalTestStore(t)
	if _, err := dst.ReplayJournal(filepath.Join(src.Dir(), "events")); err != nil {
		t.Fatal(err)
	}
	return dst
}

func TestRekeyNodeEmitsAndReplays(t *testing.T) {
	s := openJournalTestStore(t)
	revID, _ := s.CreateRevision("dom", "", "abc", "manual", "full", "{}")
	aID, _, _ := seedNodesAndEdge(t, s, revID)

	if err := s.RekeyNode(aID, "code:module:dom:a", "code:module:dom:src/a", "src/a.ts", revID); err != nil {
		t.Fatal(err)
	}

	kinds := outboxKinds(t, s)
	if kinds[EvNodeRekey] != 1 {
		t.Errorf("node_rekey = %d, want 1", kinds[EvNodeRekey])
	}
	var key, fields string
	s.QueryRowScan(`SELECT subject_key, fields FROM journal_outbox WHERE kind='node_rekey'`, &key, &fields)
	if key != "code:module:dom:a" {
		t.Errorf("node_rekey key = %q, want old node key", key)
	}
	for _, want := range []string{`"new_key":"code:module:dom:src/a"`, `"file_path":"src/a.ts"`} {
		if !strings.Contains(fields, want) {
			t.Errorf("node_rekey fields = %s, missing %s", fields, want)
		}
	}

	dst := replayInto(t, s)
	for _, db := range map[string]*Store{"live": s, "replay": dst} {
		var nodeKey, filePath string
		if err := db.QueryRowScan(`SELECT node_key, COALESCE(file_path,'') FROM graph_nodes WHERE name='a'`, &nodeKey, &filePath); err != nil {
			t.Fatal(err)
		}
		if nodeKey != "code:module:dom:src/a" || filePath != "src/a.ts" {
			t.Errorf("rekeyed node = %q file=%q, want new key + file path", nodeKey, filePath)
		}
		var edgeKey, fromKey string
		if err := db.QueryRowScan(`SELECT edge_key, from_node_key FROM graph_edges`, &edgeKey, &fromKey); err != nil {
			t.Fatal(err)
		}
		if edgeKey != "code:module:dom:src/a->code:module:dom:b:IMPORTS" || fromKey != "code:module:dom:src/a" {
			t.Errorf("edge after rekey = %q from=%q, want rewritten keys", edgeKey, fromKey)
		}
	}
}

func TestRepointEdgeToNodeEmitsAndReplays(t *testing.T) {
	s := openJournalTestStore(t)
	revID, _ := s.CreateRevision("dom", "", "abc", "manual", "full", "{}")
	_, _, edgeID := seedNodesAndEdge(t, s, revID)
	var cID int64
	s.WithTx(func(tx *Store) error {
		var err error
		cID, err = tx.UpsertNode(NodeRow{
			NodeKey: "code:module:dom:c", Layer: "code", NodeType: "module", DomainKey: "dom",
			Name: "c", Status: "active", LastSeenRevisionID: revID, Metadata: "{}",
		})
		return err
	})

	if err := s.RepointEdge(edgeID, EdgeRepoint{NewToNodeID: cID, NewToNodeKey: "code:module:dom:c"}); err != nil {
		t.Fatal(err)
	}

	var key, fields string
	s.QueryRowScan(`SELECT subject_key, fields FROM journal_outbox WHERE kind='edge_rekey'`, &key, &fields)
	if key != "code:module:dom:a->code:module:dom:b:IMPORTS" {
		t.Errorf("edge_rekey key = %q, want OLD edge key", key)
	}
	var f map[string]string
	if err := json.Unmarshal([]byte(fields), &f); err != nil {
		t.Fatal(err)
	}
	if f["new_edge_key"] != "code:module:dom:a->code:module:dom:c:IMPORTS" || f["to"] != "code:module:dom:c" {
		t.Errorf("edge_rekey fields = %s, want new_edge_key + to", fields)
	}

	dst := replayInto(t, s)
	for _, db := range map[string]*Store{"live": s, "replay": dst} {
		var toKey string
		var toID int64
		if err := db.QueryRowScan(`SELECT to_node_key, to_node_id FROM graph_edges`, &toKey, &toID); err != nil {
			t.Fatal(err)
		}
		if toKey != "code:module:dom:c" {
			t.Errorf("to_node_key = %q, want code:module:dom:c", toKey)
		}
		gotID, err := db.GetNodeIDByKey("code:module:dom:c")
		if err != nil || toID != gotID {
			t.Errorf("to_node_id = %d, want id of c (%d, err=%v)", toID, gotID, err)
		}
	}
}

func TestRepointEdgeRetypeEmitsAndReplays(t *testing.T) {
	s := openJournalTestStore(t)
	revID, _ := s.CreateRevision("dom", "", "abc", "manual", "full", "{}")
	_, _, edgeID := seedNodesAndEdge(t, s, revID)

	if err := s.RepointEdge(edgeID, EdgeRepoint{NewEdgeType: "CONTAINS"}); err != nil {
		t.Fatal(err)
	}
	var fields string
	s.QueryRowScan(`SELECT fields FROM journal_outbox WHERE kind='edge_rekey'`, &fields)
	if !strings.Contains(fields, `"edge_type":"CONTAINS"`) {
		t.Errorf("edge_rekey fields = %s, missing edge_type", fields)
	}

	dst := replayInto(t, s)
	for _, db := range map[string]*Store{"live": s, "replay": dst} {
		var edgeKey, edgeType string
		if err := db.QueryRowScan(`SELECT edge_key, edge_type FROM graph_edges`, &edgeKey, &edgeType); err != nil {
			t.Fatal(err)
		}
		if edgeType != "CONTAINS" || edgeKey != "code:module:dom:a->code:module:dom:b:CONTAINS" {
			t.Errorf("after retype: key=%q type=%q", edgeKey, edgeType)
		}
	}
}

func TestRepointEdgeKeyConflict(t *testing.T) {
	s := openJournalTestStore(t)
	revID, _ := s.CreateRevision("dom", "", "abc", "manual", "full", "{}")
	aID, bID, edgeID := seedNodesAndEdge(t, s, revID)
	// A CONTAINS edge for the same pair already exists — retype must conflict.
	s.WithTx(func(tx *Store) error {
		_, err := tx.UpsertEdge(EdgeRow{
			EdgeKey:    "code:module:dom:a->code:module:dom:b:CONTAINS",
			FromNodeID: aID, ToNodeID: bID, EdgeType: "CONTAINS", DerivationKind: "hard",
			Active: true, LastSeenRevisionID: revID, Metadata: "{}",
			FromNodeKey: "code:module:dom:a", ToNodeKey: "code:module:dom:b",
		})
		return err
	})

	err := s.RepointEdge(edgeID, EdgeRepoint{NewEdgeType: "CONTAINS"})
	if !errors.Is(err, ErrEdgeKeyConflict) {
		t.Fatalf("err = %v, want ErrEdgeKeyConflict", err)
	}
	kinds := outboxKinds(t, s)
	if kinds[EvEdgeRekey] != 0 {
		t.Errorf("edge_rekey = %d, want 0 (conflict must not emit)", kinds[EvEdgeRekey])
	}
	// Original edge untouched.
	var importsCount int
	s.QueryRowScan(`SELECT COUNT(*) FROM graph_edges WHERE edge_type='IMPORTS'`, &importsCount)
	if importsCount != 1 {
		t.Errorf("IMPORTS edges = %d, want 1 (conflict must not mutate)", importsCount)
	}
}

func TestDeactivateEdgeEmitsEdgeStatusOnce(t *testing.T) {
	s := openJournalTestStore(t)
	revID, _ := s.CreateRevision("dom", "", "abc", "manual", "full", "{}")
	_, _, edgeID := seedNodesAndEdge(t, s, revID)

	if err := s.DeactivateEdge(edgeID, "duplicate_contains"); err != nil {
		t.Fatal(err)
	}
	// Second call: already inactive — transition-only, no second event.
	if err := s.DeactivateEdge(edgeID, "duplicate_contains"); err != nil {
		t.Fatal(err)
	}

	kinds := outboxKinds(t, s)
	if kinds[EvEdgeStatus] != 1 {
		t.Errorf("edge_status = %d, want exactly 1", kinds[EvEdgeStatus])
	}
	var key, fields string
	s.QueryRowScan(`SELECT subject_key, fields FROM journal_outbox WHERE kind='edge_status'`, &key, &fields)
	if key != "code:module:dom:a->code:module:dom:b:IMPORTS" {
		t.Errorf("edge_status key = %q, want edge key", key)
	}
	for _, want := range []string{`"active":false`, `"reason":"duplicate_contains"`} {
		if !strings.Contains(fields, want) {
			t.Errorf("edge_status fields = %s, missing %s", fields, want)
		}
	}

	dst := replayInto(t, s)
	var active int
	dst.QueryRowScan(`SELECT active FROM graph_edges`, &active)
	if active != 0 {
		t.Errorf("replayed edge active = %d, want 0", active)
	}
}

func TestUpdateEdgeMetadataEmitsAndReplays(t *testing.T) {
	s := openJournalTestStore(t)
	revID, _ := s.CreateRevision("dom", "", "abc", "manual", "full", "{}")
	_, _, edgeID := seedNodesAndEdge(t, s, revID)

	meta := `{"unmatched_calls":[{"method":"GET","path":"/x/42"}]}`
	if err := s.UpdateEdgeMetadata(edgeID, meta); err != nil {
		t.Fatal(err)
	}

	kinds := outboxKinds(t, s)
	if kinds[EvEdgeMeta] != 1 {
		t.Errorf("edge_meta = %d, want 1", kinds[EvEdgeMeta])
	}
	var key string
	s.QueryRowScan(`SELECT subject_key FROM journal_outbox WHERE kind='edge_meta'`, &key)
	if key != "code:module:dom:a->code:module:dom:b:IMPORTS" {
		t.Errorf("edge_meta key = %q, want edge key", key)
	}

	dst := replayInto(t, s)
	var got string
	dst.QueryRowScan(`SELECT metadata FROM graph_edges`, &got)
	if got != meta {
		t.Errorf("replayed metadata = %s, want %s", got, meta)
	}
}

func TestSetNodeStatusEmitsTransitionOnly(t *testing.T) {
	s := openJournalTestStore(t)
	revID, _ := s.CreateRevision("dom", "", "abc", "manual", "full", "{}")
	aID, _, _ := seedNodesAndEdge(t, s, revID)

	if err := s.SetNodeStatus(aID, "deleted"); err != nil {
		t.Fatal(err)
	}
	// Same status again — no second event.
	if err := s.SetNodeStatus(aID, "deleted"); err != nil {
		t.Fatal(err)
	}

	kinds := outboxKinds(t, s)
	if kinds[EvNodeStatus] != 1 {
		t.Errorf("node_status = %d, want exactly 1", kinds[EvNodeStatus])
	}
	var key, fields string
	s.QueryRowScan(`SELECT subject_key, fields FROM journal_outbox WHERE kind='node_status'`, &key, &fields)
	if key != "code:module:dom:a" || !strings.Contains(fields, `"status":"deleted"`) {
		t.Errorf("node_status key=%q fields=%s", key, fields)
	}

	dst := replayInto(t, s)
	var status string
	dst.QueryRowScan(`SELECT status FROM graph_nodes WHERE node_key='code:module:dom:a'`, &status)
	if status != "deleted" {
		t.Errorf("replayed status = %q, want deleted", status)
	}
}

// TestMarkStaleNodesStalesEvidence: a node going stale must stale its evidence
// (journaled), or the post-replay trust recompute derives "active" from the
// still-valid evidence and replay diverges from live (the kafka/redis bug).
func TestMarkStaleNodesStalesEvidence(t *testing.T) {
	s := openJournalTestStore(t)
	rev1, _ := s.CreateRevision("dom", "", "r1", "manual", "full", "{}")
	var aID int64
	s.WithTx(func(tx *Store) error {
		var err error
		aID, err = tx.UpsertNode(NodeRow{
			NodeKey: "code:module:dom:a", Layer: "code", NodeType: "module", DomainKey: "dom",
			Name: "a", Status: "active", LastSeenRevisionID: rev1, Metadata: "{}",
		})
		return err
	})
	if _, err := s.AddEvidence(EvidenceRow{
		TargetKind: "node", NodeID: aID, SourceKind: "file", FilePath: "a.ts",
		LineStart: 1, ExtractorID: "x", ExtractorVersion: "1.0",
		Confidence: 0.8, AssertionKind: "import", Metadata: "{}",
		ValidFromRevisionID: rev1,
	}); err != nil {
		t.Fatal(err)
	}
	rev2, _ := s.CreateRevision("dom", "r1", "r2", "manual", "incremental", "{}")

	if _, err := s.MarkStaleNodes("dom", rev2); err != nil {
		t.Fatal(err)
	}

	var evStatus string
	s.QueryRowScan(`SELECT evidence_status FROM graph_evidence LIMIT 1`, &evStatus)
	if evStatus != "stale" {
		t.Errorf("evidence_status = %q, want stale", evStatus)
	}
	var staleEvents int
	s.QueryRowScan(`SELECT COUNT(*) FROM journal_outbox WHERE kind='evidence_status' AND fields LIKE '%stale%'`, &staleEvents)
	if staleEvents != 1 {
		t.Errorf("evidence_status stale events = %d, want 1", staleEvents)
	}

	dst := replayInto(t, s)
	var replayed string
	dst.QueryRowScan(`SELECT evidence_status FROM graph_evidence`, &replayed)
	if replayed != "stale" {
		t.Errorf("replayed evidence_status = %q, want stale", replayed)
	}
}

// TestDeleteNodeSupersedesEvidence: deleting a node (tombstone or merge pass)
// must supersede its valid evidence — otherwise the post-replay trust recompute
// derives "active" from it and resurrects the node only on the replay side
// (the external_system merge bug).
func TestDeleteNodeSupersedesEvidence(t *testing.T) {
	s := openJournalTestStore(t)
	revID, _ := s.CreateRevision("dom", "", "r1", "manual", "full", "{}")
	var aID int64
	s.WithTx(func(tx *Store) error {
		var err error
		aID, err = tx.UpsertNode(NodeRow{
			NodeKey: "service:external_system:dom:x", Layer: "service", NodeType: "external_system", DomainKey: "dom",
			Name: "x", Status: "active", LastSeenRevisionID: revID, Metadata: "{}",
		})
		return err
	})
	if _, err := s.AddEvidence(EvidenceRow{
		TargetKind: "node", NodeID: aID, SourceKind: "file", FilePath: "a.ts",
		LineStart: 1, ExtractorID: "x", ExtractorVersion: "1.0",
		Confidence: 0.8, AssertionKind: "http_call", Metadata: "{}",
		ValidFromRevisionID: revID,
	}); err != nil {
		t.Fatal(err)
	}

	// SetNodeStatus(deleted) — the merge-pass path. (DeleteNode shares the rule.)
	if err := s.SetNodeStatus(aID, "deleted"); err != nil {
		t.Fatal(err)
	}

	var evStatus string
	s.QueryRowScan(`SELECT evidence_status FROM graph_evidence LIMIT 1`, &evStatus)
	if evStatus != "superseded" {
		t.Errorf("evidence_status = %q, want superseded", evStatus)
	}
	var supersededEvents int
	s.QueryRowScan(`SELECT COUNT(*) FROM journal_outbox WHERE kind='evidence_status' AND fields LIKE '%superseded%'`, &supersededEvents)
	if supersededEvents != 1 {
		t.Errorf("evidence_status superseded events = %d, want 1", supersededEvents)
	}

	dst := replayInto(t, s)
	var status, replayedEv string
	dst.QueryRowScan(`SELECT status FROM graph_nodes`, &status)
	dst.QueryRowScan(`SELECT evidence_status FROM graph_evidence`, &replayedEv)
	if status != "deleted" || replayedEv != "superseded" {
		t.Errorf("replayed node status=%q evidence=%q, want deleted/superseded", status, replayedEv)
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
