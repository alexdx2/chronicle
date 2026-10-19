package scan

import (
	"context"
	"strings"
	"testing"

	"github.com/alexdx2/chronicle-core/llm"
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
