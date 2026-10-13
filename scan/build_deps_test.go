package scan

import (
	"context"
	"strings"
	"testing"

	"github.com/alexdx2/chronicle-core/llm"
)

// serviceManifestBackend answers package.json with the model's view of it: a
// service declaration and, against the schema, a dependency fact of its own.
type serviceManifestBackend struct{ countingBackend }

func (b *serviceManifestBackend) Complete(ctx context.Context, req llm.Request) (llm.Response, error) {
	resp, err := b.countingBackend.Complete(ctx, req)
	first, _, _ := strings.Cut(req.User, "\n")
	if err != nil || first != "File: package.json" {
		return resp, err
	}
	resp.Text = `{"status":"extracted","facts":[{"kind":"declares_service","to":"orders"},{"kind":"dependency","to":"@acme/ui","section":"dependencies"}]}`
	return resp, nil
}

// The deps pass runs after extraction so its edges start at the manifest's
// service node, and the model's own dependency fact does not add a second edge.
func TestBuildSnapshot_DependenciesStartAtTheServiceNode(t *testing.T) {
	root, head := newCodeRepoT(t, map[string]string{
		"package.json": `{"name":"orders","dependencies":{"@acme/ui":"1.0.0","@acme/tokens":"2.0.0"}}`,
		"src/a.ts":     "export const a = 1",
	})
	res, err := BuildSnapshot(context.Background(), buildIn(root, head, t.TempDir(), &serviceManifestBackend{}))
	if err != nil {
		t.Fatal(err)
	}
	s := decodeT(t, res.Files)
	perDep := map[string]int{}
	for _, e := range s.Edges {
		if !strings.HasSuffix(e.ID, ":DEPENDS_ON") {
			continue
		}
		from, to, _ := strings.Cut(strings.TrimSuffix(e.ID, ":DEPENDS_ON"), "->")
		if !strings.HasPrefix(from, "service:service:") {
			t.Errorf("dependency edge %s does not start at the service node", e.ID)
		}
		perDep[to]++
	}
	if len(perDep) != 2 {
		t.Fatalf("want 2 dependency targets, got %v (edges: %d)", perDep, len(s.Edges))
	}
	for to, n := range perDep {
		if n != 1 {
			t.Errorf("%s has %d DEPENDS_ON edges, want 1", to, n)
		}
	}
}
