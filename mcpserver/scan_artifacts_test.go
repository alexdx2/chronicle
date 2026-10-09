package mcpserver

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/alexdx2/chronicle-core/graph"
)

// CommitScanOutbox is the shared commit path for the MCP handler and the
// headless pipeline: it must work from a bare graph + chronicle dir, with no
// request context and no paths globals.
func TestCommitScanOutbox_Direct(t *testing.T) {
	dir := t.TempDir()
	g := newTestGraphAt(t, dir)
	st := g.Store()

	rev, err := st.CreateRevision("d", "", "abc", "manual", "full", "{}")
	if err != nil {
		t.Fatalf("CreateRevision: %v", err)
	}
	if _, err := st.CreateScanRun(rev, "d"); err != nil {
		t.Fatalf("CreateScanRun: %v", err)
	}
	obID, err := st.CreateObligation(rev, "d", "scan_file", "src/a.ts", "test")
	if err != nil {
		t.Fatalf("CreateObligation: %v", err)
	}

	chronicleDir := filepath.Join(dir, ".depbot")
	outboxDir := filepath.Join(chronicleDir, "scan-outbox", fmt.Sprintf("%d", rev))
	if err := os.MkdirAll(outboxDir, 0o755); err != nil {
		t.Fatalf("mkdir outbox: %v", err)
	}
	artifact := graph.OutboxArtifactPath(outboxDir, "src/a.ts")
	data, _ := json.Marshal(map[string]any{
		"file_path": "src/a.ts", "status": "extracted", "from_type": "provider",
		"facts": []any{}, "revision_id": rev, "domain": "d", "obligation_id": obID,
	})
	if err := os.WriteFile(artifact, data, 0o644); err != nil {
		t.Fatalf("write artifact: %v", err)
	}

	res, err := CommitScanOutbox(g, chronicleDir, "d", rev, true)
	if err != nil {
		t.Fatalf("CommitScanOutbox: %v", err)
	}
	if res.Imported != 1 || res.RowsWritten != 1 || res.Deduped != 0 || res.Errors != 0 {
		t.Errorf("result = %+v; want imported=1 rows_written=1 deduped=0 errors=0", res)
	}
	if res.Dir != outboxDir {
		t.Errorf("Dir = %q; want %q", res.Dir, outboxDir)
	}
	if _, err := os.Stat(artifact); !os.IsNotExist(err) {
		t.Errorf("artifact file should be removed after commit (stat err = %v)", err)
	}
	rows, err := st.ListUnresolvedExtractions(rev, "d")
	if err != nil || len(rows) != 1 || rows[0].FilePath != "src/a.ts" {
		t.Fatalf("extraction rows = %v, err %v; want one row for src/a.ts", rows, err)
	}
	if pending, _ := st.CountPendingObligations(rev, "scan_file"); pending != 0 {
		t.Errorf("scan_file obligations pending = %d; want 0", pending)
	}

	if _, err := CommitScanOutbox(g, chronicleDir, "d", rev+99, true); err == nil {
		t.Error("missing outbox dir should return an error")
	}
}
