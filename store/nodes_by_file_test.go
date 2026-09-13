package store

import (
	"fmt"
	"path/filepath"
	"testing"
)

// 2026-07-18 otopoint finding: some creation paths store node file_path
// WITHOUT the extension (api/src/services/payment.service) while git diffs
// carry the full path (payment.service.ts) — the review report undermapped
// scanned files. The lookup must match extension-stripped variants too.
func TestNodeKeysByFilePathsExtensionVariants(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(filepath.Join(dir, "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	if _, err := s.UpsertNode(NodeRow{
		NodeKey: "code:provider:api:api/src/services/payment.service",
		Layer:   "code", NodeType: "provider", DomainKey: "api",
		Name: "payment.service", FilePath: "api/src/services/payment.service",
		Status: "active", Confidence: 1, Freshness: 1, TrustScore: 1, Metadata: "{}",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.UpsertNode(NodeRow{
		NodeKey: "code:controller:api:api/src/controllers/auth.controller",
		Layer:   "code", NodeType: "controller", DomainKey: "api",
		Name: "auth.controller", FilePath: "api/src/controllers/auth.controller.ts",
		Status: "active", Confidence: 1, Freshness: 1, TrustScore: 1, Metadata: "{}",
	}); err != nil {
		t.Fatal(err)
	}

	got, err := s.NodeKeysByFilePaths([]string{
		"api/src/services/payment.service.ts", // node stored WITHOUT .ts
		"api/src/controllers/auth.controller.ts", // node stored WITH .ts
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got["api/src/services/payment.service.ts"]) == 0 {
		t.Errorf("extension-stripped node file_path not matched: %v", got)
	}
	if len(got["api/src/controllers/auth.controller.ts"]) == 0 {
		t.Errorf("exact file_path regression: %v", got)
	}
}

// KnownFilePaths answers "does the graph hold evidence for this file", for
// files of any language, and must survive a diff wider than SQLite's host
// parameter cap — which is exactly the diff a long-stale repo produces.
func TestKnownFilePathsChunksAndPreservesOrder(t *testing.T) {
	s := openTestStore(t)
	rev, err := s.CreateRevision("d", "", "aaa", "manual", "full", "{}")
	if err != nil {
		t.Fatal(err)
	}

	var asked []string
	for i := 0; i < 1500; i++ {
		asked = append(asked, fmt.Sprintf("src/f%04d.rb", i))
	}
	// Only three of them are known, spread across the chunk boundaries.
	wantKnown := []string{"src/f0001.rb", "src/f0700.rb", "src/f1400.rb"}
	for i, fp := range wantKnown {
		key := fmt.Sprintf("code:symbol:d:n%d", i)
		if _, err := s.UpsertNode(NodeRow{
			NodeKey: key, Layer: "code", NodeType: "symbol", DomainKey: "d",
			Name: key, FilePath: fp, Status: "active", Metadata: "{}",
		}); err != nil {
			t.Fatal(err)
		}
		nodeID, err := s.GetNodeIDByKey(key)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := s.AddEvidence(EvidenceRow{
			TargetKind: "node", NodeID: nodeID, SourceKind: "file", FilePath: fp,
			LineStart: 1, ExtractorID: "test", ExtractorVersion: "1",
			Confidence: 0.9, EvidenceStatus: "valid", EvidencePolarity: "positive",
			ValidFromRevisionID: rev, Metadata: "{}",
		}); err != nil {
			t.Fatal(err)
		}
	}

	got, err := s.KnownFilePaths(asked)
	if err != nil {
		t.Fatalf("KnownFilePaths: %v", err)
	}
	if len(got) != len(wantKnown) {
		t.Fatalf("got %v, want %v", got, wantKnown)
	}
	for i := range got {
		if got[i] != wantKnown[i] {
			t.Errorf("result must keep the caller's order: got %v, want %v", got, wantKnown)
			break
		}
	}
	if out, err := s.KnownFilePaths(nil); err != nil || out != nil {
		t.Errorf("no paths, no query: %v %v", out, err)
	}
}
