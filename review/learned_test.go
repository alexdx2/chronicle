package review

import (
	"context"
	"strings"
	"testing"

	"github.com/alexdx2/chronicle-core/gitdiff"
	"github.com/alexdx2/chronicle-core/gitutil"
	"github.com/alexdx2/chronicle-core/snapshot"
)

func learnedEdge(file string) Fact {
	e := &snapshot.Edge{
		Kind: "edge", ID: "service:service:d:a->service:service:d:b:CALLS_SERVICE", Type: "CALLS_SERVICE",
		From: "service:service:d:a", To: "service:service:d:b", Active: true, Derivation: "hard",
	}
	if file != "" {
		e.Evidence = []snapshot.Evidence{{File: file, Range: "1-2", Extractor: "fact-schema"}}
	}
	return Fact{ID: e.ID, Kind: KindEdge, Edge: e}
}

func TestPartitionLearned_NeighbourFactIsLearned(t *testing.T) {
	d := &GraphDelta{Added: []Fact{learnedEdge("src/spec.ts")}}
	code, learned := partitionLearned(d, []gitdiff.ChangedFile{{Status: "M", Path: "src/seed.ts"}})
	if len(code.Added) != 0 {
		t.Errorf("code.Added = %d, want 0", len(code.Added))
	}
	if len(learned) != 1 || learned[0].File != "src/spec.ts" || learned[0].Change.Kind != IntegrationAdded {
		t.Fatalf("learned = %+v", learned)
	}
	if len(d.Added) != 1 {
		t.Error("input delta was modified")
	}
}

func TestPartitionLearned_ChangedFileFactStaysChanged(t *testing.T) {
	d := &GraphDelta{Added: []Fact{learnedEdge("src/seed.ts")}}
	code, learned := partitionLearned(d, []gitdiff.ChangedFile{{Status: "M", Path: "src/seed.ts"}})
	if len(code.Added) != 1 || len(learned) != 0 {
		t.Errorf("code.Added=%d learned=%d, want 1/0", len(code.Added), len(learned))
	}
}

func TestPartitionLearned_RenameAndDeleteCountAsDiff(t *testing.T) {
	d := &GraphDelta{
		Added:   []Fact{learnedEdge("src/old.ts")},
		Removed: []Fact{learnedEdge("src/gone.ts")},
	}
	changed := []gitdiff.ChangedFile{
		{Status: "R", Path: "src/new.ts", OldPath: "src/old.ts"},
		{Status: "D", Path: "src/gone.ts"},
	}
	code, learned := partitionLearned(d, changed)
	if len(code.Added) != 1 || len(code.Removed) != 1 || len(learned) != 0 {
		t.Errorf("code.Added=%d code.Removed=%d learned=%d, want 1/1/0", len(code.Added), len(code.Removed), len(learned))
	}
}

func TestPartitionLearned_NoEvidenceFileKeepsToday(t *testing.T) {
	d := &GraphDelta{Added: []Fact{learnedEdge("")}}
	code, learned := partitionLearned(d, []gitdiff.ChangedFile{{Status: "M", Path: "src/seed.ts"}})
	if len(code.Added) != 1 || len(learned) != 0 {
		t.Errorf("code.Added=%d learned=%d, want 1/0", len(code.Added), len(learned))
	}
}

func TestPartitionLearned_ChangedChecksBothSides(t *testing.T) {
	before, after := learnedEdge("src/spec.ts"), learnedEdge("src/seed.ts")
	d := &GraphDelta{Changed: []Changed{{ID: before.ID, Kind: KindEdge, Before: before, After: after}}}
	code, learned := partitionLearned(d, []gitdiff.ChangedFile{{Status: "M", Path: "src/seed.ts"}})
	if len(code.Changed) != 1 || len(learned) != 0 {
		t.Errorf("code.Changed=%d learned=%d, want 1/0", len(code.Changed), len(learned))
	}
}

// TestReview_LearnedOnly_IsNoSystemChange: head adds an edge whose evidence
// lives in svc/b.ts, which the diff never touches; the diff touches svc/a.ts
// (fresh coverage). The report is "No system-level change." plus the learned
// section.
func TestReview_LearnedOnly_IsNoSystemChange(t *testing.T) {
	dir := initGitRepoT(t)
	scopedManifestT(t, dir, "svc/**")

	writeFileT(t, dir, "svc/a.ts", "v1\n")
	writeFileT(t, dir, "svc/b.ts", "b\n")
	blobA1, _ := gitutil.FileBlobSHA(dir, "svc/a.ts")
	blobB, _ := gitutil.FileBlobSHA(dir, "svc/b.ts")
	cov := func(path, blob string) snapshot.Coverage {
		return snapshot.Coverage{Kind: "coverage", Path: path, Blob: blob, Extractors: []snapshot.ExtractorRun{{ID: "fact-schema", Version: "v1", Status: "ok"}}}
	}
	svcA := snapshot.Node{Kind: "node", ID: "service:service:d:svca", Layer: "service", Type: "service", Domain: "d", Name: "svcA", Status: "active"}
	svcB := snapshot.Node{Kind: "node", ID: "service:service:d:svcb", Layer: "service", Type: "service", Domain: "d", Name: "svcB", Status: "active"}
	hdr := func(seq int) snapshot.Header {
		return snapshot.Header{Kind: "header", Format: snapshot.Format, Version: snapshot.Version, Seq: seq, FactSchema: snapshot.FactSchema, Domains: []string{"d"}}
	}
	baseSnap := &snapshot.Snapshot{Header: hdr(1), Nodes: []snapshot.Node{svcA, svcB}, Coverage: []snapshot.Coverage{cov("svc/a.ts", blobA1), cov("svc/b.ts", blobB)}}
	writeFileT(t, dir, ".depbot/graph.jsonl", encodeSnapshotT(t, baseSnap))
	baseSHA := commitAllT(t, dir, "base")

	writeFileT(t, dir, "svc/a.ts", "v2\n")
	blobA2, _ := gitutil.FileBlobSHA(dir, "svc/a.ts")
	edge := snapshot.Edge{
		Kind: "edge", ID: svcA.ID + "->" + svcB.ID + ":CALLS_SERVICE", Type: "CALLS_SERVICE", From: svcA.ID, To: svcB.ID,
		Active: true, Derivation: "hard",
		Evidence: []snapshot.Evidence{{File: "svc/b.ts", Range: "1-1", Extractor: "fact-schema"}},
	}
	headSnap := &snapshot.Snapshot{Header: hdr(2), Nodes: []snapshot.Node{svcA, svcB}, Edges: []snapshot.Edge{edge}, Coverage: []snapshot.Coverage{cov("svc/a.ts", blobA2), cov("svc/b.ts", blobB)}}
	writeFileT(t, dir, ".depbot/graph.jsonl", encodeSnapshotT(t, headSnap))
	headSHA := commitAllT(t, dir, "head")

	result, err := Review(context.Background(), ReviewInput{
		BaseSource: SnapshotSource{RepoRoot: dir, Rev: baseSHA},
		HeadSource: SnapshotSource{RepoRoot: dir, Rev: headSHA},
		RepoRoot:   dir,
	})
	if err != nil {
		t.Fatalf("Review: %v", err)
	}
	v := result.Verdict
	if v.Learned != 1 || v.SystemsAffected != 0 || v.IntegrationsAdded != 0 || !v.NoSystemChange {
		t.Fatalf("Verdict = %+v", v)
	}
	if len(result.ChangedRelationships()) != 0 {
		t.Errorf("ChangedRelationships = %+v, want none", result.ChangedRelationships())
	}
	md, err := RenderMarkdown(result, MarkdownOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(md, "**No system-level change.**") {
		t.Errorf("markdown does not start with the no-change verdict:\n%s", md)
	}
	if !strings.Contains(md, "Graph learned 1 facts") || !strings.Contains(md, "svc/b.ts") {
		t.Errorf("markdown lacks learned section:\n%s", md)
	}
}
