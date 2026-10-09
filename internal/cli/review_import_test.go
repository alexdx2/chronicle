package cli

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alexdx2/chronicle-core/graph"
	"github.com/alexdx2/chronicle-core/live"
	"github.com/alexdx2/chronicle-core/registry"
	"github.com/alexdx2/chronicle-core/review"
	"github.com/alexdx2/chronicle-core/snapshot"
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

// More marked systems than a view may carry must not abort the import: the
// diagram is published without marks and the warning names the count.
func TestReviewImport_MoreThanMaxMarksPublishesUnmarked(t *testing.T) {
	const systems = 51
	dir := t.TempDir()
	gitRun(t, dir, "init", "-q", "-b", "main")
	writeReviewManifestCLI(t, dir)

	mkNode := func(layer, typ, name, file string) snapshot.Node {
		id := layer + ":" + typ + ":tomandjerry:" + name
		return snapshot.Node{Kind: "node", ID: id, Layer: layer, Type: typ, Domain: "tomandjerry", Name: name, File: file, Status: "active"}
	}
	topic := mkNode("contract", "topic", "battle-results", "")
	svcP := mkNode("service", "service", "arena-api", "")
	producer := mkNode("code", "provider", "arena-api/producer", "arena-api/producer.ts")
	producer.Name = "BattleResultProducer"
	nodes := []snapshot.Node{topic, svcP, producer}
	publish := snapshot.Edge{Kind: "edge", ID: producer.ID + "->" + topic.ID + ":PUBLISHES_TOPIC", Type: "PUBLISHES_TOPIC", From: producer.ID, To: topic.ID, Active: true, Derivation: "hard"}
	edges := []snapshot.Edge{publish}
	var consumeEdges []snapshot.Edge
	for i := 0; i < systems; i++ {
		name := fmt.Sprintf("svc-%02d", i)
		svc := mkNode("service", "service", name, "")
		c := mkNode("code", "provider", name+"/consumer", name+"/consumer.ts")
		c.Name = "Consumer" + name
		nodes = append(nodes, svc, c)
		consumeEdges = append(consumeEdges, snapshot.Edge{Kind: "edge", ID: c.ID + "->" + topic.ID + ":CONSUMES_TOPIC", Type: "CONSUMES_TOPIC", From: c.ID, To: topic.ID, Active: true, Derivation: "hard"})
	}
	hdr := func(seq int) snapshot.Header {
		return snapshot.Header{Kind: "header", Format: snapshot.Format, Version: snapshot.Version, Seq: seq, FactSchema: snapshot.FactSchema, Domains: []string{"tomandjerry"}}
	}
	writeReviewSnapshotCLI(t, dir, &snapshot.Snapshot{Header: hdr(1), Nodes: nodes, Edges: append(append([]snapshot.Edge{}, edges...), consumeEdges...)})
	baseSHA := commitAllCLI(t, dir, "base")
	writeReviewSnapshotCLI(t, dir, &snapshot.Snapshot{Header: hdr(2), Nodes: nodes, Edges: consumeEdges}) // publisher removed
	headSHA := commitAllCLI(t, dir, "head")

	zipPath := filepath.Join(t.TempDir(), "review.zip")
	var out bytes.Buffer
	if code, msg := runReview(context.Background(), &out, dir, reviewCLIOptions{
		Base: baseSHA, Head: headSHA, Format: "markdown", Depth: 4,
		Command: "chronicle review x..y", BundlePath: zipPath,
	}); code != 0 {
		t.Fatalf("runReview exit %d: %s", code, msg)
	}

	st, err := store.Open(filepath.Join(t.TempDir(), "chronicle.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	reg, _ := registry.LoadDefaults()
	var printed bytes.Buffer
	if err := importReviewBundle(graph.New(st, reg), zipPath, t.TempDir(), &printed); err != nil {
		t.Fatalf("import must not abort on %d marks: %v", systems, err)
	}
	if !strings.Contains(printed.String(), "marked systems exceed the limit of 50") {
		t.Fatalf("printed = %q, want the marks warning", printed.String())
	}
	evs, err := st.RecentLiveEvents(live.KindDiagram, 10)
	if err != nil || len(evs) != 1 {
		t.Fatalf("diagram events = %d (err %v), want 1", len(evs), err)
	}
}

// A review whose graph did not change has no relationships to diagram: the
// import still unpacks the bundle, says so, exits clean and publishes nothing.
func TestReviewImport_NoChangedRelationshipsSkipsDiagram(t *testing.T) {
	dir := t.TempDir()
	gitRun(t, dir, "init", "-q", "-b", "main")
	writeReviewManifestCLI(t, dir)
	svc := snapshot.Node{Kind: "node", ID: "service:service:tomandjerry:arena-api", Layer: "service", Type: "service", Domain: "tomandjerry", Name: "arena-api", Status: "active"}
	snap := &snapshot.Snapshot{
		Header: snapshot.Header{Kind: "header", Format: snapshot.Format, Version: snapshot.Version, Seq: 1, FactSchema: snapshot.FactSchema, Domains: []string{"tomandjerry"}},
		Nodes:  []snapshot.Node{svc},
	}
	writeReviewSnapshotCLI(t, dir, snap)
	writePlainFile(t, dir, "notes.ts", "// v1\n")
	baseSHA := commitAllCLI(t, dir, "base")
	writePlainFile(t, dir, "notes.ts", "// v2, comment only\n")
	headSHA := commitAllCLI(t, dir, "head")

	zipPath := filepath.Join(t.TempDir(), "review.zip")
	var out bytes.Buffer
	code, msg := runReview(context.Background(), &out, dir, reviewCLIOptions{
		Base: baseSHA, Head: headSHA, Format: "markdown", Depth: 4,
		Command: "chronicle review x..y", BundlePath: zipPath,
	})
	if code != 0 {
		t.Fatalf("runReview exit %d: %s", code, msg)
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
	chronicle := t.TempDir()
	var printed bytes.Buffer
	if err := importReviewBundle(graph.New(st, reg), zipPath, chronicle, &printed); err != nil {
		t.Fatalf("import: %v", err)
	}
	unpack := filepath.Join(chronicle, "reviews", baseSHA[:7]+".."+headSHA[:7])
	if _, err := os.Stat(filepath.Join(unpack, "review.md")); err != nil {
		t.Errorf("review.md not unpacked: %v", err)
	}
	if !strings.Contains(printed.String(), unpack) || !strings.Contains(printed.String(), "no system-level change in this review — nothing to diagram") {
		t.Errorf("printed = %q", printed.String())
	}
	evs, err := st.RecentLiveEvents(live.KindDiagram, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(evs) != 0 {
		t.Errorf("diagram events = %d, want 0", len(evs))
	}
}
