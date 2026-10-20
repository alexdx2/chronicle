package scan

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alexdx2/chronicle-core/llm"
	"github.com/alexdx2/chronicle-core/snapshot"
)

// perFileBackend answers each file with the facts the test set for it.
type perFileBackend struct{ facts map[string]string }

func (b *perFileBackend) Name() string  { return "fake" }
func (b *perFileBackend) Model() string { return "fake" }
func (b *perFileBackend) Complete(ctx context.Context, req llm.Request) (llm.Response, error) {
	first, _, _ := strings.Cut(req.User, "\n")
	p := strings.TrimPrefix(first, "File: ")
	f, ok := b.facts[p]
	if !ok {
		f = ""
	}
	return llm.Response{Text: `{"status":"extracted","from_type":"controller","facts":[` + f + `]}`}, nil
}

func TestBuildSnapshot_IncrementalRetiresEndpointRemovedFromReextractedFile(t *testing.T) {
	del := `{"kind":"endpoint","method":"DELETE","target":"/orders/{id}","from_type":"controller"}`
	get := `{"kind":"endpoint","method":"GET","target":"/orders","from_type":"controller"}`
	repo, c1 := newCodeRepoT(t, map[string]string{
		"src/orders.controller.ts": "route(\"GET /orders\");\nroute(\"DELETE /orders/{id}\");\n",
		"src/other.controller.ts":  "route(\"GET /other\");\n",
	})
	b := &perFileBackend{facts: map[string]string{
		"src/orders.controller.ts": get + "," + del,
		"src/other.controller.ts":  `{"kind":"endpoint","method":"GET","target":"/other","from_type":"controller"}`,
	}}
	first, err := BuildSnapshot(context.Background(), buildIn(repo, c1, t.TempDir(), b))
	if err != nil {
		t.Fatal(err)
	}
	was := decodeT(t, first.Files)
	count := func(s string, f func(id string) bool) int {
		n := 0
		for _, e := range was.Edges {
			if e.Active && strings.Contains(e.ID, s) && f(e.ID) {
				n++
			}
		}
		return n
	}
	if count("/orders/", func(string) bool { return true }) == 0 {
		t.Fatalf("base must expose the DELETE endpoint: %+v", was.Edges)
	}

	writeT(t, repo, "src/orders.controller.ts", "route(\"GET /orders\");\n")
	c2 := commitAllT(t, repo, "drop delete")
	b.facts["src/orders.controller.ts"] = get
	in := buildIn(repo, c2, t.TempDir(), b)
	in.Base = &Base{CodeSha: c1, Files: first.Files}
	res, err := BuildSnapshot(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	now := decodeT(t, res.Files)
	for _, e := range now.Edges {
		if strings.Contains(strings.ToLower(e.ID), "delete") && e.Active {
			t.Errorf("edge to the removed endpoint is still active: %s", e.ID)
		}
	}
	for _, n := range now.Nodes {
		if strings.Contains(strings.ToLower(n.ID), "delete") && n.Status == "active" {
			t.Errorf("removed endpoint node is still active: %s", n.ID)
		}
	}
	gotGet, gotOther := false, false
	for _, e := range now.Edges {
		if e.Active && strings.Contains(strings.ToLower(e.ID), "get:/orders") {
			gotGet = true
		}
		if e.Active && strings.Contains(strings.ToLower(e.ID), "/other") {
			gotOther = true
		}
	}
	if !gotGet || !gotOther {
		t.Errorf("endpoints still declared must stay active (get=%v other=%v)", gotGet, gotOther)
	}
}

func TestBuildSnapshot_IncrementalRetiresFactsOfADeletedFile(t *testing.T) {
	repo, c1 := newCodeRepoT(t, map[string]string{
		"src/orders.controller.ts": "route(\"GET /orders\");\n",
		"src/products.resolver.ts": "route(\"GET /products-gone\");\n",
		"src/readme-neighbour.ts":  "export const x = 1;\n",
	})
	b := &perFileBackend{facts: map[string]string{
		"src/orders.controller.ts": `{"kind":"endpoint","method":"GET","target":"/orders","from_type":"controller"}`,
		"src/products.resolver.ts": `{"kind":"endpoint","method":"GET","target":"/products-gone","from_type":"controller"}`,
	}}
	first, err := BuildSnapshot(context.Background(), buildIn(repo, c1, t.TempDir(), b))
	if err != nil {
		t.Fatal(err)
	}
	active := func(files snapshot.Files, needle string) bool {
		g := decodeT(t, files)
		for _, e := range g.Edges {
			if e.Active && strings.Contains(strings.ToLower(e.ID), needle) {
				return true
			}
		}
		return false
	}
	if !active(first.Files, "products-gone") {
		for _, e := range decodeT(t, first.Files).Edges {
			t.Logf("edge %s active=%v", e.ID, e.Active)
		}
		t.Fatalf("base must expose the endpoint of the resolver")
	}
	if err := os.Remove(filepath.Join(repo, "src", "products.resolver.ts")); err != nil {
		t.Fatal(err)
	}
	c2 := commitAllT(t, repo, "delete the resolver")
	in := buildIn(repo, c2, t.TempDir(), b)
	in.Base = &Base{CodeSha: c1, Files: first.Files}
	res, err := BuildSnapshot(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	if active(res.Files, "products-gone") {
		t.Errorf("the endpoint of a deleted file is still active")
	}
	if !active(res.Files, "get:/orders") {
		t.Errorf("the endpoint of an untouched file must stay active")
	}
}

// A change that only moves an address in a config file must move the call the
// code makes with it, although no source file changed.
func TestBuildSnapshot_ConfigOnlyChangeMovesTheCallsThatUseIt(t *testing.T) {
	repo, c1 := newCodeRepoT(t, map[string]string{
		".env":                 "NOTIFY_SVC_URL=http://notify-a:3000\n",
		"src/routes/notify.ts": "import { NotifyClient } from '@acme/notify-client';\nconst client = new NotifyClient(process.env.NOTIFY_SVC_URL ?? '');\n",
		"src/routes/other.ts":  "export const x = 1;\n",
	})
	b := &perFileBackend{facts: map[string]string{}}
	first, err := BuildSnapshot(context.Background(), buildIn(repo, c1, t.TempDir(), b))
	if err != nil {
		t.Fatal(err)
	}
	callsTo := func(files snapshot.Files, host string) bool {
		for _, e := range decodeT(t, files).Edges {
			if e.Active && strings.Contains(e.ID, "CALLS_SERVICE") && strings.HasSuffix(strings.Split(e.ID, "->")[1], ":"+host+":CALLS_SERVICE") {
				return true
			}
		}
		return false
	}
	if !callsTo(first.Files, "notify-a") {
		for _, e := range decodeT(t, first.Files).Edges {
			t.Logf("edge %s %v", e.ID, e.Active)
		}
		t.Fatalf("base must call notify-a")
	}
	writeT(t, repo, ".env", "NOTIFY_SVC_URL=http://notify-b:3000\n")
	c2 := commitAllT(t, repo, "repoint notify")
	in := buildIn(repo, c2, t.TempDir(), b)
	in.Base = &Base{CodeSha: c1, Files: first.Files}
	res, err := BuildSnapshot(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	if !callsTo(res.Files, "notify-b") {
		t.Errorf("the call must move to notify-b")
	}
	if callsTo(res.Files, "notify-a") {
		for _, e := range decodeT(t, res.Files).Edges {
			t.Logf("edge %s active=%v ev=%d", e.ID, e.Active, len(e.Evidence))
			for _, ev := range e.Evidence {
				t.Logf("   ev %s status=%s ver=%s", ev.File, ev.Status, ev.Verification)
			}
		}
		t.Errorf("the call to the old address must be gone")
	}
}
