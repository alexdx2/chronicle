package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alexdx2/chronicle-core/review"
	"github.com/alexdx2/chronicle-core/snapshot"
	"github.com/alexdx2/chronicle-core/truth/reviewcheck"
)

func TestTruthReviewCheck(t *testing.T) {
	dir := t.TempDir()
	g := func(args ...string) string {
		c := exec.Command("git", append([]string{"-C", dir}, args...)...)
		c.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		out, err := c.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	w := func(rel, s string) {
		p := filepath.Join(dir, rel)
		os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, []byte(s), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	enc := func(s *snapshot.Snapshot) string {
		var b bytes.Buffer
		if err := snapshot.Encode(&b, s); err != nil {
			t.Fatal(err)
		}
		return b.String()
	}
	hdr := func(seq int) snapshot.Header {
		return snapshot.Header{Kind: "header", Format: snapshot.Format, Version: snapshot.Version, Seq: seq, FactSchema: snapshot.FactSchema, Domains: []string{"d"}}
	}
	ep1 := snapshot.Node{Kind: "node", ID: "contract:endpoint:d:GET:/cats/{id}", Layer: "contract", Type: "endpoint", Domain: "d", Name: "GET /cats/{id}", Status: "active"}
	ep2 := snapshot.Node{Kind: "node", ID: "contract:endpoint:d:POST:/cats/new", Layer: "contract", Type: "endpoint", Domain: "d", Name: "POST /cats/new", Status: "active"}
	baseSnap := &snapshot.Snapshot{Header: hdr(1), Nodes: []snapshot.Node{ep1}}
	headSnap := &snapshot.Snapshot{Header: hdr(2), Nodes: []snapshot.Node{ep1, ep2}}

	g("init", "-q", "-b", "main")
	w(".depbot/chronicle.domain.yaml", "domains:\n  - name: d\n    scan:\n      include:\n        - \"zzz-nomatch/**\"\n")
	w("src/cats.controller.ts", nestController)
	w(".depbot/graph.jsonl", enc(baseSnap))
	g("add", "-A")
	g("commit", "-q", "-m", "base")
	baseSHA := g("rev-parse", "HEAD")
	w("src/cats.controller.ts", strings.Replace(nestController, "one() {}", "one() {}\n  @Post('/new')\n  create() {}", 1))
	w(".depbot/graph.jsonl", enc(headSnap))
	g("add", "-A")
	g("commit", "-q", "-m", "head")
	headSHA := g("rev-parse", "HEAD")

	rv, err := review.Review(context.Background(), review.ReviewInput{
		BaseSource: review.SnapshotSource{RepoRoot: dir, Rev: baseSHA},
		HeadSource: review.SnapshotSource{RepoRoot: dir, Rev: headSHA},
		RepoRoot:   dir,
	})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := review.RenderJSON(rv)
	if err != nil {
		t.Fatal(err)
	}
	rvPath := filepath.Join(t.TempDir(), "review.json")
	os.WriteFile(rvPath, raw, 0o644)

	out := runTruth(t, "review-check", "--root", dir, "--base", baseSHA, "--head", headSHA, "--review", rvPath, "--json")
	var res reviewcheck.Result
	if err := json.Unmarshal(out, &res); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if res.Reported != 1 || res.Missed != 0 || res.Recall == nil || *res.Recall != 1 || len(res.Unexplained) != 0 {
		t.Fatalf("result %+v", res)
	}

	// Explicit graph files and the text table.
	gp := filepath.Join(t.TempDir(), "head.jsonl")
	os.WriteFile(gp, []byte(enc(headSnap)), 0o644)
	text := string(runTruth(t, "review-check", "--root", dir, "--base", baseSHA, "--head", headSHA, "--review", rvPath, "--head-graph", gp))
	for _, want := range []string{"class", "endpoints", "recall: 100%"} {
		if !strings.Contains(text, want) {
			t.Errorf("text output lacks %q:\n%s", want, text)
		}
	}
}
