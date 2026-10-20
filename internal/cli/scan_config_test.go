package cli

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// runScanConfig runs `chronicle scan-config <args>` inside root and returns
// what it printed to stdout.
func runScanConfig(t *testing.T, root string, stdin string, args ...string) (string, error) {
	t.Helper()
	t.Chdir(root)
	oldOut, oldIn := os.Stdout, os.Stdin
	defer func() { os.Stdout, os.Stdin = oldOut, oldIn }()
	ro, wo, _ := os.Pipe()
	os.Stdout = wo
	ri, wi, _ := os.Pipe()
	os.Stdin = ri
	go func() { _, _ = wi.WriteString(stdin); wi.Close() }()
	cmd := newScanConfigCmd()
	cmd.SetArgs(args)
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	err := cmd.Execute()
	wo.Close()
	var buf bytes.Buffer
	_, _ = io.Copy(&buf, ro)
	return buf.String(), err
}

func scanConfigRepo(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "app.py"), []byte("import mybus\nbus.announce('orders.v1')\nbus.announce('refunds.v1')\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

const cliCandidate = "channels:\n  - id: mybus\n    patterns: [{call: 'bus.announce({name}', side: produce}]\n"

func TestScanConfigCmd_Workflow(t *testing.T) {
	root := scanConfigRepo(t)

	out, err := runScanConfig(t, root, "", "get")
	var got struct {
		Exists bool     `json:"exists"`
		Rules  []string `json:"rules"`
	}
	if err != nil || json.Unmarshal([]byte(out), &got) != nil || got.Exists || len(got.Rules) == 0 {
		t.Fatalf("get: %q %v", out, err)
	}

	// validate: stdin and --file, valid and invalid (invalid exits non-zero)
	if out, err = runScanConfig(t, root, cliCandidate, "validate"); err != nil || !strings.Contains(out, `"valid": true`) {
		t.Fatalf("validate stdin: %q %v", out, err)
	}
	f := filepath.Join(t.TempDir(), "cand.yaml")
	_ = os.WriteFile(f, []byte(cliCandidate), 0o644)
	if out, err = runScanConfig(t, root, "", "validate", "--file", f); err != nil || !strings.Contains(out, `"valid": true`) {
		t.Fatalf("validate file: %q %v", out, err)
	}
	if out, err = runScanConfig(t, root, "channels: [{id: Bad}]", "validate"); err == nil || !strings.Contains(out, `"valid": false`) {
		t.Fatalf("validate bad must fail: %q %v", out, err)
	}
	if _, err = runScanConfig(t, root, "", "validate", "--file", filepath.Join(t.TempDir(), "missing.yaml")); err == nil {
		t.Fatal("missing candidate file must fail")
	}

	// dry-run reports facts and writes nothing
	out, err = runScanConfig(t, root, cliCandidate, "dry-run")
	if err != nil || !strings.Contains(out, "topic:orders.v1#produce") {
		t.Fatalf("dry-run: %q %v", out, err)
	}
	if _, serr := os.Stat(filepath.Join(root, ".depbot", "scan.yaml")); serr == nil {
		t.Fatal("dry-run wrote the project file")
	}

	// save, then explain sees the rule
	if out, err = runScanConfig(t, root, cliCandidate, "save"); err != nil || !strings.Contains(out, `"saved": true`) {
		t.Fatalf("save: %q %v", out, err)
	}
	if b, _ := os.ReadFile(filepath.Join(root, ".depbot", "scan.yaml")); string(b) != cliCandidate {
		t.Fatalf("saved file: %q", b)
	}
	if out, err = runScanConfig(t, root, "", "explain", "app.py"); err != nil || !strings.Contains(out, "orders.v1") {
		t.Fatalf("explain: %q %v", out, err)
	}
	if _, err = runScanConfig(t, root, "", "explain"); err == nil {
		t.Fatal("explain without a file must fail")
	}

	// the loss guard: dropping the rule is refused (non-zero), accepted with the flag
	if out, err = runScanConfig(t, root, "version: 1\n", "save"); err == nil || !strings.Contains(out, "accept-removed") {
		t.Fatalf("loss guard: %q %v", out, err)
	}
	if b, _ := os.ReadFile(filepath.Join(root, ".depbot", "scan.yaml")); string(b) != cliCandidate {
		t.Fatal("a refused save changed the file")
	}
	if out, err = runScanConfig(t, root, "version: 1\n", "save", "--accept-removed"); err != nil || !strings.Contains(out, `"saved": true`) {
		t.Fatalf("accepted save: %q %v", out, err)
	}
}
