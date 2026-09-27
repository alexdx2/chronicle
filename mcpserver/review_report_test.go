package mcpserver

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alexdx2/chronicle-core/graph"
	"github.com/alexdx2/chronicle-core/paths"
	"github.com/alexdx2/chronicle-core/registry"
	"github.com/alexdx2/chronicle-core/snapshot"
	"github.com/alexdx2/chronicle-core/store"
	"github.com/alexdx2/chronicle-core/validate"
	mcplib "github.com/mark3labs/mcp-go/mcp"
)

const reviewTestSchemaV1 = `model Battle {
  id       String @id
  winnerId String
}
`

const reviewTestSchemaV2 = `model Battle {
  id String @id
}
`

// newReviewTestRepo builds a temp git repo (main branch) whose HEAD holds
// schema v1, with v2 (winnerId removed) in the working tree.
func newReviewTestRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t",
		)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-b", "main")
	if err := os.MkdirAll(filepath.Join(dir, "prisma"), 0o755); err != nil {
		t.Fatal(err)
	}
	schemaPath := filepath.Join(dir, "prisma", "schema.prisma")
	if err := os.WriteFile(schemaPath, []byte(reviewTestSchemaV1), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", ".")
	run("commit", "-m", "schema v1")
	if err := os.WriteFile(schemaPath, []byte(reviewTestSchemaV2), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

// TestReviewReportHandler is this file's original test, predating Task 14:
// a repo with NO committed .depbot/graph.jsonl, so this must still take the
// (unchanged) old BuildReviewReportFromGit path after Task 14's new
// engine/fallback branch is added — a working "# Chronicle Review Report"
// heading and field-level prisma diff prove the old path itself is untouched.
func TestReviewReportHandler(t *testing.T) {
	repo := newReviewTestRepo(t)
	paths.SetProjectRoot(repo)
	t.Cleanup(func() { paths.SetProjectRoot("") })

	dbDir := t.TempDir()
	s, err := store.Open(filepath.Join(dbDir, "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	reg, err := registry.LoadDefaults()
	if err != nil {
		t.Fatalf("LoadDefaults: %v", err)
	}
	g := graph.New(s, reg)
	revID, _ := s.CreateRevision("orders", "", "sha1", "manual", "full", "{}")

	if _, err := g.UpsertNode(validate.NodeInput{
		NodeKey: "data:model:orders:battle", Layer: "data", NodeType: "model",
		DomainKey: "orders", Name: "Battle", FilePath: "prisma/schema.prisma",
	}, revID); err != nil {
		t.Fatalf("UpsertNode: %v", err)
	}
	if _, err := g.UpsertNode(validate.NodeInput{
		NodeKey: "code:provider:billing:consumer", Layer: "code", NodeType: "provider",
		DomainKey: "billing", Name: "BillingConsumer",
	}, revID); err != nil {
		t.Fatalf("UpsertNode consumer: %v", err)
	}
	if _, err := g.UpsertEdge(validate.EdgeInput{
		FromNodeKey: "code:provider:billing:consumer", ToNodeKey: "data:model:orders:battle",
		EdgeType: "USES_MODEL", DerivationKind: "hard", FromLayer: "code", ToLayer: "data",
	}, revID); err != nil {
		t.Fatalf("UpsertEdge: %v", err)
	}

	md := callToolText(t, reviewReportHandler(g), map[string]any{"base": "HEAD", "domain": "orders"})

	for _, want := range []string{
		"# Chronicle Review Report",
		"data:model:orders:battle",
		"Battle.winnerId", // field-level row from the prisma diff
		"removed",
		"code:provider:billing:consumer", // cross-domain consumer → external section
		"## External services affected",
	} {
		if !strings.Contains(md, want) {
			t.Errorf("markdown missing %q\n---\n%s", want, md)
		}
	}
}

// --- Task 14: fixture helpers for the new engine / fallback split ---------
//
// Not shared with internal/cli's own review_test.go helpers of the same
// shape: mcpserver cannot import the internal/cli package (Go's own
// internal/ visibility rule), and per this codebase's established
// convention (see review/graph_test.go's own comment on this exact
// tradeoff) test helpers are duplicated across such a boundary rather than
// forced into a shared package neither task's brief asked for.

func gitRunMCP(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir, "-c", "user.email=t@t", "-c", "user.name=t"}, args...)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func gitCaptureMCP(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git %v: %v", args, err)
	}
	return strings.TrimSpace(string(out))
}

func writePlainFileMCP(t *testing.T, dir, relPath, content string) {
	t.Helper()
	full := filepath.Join(dir, relPath)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func commitAllMCP(t *testing.T, dir, msg string) string {
	t.Helper()
	gitRunMCP(t, dir, "add", "-A")
	gitRunMCP(t, dir, "commit", "-q", "-m", msg)
	return gitCaptureMCP(t, dir, "rev-parse", "HEAD")
}

// writeReviewGraphFixtureMCP encodes snap via the real snapshot.Encode (so
// the fixture is exactly what production code would write) and writes it at
// the .depbot/graph.jsonl convention review.SnapshotSource resolves.
func writeReviewGraphFixtureMCP(t *testing.T, dir string, snap *snapshot.Snapshot) {
	t.Helper()
	var buf bytes.Buffer
	if err := snapshot.Encode(&buf, snap); err != nil {
		t.Fatalf("snapshot.Encode: %v", err)
	}
	writePlainFileMCP(t, dir, ".depbot/graph.jsonl", buf.String())
}

func writeReviewManifestMCP(t *testing.T, dir string) {
	t.Helper()
	writePlainFileMCP(t, dir, ".depbot/chronicle.domain.yaml", ""+
		"domains:\n"+
		"  - name: tomandjerry\n"+
		"    scan:\n"+
		"      include:\n"+
		"        - \"zzz-nomatch/**\"\n")
}

// newTestGraphAt opens (creating if needed) a store+registry-backed
// *graph.Graph rooted at dir/.depbot/chronicle.db — the same directory the
// git fixture and any .depbot/graph.jsonl live in, matching how a real
// project has exactly one .depbot for both.
func newTestGraphAt(t *testing.T, dir string) *graph.Graph {
	t.Helper()
	s, err := store.Open(filepath.Join(dir, ".depbot", "chronicle.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	reg, err := registry.LoadDefaults()
	if err != nil {
		t.Fatalf("registry.LoadDefaults: %v", err)
	}
	return graph.New(s, reg)
}

// setProjectRootForTest points graph.ProjectRoot() (and so
// reviewReportHandler's own repoRoot resolution) at dir for the duration of
// the test.
func setProjectRootForTest(t *testing.T, dir string) {
	t.Helper()
	paths.SetProjectRoot(dir)
	t.Cleanup(func() { paths.SetProjectRoot("") })
}

// --- chronicle_review_report: new engine vs. old fallback -----------------

// TestReviewReportTool_UsesEngineWhenSnapshotCommitted: a repo with a REAL
// committed .depbot/graph.jsonl on two revisions (the same battle-results
// shape review/graph_test.go's own TestReview_E2E_Fixture uses — arena-api
// starts publishing to battle-results) must route through review.Review, not
// the old BuildReviewReportFromGit path: the JSON response must carry the new
// engine's own Verdict field, which the old ReviewReport type has no
// equivalent of.
func TestReviewReportTool_UsesEngineWhenSnapshotCommitted(t *testing.T) {
	dir := t.TempDir()
	gitRunMCP(t, dir, "init", "-q", "-b", "main")
	writeReviewManifestMCP(t, dir)

	svcArena := snapshot.Node{Kind: "node", ID: "service:service:tomandjerry:arena-api", Layer: "service", Type: "service", Domain: "tomandjerry", Name: "arena-api", Status: "active"}
	svcScoreboard := snapshot.Node{Kind: "node", ID: "service:service:tomandjerry:scoreboard-api", Layer: "service", Type: "service", Domain: "tomandjerry", Name: "scoreboard-api", Status: "active"}
	producer := snapshot.Node{Kind: "node", ID: "code:provider:tomandjerry:arena-api/producer", Layer: "code", Type: "provider", Domain: "tomandjerry", Name: "BattleResultProducer", File: "arena-api/producer.ts", Status: "active"}
	consumer := snapshot.Node{Kind: "node", ID: "code:provider:tomandjerry:scoreboard-api/consumer", Layer: "code", Type: "provider", Domain: "tomandjerry", Name: "BattleResultConsumer", File: "scoreboard-api/consumer.ts", Status: "active"}
	topic := snapshot.Node{Kind: "node", ID: "contract:topic:tomandjerry:battle-results", Layer: "contract", Type: "topic", Domain: "tomandjerry", Name: "battle-results", Status: "active"}
	nodes := []snapshot.Node{svcArena, svcScoreboard, producer, consumer, topic}

	publishEdge := snapshot.Edge{Kind: "edge", ID: producer.ID + "->" + topic.ID + ":PUBLISHES_TOPIC", Type: "PUBLISHES_TOPIC", From: producer.ID, To: topic.ID, Active: true, Derivation: "hard"}
	consumeEdge := snapshot.Edge{Kind: "edge", ID: consumer.ID + "->" + topic.ID + ":CONSUMES_TOPIC", Type: "CONSUMES_TOPIC", From: consumer.ID, To: topic.ID, Active: true, Derivation: "hard"}

	baseSnap := &snapshot.Snapshot{
		Header: snapshot.Header{Kind: "header", Format: snapshot.Format, Version: snapshot.Version, Seq: 1, FactSchema: snapshot.FactSchema, Domains: []string{"tomandjerry"}},
		Nodes:  nodes,
		Edges:  []snapshot.Edge{consumeEdge}, // not published yet
	}
	writeReviewGraphFixtureMCP(t, dir, baseSnap)
	baseSHA := commitAllMCP(t, dir, "base")

	headSnap := &snapshot.Snapshot{
		Header: snapshot.Header{Kind: "header", Format: snapshot.Format, Version: snapshot.Version, Seq: 2, FactSchema: snapshot.FactSchema, Domains: []string{"tomandjerry"}},
		Nodes:  nodes,
		Edges:  []snapshot.Edge{publishEdge, consumeEdge}, // publish added
	}
	writeReviewGraphFixtureMCP(t, dir, headSnap)
	headSHA := commitAllMCP(t, dir, "head")

	setProjectRootForTest(t, dir)
	g := newTestGraphAt(t, dir)

	var req mcplib.CallToolRequest
	req.Params.Arguments = map[string]any{"base": baseSHA, "head": headSHA, "format": "json"}
	res, err := reviewReportHandler(g)(context.Background(), req)
	if err != nil {
		t.Fatalf("reviewReportHandler: %v", err)
	}
	if res.IsError {
		t.Fatalf("reviewReportHandler returned an error result: %+v", res)
	}
	text := res.Content[0].(mcplib.TextContent).Text
	var decoded map[string]any
	if err := json.Unmarshal([]byte(text), &decoded); err != nil {
		t.Fatalf("response is not valid JSON: %v\n%s", err, text)
	}
	if _, ok := decoded["Verdict"]; !ok {
		t.Errorf("response = %s, want the new engine's Verdict field", text)
	}
}

// TestReviewReportTool_FallsBackWithoutCommittedGraph: a repo that never
// committed .depbot/graph.jsonl must still go through the OLD
// BuildReviewReportFromGit path (today's behavior, unchanged) — but the JSON
// response must now ALSO carry a "note" explaining why.
func TestReviewReportTool_FallsBackWithoutCommittedGraph(t *testing.T) {
	dir := t.TempDir()
	gitRunMCP(t, dir, "init", "-q", "-b", "main")
	writePlainFileMCP(t, dir, "a.txt", "v1\n")
	sha := commitAllMCP(t, dir, "c1")
	// No .depbot/graph.jsonl ever committed: snapshot.ResolveVersion must
	// report ErrNoVersion for this repo's HEAD.

	g := newTestGraphAt(t, dir)
	// One node gives the store exactly one domain, so the fallback's own
	// (unchanged) domain auto-detection resolves without needing --domain.
	if _, err := g.Store().UpsertNode(store.NodeRow{
		NodeKey: "code:provider:tomandjerry:a", Layer: "code", NodeType: "provider", DomainKey: "tomandjerry",
		Name: "A", FilePath: "a.ts", Status: "active",
		Confidence: 1, Freshness: 1, TrustScore: 1, Metadata: "{}",
	}); err != nil {
		t.Fatalf("UpsertNode: %v", err)
	}

	setProjectRootForTest(t, dir)

	var req mcplib.CallToolRequest
	req.Params.Arguments = map[string]any{"base": sha, "head": sha, "format": "json"}
	res, err := reviewReportHandler(g)(context.Background(), req)
	if err != nil {
		t.Fatalf("reviewReportHandler: %v", err)
	}
	if res.IsError {
		t.Fatalf("reviewReportHandler returned an error result: %+v", res)
	}
	text := res.Content[0].(mcplib.TextContent).Text
	var decoded map[string]any
	if err := json.Unmarshal([]byte(text), &decoded); err != nil {
		t.Fatalf("response is not valid JSON: %v\n%s", err, text)
	}
	if _, ok := decoded["stats"]; !ok {
		t.Errorf("response = %s, want the OLD ReviewReport shape (a stats field)", text)
	}
	note, _ := decoded["note"].(string)
	if note == "" {
		t.Fatalf("response = %s, want a non-empty \"note\" field", text)
	}
	if !strings.Contains(note, "no committed graph.jsonl") {
		t.Errorf("note = %q, want it to explain the fallback", note)
	}
}

// TestReviewReportTool_FallbackMarkdownCarriesNote: the Markdown format of
// the SAME fallback path must surface the note too, not only the JSON one —
// a human reading the MR comment needs the same heads-up an agent reading
// JSON gets.
func TestReviewReportTool_FallbackMarkdownCarriesNote(t *testing.T) {
	dir := t.TempDir()
	gitRunMCP(t, dir, "init", "-q", "-b", "main")
	writePlainFileMCP(t, dir, "a.txt", "v1\n")
	sha := commitAllMCP(t, dir, "c1")

	g := newTestGraphAt(t, dir)
	if _, err := g.Store().UpsertNode(store.NodeRow{
		NodeKey: "code:provider:tomandjerry:a", Layer: "code", NodeType: "provider", DomainKey: "tomandjerry",
		Name: "A", FilePath: "a.ts", Status: "active",
		Confidence: 1, Freshness: 1, TrustScore: 1, Metadata: "{}",
	}); err != nil {
		t.Fatalf("UpsertNode: %v", err)
	}

	setProjectRootForTest(t, dir)

	var req mcplib.CallToolRequest
	req.Params.Arguments = map[string]any{"base": sha, "head": sha}
	res, err := reviewReportHandler(g)(context.Background(), req)
	if err != nil {
		t.Fatalf("reviewReportHandler: %v", err)
	}
	if res.IsError {
		t.Fatalf("reviewReportHandler returned an error result: %+v", res)
	}
	text := res.Content[0].(mcplib.TextContent).Text
	if !strings.Contains(text, "no committed graph.jsonl") {
		t.Errorf("markdown response = %q, want it to carry the fallback note", text)
	}
}
