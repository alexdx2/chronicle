package graph

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alexdx2/chronicle-core/manifest"
)

func TestLayerOf(t *testing.T) {
	cases := map[string]int{
		"package.json": LayerContracts, "Api/Api.csproj": LayerContracts, "prisma/schema.prisma": LayerContracts,
		"db/migrations/001_init.sql": LayerContracts, "api/openapi.yaml": LayerContracts, "proto/x.proto": LayerContracts,
		"src/orders/orders.module.ts": LayerStructure, "src/orders/orders.controller.ts": LayerStructure, "src/Controllers/OrderController.cs": LayerStructure,
		"src/orders/orders.service.ts": LayerStructure, "src/main.ts": LayerStructure,
		"src/clients/billing.client.ts": LayerIntegrations, "src/kafka/OrderConsumer.cs": LayerIntegrations, "src/Messaging/Producer.cs": LayerIntegrations,
		"src/integrations/stripe.ts": LayerIntegrations, "src/sdk/aws.ts": LayerIntegrations,
		"tests/OrderTests.cs": LayerTests, "src/orders/orders.spec.ts": LayerTests, "pkg/x_test.go": LayerTests, "__tests__/a.ts": LayerTests,
		"src/util/strings.ts": LayerStructure, "tests/clients/x.ts": LayerTests, "services/gateway/src/orders.controller.ts": LayerStructure, "src/clientele/list.ts": LayerStructure, "Dockerfile": LayerContracts,
	}
	for p, want := range cases {
		if got := LayerOf(p); got != want {
			t.Errorf("%s: layer %d, want %d", p, got, want)
		}
	}
}

func layersRepo(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	run := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-b", "main")
	for p, c := range files {
		full := filepath.Join(dir, p)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(c), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	run("add", ".")
	run("commit", "-m", "c")
	return dir
}

func TestDiscoverFilesOpts_OrdersObligationsByLayer(t *testing.T) {
	g, st, rev := setupTestGraph(t)
	root := layersRepo(t, map[string]string{
		"tests/a_test.go": "package a", "src/clients/x.go": "package c", "src/svc.go": "package s", "schema.prisma": "model A {}",
	})
	m := &manifest.Manifest{Domains: []manifest.DomainEntry{{Key: "testapp", Name: "testapp", Scan: manifest.ScanConfig{Include: []string{"**"}}}}}
	if _, err := g.DiscoverFilesOpts(root, "testapp", rev, m, DiscoverOpts{VotesNeeded: 1}); err != nil {
		t.Fatal(err)
	}
	claimed, err := st.ClaimObligations(rev, "scan_file", 10)
	if err != nil || len(claimed) != 4 {
		t.Fatalf("%d obligations, %v", len(claimed), err)
	}
	var got []string
	for _, c := range claimed {
		got = append(got, c.TargetKey)
	}
	want := []string{"schema.prisma", "src/svc.go", "src/clients/x.go", "tests/a_test.go"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("order = %v, want %v", got, want)
	}
}
