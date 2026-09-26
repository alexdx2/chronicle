package store

import "testing"

func TestSettings_RoundTrip(t *testing.T) {
	s := openTestStore(t)
	if _, err := s.GetSetting("missing"); err == nil {
		t.Error("missing setting should error")
	}
	if err := s.SetSetting("theme", "dark"); err != nil {
		t.Fatalf("SetSetting: %v", err)
	}
	if err := s.SetSetting("theme", "light"); err != nil { // upsert path
		t.Fatalf("SetSetting upsert: %v", err)
	}
	v, err := s.GetSetting("theme")
	if err != nil || v != "light" {
		t.Fatalf("GetSetting: %q err=%v", v, err)
	}
}

func TestPacks_RoundTrip(t *testing.T) {
	s := openTestStore(t)
	if s.PacksDir() == "" {
		t.Fatal("PacksDir empty")
	}
	if ids, err := s.ListPackFiles(); err != nil || len(ids) != 0 {
		t.Fatalf("empty list: %v %v", ids, err)
	}
	if err := s.SavePackFile("", "x"); err == nil {
		t.Error("empty id should error")
	}
	if err := s.SavePackFile("custom/django", "# Django pack"); err != nil {
		t.Fatalf("SavePackFile: %v", err)
	}
	got, err := s.LoadPackFile("custom/django")
	if err != nil || got != "# Django pack" {
		t.Fatalf("LoadPackFile: %q err=%v", got, err)
	}
	if _, err := s.LoadPackFile("nope/missing"); err == nil {
		t.Error("missing pack load should error")
	}
	ids, err := s.ListPackFiles()
	if err != nil || len(ids) != 1 || ids[0] != "django" {
		t.Fatalf("ListPackFiles: %v err=%v", ids, err)
	}
}

func TestDiscoveries_CRUD(t *testing.T) {
	s := openTestStore(t)
	id, err := s.AddDiscovery(Discovery{
		DomainKey: "d", Category: "insight", Severity: "warning",
		Title: "T", Description: "D", SuggestedAction: "fix it", Source: "claude", Confidence: 0.9,
	})
	if err != nil || id == 0 {
		t.Fatalf("AddDiscovery: id=%d err=%v", id, err)
	}
	got, err := s.ListDiscoveries("d", "insight")
	if err != nil || len(got) != 1 || got[0].Title != "T" {
		t.Fatalf("ListDiscoveries filtered: %+v err=%v", got, err)
	}
	if all, _ := s.ListDiscoveries("d", ""); len(all) != 1 {
		t.Fatalf("ListDiscoveries all: %+v", all)
	}
	if none, _ := s.ListDiscoveries("d", "other-cat"); len(none) != 0 {
		t.Fatalf("ListDiscoveries other cat should be empty: %+v", none)
	}
	if err := s.MarkDiscoveryApplied(id); err != nil {
		t.Fatalf("MarkDiscoveryApplied: %v", err)
	}
	after, _ := s.ListDiscoveries("d", "insight")
	if len(after) != 1 || !after[0].Applied {
		t.Fatalf("discovery should be applied: %+v", after)
	}
}

func TestLanguage_GlossaryAndChecks(t *testing.T) {
	s := openTestStore(t)
	_, err := s.UpsertTerm(DomainTerm{
		DomainKey: "d", Term: "Battle", Aliases: []string{"fight"},
		AntiPatterns: []string{"brawl"}, Description: "a battle", Context: "arena",
	})
	if err != nil {
		t.Fatalf("UpsertTerm: %v", err)
	}
	g, err := s.GetGlossary("d")
	if err != nil || len(g) != 1 || g[0].Term != "Battle" {
		t.Fatalf("GetGlossary: %+v err=%v", g, err)
	}
	term, err := s.getTermByName("d", "Battle")
	if err != nil || term == nil {
		t.Fatalf("getTermByName: %+v err=%v", term, err)
	}
	// CheckLanguage runs without error (violations depend on graph contents).
	if _, err := s.CheckLanguage("d"); err != nil {
		t.Fatalf("CheckLanguage: %v", err)
	}
	if err := s.RemoveAntiPattern("d", "Battle", "brawl"); err != nil {
		t.Fatalf("RemoveAntiPattern: %v", err)
	}
	if err := s.DeleteTerm("d", "Battle"); err != nil {
		t.Fatalf("DeleteTerm: %v", err)
	}
	if g2, _ := s.GetGlossary("d"); len(g2) != 0 {
		t.Fatalf("glossary should be empty after delete: %+v", g2)
	}
}

func TestObligations_Lifecycle(t *testing.T) {
	s, rev := testStoreWithRevision(t)
	id, err := s.CreateObligation(rev, "d", "extract_file", "src/a.ts", "needs extraction")
	if err != nil || id == 0 {
		t.Fatalf("CreateObligation: id=%d err=%v", id, err)
	}
	if n, err := s.CountPendingObligations(rev, "extract_file"); err != nil || n != 1 {
		t.Fatalf("CountPending: %d err=%v", n, err)
	}
	open, err := s.ListOpenObligations(rev)
	if err != nil || len(open) != 1 {
		t.Fatalf("ListOpenObligations: %+v err=%v", open, err)
	}
	if all, _ := s.ListAllObligations(rev); len(all) != 1 {
		t.Fatalf("ListAllObligations: %+v", all)
	}
	// Defer then requeue.
	if err := s.DeferObligation(rev, "extract_file", "src/a.ts", "later"); err != nil {
		t.Fatalf("DeferObligation: %v", err)
	}
	if err := s.RequeueObligation(id); err != nil {
		t.Fatalf("RequeueObligation: %v", err)
	}
	// Skip.
	if err := s.SkipObligation(rev, "extract_file", "src/a.ts", "irrelevant"); err != nil {
		t.Fatalf("SkipObligation: %v", err)
	}
}

func TestExtractions_CRUD(t *testing.T) {
	s, rev := testStoreWithRevision(t)
	id, err := s.SaveExtraction(rev, "d", "src/a.ts", "extracted", "controller", `{"nodes":[]}`, "")
	if err != nil || id == 0 {
		t.Fatalf("SaveExtraction: id=%d err=%v", id, err)
	}
	if _, err := s.SaveExtraction(rev, "d", "src/b.ts", "no_runtime_architecture", "", "{}", ""); err != nil {
		t.Fatalf("SaveExtraction b: %v", err)
	}
	list, err := s.ListExtractions(rev, "d")
	if err != nil || len(list) != 2 {
		t.Fatalf("ListExtractions: %+v err=%v", list, err)
	}
	if un, _ := s.ListUnresolvedExtractions(rev, "d"); un == nil && len(un) != 0 {
		t.Fatalf("ListUnresolvedExtractions err")
	}
	total, extracted, noArch, skipped, errored, err := s.GetScanCoverage(rev, "d")
	if err != nil {
		t.Fatalf("GetScanCoverage: %v", err)
	}
	if total < 2 || extracted < 1 || noArch < 1 {
		t.Fatalf("coverage counts: total=%d extracted=%d noArch=%d skipped=%d errored=%d", total, extracted, noArch, skipped, errored)
	}
	if err := s.MarkExtractionsResolved(rev, "d"); err != nil {
		t.Fatalf("MarkExtractionsResolved: %v", err)
	}
}

// TestCoverage_UpsertListClear exercises file_coverage: per-(domain, file,
// extractor) upsert, domain-scoped listing in file_path/extractor_id order,
// re-observation updating in place rather than duplicating, and
// ClearCoverageBlob blanking the blob for every extractor on a file while
// leaving status (and other files) alone.
func TestCoverage_UpsertListClear(t *testing.T) {
	s := openTestStore(t)

	for _, c := range []CoverageRow{
		{DomainKey: "d", FilePath: "b.ts", ExtractorID: "chronicle-scan",
			ExtractorVersion: "fact-schema@1", Status: "extracted", BlobSHA: "blob-b-1"},
		{DomainKey: "d", FilePath: "a.ts", ExtractorID: "chronicle-structural",
			ExtractorVersion: "1", Status: "extracted", BlobSHA: "blob-a-struct"},
		{DomainKey: "d", FilePath: "a.ts", ExtractorID: "chronicle-scan",
			ExtractorVersion: "fact-schema@1", Status: "extracted", BlobSHA: "blob-a-scan"},
		{DomainKey: "other", FilePath: "z.ts", ExtractorID: "chronicle-scan",
			ExtractorVersion: "fact-schema@1", Status: "extracted", BlobSHA: "blob-z"},
	} {
		if err := s.UpsertCoverage(c); err != nil {
			t.Fatalf("UpsertCoverage(%+v): %v", c, err)
		}
	}

	rows, err := s.ListCoverage("d")
	if err != nil {
		t.Fatalf("ListCoverage(d): %v", err)
	}
	if len(rows) != 3 {
		t.Fatalf("ListCoverage(d) = %d rows, want 3: %+v", len(rows), rows)
	}
	// Ordered by file_path then extractor_id: a.ts/chronicle-scan,
	// a.ts/chronicle-structural, b.ts/chronicle-scan.
	if rows[0].FilePath != "a.ts" || rows[0].ExtractorID != "chronicle-scan" {
		t.Errorf("rows[0] = %+v, want a.ts/chronicle-scan", rows[0])
	}
	if rows[1].FilePath != "a.ts" || rows[1].ExtractorID != "chronicle-structural" {
		t.Errorf("rows[1] = %+v, want a.ts/chronicle-structural", rows[1])
	}
	if rows[2].FilePath != "b.ts" || rows[2].ExtractorID != "chronicle-scan" {
		t.Errorf("rows[2] = %+v, want b.ts/chronicle-scan", rows[2])
	}

	if all, err := s.ListCoverage(""); err != nil || len(all) != 4 {
		t.Fatalf("ListCoverage(\"\") = %d rows err=%v, want 4", len(all), err)
	}

	// Re-observing the SAME (domain, file, extractor) updates in place.
	if err := s.UpsertCoverage(CoverageRow{
		DomainKey: "d", FilePath: "a.ts", ExtractorID: "chronicle-scan",
		ExtractorVersion: "fact-schema@1", Status: "extracted", BlobSHA: "blob-a-scan-v2",
	}); err != nil {
		t.Fatalf("UpsertCoverage re-observe: %v", err)
	}
	rows, err = s.ListCoverage("d")
	if err != nil || len(rows) != 3 {
		t.Fatalf("re-upsert must update in place, got %d rows err=%v: %+v", len(rows), err, rows)
	}
	if rows[0].BlobSHA != "blob-a-scan-v2" {
		t.Errorf("re-upsert blob = %q, want blob-a-scan-v2", rows[0].BlobSHA)
	}

	// ClearCoverageBlob blanks the blob for EVERY extractor on that file
	// (no extractor_id param) but keeps the row and its status, and must not
	// touch a different file.
	if err := s.ClearCoverageBlob("d", "a.ts"); err != nil {
		t.Fatalf("ClearCoverageBlob: %v", err)
	}
	rows, err = s.ListCoverage("d")
	if err != nil {
		t.Fatalf("ListCoverage after clear: %v", err)
	}
	for _, r := range rows {
		if r.FilePath != "a.ts" {
			continue
		}
		if r.BlobSHA != "" {
			t.Errorf("ClearCoverageBlob left a blob on %+v", r)
		}
		if r.Status != "extracted" {
			t.Errorf("ClearCoverageBlob must not touch status: %+v", r)
		}
	}
	for _, r := range rows {
		if r.FilePath == "b.ts" && r.BlobSHA != "blob-b-1" {
			t.Errorf("ClearCoverageBlob touched an unrelated file: %+v", r)
		}
	}
}
