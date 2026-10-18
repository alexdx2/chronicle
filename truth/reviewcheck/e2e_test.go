package reviewcheck_test

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
	"github.com/alexdx2/chronicle-core/truth"
	"github.com/alexdx2/chronicle-core/truth/extract"
	"github.com/alexdx2/chronicle-core/truth/reviewcheck"
)

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func write(t *testing.T, dir, rel, content string) {
	t.Helper()
	full := filepath.Join(dir, rel)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func encode(t *testing.T, s *snapshot.Snapshot) string {
	t.Helper()
	var buf bytes.Buffer
	if err := snapshot.Encode(&buf, s); err != nil {
		t.Fatal(err)
	}
	return buf.String()
}

const (
	ctrlBase = "import { Controller, Get } from '@nestjs/common';\n@Controller('orders')\nexport class C {\n  @Get(':id')\n  one() {}\n}\n"
	ctrlHead = "import { Controller, Get, Post } from '@nestjs/common';\n@Controller('orders')\nexport class C {\n  @Get(':id')\n  one() {}\n  @Post('/new')\n  create() {}\n}\n"
	evBase   = "await producer.send({ topic: 'battle-results', messages: [] })\n"
	evHead   = "await producer.send({ topic: 'battle-results-v2', messages: [] })\nconst r = await fetch('https://feed.example.com/items')\n"
	pkgBase  = `{"name":"api","dependencies":{"express":"^4.18.0"}}`
	pkgHead  = `{"name":"api","dependencies":{"express":"^4.18.0","lodash":"^4.17.0"}}`
)

func node(id, layer, typ, name, status string) snapshot.Node {
	return snapshot.Node{Kind: "node", ID: id, Layer: layer, Type: typ, Domain: "tj", Name: name, Status: status}
}

func edge(from, to, typ string) snapshot.Edge {
	return snapshot.Edge{Kind: "edge", ID: from + "->" + to + ":" + typ, Type: typ, From: from, To: to, Active: true, Derivation: "hard"}
}

func hdr(seq int) snapshot.Header {
	return snapshot.Header{Kind: "header", Format: snapshot.Format, Version: snapshot.Version, Seq: seq, FactSchema: snapshot.FactSchema, Domains: []string{"tj"}}
}

func reportAt(t *testing.T, dir, rev string) *truth.Report {
	t.Helper()
	tmp := t.TempDir()
	tarPath := filepath.Join(tmp, "x.tar")
	git(t, dir, "archive", "--format=tar", "-o", tarPath, rev)
	if out, err := exec.Command("tar", "-xf", tarPath, "-C", tmp).CombinedOutput(); err != nil {
		t.Fatalf("tar: %v %s", err, out)
	}
	rep, err := extract.Extract(os.DirFS(tmp))
	if err != nil {
		t.Fatal(err)
	}
	return rep
}

func TestEndToEnd(t *testing.T) {
	dir := t.TempDir()
	git(t, dir, "init", "-q", "-b", "main")
	write(t, dir, ".depbot/chronicle.domain.yaml", "domains:\n  - name: d\n    scan:\n      include:\n        - \"zzz-nomatch/**\"\n")

	svc := node("service:service:tj:api", "service", "service", "api", "active")
	caller := node("code:provider:tj:api/events", "code", "provider", "Events", "active")
	ep1 := node("contract:endpoint:tj:GET:/orders/{id}", "contract", "endpoint", "GET /orders/{id}", "active")
	ep2 := node("contract:endpoint:tj:POST:/orders/new", "contract", "endpoint", "POST /orders/new", "active")
	t1 := node("contract:topic:tj:battle-results", "contract", "topic", "battle-results", "active")
	t2 := node("contract:topic:tj:battle-results-v2", "contract", "topic", "battle-results-v2", "active")
	feed := node("service:external_system:tj:feed.example.com", "service", "external_system", "feed.example.com", "active")
	express := node("code:package:tj:npm:express", "code", "package", "express", "active")
	lodash := node("code:package:tj:npm:lodash", "code", "package", "lodash", "active")

	common := []snapshot.Node{svc, caller, ep1, express}
	baseSnap := &snapshot.Snapshot{Header: hdr(1), Nodes: append(append([]snapshot.Node{}, common...), t1),
		Edges: []snapshot.Edge{edge(caller.ID, t1.ID, "PUBLISHES_TOPIC"), edge(svc.ID, express.ID, "DEPENDS_ON")}}
	headSnap := &snapshot.Snapshot{Header: hdr(2), Nodes: append(append([]snapshot.Node{}, common...), ep2, t2, feed, lodash),
		Edges: []snapshot.Edge{edge(caller.ID, t2.ID, "PUBLISHES_TOPIC"), edge(caller.ID, feed.ID, "CALLS_SERVICE"),
			edge(svc.ID, express.ID, "DEPENDS_ON"), edge(svc.ID, lodash.ID, "DEPENDS_ON")}}

	write(t, dir, "api/orders.controller.ts", ctrlBase)
	write(t, dir, "api/events.ts", evBase)
	write(t, dir, "api/package.json", pkgBase)
	write(t, dir, ".depbot/graph.jsonl", encode(t, baseSnap))
	git(t, dir, "add", "-A")
	git(t, dir, "commit", "-q", "-m", "base")
	baseSHA := git(t, dir, "rev-parse", "HEAD")
	write(t, dir, "api/orders.controller.ts", ctrlHead)
	write(t, dir, "api/events.ts", evHead)
	write(t, dir, "api/package.json", pkgHead)
	write(t, dir, ".depbot/graph.jsonl", encode(t, headSnap))
	git(t, dir, "add", "-A")
	git(t, dir, "commit", "-q", "-m", "head")
	headSHA := git(t, dir, "rev-parse", "HEAD")

	rev, err := review.Review(context.Background(), review.ReviewInput{
		BaseSource: review.SnapshotSource{RepoRoot: dir, Rev: baseSHA},
		HeadSource: review.SnapshotSource{RepoRoot: dir, Rev: headSHA},
		RepoRoot:   dir,
	})
	if err != nil {
		t.Fatal(err)
	}
	// Round trip through the JSON the CLI reads.
	raw, err := review.RenderJSON(rev)
	if err != nil {
		t.Fatal(err)
	}
	rev = decodeReview(t, raw)

	// One hand-injected false positive.
	rev.Delta.Added = append(rev.Delta.Added, review.Fact{ID: "contract:topic:tj:ghost", Kind: review.KindNode,
		Node: &snapshot.Node{Kind: "node", ID: "contract:topic:tj:ghost", Layer: "contract", Type: "topic", Name: "ghost"}})

	res := reviewcheck.Check(reviewcheck.Input{
		Base: reportAt(t, dir, baseSHA), Head: reportAt(t, dir, headSHA),
		BaseSnap: baseSnap, HeadSnap: headSnap, Review: rev,
	})
	want := map[string]reviewcheck.Status{
		"endpoints POST /orders/new added":               reviewcheck.Reported,
		"http feed.example.com added":                    reviewcheck.Reported,
		"channels topic:battle-results#produce removed":  reviewcheck.Reported,
		"channels topic:battle-results-v2#produce added": reviewcheck.Reported,
		// The review delta carries no structural facts: a new dependency
		// (package node + DEPENDS_ON edge) is not tracked by it.
		"deps npm:lodash added": reviewcheck.NotTracked,
	}
	got := map[string]reviewcheck.Status{}
	for _, f := range res.Facts {
		got[string(f.Fact.Class)+" "+f.Fact.Key+" "+f.Change] = f.Status
	}
	for k, w := range want {
		if got[k] != w {
			t.Errorf("%s: status %q, want %q", k, got[k], w)
		}
	}
	if len(got) != len(want) {
		t.Errorf("facts: got %v", got)
	}
	if res.Reported != 4 || res.Missed != 0 || res.NotInGraph != 0 || res.NotTracked != 1 {
		t.Errorf("counts: %+v", res)
	}
	if res.Recall == nil || *res.Recall != 1 {
		t.Errorf("recall: %v", res.Recall)
	}
	if len(res.Unexplained) != 1 || res.Unexplained[0].ID != "contract:topic:tj:ghost" || res.Unexplained[0].Change != "added" {
		t.Errorf("unexplained: %+v", res.Unexplained)
	}
}

func decodeReview(t *testing.T, raw []byte) *review.ReviewResult {
	t.Helper()
	var r review.ReviewResult
	if err := json.Unmarshal(raw, &r); err != nil {
		t.Fatal(err)
	}
	return &r
}
