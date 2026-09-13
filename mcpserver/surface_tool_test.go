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

	"github.com/alexdx2/chronicle-core/graph"
	"github.com/alexdx2/chronicle-core/paths"
)

// seedSurfaceGraph gives the graph exactly the code-side nodes the mini
// surface extract points at — the two Shop fields, the mutation its controls
// write through, and the page endpoint that serves its screen.
func seedSurfaceGraph(t *testing.T) *graph.Graph {
	t.Helper()
	g := newLabTestGraph(t)
	revID, err := g.Store().CreateRevision("mini", "", "seed0000", "manual", "full", "{}")
	if err != nil {
		t.Fatalf("CreateRevision: %v", err)
	}
	res, err := g.ImportAll(graph.ImportPayload{Nodes: []graph.ImportNode{
		{NodeKey: "data:field:mini:shop/quiet-from-hour", Layer: "data", NodeType: "field", DomainKey: "mini", Name: "quietFromHour"},
		{NodeKey: "data:field:mini:shop/sms-sender-id", Layer: "data", NodeType: "field", DomainKey: "mini", Name: "smsSenderId"},
		{NodeKey: "contract:endpoint:mini:mutation:/saveshopsettings", Layer: "contract", NodeType: "endpoint", DomainKey: "mini", Name: "saveShopSettings"},
		{NodeKey: "contract:endpoint:mini:get:/panel/ustawienia", Layer: "contract", NodeType: "endpoint", DomainKey: "mini", Name: "GET /panel/ustawienia"},
	}}, revID)
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	if len(res.Rejected) > 0 {
		t.Fatalf("seed rejected: %+v", res.Rejected)
	}
	return g
}

// surfaceRepo builds a real one-commit git repo, makes it the project root for
// the duration of the test, and drops a copy of the mini extract in it stamped
// with that repo's HEAD. The tool's git ancestry check then runs for real
// instead of being skipped.
func surfaceRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	git := func(args ...string) string {
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	git("init", "-q", "-b", "main")
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git("add", ".")
	git("commit", "-q", "-m", "a")
	head := git("rev-parse", "HEAD")

	raw, err := os.ReadFile("../testdata/surface/mini.surface.json")
	if err != nil {
		t.Fatal(err)
	}
	doc := strings.Replace(string(raw), "0000000000000000000000000000000000000001", head, 1)
	path := filepath.Join(dir, "mini.surface.json")
	if err := os.WriteFile(path, []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}

	// Production sets both from the same --project flag: the graph root and
	// the directory git is measured in.
	paths.SetProjectRoot(dir)
	paths.SetGitDir(dir)
	t.Cleanup(func() { paths.SetProjectRoot(""); paths.SetGitDir("") })
	return path
}

func callSurfaceTool(t *testing.T, g *graph.Graph, args map[string]any) (map[string]any, bool) {
	t.Helper()
	var req mcplib.CallToolRequest
	req.Params.Arguments = args
	res, err := importSurfaceHandler(g)(context.Background(), req)
	if err != nil {
		t.Fatalf("handler: %v", err)
	}
	text, ok := res.Content[0].(mcplib.TextContent)
	if !ok {
		t.Fatalf("first content block is not text: %#v", res.Content[0])
	}
	if res.IsError {
		// Error results carry a message, not JSON.
		return map[string]any{"error": text.Text}, true
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(text.Text), &out); err != nil {
		t.Fatalf("unmarshal %q: %v", text.Text, err)
	}
	return out, false
}

func TestImportSurfaceTool(t *testing.T) {
	g := seedSurfaceGraph(t)
	path := surfaceRepo(t)

	out, isErr := callSurfaceTool(t, g, map[string]any{"path": path, "domain": "mini"})
	if isErr {
		t.Fatalf("first call errored: %v", out)
	}
	if rev, _ := out["revision_id"].(float64); rev <= 0 {
		t.Fatalf("revision_id = %v", out["revision_id"])
	}
	if out["already_imported"] != false {
		t.Fatalf("already_imported = %v", out["already_imported"])
	}
	if n, _ := out["nodes"].(float64); n != 5 {
		t.Fatalf("nodes = %v (%v)", out["nodes"], out)
	}

	out2, isErr := callSurfaceTool(t, g, map[string]any{"path": path, "domain": "mini"})
	if isErr {
		t.Fatalf("second call errored: %v", out2)
	}
	if out2["already_imported"] != true {
		t.Fatalf("second call already_imported = %v", out2["already_imported"])
	}
}

// TestImportSurfaceToolDefaultsDomain: the store holds one domain, so the
// caller need not name it.
func TestImportSurfaceToolDefaultsDomain(t *testing.T) {
	g := seedSurfaceGraph(t)
	path := surfaceRepo(t)
	out, isErr := callSurfaceTool(t, g, map[string]any{"path": path})
	if isErr {
		t.Fatalf("errored: %v", out)
	}
	if n, _ := out["nodes"].(float64); n != 5 {
		t.Fatalf("nodes = %v", out["nodes"])
	}
}

// TestImportSurfaceToolRefusesUnknownCommit: the extract names a commit this
// repo has never had.
func TestImportSurfaceToolRefusesUnknownCommit(t *testing.T) {
	g := seedSurfaceGraph(t)
	dir := filepath.Dir(surfaceRepo(t))
	stale := filepath.Join(dir, "stale.surface.json")
	raw, err := os.ReadFile("../testdata/surface/mini.surface.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(stale, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	out, isErr := callSurfaceTool(t, g, map[string]any{"path": stale, "domain": "mini"})
	if !isErr {
		t.Fatalf("unknown commit must be an error result: %v", out)
	}
}

func TestImportSurfaceToolRequiresPath(t *testing.T) {
	g := seedSurfaceGraph(t)
	out, isErr := callSurfaceTool(t, g, map[string]any{})
	if !isErr {
		t.Fatalf("missing path must be an error result: %v", out)
	}
}

func TestImportSurfaceToolRegistered(t *testing.T) {
	g := seedSurfaceGraph(t)
	found := false
	for _, st := range Tools(g) {
		if st.Tool.Name == "chronicle_import_surface" {
			found = true
		}
	}
	if !found {
		t.Fatal("chronicle_import_surface missing from the core toolset")
	}
	if _, ok := UserCommands["surface"]; !ok {
		t.Fatal("the surface command is missing from the help listing")
	}
	if _, ok := CommandInstructions["surface"]; !ok {
		t.Fatal("the surface command has no instructions")
	}
	if !strings.Contains(CommandInstructions["help"], "/chronicle-surface") {
		t.Fatal("the help listing does not mention the surface command")
	}
}

// A refusal an agent can act on differently from a crash: the CLI has said so
// with exit code 2 since the importer shipped, and the MCP door said it only
// in prose. The kind is a field now, so a caller can branch on it.
func TestImportSurfaceRefusalCarriesItsKind(t *testing.T) {
	g := seedSurfaceGraph(t)
	path := surfaceRepo(t)

	// Re-point the extract at a commit this repo has never had.
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	doc["commit"] = "0000000000000000000000000000000000000009"
	out, _ := json.Marshal(doc)
	if err := os.WriteFile(path, out, 0o644); err != nil {
		t.Fatal(err)
	}

	res, isErr := callSurfaceTool(t, g, map[string]any{"path": path, "domain": "mini"})
	if !isErr {
		t.Fatalf("an unknown commit must be refused: %v", res)
	}
	text, _ := res["error"].(string)
	var payload map[string]string
	if err := json.Unmarshal([]byte(text), &payload); err != nil {
		t.Fatalf("a refusal must be JSON a caller can branch on, got %q", text)
	}
	if payload["refusal"] == "" {
		t.Errorf("refusal kind missing from %v", payload)
	}
}

// The happy path must stay exactly what it was: one JSON object, no wrapper.
func TestImportSurfaceRefusalKindDoesNotChangeSuccess(t *testing.T) {
	g := seedSurfaceGraph(t)
	path := surfaceRepo(t)
	out, isErr := callSurfaceTool(t, g, map[string]any{"path": path, "domain": "mini"})
	if isErr {
		t.Fatalf("errored: %v", out)
	}
	if _, ok := out["refusal"]; ok {
		t.Errorf("a successful import must not carry a refusal field: %v", out)
	}
}
