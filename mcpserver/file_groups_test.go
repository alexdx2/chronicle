package mcpserver

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	mcplib "github.com/mark3labs/mcp-go/mcp"
)

// setupFileGroupsRepo creates a temp git repo with 5 real directories (one
// file each) plus a node_modules directory that must never surface in
// chronicle_file_groups output, then chdirs the test process into it so
// graph.ProjectRoot() resolves there (same pattern as
// commit_ast_merge_test.go's repoRootFromWD + os.Chdir).
func setupFileGroupsRepo(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	dirNames := []string{"dirA", "dirB", "dirC", "dirD", "dirE", "node_modules/some-pkg"}
	for _, d := range dirNames {
		if err := os.MkdirAll(filepath.Join(dir, d), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", d, err)
		}
	}
	files := map[string]string{
		"dirA/a.go":                      "package a\n",
		"dirB/b.go":                      "package b\n",
		"dirC/c.go":                      "package c\n",
		"dirD/d.go":                      "package d\n",
		"dirE/e.go":                      "package e\n",
		"node_modules/some-pkg/index.js": "module.exports = {}\n",
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	runGit := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	runGit("init", "-q")
	runGit("-c", "user.email=test@test.com", "-c", "user.name=test", "add", "-A")
	runGit("-c", "user.email=test@test.com", "-c", "user.name=test", "commit", "-q", "-m", "init")

	orig, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(orig) })
}

func callFileGroups(t *testing.T, args map[string]any) map[string]any {
	t.Helper()
	h := fileGroupsHandler(nil)
	res, err := h(context.Background(), makeRevisionRequest(args))
	if err != nil {
		t.Fatalf("handler: %v", err)
	}
	if res.IsError {
		t.Fatalf("handler returned error: %v", res.Content)
	}
	text, ok := res.Content[0].(mcplib.TextContent)
	if !ok {
		t.Fatalf("expected TextContent, got %T", res.Content[0])
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(text.Text), &out); err != nil {
		t.Fatalf("unmarshal result: %v\nraw: %s", err, text.Text)
	}
	return out
}

// TestFileGroups_CursorPagination is the Task 9 RED/GREEN test: 5 dirs with
// limit=2 must paginate into 3 stable pages (2, 2, 1) with an empty
// next_cursor on the last page, and every directory must appear exactly
// once across the pages (cursor advances, no gaps, no repeats).
func TestFileGroups_CursorPagination(t *testing.T) {
	setupFileGroupsRepo(t)

	var allPaths []string
	cursor := ""
	pages := 0
	for {
		pages++
		if pages > 10 {
			t.Fatalf("too many pages — pagination did not terminate (paths so far: %v)", allPaths)
		}
		out := callFileGroups(t, map[string]any{"cursor": cursor, "limit": float64(2)})
		tree, ok := out["tree"].([]any)
		if !ok {
			t.Fatalf("tree is not an array: %#v", out["tree"])
		}
		for _, entry := range tree {
			m, ok := entry.(map[string]any)
			if !ok {
				t.Fatalf("tree entry is not an object: %#v", entry)
			}
			p, _ := m["path"].(string)
			allPaths = append(allPaths, p)
		}
		nc, _ := out["next_cursor"].(string)
		if nc != "" && nc == cursor {
			t.Fatalf("cursor did not advance: %q", nc)
		}
		cursor = nc
		if cursor == "" {
			break
		}
	}

	if pages != 3 {
		t.Errorf("expected 3 pages for 5 dirs / limit 2, got %d (paths=%v)", pages, allPaths)
	}
	if len(allPaths) != 5 {
		t.Errorf("expected 5 total directories across pages, got %d: %v", len(allPaths), allPaths)
	}
	seen := map[string]bool{}
	for _, p := range allPaths {
		if seen[p] {
			t.Errorf("directory %q returned more than once across pages", p)
		}
		seen[p] = true
		if strings.Contains(p, "node_modules") {
			t.Errorf("node_modules leaked into file_groups output: %s", p)
		}
	}
}

// TestFileGroups_ExcludesNodeModules checks the tool description's own
// promise: alwaysExclude directories (node_modules chief among them) never
// appear, either as a tree entry or inside another entry's file list, and
// total_dirs reflects the filtered count.
func TestFileGroups_ExcludesNodeModules(t *testing.T) {
	setupFileGroupsRepo(t)

	out := callFileGroups(t, map[string]any{"limit": float64(1000)})
	tree, ok := out["tree"].([]any)
	if !ok {
		t.Fatalf("tree is not an array: %#v", out["tree"])
	}
	for _, entry := range tree {
		m := entry.(map[string]any)
		path, _ := m["path"].(string)
		if strings.Contains(path, "node_modules") {
			t.Fatalf("node_modules directory present in file_groups tree: %s", path)
		}
		files, _ := m["files"].([]any)
		for _, f := range files {
			if s, ok := f.(string); ok && strings.Contains(s, "node_modules") {
				t.Fatalf("node_modules file present in file_groups: %v", f)
			}
		}
	}

	totalDirs, ok := out["total_dirs"].(float64)
	if !ok {
		t.Fatalf("total_dirs missing or not a number: %#v", out["total_dirs"])
	}
	if int(totalDirs) != 5 {
		t.Errorf("expected total_dirs=5 (node_modules excluded), got %v", out["total_dirs"])
	}
	if nc, _ := out["next_cursor"].(string); nc != "" {
		t.Errorf("expected empty next_cursor when limit covers everything, got %q", nc)
	}
}
