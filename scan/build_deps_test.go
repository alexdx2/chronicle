package scan

import (
	"context"
	"strings"
	"testing"

	"github.com/alexdx2/chronicle-core/llm"
	"github.com/alexdx2/chronicle-core/store"
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

// callerBackend answers src/a.ts with a call to the sibling service by name
// and every manifest with nothing: service identity must come from the
// deterministic deps pass alone.
type callerBackend struct{ countingBackend }

func (b *callerBackend) Complete(ctx context.Context, req llm.Request) (llm.Response, error) {
	resp, err := b.countingBackend.Complete(ctx, req)
	first, _, _ := strings.Cut(req.User, "\n")
	switch {
	case err != nil:
		return resp, err
	case first == "File: src/a.ts":
		resp.Text = `{"status":"extracted","facts":[{"kind":"calls_service","to":"jerry-api","method":"get"}]}`
	case strings.HasSuffix(first, "package.json"):
		resp.Text = `{"status":"extracted","facts":[]}`
	}
	return resp, nil
}

var twoServiceFiles = map[string]string{
	"tom-api/package.json":   `{"name":"tom-api","dependencies":{"kafkajs":"^2.0.0"}}`,
	"jerry-api/package.json": `{"name":"jerry-api","dependencies":{"@nestjs/common":"^10.0.0"}}`,
	"src/a.ts":               "export const a = 1",
}

func TestBuildSnapshot_ManifestsMintServiceNodesWithoutTheModel(t *testing.T) {
	root, head := newCodeRepoT(t, twoServiceFiles)
	res, err := BuildSnapshot(context.Background(), buildIn(root, head, t.TempDir(), &callerBackend{}))
	if err != nil {
		t.Fatal(err)
	}
	s := decodeT(t, res.Files)
	for _, n := range s.Nodes {
		if n.Type == "external_system" && n.Status == "active" && strings.HasSuffix(n.ID, ":jerry-api") {
			t.Errorf("own service survives as an external boundary: %s", n.ID)
		}
	}
	have := map[string]bool{}
	for _, n := range s.Nodes {
		if n.Status == "active" {
			have[n.ID] = true
		}
	}
	for _, id := range []string{"tom-api", "jerry-api"} {
		found := false
		for k := range have {
			if strings.HasPrefix(k, "service:service:") && strings.HasSuffix(k, ":"+id) {
				found = true
			}
		}
		if !found {
			t.Errorf("no service:service node for %s in %v", id, have)
		}
	}
}

func TestFullScan_PlainRunMintsServiceNodesFromManifests(t *testing.T) {
	e := newScanRepoT(t, twoServiceFiles, true)
	o := fullOpts(e, &countingBackend{})
	o.NoExtract = true
	if _, err := FullScan(context.Background(), e.g, o); err != nil {
		t.Fatal(err)
	}
	nodes, _ := e.s.ListNodes(store.NodeFilter{})
	got := map[string]bool{}
	for _, n := range nodes {
		if n.Status == "active" && n.NodeType == "service" && n.Layer == "service" {
			got[n.Name] = true
		}
		if n.Status == "active" && n.NodeType == "external_system" {
			t.Errorf("external_system %s in a run with no calls", n.NodeKey)
		}
	}
	if !got["tom-api"] || !got["jerry-api"] {
		t.Fatalf("service nodes = %v", got)
	}
}
