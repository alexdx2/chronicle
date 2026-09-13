package store

import "testing"

func TestStructuralHashRoundTrip(t *testing.T) {
	s := openTestStore(t)

	if _, _, ok, err := s.GetStructuralHash("orders", "src/a.ts"); err != nil || ok {
		t.Fatalf("unknown file: ok=%v err=%v, want false/nil", ok, err)
	}

	if err := s.SetStructuralHash("orders", "src/a.ts", "aaa", "1", 7); err != nil {
		t.Fatalf("SetStructuralHash: %v", err)
	}
	hash, pack, ok, err := s.GetStructuralHash("orders", "src/a.ts")
	if err != nil || !ok || hash != "aaa" || pack != "1" {
		t.Fatalf("got %q/%q ok=%v err=%v, want aaa/1/true/nil", hash, pack, ok, err)
	}

	// A second look at the same file overwrites rather than accumulating —
	// this is the record the phase compares against, not a history.
	if err := s.SetStructuralHash("orders", "src/a.ts", "bbb", "2", 8); err != nil {
		t.Fatalf("SetStructuralHash overwrite: %v", err)
	}
	if hash, pack, _, _ := s.GetStructuralHash("orders", "src/a.ts"); hash != "bbb" || pack != "2" {
		t.Errorf("after overwrite: %q/%q, want bbb/2", hash, pack)
	}

	// Domains do not see each other's records.
	if _, _, ok, _ := s.GetStructuralHash("billing", "src/a.ts"); ok {
		t.Error("another domain's file resolved")
	}

	// A path with colons in it survives the round trip (the key is prefixed,
	// not split).
	if err := s.SetStructuralHash("orders", "src/weird:name.ts", "ccc", "1", 9); err != nil {
		t.Fatalf("colon path: %v", err)
	}
	if hash, pack, ok, _ := s.GetStructuralHash("orders", "src/weird:name.ts"); !ok || hash != "ccc" || pack != "1" {
		t.Errorf("colon path: %q/%q ok=%v, want ccc/1/true", hash, pack, ok)
	}

	if err := s.DeleteStructuralHash("orders", "src/a.ts"); err != nil {
		t.Fatalf("DeleteStructuralHash: %v", err)
	}
	if _, _, ok, _ := s.GetStructuralHash("orders", "src/a.ts"); ok {
		t.Error("deleted file still resolves")
	}
	// Deleting what is not there is not an error — a deleted file may never
	// have been extracted.
	if err := s.DeleteStructuralHash("orders", "src/a.ts"); err != nil {
		t.Errorf("second delete: %v", err)
	}
}

// The rules-pack backlog: which files were last looked at by an older pack.
func TestStructuralHashBacklogByPack(t *testing.T) {
	s := openTestStore(t)

	must := func(domain, file, hash, pack string) {
		t.Helper()
		if err := s.SetStructuralHash(domain, file, hash, pack, 1); err != nil {
			t.Fatalf("SetStructuralHash %s: %v", file, err)
		}
	}
	must("orders", "old.ts", "h1", "1")
	must("orders", "older.ts", "h2", "1")
	must("orders", "current.ts", "h3", "3")
	must("billing", "elsewhere.ts", "h4", "1") // another domain's backlog

	n, err := s.CountStructuralHashesNotOnPack("orders", "3")
	if err != nil {
		t.Fatalf("CountStructuralHashesNotOnPack: %v", err)
	}
	if n != 2 {
		t.Errorf("count = %d, want 2", n)
	}

	files, err := s.FilesWithStructuralHashNotOnPack("orders", "3", 10)
	if err != nil {
		t.Fatalf("FilesWithStructuralHashNotOnPack: %v", err)
	}
	if len(files) != 2 || files[0] != "old.ts" && files[0] != "older.ts" {
		t.Fatalf("files = %v, want old.ts and older.ts", files)
	}
	for _, f := range files {
		if f == "elsewhere.ts" || f == "current.ts" {
			t.Errorf("%s is not this domain's backlog on this pack", f)
		}
	}

	// The batch is bounded, oldest pack first.
	must("orders", "ancient.ts", "h5", "2")
	one, err := s.FilesWithStructuralHashNotOnPack("orders", "3", 1)
	if err != nil {
		t.Fatalf("limit 1: %v", err)
	}
	if len(one) != 1 {
		t.Fatalf("limit 1 returned %d files", len(one))
	}
	if one[0] == "ancient.ts" {
		t.Errorf("first batch took pack 2 before pack 1: %v", one)
	}
	if n, _ := s.CountStructuralHashesNotOnPack("orders", "3"); n != 3 {
		t.Errorf("count is capped by the batch size: %d, want 3", n)
	}

	// Everything on the current pack: no backlog.
	if n, _ := s.CountStructuralHashesNotOnPack("billing", "1"); n != 0 {
		t.Errorf("billing backlog = %d, want 0", n)
	}

	// A record written by some other writer in a shape we do not recognise
	// counts as backlog — re-extracting is the safe answer.
	if err := s.SetSetting("structural:hash:orders:garbled.ts", "no-separator"); err != nil {
		t.Fatal(err)
	}
	if n, _ := s.CountStructuralHashesNotOnPack("orders", "3"); n != 4 {
		t.Errorf("malformed record not counted: %d, want 4", n)
	}
}

// A rebase strands the structural pointer on a branch nobody merged, and with
// it every content record those runs wrote: they say "already done" about
// files whose structure came from code this history does not contain. The
// records written after the base the phase falls back to are forgotten, so
// those files are read again — the one case where a content hash would hide a
// change rather than skip a no-op.
func TestDeleteStructuralHashesAfter(t *testing.T) {
	s := openTestStore(t)

	set := func(domain, file string, rev int64) {
		t.Helper()
		if err := s.SetStructuralHash(domain, file, "h", "1", rev); err != nil {
			t.Fatal(err)
		}
	}
	set("orders", "main-a.ts", 4)   // recorded under the base we fall back to
	set("orders", "main-b.ts", 5)   // the base itself
	set("orders", "branch-a.ts", 6) // the abandoned branch
	set("orders", "branch-b.ts", 9)
	set("billing", "other.ts", 9) // another domain's records are not ours
	// A record written before the revision was part of the value reads as 0:
	// older than anything, never branch-only work.
	if err := s.SetSetting("structural:hash:orders:legacy.ts", "h:1"); err != nil {
		t.Fatal(err)
	}

	n, err := s.DeleteStructuralHashesAfter("orders", 5)
	if err != nil {
		t.Fatalf("DeleteStructuralHashesAfter: %v", err)
	}
	if n != 2 {
		t.Fatalf("deleted %d records, want 2 (the two written after revision 5)", n)
	}
	for _, kept := range []string{"main-a.ts", "main-b.ts", "legacy.ts"} {
		if _, _, ok, _ := s.GetStructuralHash("orders", kept); !ok {
			t.Errorf("%s was recorded at or before the base and must survive", kept)
		}
	}
	for _, gone := range []string{"branch-a.ts", "branch-b.ts"} {
		if _, _, ok, _ := s.GetStructuralHash("orders", gone); ok {
			t.Errorf("%s was branch-only and must be forgotten", gone)
		}
	}
	if _, _, ok, _ := s.GetStructuralHash("billing", "other.ts"); !ok {
		t.Error("another domain's record was deleted")
	}
}

// The revision rides in the value, so the pack must still be read correctly —
// and a two-field record written before the revision existed still reads.
func TestStructuralHashPackSurvivesTheRevisionField(t *testing.T) {
	s := openTestStore(t)
	if err := s.SetStructuralHash("orders", "a.ts", "aaa", "2", 11); err != nil {
		t.Fatal(err)
	}
	if err := s.SetSetting("structural:hash:orders:legacy.ts", "bbb:2"); err != nil {
		t.Fatal(err)
	}
	if hash, pack, ok, _ := s.GetStructuralHash("orders", "a.ts"); !ok || hash != "aaa" || pack != "2" {
		t.Errorf("got %q/%q ok=%v, want aaa/2/true", hash, pack, ok)
	}
	if hash, pack, ok, _ := s.GetStructuralHash("orders", "legacy.ts"); !ok || hash != "bbb" || pack != "2" {
		t.Errorf("legacy record: %q/%q ok=%v, want bbb/2/true", hash, pack, ok)
	}
	if n, _ := s.CountStructuralHashesNotOnPack("orders", "2"); n != 0 {
		t.Errorf("both records are on pack 2, backlog = %d", n)
	}
	if n, _ := s.CountStructuralHashesNotOnPack("orders", "3"); n != 2 {
		t.Errorf("both records are behind pack 3, backlog = %d", n)
	}
}

// Two writers can share a revision now, so a resolve may only see and close the
// rows of its own role.
func TestExtractionsAreListedAndResolvedByRole(t *testing.T) {
	s := openTestStore(t)
	revID, err := s.CreateRevision("d", "", "sha", "manual", "full", "{}")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.SaveExtraction(revID, "d", "src/agent.ts", "extracted", "provider", `[{"kind":"import","to":"./x"}]`, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SaveStructuralExtraction(revID, "d", "src/struct.ts", "extracted", "provider", `[{"kind":"import","to":"./y"}]`, ""); err != nil {
		t.Fatal(err)
	}

	got, err := s.ListUnresolvedExtractionsByRole(revID, "d", StructuralExtractionRole)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].FilePath != "src/struct.ts" {
		t.Fatalf("role-scoped list = %+v, want only the structural row", got)
	}
	if all, _ := s.ListUnresolvedExtractions(revID, "d"); len(all) != 2 {
		t.Fatalf("the unscoped list must still see both: %d", len(all))
	}

	if err := s.MarkExtractionsResolvedByRole(revID, "d", StructuralExtractionRole); err != nil {
		t.Fatal(err)
	}
	left, err := s.ListUnresolvedExtractions(revID, "d")
	if err != nil {
		t.Fatal(err)
	}
	if len(left) != 1 || left[0].FilePath != "src/agent.ts" {
		t.Fatalf("the other writer's row was closed too: %+v", left)
	}
}
