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
