package store

import "testing"

func TestStructuralHashRoundTrip(t *testing.T) {
	s := openTestStore(t)

	if _, _, ok, err := s.GetStructuralHash("orders", "src/a.ts"); err != nil || ok {
		t.Fatalf("unknown file: ok=%v err=%v, want false/nil", ok, err)
	}

	if err := s.SetStructuralHash("orders", "src/a.ts", "aaa", "1"); err != nil {
		t.Fatalf("SetStructuralHash: %v", err)
	}
	hash, pack, ok, err := s.GetStructuralHash("orders", "src/a.ts")
	if err != nil || !ok || hash != "aaa" || pack != "1" {
		t.Fatalf("got %q/%q ok=%v err=%v, want aaa/1/true/nil", hash, pack, ok, err)
	}

	// A second look at the same file overwrites rather than accumulating —
	// this is the record the phase compares against, not a history.
	if err := s.SetStructuralHash("orders", "src/a.ts", "bbb", "2"); err != nil {
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
	if err := s.SetStructuralHash("orders", "src/weird:name.ts", "ccc", "1"); err != nil {
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
		if err := s.SetStructuralHash(domain, file, hash, pack); err != nil {
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
