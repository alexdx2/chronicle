package review

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alexdx2/chronicle-core/graph"
	"github.com/alexdx2/chronicle-core/registry"
	"github.com/alexdx2/chronicle-core/snapshot"
)

func TestLoadGraphContext_GivesUpWhenContextEnds(t *testing.T) {
	release, closed := make(chan struct{}), make(chan struct{})
	old := snapshotLoad
	snapshotLoad = func(*snapshot.Snapshot, *registry.Registry, snapshot.LoadOptions) (*graph.Graph, func() error, error) {
		<-release // a build that takes "forever"
		return nil, func() error { close(closed); return nil }, nil
	}
	defer func() { snapshotLoad = old }()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, _, err := LoadGraphContext(ctx, &snapshot.Snapshot{}, nil, snapshot.LoadOptions{})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("want DeadlineExceeded, got %v", err)
	}
	if d := time.Since(start); d > time.Second {
		t.Fatalf("LoadGraphContext waited %s for a build that ctx had already given up on", d)
	}
	close(release)
	select {
	case <-closed: // the abandoned graph was closed, nothing leaks
	case <-time.After(2 * time.Second):
		t.Fatal("abandoned build was never closed")
	}
}

func TestLoadGraphContext_ReturnsGraphWhenFast(t *testing.T) {
	reg, err := registry.LoadDefaults()
	if err != nil {
		t.Fatal(err)
	}
	snap := &snapshot.Snapshot{Header: snapshot.Header{
		Kind: "header", Format: snapshot.Format, Version: snapshot.Version, Seq: 1, FactSchema: snapshot.FactSchema,
		Domains: []string{"d"},
	}}
	g, closer, err := LoadGraphContext(context.Background(), snap, reg, snapshot.LoadOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if g == nil || closer == nil {
		t.Fatalf("graph=%v closer nil=%v", g, closer == nil)
	}
	if err := closer(); err != nil {
		t.Fatalf("closer: %v", err)
	}
}

func TestLoadGraphContext_AlreadyCancelledStartsNothing(t *testing.T) {
	var calls atomic.Int32
	old := snapshotLoad
	snapshotLoad = func(*snapshot.Snapshot, *registry.Registry, snapshot.LoadOptions) (*graph.Graph, func() error, error) {
		calls.Add(1)
		return nil, func() error { return nil }, nil
	}
	defer func() { snapshotLoad = old }()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := LoadGraphContext(ctx, &snapshot.Snapshot{}, nil, snapshot.LoadOptions{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("want Canceled, got %v", err)
	}
	time.Sleep(20 * time.Millisecond)
	if n := calls.Load(); n != 0 {
		t.Fatalf("snapshotLoad called %d times, want 0", n)
	}
}

// slowBuild makes snapshotLoad block until the returned release func is called.
func slowBuild(t *testing.T) (release func()) {
	t.Helper()
	ch := make(chan struct{})
	old := snapshotLoad
	snapshotLoad = func(*snapshot.Snapshot, *registry.Registry, snapshot.LoadOptions) (*graph.Graph, func() error, error) {
		<-ch
		return nil, func() error { return nil }, nil
	}
	var once = false
	rel := func() {
		if !once {
			once = true
			close(ch)
		}
	}
	t.Cleanup(func() { rel(); snapshotLoad = old })
	return rel
}

func TestFileSource_LoadHonoursContextDuringBuild(t *testing.T) {
	dir := initGitRepoT(t)
	writeManifestT(t, dir)
	writeFileT(t, dir, ".depbot/graph.jsonl", encodeSnapshotT(t, fileSourceSnap(1, "")))
	slowBuild(t)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := FileSource{RepoRoot: dir, Path: filepath.Join(dir, ".depbot", "graph.jsonl"), SkipFreshness: true}.Load(ctx)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("want DeadlineExceeded, got %v", err)
	}
	if d := time.Since(start); d > time.Second {
		t.Fatalf("FileSource.Load waited %s", d)
	}
}

func TestSnapshotSource_LoadHonoursContextDuringBuild(t *testing.T) {
	dir := initGitRepoT(t)
	writeManifestT(t, dir)
	writeFileT(t, dir, ".depbot/graph.jsonl", encodeSnapshotT(t, fileSourceSnap(1, "")))
	head := commitAllT(t, dir, "graph")
	slowBuild(t)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := SnapshotSource{RepoRoot: dir, Rev: head}.Load(ctx)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("want DeadlineExceeded, got %v", err)
	}
	if d := time.Since(start); d > time.Second {
		t.Fatalf("SnapshotSource.Load waited %s", d)
	}
}

func TestLoadGraphContext_PanicBecomesError(t *testing.T) {
	old := snapshotLoad
	snapshotLoad = func(*snapshot.Snapshot, *registry.Registry, snapshot.LoadOptions) (*graph.Graph, func() error, error) {
		panic("boom")
	}
	defer func() { snapshotLoad = old }()
	_, _, err := LoadGraphContext(context.Background(), &snapshot.Snapshot{}, nil, snapshot.LoadOptions{})
	if err == nil || !strings.Contains(err.Error(), "graph load panicked: boom") {
		t.Fatalf("want panic error, got %v", err)
	}
}
