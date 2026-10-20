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

// A source file the model has nothing to say about still builds a client from
// a configured address; that call must not depend on the model reporting it.
func TestBuildSnapshot_ClientBuiltFromConfigIsACallEvenWhenTheModelFindsNothing(t *testing.T) {
	repo, c1 := newCodeRepoT(t, map[string]string{
		"app.config.js":     "module.exports = { extra: { apiUrl: process.env.API_URL || 'http://gateway:8080' } };\n",
		"src/api/client.ts": "import Constants from 'expo-constants';\nconst apiUrl = Constants.expoConfig?.extra?.apiUrl as string;\nexport const api = axios.create({ baseURL: apiUrl });\n",
	})
	b := &perFileBackend{facts: map[string]string{}} // every file: no facts
	res, err := BuildSnapshot(context.Background(), buildIn(repo, c1, t.TempDir(), b))
	if err != nil {
		t.Fatal(err)
	}
	ok := false
	for _, e := range decodeT(t, res.Files).Edges {
		if e.Active && strings.Contains(e.ID, ":gateway:CALLS_SERVICE") {
			ok = true
		}
	}
	if !ok {
		for _, e := range decodeT(t, res.Files).Edges {
			t.Logf("edge %s %v", e.ID, e.Active)
		}
		t.Fatal("no active call to gateway")
	}
}

// The files that read a changed key are found by the key's text, whatever
// language they are written in.
func TestBuildSnapshot_ConfigOnlyChangeReachesFilesThatNameTheKey(t *testing.T) {
	repo, c1 := newCodeRepoT(t, map[string]string{
		"src/main/resources/application.yml": "services:\n  catalog:\n    url: http://catalog-a:3000\n",
		"src/main/java/Config.java":          "class Config { @Value(\"${services.catalog.url}\") String u; }\n",
		"src/main/java/Other.java":           "class Other { }\n",
	})
	reread := map[string]int{}
	b := &fileCountBackend{inner: &perFileBackend{facts: map[string]string{}}, seen: reread}
	first, err := BuildSnapshot(context.Background(), buildIn(repo, c1, t.TempDir(), b))
	if err != nil {
		t.Fatal(err)
	}
	for k := range reread {
		delete(reread, k)
	}
	writeT(t, repo, "src/main/resources/application.yml", "services:\n  catalog:\n    url: http://catalog-b:3000\n")
	c2 := commitAllT(t, repo, "repoint")
	in := buildIn(repo, c2, t.TempDir(), b)
	in.Base = &Base{CodeSha: c1, Files: first.Files}
	if _, err := BuildSnapshot(context.Background(), in); err != nil {
		t.Fatal(err)
	}
	if reread["src/main/java/Config.java"] == 0 {
		t.Errorf("the file that names services.catalog.url must be extracted again: %v", reread)
	}
	if reread["src/main/java/Other.java"] != 0 {
		t.Errorf("a file that does not name the key must not be extracted again: %v", reread)
	}
}

// fileCountBackend counts the files a build sends to the model.
type fileCountBackend struct {
	inner llm.Backend
	seen  map[string]int
}

func (b *fileCountBackend) Name() string  { return b.inner.Name() }
func (b *fileCountBackend) Model() string { return b.inner.Model() }
func (b *fileCountBackend) Complete(ctx context.Context, req llm.Request) (llm.Response, error) {
	first, _, _ := strings.Cut(req.User, "\n")
	b.seen[strings.TrimPrefix(first, "File: ")]++
	return b.inner.Complete(ctx, req)
}

// A channel whose name comes from configuration follows the configuration:
// renaming the key's value in appsettings moves the consumer to the new channel.
func TestBuildSnapshot_ConfigRenamesAConsumedChannel(t *testing.T) {
	repo, c1 := newCodeRepoT(t, map[string]string{
		"appsettings.json":          "{ \"Kafka\": { \"Topics\": { \"Payments\": \"payments\" } } }\n",
		"Kafka/PaymentsConsumer.cs": "class C { void F(IConfiguration cfg) { consumer.Subscribe(cfg[\"Kafka:Topics:Payments\"]); } }\n",
	})
	b := &perFileBackend{facts: map[string]string{
		"Kafka/PaymentsConsumer.cs": `{"kind":"consumes","to":"Kafka:Topics:Payments","from_type":"service"}`,
	}}
	first, err := BuildSnapshot(context.Background(), buildIn(repo, c1, t.TempDir(), b))
	if err != nil {
		t.Fatal(err)
	}
	active := func(files snapshot.Files, suffix string) bool {
		for _, e := range decodeT(t, files).Edges {
			if e.Active && strings.Contains(e.ID, "CONSUMES_TOPIC") && strings.Contains(e.ID, suffix+":CONSUMES_TOPIC") {
				return true
			}
		}
		return false
	}
	if !active(first.Files, "payments") {
		for _, e := range decodeT(t, first.Files).Edges {
			t.Logf("edge %s %v", e.ID, e.Active)
		}
		t.Fatal("base must consume payments")
	}
	writeT(t, repo, "appsettings.json", "{ \"Kafka\": { \"Topics\": { \"Payments\": \"payments-v2\" } } }\n")
	c2 := commitAllT(t, repo, "rename the topic")
	in := buildIn(repo, c2, t.TempDir(), b)
	in.Base = &Base{CodeSha: c1, Files: first.Files}
	res, err := BuildSnapshot(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	if !active(res.Files, "payments-v2") {
		t.Errorf("the consumer must move to payments-v2")
	}
	if active(res.Files, "payments") {
		t.Errorf("the old channel must be gone")
	}
}
