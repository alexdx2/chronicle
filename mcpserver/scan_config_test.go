package mcpserver

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
)

func callScanConfig(t *testing.T, args map[string]any) (string, bool) {
	t.Helper()
	req := mcp.CallToolRequest{}
	req.Params.Name = "chronicle_scan_config"
	req.Params.Arguments = args
	res, err := scanConfigHandler(nil)(context.Background(), req)
	if err != nil {
		t.Fatalf("handler returned a Go error (tool errors travel in the result): %v", err)
	}
	txt := ""
	if len(res.Content) > 0 {
		if c, ok := res.Content[0].(mcp.TextContent); ok {
			txt = c.Text
		}
	}
	return txt, res.IsError
}

type fakeScanConfig struct {
	calls   []string
	saveErr error
}

func (f *fakeScanConfig) backend() *ScanConfigBackend {
	return &ScanConfigBackend{
		Get:    func(root string) any { f.calls = append(f.calls, "get"); return map[string]any{"rules": []string{"a"}} },
		DryRun: func(root, y string) any { f.calls = append(f.calls, "dry:"+y); return map[string]any{"valid": true} },
		Valid: func(root, y string) (bool, string) {
			f.calls = append(f.calls, "valid:"+y)
			return y == "ok", "bad: " + y
		},
		Explain: func(root, file string) any { f.calls = append(f.calls, "explain:"+file); return []string{file} },
		Save: func(root, y string, acc bool) (any, error) {
			f.calls = append(f.calls, "save:"+y)
			if acc {
				f.calls = append(f.calls, "accepted")
			}
			return map[string]any{"saved": f.saveErr == nil}, f.saveErr
		},
	}
}

func TestScanConfigTool_Dispatch(t *testing.T) {
	old := ScanConfig
	defer func() { ScanConfig = old }()
	f := &fakeScanConfig{}
	ScanConfig = f.backend()

	var m map[string]any
	txt, isErr := callScanConfig(t, map[string]any{"action": "get"})
	if isErr || json.Unmarshal([]byte(txt), &m) != nil || m["rules"] == nil {
		t.Fatalf("get: %q err=%v", txt, isErr)
	}
	txt, _ = callScanConfig(t, map[string]any{"action": "validate", "yaml": "ok"})
	if err := json.Unmarshal([]byte(txt), &m); err != nil || m["valid"] != true {
		t.Fatalf("validate ok: %q", txt)
	}
	txt, _ = callScanConfig(t, map[string]any{"action": "validate", "yaml": "nope"})
	if err := json.Unmarshal([]byte(txt), &m); err != nil || m["valid"] != false || m["errors"] != "bad: nope" {
		t.Fatalf("validate bad: %q", txt)
	}
	if _, isErr := callScanConfig(t, map[string]any{"action": "dry_run", "yaml": "y1"}); isErr {
		t.Fatal("dry_run")
	}
	if txt, isErr := callScanConfig(t, map[string]any{"action": "explain", "file": "a/b.py"}); isErr || txt != `["a/b.py"]` {
		t.Fatalf("explain: %q %v", txt, isErr)
	}
	if _, isErr := callScanConfig(t, map[string]any{"action": "save", "yaml": "y2", "accept_removed": true}); isErr {
		t.Fatal("save")
	}
	want := []string{"get", "valid:ok", "valid:nope", "dry:y1", "explain:a/b.py", "save:y2", "accepted"}
	if len(f.calls) != len(want) {
		t.Fatalf("calls %v, want %v", f.calls, want)
	}
	for i := range want {
		if f.calls[i] != want[i] {
			t.Fatalf("calls %v, want %v", f.calls, want)
		}
	}
}

func TestScanConfigTool_Errors(t *testing.T) {
	old := ScanConfig
	defer func() { ScanConfig = old }()

	ScanConfig = nil
	if txt, isErr := callScanConfig(t, map[string]any{"action": "get"}); !isErr || txt == "" {
		t.Fatalf("no backend: %q %v", txt, isErr)
	}

	f := &fakeScanConfig{saveErr: errors.New("disk full")}
	ScanConfig = f.backend()
	for name, args := range map[string]map[string]any{
		"unknown action":    {"action": "delete"},
		"missing action":    {},
		"explain no file":   {"action": "explain"},
		"save write failed": {"action": "save", "yaml": "y"},
	} {
		if txt, isErr := callScanConfig(t, args); !isErr || txt == "" {
			t.Errorf("%s: want a tool error, got %q (isError=%v)", name, txt, isErr)
		}
	}
	// accept_removed of the wrong type is not truthy
	f.saveErr = nil
	f.calls = nil
	callScanConfig(t, map[string]any{"action": "save", "yaml": "y", "accept_removed": "true"})
	for _, c := range f.calls {
		if c == "accepted" {
			t.Fatal(`accept_removed:"true" (a string) must not count as acceptance`)
		}
	}
}

func TestScanConfigTool_Registered(t *testing.T) {
	var found bool
	for _, tl := range Tools(nil) {
		if tl.Tool.Name == "chronicle_scan_config" {
			found = true
		}
	}
	if !found {
		t.Fatal("chronicle_scan_config is not in the tool registry")
	}
}
