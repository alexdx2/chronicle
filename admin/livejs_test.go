package admin

import (
	"os/exec"
	"testing"
)

// The dashboard's live client is plain JavaScript; its logic runs under
// node's built-in test runner. Skips only when node is not installed.
func TestLiveClientJS(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not on PATH — admin/jstest needs node ≥ 18")
	}
	out, err := exec.Command(node, "--test", "jstest/live-client.test.mjs").CombinedOutput()
	if err != nil {
		t.Fatalf("node --test: %v\n%s", err, out)
	}
}
