package mcpserver

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/alexdx2/chronicle-core/gitutil"
	"github.com/alexdx2/chronicle-core/paths"
	"github.com/alexdx2/chronicle-core/store"
)

// TestFileExtracted_RecordsCoverageWithBlob checks the single-file report path
// (chronicle_file_extracted / fileExtractedHandler): after a report,
// file_coverage carries a row for (domain, file, "chronicle-scan") whose blob
// matches the file's actual on-disk content at report time.
func TestFileExtracted_RecordsCoverageWithBlob(t *testing.T) {
	root := t.TempDir()
	content := []byte("export class OrderService {}\n")
	if err := os.WriteFile(filepath.Join(root, "order-service.ts"), content, 0o644); err != nil {
		t.Fatal(err)
	}
	paths.SetProjectRoot(root)
	t.Cleanup(func() { paths.SetProjectRoot("") })

	g := newSearchTestGraph(t)
	revID, _ := g.Store().CreateRevision("orders", "", "sha-single", "manual", "full", "{}")

	out := callToolText(t, fileExtractedHandler(g), map[string]any{
		"file_path": "order-service.ts", "status": "extracted",
		"revision_id": float64(revID), "domain": "orders", "facts": "[]",
	})
	var res struct {
		FilePath string `json:"file_path"`
		Status   string `json:"status"`
	}
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("bad response: %v\n%s", err, out)
	}
	if res.FilePath != "order-service.ts" || res.Status != "extracted" {
		t.Fatalf("unexpected response: %s", out)
	}

	wantBlob, err := gitutil.FileBlobSHA(root, "order-service.ts")
	if err != nil {
		t.Fatalf("FileBlobSHA: %v", err)
	}
	if wantBlob == "" {
		t.Fatal("test setup: computed want-blob is empty")
	}

	found := coverageRowFor(t, g.Store(), "orders", "order-service.ts", "chronicle-scan")
	if found.BlobSHA != wantBlob {
		t.Errorf("coverage blob = %q, want %q (file's actual on-disk blob)", found.BlobSHA, wantBlob)
	}
	if found.Status != "extracted" {
		t.Errorf("coverage status = %q, want extracted", found.Status)
	}
	wantVersion := fmt.Sprintf("fact-schema@%d", store.FactSchemaVersion)
	if found.ExtractorVersion != wantVersion {
		t.Errorf("coverage extractor_version = %q, want %q", found.ExtractorVersion, wantVersion)
	}
}

// TestFileExtractedBatch_RecordsCoverageWithBlob is the same check for the
// batch report path (chronicle_file_extracted_batch / commitOneFileExtracted)
// — a separate, independently-implemented handler from the single-file one
// (see task-2-report.md), so it needs its own proof the wiring landed.
func TestFileExtractedBatch_RecordsCoverageWithBlob(t *testing.T) {
	root := t.TempDir()
	content := []byte("export class PaymentService {}\n")
	if err := os.WriteFile(filepath.Join(root, "payment-service.ts"), content, 0o644); err != nil {
		t.Fatal(err)
	}
	paths.SetProjectRoot(root)
	t.Cleanup(func() { paths.SetProjectRoot("") })

	g := newSearchTestGraph(t)
	revID, _ := g.Store().CreateRevision("orders", "", "sha-batch", "manual", "full", "{}")

	items := `[{"file_path":"payment-service.ts","status":"extracted","from_type":"","facts":[],"obligation_id":0}]`
	out := callToolText(t, fileExtractedBatchHandler(g), map[string]any{
		"domain": "orders", "revision_id": float64(revID), "items": items,
	})
	var res struct {
		Committed int      `json:"committed"`
		Failures  []string `json:"failures"`
	}
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("bad response: %v\n%s", err, out)
	}
	if res.Committed != 1 || len(res.Failures) != 0 {
		t.Fatalf("batch commit failed: %s", out)
	}

	wantBlob, err := gitutil.FileBlobSHA(root, "payment-service.ts")
	if err != nil {
		t.Fatalf("FileBlobSHA: %v", err)
	}

	found := coverageRowFor(t, g.Store(), "orders", "payment-service.ts", "chronicle-scan")
	if found.BlobSHA != wantBlob {
		t.Errorf("coverage blob = %q, want %q (file's actual on-disk blob)", found.BlobSHA, wantBlob)
	}
}

// coverageRowFor finds one file_coverage row or fails the test.
func coverageRowFor(t *testing.T, s *store.Store, domain, filePath, extractorID string) store.CoverageRow {
	t.Helper()
	rows, err := s.ListCoverage(domain)
	if err != nil {
		t.Fatalf("ListCoverage: %v", err)
	}
	for _, r := range rows {
		if r.FilePath == filePath && r.ExtractorID == extractorID {
			return r
		}
	}
	t.Fatalf("no file_coverage row for %s/%s/%s: %+v", domain, filePath, extractorID, rows)
	return store.CoverageRow{}
}
