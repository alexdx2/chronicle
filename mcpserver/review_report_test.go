package mcpserver

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alexdx2/chronicle-core/graph"
	"github.com/alexdx2/chronicle-core/paths"
	"github.com/alexdx2/chronicle-core/registry"
	"github.com/alexdx2/chronicle-core/store"
	"github.com/alexdx2/chronicle-core/validate"
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
		"Battle.winnerId",   // field-level row from the prisma diff
		"removed",
		"code:provider:billing:consumer", // cross-domain consumer → external section
		"## External services affected",
	} {
		if !strings.Contains(md, want) {
			t.Errorf("markdown missing %q\n---\n%s", want, md)
		}
	}
}
