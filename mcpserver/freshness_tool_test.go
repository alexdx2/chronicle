package mcpserver

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alexdx2/chronicle-core/freshness"
	"github.com/alexdx2/chronicle-core/graph"
	"github.com/alexdx2/chronicle-core/registry"
	"github.com/alexdx2/chronicle-core/store"
	mcplib "github.com/mark3labs/mcp-go/mcp"
)

// newTestGraphInGitRepo builds a temp git repo with one commit and a store
// under <dir>/.depbot — the same shape a real project has.
func newTestGraphInGitRepo(t *testing.T) (*graph.Graph, string) {
	t.Helper()
	dir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir,
			"-c", "user.email=t@t", "-c", "user.name=t"}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q", "-b", "main")
	if err := os.WriteFile(filepath.Join(dir, "a.ts"), []byte("export const a = 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", "a.ts")
	run("commit", "-q", "-m", "a")

	s, err := store.Open(filepath.Join(dir, ".depbot", "chronicle.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	reg, err := registry.LoadDefaults()
	if err != nil {
		t.Fatalf("LoadDefaults: %v", err)
	}
	return graph.New(s, reg), dir
}

func gitHead(t *testing.T, dir string) string {
	t.Helper()
	out, err := exec.Command("git", "-C", dir, "rev-parse", "HEAD").Output()
	if err != nil {
		t.Fatalf("rev-parse: %v", err)
	}
	return strings.TrimSpace(string(out))
}

func TestFreshnessToolReturnsReport(t *testing.T) {
	g, dir := newTestGraphInGitRepo(t)
	sha := gitHead(t, dir)
	if _, err := g.Store().CreateRevision("d", "", sha, "manual", "full", "{}"); err != nil {
		t.Fatal(err)
	}

	var req mcplib.CallToolRequest
	req.Params.Arguments = map[string]any{}
	res, err := freshnessHandler(g, dir)(context.Background(), req)
	if err != nil || res.IsError {
		t.Fatalf("err %v res %+v", err, res)
	}
	var rep freshness.Report
	if err := json.Unmarshal([]byte(res.Content[0].(mcplib.TextContent).Text), &rep); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if rep.Status != "fresh" {
		t.Fatalf("want fresh, got %+v", rep)
	}
	if rep.Head == nil || rep.Head.SHA != sha {
		t.Fatalf("head not filled: %+v", rep.Head)
	}
	if rep.Message == "" || !strings.Contains(rep.Line(), "current") {
		t.Fatalf("line: %q", rep.Line())
	}
}

func TestFreshnessToolRepoLabelOverride(t *testing.T) {
	g, dir := newTestGraphInGitRepo(t)
	sha := gitHead(t, dir)
	if _, err := g.Store().CreateRevision("d", "", sha, "manual", "full", "{}"); err != nil {
		t.Fatal(err)
	}
	var req mcplib.CallToolRequest
	req.Params.Arguments = map[string]any{"repo": "auto"}
	res, err := freshnessHandler(g, dir)(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	var rep freshness.Report
	json.Unmarshal([]byte(res.Content[0].(mcplib.TextContent).Text), &rep) //nolint:errcheck
	if rep.Repo != "auto" {
		t.Fatalf("repo label = %q, want auto", rep.Repo)
	}
}

// FreshnessReport is what the admin dashboard and pro call; it must default
// the repo label from the directory and never need a domain.
func TestFreshnessReportExportedEntryPoint(t *testing.T) {
	g, dir := newTestGraphInGitRepo(t)
	if _, err := g.Store().CreateRevision("d", "", gitHead(t, dir), "manual", "full", "{}"); err != nil {
		t.Fatal(err)
	}
	rep, err := FreshnessReport(g, dir)
	if err != nil {
		t.Fatalf("FreshnessReport: %v", err)
	}
	if rep.Status != "fresh" || rep.Domain != "d" {
		t.Fatalf("got %+v", rep)
	}
	if rep.Repo != filepath.Base(dir) {
		t.Fatalf("repo label = %q, want %q", rep.Repo, filepath.Base(dir))
	}
}

// A store with no revisions is the first-run case: no scan, no error.
func TestFreshnessToolOnEmptyStore(t *testing.T) {
	g, dir := newTestGraphInGitRepo(t)
	rep, err := FreshnessReport(g, dir)
	if err != nil {
		t.Fatalf("FreshnessReport: %v", err)
	}
	if rep.Status != "empty" {
		t.Fatalf("want empty, got %q", rep.Status)
	}
}

// The tool must be registered — a handler nothing serves is dead code.
func TestFreshnessToolRegistered(t *testing.T) {
	g, _ := newTestGraphInGitRepo(t)
	found := false
	for _, st := range Tools(g) {
		if st.Tool.Name == "chronicle_freshness" {
			found = true
		}
	}
	if !found {
		t.Fatal("chronicle_freshness missing from Tools()")
	}
}
