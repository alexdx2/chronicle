package store

import "testing"

// structuralExtractorID is the id extract/structural stamps on its rows. These
// helpers are generic over extractor_id — the store deliberately does not
// import the extractor — so this is a test value, not a contract the store
// enforces; extract/structural.ExtractorID is where the real one lives.
const structuralExtractorID = "chronicle-structural"

// astEvidence seeds one graph_evidence row the way the structural phase writes
// them: source_kind "ast", the structural extractor's id.
func astEvidence(t *testing.T, s *Store, nodeID int64, file string, line int, version string, revID int64) int64 {
	t.Helper()
	id, err := s.AddEvidence(EvidenceRow{
		TargetKind: "node", NodeID: nodeID,
		SourceKind: "ast", FilePath: file, LineStart: line,
		ExtractorID: structuralExtractorID, ExtractorVersion: version,
		Confidence: 1, EvidenceStatus: "valid", EvidencePolarity: "positive",
		ValidFromRevisionID: revID, Metadata: "{}",
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

	evA := astEvidence(t, s, node1, "f.ts", 10, "1", rev1)
	evB := astEvidence(t, s, node1, "f.ts", 20, "1", rev1)

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
	again := astEvidence(t, s, node1, "f.ts", 10, "2", rev2)
	if again != evA {
		t.Fatalf("re-assertion created row %d, want the existing %d", again, evA)
	}
	evC := astEvidence(t, s, node2, "f.ts", 30, "2", rev2)

	keep, err := s.EvidenceIDsCreatedIn("f.ts", structuralExtractorID, rev2)
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

	changed, err := s.SupersedeEvidenceNotIn("f.ts", structuralExtractorID, keep, rev2)
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
	evA := astEvidence(t, s, node1, "gone.ts", 10, "1", rev1)

	changed, err := s.SupersedeEvidenceNotIn("gone.ts", structuralExtractorID, nil, rev2)
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
	again, err := s.SupersedeEvidenceNotIn("gone.ts", structuralExtractorID, nil, rev2)
	if err != nil {
		t.Fatalf("second SupersedeEvidenceNotIn: %v", err)
	}
	if again != 0 {
		t.Errorf("second call changed %d rows, want 0", again)
	}
}

// The rules-pack backlog: which files still carry rows from an older pack.
func TestFilesWithExtractorVersionOtherThan(t *testing.T) {
	s := openTestStore(t)
	rev1, node1, _ := seedNodes(t, s)

	astEvidence(t, s, node1, "old.ts", 1, "1", rev1)
	astEvidence(t, s, node1, "old.ts", 2, "1", rev1) // same file, still one entry
	astEvidence(t, s, node1, "current.ts", 1, "2", rev1)

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
		ExtractorID: structuralExtractorID, ExtractorVersion: "1",
		Confidence: 1, EvidenceStatus: "superseded", EvidencePolarity: "positive",
		ValidFromRevisionID: rev1, Metadata: "{}",
	}); err != nil {
		t.Fatalf("AddEvidence dead: %v", err)
	}

	files, err := s.FilesWithExtractorVersionOtherThan(structuralExtractorID, "2", 10)
	if err != nil {
		t.Fatalf("FilesWithExtractorVersionOtherThan: %v", err)
	}
	if len(files) != 1 || files[0] != "old.ts" {
		t.Fatalf("files = %v, want [old.ts]", files)
	}

	n, err := s.CountFilesWithExtractorVersionOtherThan(structuralExtractorID, "2")
	if err != nil {
		t.Fatalf("CountFilesWithExtractorVersionOtherThan: %v", err)
	}
	if n != 1 {
		t.Errorf("count = %d, want 1", n)
	}

	// The batch is bounded.
	astEvidence(t, s, node1, "older.ts", 1, "1", rev1)
	one, err := s.FilesWithExtractorVersionOtherThan(structuralExtractorID, "2", 1)
	if err != nil {
		t.Fatalf("FilesWithExtractorVersionOtherThan limit 1: %v", err)
	}
	if len(one) != 1 {
		t.Errorf("limit 1 returned %d files, want 1", len(one))
	}
	if n, _ := s.CountFilesWithExtractorVersionOtherThan(structuralExtractorID, "2"); n != 2 {
		t.Errorf("count after second old file = %d, want 2", n)
	}

	// Nothing behind the current pack once every row is at it.
	if files, _ := s.FilesWithExtractorVersionOtherThan(structuralExtractorID, "1", 10); len(files) != 1 || files[0] != "current.ts" {
		t.Errorf("below \"1\" = %v, want [current.ts]", files)
	}
}

// A pack bump past 9 must not send the backlog backwards: "10" is newer than
// "9", which a plain string compare gets exactly wrong.
func TestFilesWithExtractorVersionOtherThanOrdersPacksNumerically(t *testing.T) {
	s := openTestStore(t)
	rev1, node1, _ := seedNodes(t, s)

	astEvidence(t, s, node1, "on-ten.ts", 1, "10", rev1)
	astEvidence(t, s, node1, "on-nine.ts", 1, "9", rev1)

	files, err := s.FilesWithExtractorVersionOtherThan(structuralExtractorID, "11", 10)
	if err != nil {
		t.Fatalf("FilesWithExtractorVersionOtherThan: %v", err)
	}
	if len(files) != 2 || files[0] != "on-nine.ts" {
		t.Fatalf("files = %v, want on-nine.ts (pack 9) before on-ten.ts (pack 10)", files)
	}
}

// Phase-1 verification does not own what another writer will put right itself.
// The exemption is narrow on purpose: it is the structural phase's own rows —
// `ast` from extractor "chronicle-structural" — not the whole `ast` kind.
// graph/complexity.go and graph/similarity.go write `ast` rows too, from their
// own extractors, and a full scan is what re-asserts those; exempting them
// would freeze a churn metric at whatever it was the day it was measured.
func TestOnlyTheStructuralPhasesOwnAstRowsEscapeVerification(t *testing.T) {
	s := openTestStore(t)
	rev1, node1, _ := seedNodes(t, s)

	add := func(sourceKind, extractorID string, line int) int64 {
		t.Helper()
		id, err := s.AddEvidence(EvidenceRow{
			TargetKind: "node", NodeID: node1,
			SourceKind: sourceKind, FilePath: "changed.ts", LineStart: line,
			ExtractorID: extractorID, ExtractorVersion: "1",
			Confidence: 0.8, EvidenceStatus: "valid", EvidencePolarity: "positive",
			ValidFromRevisionID: rev1, Metadata: "{}",
		})
		if err != nil {
			t.Fatalf("AddEvidence %s/%s: %v", sourceKind, extractorID, err)
		}
		return id
	}

	evStructural := add("ast", structuralExtractorID, 1)
	evComplexity := add("ast", "chronicle-complexity", 2)
	evAgent := add("file", "claude", 3)
	evDeclared := add("declared", "surface-import", 4)

	if _, _, _, err := s.MarkEvidenceStaleByFiles([]string{"changed.ts"}); err != nil {
		t.Fatalf("MarkEvidenceStaleByFiles: %v", err)
	}

	for _, tc := range []struct {
		name string
		id   int64
		want string
		why  string
	}{
		{"structural ast", evStructural, "valid", "phase 2 supersedes it in the same run"},
		{"complexity ast", evComplexity, "stale", "a full scan re-asserts it, so it must refresh with the file"},
		{"agent", evAgent, "stale", "it is exactly what verification owns"},
		{"declared", evDeclared, "valid", "the importer owns it"},
	} {
		if st, _ := evidenceStatus(t, s, tc.id); st != tc.want {
			t.Errorf("%s row is %q, want %q — %s", tc.name, st, tc.want, tc.why)
		}
	}

	// The same split on the re-verification queue.
	reverifiable, err := s.ListReverifiableEvidenceByFile("changed.ts")
	if err != nil {
		t.Fatalf("ListReverifiableEvidenceByFile: %v", err)
	}
	offered := map[int64]bool{}
	for _, r := range reverifiable {
		offered[r.EvidenceID] = true
	}
	if offered[evStructural] {
		t.Error("the structural phase's own row was offered for re-verification")
	}
	if !offered[evComplexity] {
		t.Error("a complexity ast row was withheld from re-verification — nothing else will refresh it")
	}

	// And on what an agent is told to rescan.
	files, err := s.StaleFilePaths()
	if err != nil {
		t.Fatalf("StaleFilePaths: %v", err)
	}
	if len(files) != 1 || files[0] != "changed.ts" {
		t.Errorf("stale file paths = %v, want [changed.ts]", files)
	}
}

// The Go form of the same rule, for callers that hold rows rather than SQL.
