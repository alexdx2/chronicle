package admin

import (
	"os/exec"
	"path/filepath"
	"testing"
)

// The dashboard's live client is plain JavaScript; its logic runs under
// node's built-in test runner. Skips only when node is not installed. Runs
// every jstest/*.test.mjs file, not just one, so a new suite (e.g.
// marks-render.test.mjs) is picked up without touching this wrapper again.
func TestLiveClientJS(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not on PATH — admin/jstest needs node ≥ 18")
	}
	files, err := filepath.Glob("jstest/*.test.mjs")
	if err != nil {
		t.Fatalf("glob jstest/*.test.mjs: %v", err)
	}
	if len(files) == 0 {
		t.Fatal("no jstest/*.test.mjs files found")
	}
	args := append([]string{"--test"}, files...)
	out, err := exec.Command(node, args...).CombinedOutput()
	if err != nil {
		t.Fatalf("node --test: %v\n%s", err, out)
	}
}
