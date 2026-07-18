package store

import (
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
