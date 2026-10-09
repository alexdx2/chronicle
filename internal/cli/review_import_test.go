package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alexdx2/chronicle-core/graph"
	"github.com/alexdx2/chronicle-core/live"
	"github.com/alexdx2/chronicle-core/registry"
	"github.com/alexdx2/chronicle-core/review"
	"github.com/alexdx2/chronicle-core/store"
)

func TestReviewImport_PublishesDiagram(t *testing.T) {
	dir, baseSHA, headSHA := buildReviewFixtureRepo(t)
	zipPath := filepath.Join(t.TempDir(), "review.zip")
	usage := filepath.Join(t.TempDir(), "usage.json")
	if err := os.WriteFile(usage, []byte(`{"tokens":7}`), 0o644); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	code, msg := runReview(context.Background(), &out, dir, reviewCLIOptions{
		Base: baseSHA, Head: headSHA, Format: "markdown", Depth: 4,
		Command: "chronicle review x..y", BundlePath: zipPath, UsagePath: usage,
	})
	if code != 0 {
		t.Fatalf("runReview exit %d: %s", code, msg)
	}

	b, err := review.ReadBundle(zipPath)
	if err != nil {
		t.Fatalf("ReadBundle: %v", err)
	}
	if b.Manifest.BaseCommit != baseSHA || b.Manifest.HeadCommit != headSHA || b.Manifest.CreatedAt == "" ||
		b.Manifest.Command != "chronicle review x..y" || b.Manifest.Repo != filepath.Base(dir) {
		t.Errorf("manifest = %+v", b.Manifest)
	}
	if b.BaseSnapshot == nil || b.HeadSnapshot == nil || string(b.UsageJSON) != `{"tokens":7}` {
		t.Errorf("bundle missing snapshots/usage")
	}
	if strings.Contains(b.Markdown, b.Manifest.CreatedAt) || strings.Contains(string(b.ReviewJSON), b.Manifest.CreatedAt) {
		t.Error("timestamp leaked into deterministic review output")
	}
	if b.Markdown != out.String() && strings.TrimSuffix(out.String(), "\n") != b.Markdown {
		t.Errorf("bundle markdown differs from stdout report")
	}

	st, err := store.Open(filepath.Join(t.TempDir(), "chronicle.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	reg, err := registry.LoadDefaults()
	if err != nil {
		t.Fatal(err)
	}
	g := graph.New(st, reg)

	chronicle := t.TempDir()
	var printed bytes.Buffer
	if err := importReviewBundle(g, zipPath, chronicle, &printed); err != nil {
		t.Fatalf("import: %v", err)
	}

	id := baseSHA[:7] + ".." + headSHA[:7]
	unpack := filepath.Join(chronicle, "reviews", id)
	if _, err := os.Stat(filepath.Join(unpack, "review.md")); err != nil {
		t.Errorf("review.md not unpacked: %v", err)
	}
	if !strings.Contains(printed.String(), "warning: ") || !strings.Contains(printed.String(), "marked nodes are not in this database") {
		t.Errorf("printed = %q, want the missing-nodes warning (store lacks the head's nodes)", printed.String())
	}
	if !strings.Contains(printed.String(), unpack) || !strings.Contains(printed.String(), "chronicle admin") {
		t.Errorf("printed = %q", printed.String())
	}

	evs, err := st.RecentLiveEvents(live.KindDiagram, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(evs) != 1 {
		t.Fatalf("diagram events = %d, want 1", len(evs))
	}
	if evs[0].Source != "review-import" || !strings.Contains(string(evs[0].Payload), "Chronicle review "+id) {
		t.Errorf("event = %+v", evs[0])
	}
}

func TestReviewImport_RejectsTraversalCommit(t *testing.T) {
	tmp := t.TempDir()
	zipPath := filepath.Join(tmp, "evil.zip")
	if err := review.WriteBundle(zipPath, review.Bundle{
		Manifest:     review.BundleManifest{BaseCommit: "../../a", HeadCommit: "abcdef1"},
		HeadSnapshot: []byte("x"), ReviewJSON: []byte("{}"),
	}); err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(filepath.Join(tmp, "c.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	reg, _ := registry.LoadDefaults()
	chronicle := filepath.Join(tmp, "chr")
	err = importReviewBundle(graph.New(st, reg), zipPath, chronicle, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "invalid commit in bundle manifest") {
		t.Fatalf("err = %v", err)
	}
	if _, serr := os.Stat(chronicle); serr == nil {
		t.Error("something was written under the chronicle dir")
	}
	if _, serr := os.Stat(filepath.Join(tmp, "a")); serr == nil {
		t.Error("something was written outside reviews/")
	}
}
