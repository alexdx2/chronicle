package store

import (
	"path/filepath"
	"strings"
	"testing"
)

func makeEvidence(targetKind string, targetID int64, line int) EvidenceRow {
	e := EvidenceRow{
		TargetKind:       targetKind,
		SourceKind:       "ast",
		RepoName:         "myrepo",
		FilePath:         "src/orders.go",
		LineStart:        line,
		LineEnd:          line + 5,
		ExtractorID:      "go-calls",
		ExtractorVersion: "1.0.0",
		Confidence:       0.9,
		Metadata:         "{}",
	}
	if targetKind == "node" {
		e.NodeID = targetID
	} else {
		e.EdgeID = targetID
	}
	return e
}

func TestAddEvidenceForEdge(t *testing.T) {
	s := openTestStore(t)
	revID, n1, n2 := seedNodes(t, s)
	edgeID, err := s.UpsertEdge(makeEdgeRow("edge:calls:oc:os", n1, n2, revID))
	if err != nil {
		t.Fatalf("UpsertEdge: %v", err)
	}

	evID, err := s.AddEvidence(makeEvidence("edge", edgeID, 10))
	if err != nil {
		t.Fatalf("AddEvidence: %v", err)
	}
	if evID <= 0 {
		t.Fatalf("expected positive id, got %d", evID)
	}
}

func TestAddEvidenceDedup(t *testing.T) {
	s := openTestStore(t)
	revID, n1, n2 := seedNodes(t, s)
	edgeID, _ := s.UpsertEdge(makeEdgeRow("edge:calls:oc:os", n1, n2, revID))

	ev := makeEvidence("edge", edgeID, 10)
	id1, err := s.AddEvidence(ev)
	if err != nil {
		t.Fatalf("AddEvidence first: %v", err)
	}

	// Same location — should update, not insert.
	ev.Confidence = 0.75
	ev.CommitSHA = "abc123"
	id2, err := s.AddEvidence(ev)
	if err != nil {
		t.Fatalf("AddEvidence dedup: %v", err)
	}
	if id1 != id2 {
		t.Errorf("expected same evidence_id on dedup, got %d vs %d", id1, id2)
	}

	// Verify count is still 1.
	rows, err := s.ListEvidenceByEdge(edgeID)
	if err != nil {
		t.Fatalf("ListEvidenceByEdge: %v", err)
	}
	if len(rows) != 1 {
		t.Errorf("expected 1 evidence row after dedup, got %d", len(rows))
	}
}

func TestAddEvidenceDifferentLine(t *testing.T) {
	s := openTestStore(t)
	revID, n1, n2 := seedNodes(t, s)
	edgeID, _ := s.UpsertEdge(makeEdgeRow("edge:calls:oc:os", n1, n2, revID))

	_, err := s.AddEvidence(makeEvidence("edge", edgeID, 10))
	if err != nil {
		t.Fatalf("AddEvidence line 10: %v", err)
	}
	_, err = s.AddEvidence(makeEvidence("edge", edgeID, 20))
	if err != nil {
		t.Fatalf("AddEvidence line 20: %v", err)
	}

	rows, err := s.ListEvidenceByEdge(edgeID)
	if err != nil {
		t.Fatalf("ListEvidenceByEdge: %v", err)
	}
	if len(rows) != 2 {
		t.Errorf("expected 2 evidence rows for different lines, got %d", len(rows))
	}
}

func TestListEvidenceByNode(t *testing.T) {
	s := openTestStore(t)
	revID, n1, n2 := seedNodes(t, s)
	_ = revID

	s.AddEvidence(makeEvidence("node", n1, 5))
	s.AddEvidence(makeEvidence("node", n1, 15))
	s.AddEvidence(makeEvidence("node", n2, 5))

	rows, err := s.ListEvidenceByNode(n1)
	if err != nil {
		t.Fatalf("ListEvidenceByNode: %v", err)
	}
	if len(rows) != 2 {
		t.Errorf("expected 2 evidence rows for n1, got %d", len(rows))
	}
}

// TestListEvidenceByNodeAndEdge_PopulateEvidenceUID is the regression test
// for queryEvidenceArgs's evidence_uid fix: before it, ListEvidenceByNode and
// ListEvidenceByEdge never selected evidence_uid at all, so EvidenceRow.EvidenceUID
// off either was always "" regardless of what was actually in the column.
// AddEvidence always sets a real, non-empty evidence_uid on insert (an
// explicit one, or its EvidenceKey(...) fallback), so a fresh row here proves
// the fix by construction: if this ever regresses back to reading "", it is
// caught here directly, not inferred from some other test merely not failing.
func TestListEvidenceByNodeAndEdge_PopulateEvidenceUID(t *testing.T) {
	s := openTestStore(t)
	revID, n1, n2 := seedNodes(t, s)
	edgeID, err := s.UpsertEdge(makeEdgeRow("edge:calls:oc:os", n1, n2, revID))
	if err != nil {
		t.Fatalf("UpsertEdge: %v", err)
	}

	if _, err := s.AddEvidence(makeEvidence("node", n1, 5)); err != nil {
		t.Fatalf("AddEvidence (node): %v", err)
	}
	if _, err := s.AddEvidence(makeEvidence("edge", edgeID, 10)); err != nil {
		t.Fatalf("AddEvidence (edge): %v", err)
	}

	nodeRows, err := s.ListEvidenceByNode(n1)
	if err != nil {
		t.Fatalf("ListEvidenceByNode: %v", err)
	}
	if len(nodeRows) != 1 {
		t.Fatalf("len(nodeRows) = %d, want 1", len(nodeRows))
	}
	if nodeRows[0].EvidenceUID == "" {
		t.Error("ListEvidenceByNode: EvidenceUID is empty, want the stored value")
	}
	if want := evidenceUIDByID(t, s, nodeRows[0].EvidenceID); nodeRows[0].EvidenceUID != want {
		t.Errorf("ListEvidenceByNode: EvidenceUID = %q, want %q (direct column read)", nodeRows[0].EvidenceUID, want)
	}

	edgeRows, err := s.ListEvidenceByEdge(edgeID)
	if err != nil {
		t.Fatalf("ListEvidenceByEdge: %v", err)
	}
	if len(edgeRows) != 1 {
		t.Fatalf("len(edgeRows) = %d, want 1", len(edgeRows))
	}
	if edgeRows[0].EvidenceUID == "" {
		t.Error("ListEvidenceByEdge: EvidenceUID is empty, want the stored value")
	}
	if want := evidenceUIDByID(t, s, edgeRows[0].EvidenceID); edgeRows[0].EvidenceUID != want {
		t.Errorf("ListEvidenceByEdge: EvidenceUID = %q, want %q (direct column read)", edgeRows[0].EvidenceUID, want)
	}
}

// TestListEvidenceByAssertionKind verifies the selector filters by
// assertion_kind across target kinds/domains (no domain filter, unlike
// ListRejectedEvidence) and that the returned rows carry NodeID/EdgeID —
// pro's package index needs EdgeID populated to attach identities to edges.
func TestListEvidenceByAssertionKind(t *testing.T) {
	s := openTestStore(t)
	revID, n1, n2 := seedNodes(t, s)
	edgeID, err := s.UpsertEdge(makeEdgeRow("edge:calls:oc:os", n1, n2, revID))
	if err != nil {
		t.Fatalf("UpsertEdge: %v", err)
	}

	withAssertion := makeEvidence("edge", edgeID, 10)
	withAssertion.Assertion = `{"module":"@okeep/auth-client"}`
	withAssertion.AssertionKind = "import_specifier"
	if _, err := s.AddEvidence(withAssertion); err != nil {
		t.Fatalf("AddEvidence withAssertion: %v", err)
	}

	// Different assertion_kind — must not be picked up.
	otherKind := makeEvidence("node", n1, 5)
	otherKind.Assertion = `{"foo":"bar"}`
	otherKind.AssertionKind = "other_kind"
	if _, err := s.AddEvidence(otherKind); err != nil {
		t.Fatalf("AddEvidence otherKind: %v", err)
	}

	// No assertion at all — must not be picked up.
	if _, err := s.AddEvidence(makeEvidence("node", n2, 5)); err != nil {
		t.Fatalf("AddEvidence plain: %v", err)
	}

	rows, err := s.ListEvidenceByAssertionKind("import_specifier")
	if err != nil {
		t.Fatalf("ListEvidenceByAssertionKind: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 row for import_specifier, got %d", len(rows))
	}
	if rows[0].Assertion != `{"module":"@okeep/auth-client"}` {
		t.Errorf("Assertion = %q, want the seeded payload", rows[0].Assertion)
	}
	if rows[0].EdgeID != edgeID {
		t.Errorf("EdgeID = %d, want %d", rows[0].EdgeID, edgeID)
	}

	none, err := s.ListEvidenceByAssertionKind("does_not_exist")
	if err != nil {
		t.Fatalf("ListEvidenceByAssertionKind (empty): %v", err)
	}
	if len(none) != 0 {
		t.Errorf("expected 0 rows for unknown assertion_kind, got %d", len(none))
	}
}

// evidenceUIDByID reads evidence_uid directly by evidence_id. Needed because
// queryEvidenceArgs — the SELECT shared by ListEvidenceByNode/ListEvidenceByEdge/
// ListEvidenceByEdgeIDs — does not select evidence_uid (or context_id) at
// all, so EvidenceRow.EvidenceUID on anything those return is always "",
// regardless of what is actually stored. Discovered while writing
// TestInsertEvidenceBatch_Inserts: asserting EvidenceUID off a
// ListEvidenceByNode result silently passed on empty strings on both sides,
// which is exactly the kind of true-by-accident assertion worth naming
// rather than leaving in place — this helper goes straight at the column
// instead. Pre-existing in queryEvidenceArgs, unrelated to InsertEvidenceBatch
// and out of scope for this test to fix (it also affects AddEvidence's
// existing callers and snapshot/export.go's exportNodes/exportEdges, which
// read node/edge evidence through the same two listing functions) — see the
// task report.
func evidenceUIDByID(t *testing.T, s *Store, evidenceID int64) string {
	t.Helper()
	var uid string
	if err := s.db.QueryRow(`SELECT COALESCE(evidence_uid,'') FROM graph_evidence WHERE evidence_id = ?`, evidenceID).Scan(&uid); err != nil {
		t.Fatalf("evidenceUIDByID(%d): %v", evidenceID, err)
	}
	return uid
}

// TestInsertEvidenceBatch_Inserts proves the batch path (no dedup lookup,
// see the method's own doc comment) inserts node- and edge-attached rows
// correctly — same fields, same evidence_uid — round-tripping through
// ListEvidenceByNode/ListEvidenceByEdge (plus a direct evidence_uid column
// read — see evidenceUIDByID) exactly like AddEvidence's own insert path
// would.
func TestInsertEvidenceBatch_Inserts(t *testing.T) {
	s := openTestStore(t)
	revID, n1, n2 := seedNodes(t, s)
	edgeID, err := s.UpsertEdge(makeEdgeRow("edge:calls:oc:os", n1, n2, revID))
	if err != nil {
		t.Fatalf("UpsertEdge: %v", err)
	}

	rows := []EvidenceRow{
		{
			TargetKind: "node", NodeID: n1, SourceKind: "file", FilePath: "src/a.go",
			LineStart: 5, ExtractorID: "chronicle-scan", ExtractorVersion: "1",
			Confidence: 0.9, EvidenceStatus: "valid", EvidencePolarity: "positive",
			EvidenceUID: "ev-node-1", Metadata: "{}",
		},
		{
			TargetKind: "node", NodeID: n2, SourceKind: "file", FilePath: "src/b.go",
			LineStart: 10, ExtractorID: "chronicle-scan", ExtractorVersion: "1",
			Confidence: 0.8, EvidenceStatus: "valid", EvidencePolarity: "positive",
			EvidenceUID: "ev-node-2", Metadata: "{}",
		},
		{
			TargetKind: "edge", EdgeID: edgeID, SourceKind: "file", FilePath: "src/c.go",
			LineStart: 20, ExtractorID: "chronicle-scan", ExtractorVersion: "1",
			Confidence: 0.7, EvidenceStatus: "valid", EvidencePolarity: "positive",
			EvidenceUID: "ev-edge-1", Metadata: "{}", BlobSHA: "deadbeef",
		},
	}

	if err := s.InsertEvidenceBatch(rows); err != nil {
		t.Fatalf("InsertEvidenceBatch: %v", err)
	}

	n1Rows, err := s.ListEvidenceByNode(n1)
	if err != nil {
		t.Fatalf("ListEvidenceByNode(n1): %v", err)
	}
	if len(n1Rows) != 1 {
		t.Fatalf("n1: len(rows) = %d, want 1: %+v", len(n1Rows), n1Rows)
	}
	if n1Rows[0].Confidence != 0.9 || n1Rows[0].FilePath != "src/a.go" || n1Rows[0].LineStart != 5 {
		t.Errorf("n1 evidence = %+v, want the fields from the batch row", n1Rows[0])
	}
	if got := evidenceUIDByID(t, s, n1Rows[0].EvidenceID); got != "ev-node-1" {
		t.Errorf("n1 evidence_uid = %q, want %q", got, "ev-node-1")
	}

	n2Rows, err := s.ListEvidenceByNode(n2)
	if err != nil {
		t.Fatalf("ListEvidenceByNode(n2): %v", err)
	}
	if len(n2Rows) != 1 {
		t.Fatalf("n2: len(rows) = %d, want 1: %+v", len(n2Rows), n2Rows)
	}
	if n2Rows[0].Confidence != 0.8 {
		t.Errorf("n2 evidence = %+v, want the fields from the batch row", n2Rows[0])
	}
	if got := evidenceUIDByID(t, s, n2Rows[0].EvidenceID); got != "ev-node-2" {
		t.Errorf("n2 evidence_uid = %q, want %q", got, "ev-node-2")
	}

	edgeRows, err := s.ListEvidenceByEdge(edgeID)
	if err != nil {
		t.Fatalf("ListEvidenceByEdge: %v", err)
	}
	if len(edgeRows) != 1 {
		t.Fatalf("edge: len(rows) = %d, want 1: %+v", len(edgeRows), edgeRows)
	}
	if edgeRows[0].Confidence != 0.7 || edgeRows[0].BlobSHA != "deadbeef" {
		t.Errorf("edge evidence = %+v, want the fields from the batch row", edgeRows[0])
	}
	if got := evidenceUIDByID(t, s, edgeRows[0].EvidenceID); got != "ev-edge-1" {
		t.Errorf("edge evidence_uid = %q, want %q", got, "ev-edge-1")
	}
}

// TestInsertEvidenceBatch_RollsBackOnConstraintFailure proves the batch is
// all-or-nothing: a row that fails a SQL CHECK constraint partway through
// must roll back rows the transaction already executed successfully before
// it, not just skip the bad one.
func TestInsertEvidenceBatch_RollsBackOnConstraintFailure(t *testing.T) {
	s := openTestStore(t)
	_, n1, n2 := seedNodes(t, s)

	rows := []EvidenceRow{
		{
			TargetKind: "node", NodeID: n1, SourceKind: "file", FilePath: "src/a.go",
			LineStart: 5, ExtractorID: "chronicle-scan", ExtractorVersion: "1",
			Confidence: 0.9, EvidenceStatus: "valid", EvidencePolarity: "positive",
			EvidenceUID: "ev-good", Metadata: "{}",
		},
		{
			// Confidence 1.5 violates graph_evidence's own
			// CHECK (confidence >= 0 AND confidence <= 1).
			TargetKind: "node", NodeID: n2, SourceKind: "file", FilePath: "src/b.go",
			LineStart: 10, ExtractorID: "chronicle-scan", ExtractorVersion: "1",
			Confidence: 1.5, EvidenceStatus: "valid", EvidencePolarity: "positive",
			EvidenceUID: "ev-bad", Metadata: "{}",
		},
	}

	if err := s.InsertEvidenceBatch(rows); err == nil {
		t.Fatal("InsertEvidenceBatch: want an error from the out-of-range confidence, got nil")
	}

	n1Rows, err := s.ListEvidenceByNode(n1)
	if err != nil {
		t.Fatalf("ListEvidenceByNode(n1): %v", err)
	}
	if len(n1Rows) != 0 {
		t.Errorf("n1 has %d evidence rows after a failed batch, want 0 (the row before the failing one should have rolled back too): %+v", len(n1Rows), n1Rows)
	}

	var total int
	if err := s.QueryRowScan(`SELECT COUNT(*) FROM graph_evidence`, &total); err != nil {
		t.Fatalf("count graph_evidence: %v", err)
	}
	if total != 0 {
		t.Errorf("graph_evidence has %d rows after a failed batch, want 0", total)
	}
}

// TestInsertEvidenceBatch_RefusesTransactionScopedStore confirms the
// transaction-scoping guard InsertEvidenceBatch actually has — it must
// refuse to run (and insert nothing) when called on a store already inside
// a WithTx callback, since it needs to open its own top-level transaction
// (mirroring CopyLocalState in store/journal_rebuild.go), and *sql.Tx
// cannot nest.
func TestInsertEvidenceBatch_RefusesTransactionScopedStore(t *testing.T) {
	s := openTestStore(t)
	_, n1, _ := seedNodes(t, s)

	batchErr := s.WithTx(func(tx *Store) error {
		return tx.InsertEvidenceBatch([]EvidenceRow{
			{
				TargetKind: "node", NodeID: n1, SourceKind: "file", FilePath: "src/a.go",
				ExtractorID: "chronicle-scan", ExtractorVersion: "1", Confidence: 0.9,
				EvidenceStatus: "valid", EvidencePolarity: "positive", EvidenceUID: "ev-x", Metadata: "{}",
			},
		})
	})
	if batchErr == nil {
		t.Fatal("InsertEvidenceBatch on a transaction-scoped store: want an error, got nil")
	}
	if !strings.Contains(batchErr.Error(), "transaction-scoped") {
		t.Errorf("error = %q, want it to mention the transaction-scoping guard", batchErr.Error())
	}

	rows, err := s.ListEvidenceByNode(n1)
	if err != nil {
		t.Fatalf("ListEvidenceByNode: %v", err)
	}
	if len(rows) != 0 {
		t.Errorf("evidence was inserted despite the transaction-scoping guard: %+v", rows)
	}
}

// TestEvidence_BlobSHARoundTripsThroughJournal writes evidence carrying a
// BlobSHA, flushes the journal to events/*.jsonl, replays those events into a
// completely fresh store (the same mechanism `chronicle journal rebuild`
// uses), and checks the blob survived — not just that it sits in the live
// table AddEvidence wrote to directly.
func TestEvidence_BlobSHARoundTripsThroughJournal(t *testing.T) {
	src := openJournalTestStore(t)
	_, n1, _ := seedNodes(t, src)

	const wantBlob = "0123456789abcdef0123456789abcdef01234567"
	ev := makeEvidence("node", n1, 3)
	ev.ExtractorID = "chronicle-scan"
	ev.AssertionKind = "declares_service"
	ev.Assertion = `{}`
	ev.BlobSHA = wantBlob
	if _, err := src.AddEvidence(ev); err != nil {
		t.Fatalf("AddEvidence: %v", err)
	}

	if _, err := src.FlushJournal(); err != nil {
		t.Fatalf("FlushJournal: %v", err)
	}

	dst := openJournalTestStore(t)
	res, err := dst.ReplayJournal(filepath.Join(src.Dir(), "events"))
	if err != nil {
		t.Fatalf("ReplayJournal: %v", err)
	}
	if res.Applied == 0 {
		t.Fatal("ReplayJournal applied 0 events — nothing to check")
	}

	dstNodeID, err := dst.GetNodeIDByKey("code:controller:orders:orderscontroller")
	if err != nil {
		t.Fatalf("replayed node missing: %v", err)
	}
	rows, err := dst.ListEvidenceByNode(dstNodeID)
	if err != nil {
		t.Fatalf("ListEvidenceByNode: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 replayed evidence row, got %d: %+v", len(rows), rows)
	}
	if rows[0].BlobSHA != wantBlob {
		t.Errorf("replayed BlobSHA = %q, want %q — blob did not survive journal rebuild", rows[0].BlobSHA, wantBlob)
	}
}
