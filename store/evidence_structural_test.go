package store

import "testing"

// astEvidence seeds one graph_evidence row the way the structural extractor
// writes them (source_kind "ast", extractor_id "chronicle-ast").
func astEvidence(t *testing.T, s *Store, nodeID int64, file string, line int, version, hash string, revID int64) int64 {
	t.Helper()
	md := "{}"
	if hash != "" {
		md = `{"content_hash":"` + hash + `"}`
	}
	id, err := s.AddEvidence(EvidenceRow{
		TargetKind: "node", NodeID: nodeID,
		SourceKind: "ast", FilePath: file, LineStart: line,
		ExtractorID: "chronicle-ast", ExtractorVersion: version,
		Confidence: 1, EvidenceStatus: "valid", EvidencePolarity: "positive",
		ValidFromRevisionID: revID, Metadata: md,
	})
	if err != nil {
		t.Fatalf("astEvidence %s:%d: %v", file, line, err)
	}
	return id
}

func evidenceStatus(t *testing.T, s *Store, id int64) (status string, invalidatedBy int64) {
	t.Helper()
	if err := s.db.QueryRow(
		`SELECT evidence_status, COALESCE(invalidated_by_revision_id,0) FROM graph_evidence WHERE evidence_id = ?`,
		id).Scan(&status, &invalidatedBy); err != nil {
		t.Fatalf("evidenceStatus %d: %v", id, err)
	}
	return
}

// A structural re-extraction replaces the previous contribution of the same
// (file, extractor): what the new pass did not re-assert is superseded, and
// nothing else on the file moves.
func TestSupersedeEvidenceNotIn(t *testing.T) {
	s := openTestStore(t)
	rev1, node1, node2 := seedNodes(t, s)
	rev2, err := s.CreateRevision("orders", "sha1", "sha2", "git_hook", "incremental",
		`{"kind":"structural","complete":true}`)
	if err != nil {
		t.Fatalf("CreateRevision rev2: %v", err)
	}

	evA := astEvidence(t, s, node1, "f.ts", 10, "1", "aaa", rev1)
	evB := astEvidence(t, s, node1, "f.ts", 20, "1", "aaa", rev1)

	// An importer-owned row on the same file: not the structural pass's to touch.
	evDeclared, err := s.AddEvidence(EvidenceRow{
		TargetKind: "node", NodeID: node1,
		SourceKind: "declared", FilePath: "f.ts", LineStart: 10,
		ExtractorID: "manifest", ExtractorVersion: "1",
		Confidence: 1, EvidenceStatus: "valid", EvidencePolarity: "positive",
		ValidFromRevisionID: rev1, Metadata: "{}",
	})
	if err != nil {
		t.Fatalf("AddEvidence declared: %v", err)
	}

	// rev2: the same anchor is re-asserted (dedup updates evA in place) and one
	// new anchor appears.
	again := astEvidence(t, s, node1, "f.ts", 10, "2", "bbb", rev2)
	if again != evA {
		t.Fatalf("re-assertion created row %d, want the existing %d", again, evA)
	}
	evC := astEvidence(t, s, node2, "f.ts", 30, "2", "bbb", rev2)

	keep, err := s.EvidenceIDsCreatedIn("f.ts", "chronicle-ast", rev2)
	if err != nil {
		t.Fatalf("EvidenceIDsCreatedIn: %v", err)
	}
	got := map[int64]bool{}
	for _, id := range keep {
		got[id] = true
	}
	if len(keep) != 2 || !got[evA] || !got[evC] {
		t.Fatalf("EvidenceIDsCreatedIn(rev2) = %v, want [%d %d]", keep, evA, evC)
	}

	changed, err := s.SupersedeEvidenceNotIn("f.ts", "chronicle-ast", keep, rev2)
	if err != nil {
		t.Fatalf("SupersedeEvidenceNotIn: %v", err)
	}
	if changed != 1 {
		t.Fatalf("SupersedeEvidenceNotIn changed %d rows, want 1", changed)
	}

	if st, by := evidenceStatus(t, s, evB); st != "superseded" || by != rev2 {
		t.Errorf("un-re-asserted row: status=%q invalidated_by=%d, want superseded/%d", st, by, rev2)
	}
	for _, tc := range []struct {
		name string
		id   int64
	}{{"re-asserted", evA}, {"new", evC}, {"declared", evDeclared}} {
		if st, by := evidenceStatus(t, s, tc.id); st != "valid" || by != 0 {
			t.Errorf("%s row: status=%q invalidated_by=%d, want valid/0", tc.name, st, by)
		}
	}
}

// A file whose new extraction asserts nothing loses its whole contribution.
func TestSupersedeEvidenceNotInEmptyKeep(t *testing.T) {
	s := openTestStore(t)
	rev1, node1, _ := seedNodes(t, s)
	rev2, _ := s.CreateRevision("orders", "sha1", "sha2", "git_hook", "incremental", `{"kind":"structural"}`)
	evA := astEvidence(t, s, node1, "gone.ts", 10, "1", "aaa", rev1)

	changed, err := s.SupersedeEvidenceNotIn("gone.ts", "chronicle-ast", nil, rev2)
	if err != nil {
		t.Fatalf("SupersedeEvidenceNotIn: %v", err)
	}
	if changed != 1 {
		t.Fatalf("changed %d rows, want 1", changed)
	}
	if st, _ := evidenceStatus(t, s, evA); st != "superseded" {
		t.Errorf("status = %q, want superseded", st)
	}

	// Superseding twice is a no-op: only valid/revalidated rows transition.
	again, err := s.SupersedeEvidenceNotIn("gone.ts", "chronicle-ast", nil, rev2)
	if err != nil {
		t.Fatalf("second SupersedeEvidenceNotIn: %v", err)
	}
	if again != 0 {
		t.Errorf("second call changed %d rows, want 0", again)
	}
}

// The rules-pack backlog: which files still carry rows from an older pack.
func TestFilesWithExtractorVersionBelow(t *testing.T) {
	s := openTestStore(t)
	rev1, node1, _ := seedNodes(t, s)

	astEvidence(t, s, node1, "old.ts", 1, "1", "aaa", rev1)
	astEvidence(t, s, node1, "old.ts", 2, "1", "aaa", rev1) // same file, still one entry
	astEvidence(t, s, node1, "current.ts", 1, "2", "bbb", rev1)

	// Another extractor on an old version is not this backlog.
	if _, err := s.AddEvidence(EvidenceRow{
		TargetKind: "node", NodeID: node1,
		SourceKind: "file", FilePath: "agent.ts", LineStart: 1,
		ExtractorID: "claude", ExtractorVersion: "1",
		Confidence: 1, EvidenceStatus: "valid", EvidencePolarity: "positive",
		ValidFromRevisionID: rev1, Metadata: "{}",
	}); err != nil {
		t.Fatalf("AddEvidence claude: %v", err)
	}
	// A superseded row is not knowledge any more.
	if _, err := s.AddEvidence(EvidenceRow{
		TargetKind: "node", NodeID: node1,
		SourceKind: "ast", FilePath: "dead.ts", LineStart: 1,
		ExtractorID: "chronicle-ast", ExtractorVersion: "1",
		Confidence: 1, EvidenceStatus: "superseded", EvidencePolarity: "positive",
		ValidFromRevisionID: rev1, Metadata: "{}",
	}); err != nil {
		t.Fatalf("AddEvidence dead: %v", err)
	}

	files, err := s.FilesWithExtractorVersionBelow("chronicle-ast", "2", 10)
	if err != nil {
		t.Fatalf("FilesWithExtractorVersionBelow: %v", err)
	}
	if len(files) != 1 || files[0] != "old.ts" {
		t.Fatalf("files = %v, want [old.ts]", files)
	}

	n, err := s.CountFilesWithExtractorVersionBelow("chronicle-ast", "2")
	if err != nil {
		t.Fatalf("CountFilesWithExtractorVersionBelow: %v", err)
	}
	if n != 1 {
		t.Errorf("count = %d, want 1", n)
	}

	// The batch is bounded.
	astEvidence(t, s, node1, "older.ts", 1, "1", "aaa", rev1)
	one, err := s.FilesWithExtractorVersionBelow("chronicle-ast", "2", 1)
	if err != nil {
		t.Fatalf("FilesWithExtractorVersionBelow limit 1: %v", err)
	}
	if len(one) != 1 {
		t.Errorf("limit 1 returned %d files, want 1", len(one))
	}
	if n, _ := s.CountFilesWithExtractorVersionBelow("chronicle-ast", "2"); n != 2 {
		t.Errorf("count after second old file = %d, want 2", n)
	}

	// Nothing behind the current pack once every row is at it.
	if files, _ := s.FilesWithExtractorVersionBelow("chronicle-ast", "1", 10); len(files) != 1 || files[0] != "current.ts" {
		t.Errorf("below \"1\" = %v, want [current.ts]", files)
	}
}

// The content hash of the last structural look at a file — same pack + same
// content is a no-op, so the phase has to be able to ask.
func TestContentHashForFileExtractor(t *testing.T) {
	s := openTestStore(t)
	rev1, node1, _ := seedNodes(t, s)

	if h, err := s.ContentHashForFileExtractor("never.ts", "chronicle-ast"); err != nil || h != "" {
		t.Fatalf("unknown file: hash=%q err=%v, want \"\"/nil", h, err)
	}

	astEvidence(t, s, node1, "f.ts", 1, "1", "aaa", rev1)
	if h, err := s.ContentHashForFileExtractor("f.ts", "chronicle-ast"); err != nil || h != "aaa" {
		t.Fatalf("hash = %q err=%v, want aaa", h, err)
	}

	// A newer row wins.
	astEvidence(t, s, node1, "f.ts", 2, "1", "bbb", rev1)
	if h, _ := s.ContentHashForFileExtractor("f.ts", "chronicle-ast"); h != "bbb" {
		t.Errorf("hash = %q, want bbb (newest row)", h)
	}

	// Malformed metadata must not fail the query for the whole file.
	if _, err := s.db.Exec(
		`UPDATE graph_evidence SET metadata = 'not json' WHERE file_path = 'f.ts' AND line_start = 2`); err != nil {
		t.Fatalf("corrupt metadata: %v", err)
	}
	if h, err := s.ContentHashForFileExtractor("f.ts", "chronicle-ast"); err != nil || h != "aaa" {
		t.Fatalf("with malformed metadata: hash=%q err=%v, want aaa", h, err)
	}

	// Another extractor's hash is not this extractor's answer.
	if h, _ := s.ContentHashForFileExtractor("f.ts", "claude"); h != "" {
		t.Errorf("other extractor hash = %q, want \"\"", h)
	}
}
