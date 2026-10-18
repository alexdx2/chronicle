package review

import (
	"context"
	"encoding/json"
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

const (
	pairSvc = "service:service:d:a"
	pairEP  = "contract:endpoint:d:get-x"
)

func pairFacts(edgeFile, nodeFile string) (Fact, Fact) {
	e := &snapshot.Edge{
		Kind: "edge", ID: pairSvc + "->" + pairEP + ":EXPOSES_ENDPOINT", Type: "EXPOSES_ENDPOINT",
		From: pairSvc, To: pairEP, Active: true, Derivation: "hard",
		Evidence: []snapshot.Evidence{{File: edgeFile, Range: "1-2", Extractor: "fact-schema"}},
	}
	n := &snapshot.Node{
		Kind: "node", ID: pairEP, Layer: "contract", Type: "endpoint", Domain: "d", Name: "get-x", Status: "active",
		Evidence: []snapshot.Evidence{{File: nodeFile, Range: "1-2", Extractor: "fact-schema"}},
	}
	return Fact{ID: e.ID, Kind: KindEdge, Edge: e}, Fact{ID: n.ID, Kind: KindNode, Node: n}
}

func TestPartitionLearned_MergePairStaysTogether(t *testing.T) {
	changed := []gitdiff.ChangedFile{{Status: "M", Path: "src/seed.ts"}}
	for _, tc := range []struct{ name, edgeFile, nodeFile string }{
		{"edge inside, node outside", "src/seed.ts", "src/other.ts"},
		{"edge outside, node inside", "src/other.ts", "src/seed.ts"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e, n := pairFacts(tc.edgeFile, tc.nodeFile)
			d := &GraphDelta{Added: []Fact{e, n}}
			code, learned := partitionLearned(d, changed)
			if len(code.Added) != 2 || len(learned) != 0 {
				t.Fatalf("code.Added=%d learned=%d, want 2/0", len(code.Added), len(learned))
			}
			cs := ClassifyDelta(code)
			if len(cs) != 1 || cs[0].Kind != ContractAdded {
				t.Fatalf("ClassifyDelta = %+v, want one ContractAdded", cs)
			}
		})
	}
}

func TestPartitionLearned_MergePairBothOutsideIsOneLearnedFinding(t *testing.T) {
	e, n := pairFacts("src/b.ts", "src/c.ts")
	d := &GraphDelta{Added: []Fact{e, n}}
	code, learned := partitionLearned(d, []gitdiff.ChangedFile{{Status: "M", Path: "src/seed.ts"}})
	if len(code.Added) != 0 {
		t.Errorf("code.Added = %d, want 0", len(code.Added))
	}
	if len(learned) != 1 || learned[0].Change.Kind != ContractAdded || len(learned[0].Change.Facts) != 2 {
		t.Fatalf("learned = %+v", learned)
	}
	if learned[0].File != "src/b.ts" || len(learned[0].Files) != 2 {
		t.Errorf("File=%q Files=%v", learned[0].File, learned[0].Files)
	}
	r := &ReviewResult{Findings: learnedFindings(learned, fakeImpactQuerier{}, fakeImpactQuerier{})}
	if got := verdictFrom(r.Findings).Learned; got != 1 {
		t.Errorf("Verdict.Learned = %d, want 1", got)
	}
}

func TestPartitionLearned_RemovedPairIsLearnedRemoved(t *testing.T) {
	e, n := pairFacts("src/b.ts", "src/b.ts")
	d := &GraphDelta{Removed: []Fact{e, n}}
	code, learned := partitionLearned(d, []gitdiff.ChangedFile{{Status: "M", Path: "src/seed.ts"}})
	if len(code.Removed) != 0 || len(learned) != 1 || learned[0].Change.Kind != ContractRemoved {
		t.Fatalf("code.Removed=%d learned=%+v", len(code.Removed), learned)
	}
	// a plain removed edge is learned too, with the base seed
	d = &GraphDelta{Removed: []Fact{learnedEdge("src/b.ts")}}
	_, learned = partitionLearned(d, []gitdiff.ChangedFile{{Status: "M", Path: "src/seed.ts"}})
	if len(learned) != 1 || learned[0].Change.Kind != IntegrationRemoved || learned[0].Change.Seed != seedBase {
		t.Fatalf("learned = %+v", learned)
	}
}

func TestPartitionLearned_SortedByFileThenFact(t *testing.T) {
	d := &GraphDelta{Added: []Fact{learnedEdge("src/z.ts"), learnedEdge("src/a.ts")}}
	_, learned := partitionLearned(d, nil)
	if len(learned) != 2 || learned[0].File != "src/a.ts" || learned[1].File != "src/z.ts" {
		t.Fatalf("learned = %+v", learned)
	}
}

// TestReview_MixedDelta_LearnedSectionAfterTable: one real change (svc/a.ts
// is in the diff) and one learned fact (svc/c.ts is not).
func TestReview_MixedDelta_LearnedSectionAfterTable(t *testing.T) {
	dir := initGitRepoT(t)
	scopedManifestT(t, dir, "svc/**")
	writeFileT(t, dir, "svc/a.ts", "v1\n")
	writeFileT(t, dir, "svc/c.ts", "c\n")
	blobA1, _ := gitutil.FileBlobSHA(dir, "svc/a.ts")
	blobC, _ := gitutil.FileBlobSHA(dir, "svc/c.ts")
	cov := func(path, blob string) snapshot.Coverage {
		return snapshot.Coverage{Kind: "coverage", Path: path, Blob: blob, Extractors: []snapshot.ExtractorRun{{ID: "fact-schema", Version: "v1", Status: "ok"}}}
	}
	mk := func(n string) snapshot.Node {
		return snapshot.Node{Kind: "node", ID: "service:service:d:" + n, Layer: "service", Type: "service", Domain: "d", Name: n, Status: "active"}
	}
	a, b, c, dd := mk("svca"), mk("svcb"), mk("svcc"), mk("svcd")
	hdr := func(seq int) snapshot.Header {
		return snapshot.Header{Kind: "header", Format: snapshot.Format, Version: snapshot.Version, Seq: seq, FactSchema: snapshot.FactSchema, Domains: []string{"d"}}
	}
	nodes := []snapshot.Node{a, b, c, dd}
	writeFileT(t, dir, ".depbot/graph.jsonl", encodeSnapshotT(t, &snapshot.Snapshot{Header: hdr(1), Nodes: nodes, Coverage: []snapshot.Coverage{cov("svc/a.ts", blobA1), cov("svc/c.ts", blobC)}}))
	baseSHA := commitAllT(t, dir, "base")

	writeFileT(t, dir, "svc/a.ts", "v2\n")
	blobA2, _ := gitutil.FileBlobSHA(dir, "svc/a.ts")
	edge := func(from, to snapshot.Node, file string) snapshot.Edge {
		return snapshot.Edge{
			Kind: "edge", ID: from.ID + "->" + to.ID + ":CALLS_SERVICE", Type: "CALLS_SERVICE", From: from.ID, To: to.ID,
			Active: true, Derivation: "hard",
			Evidence: []snapshot.Evidence{{File: file, Range: "1-1", Extractor: "fact-schema"}},
		}
	}
	writeFileT(t, dir, ".depbot/graph.jsonl", encodeSnapshotT(t, &snapshot.Snapshot{
		Header: hdr(2), Nodes: nodes,
		Edges:    []snapshot.Edge{edge(a, b, "svc/a.ts"), edge(c, dd, "svc/c.ts")},
		Coverage: []snapshot.Coverage{cov("svc/a.ts", blobA2), cov("svc/c.ts", blobC)},
	}))
	headSHA := commitAllT(t, dir, "head")

	headSrc := SnapshotSource{RepoRoot: dir, Rev: headSHA}
	result, err := Review(context.Background(), ReviewInput{
		BaseSource: SnapshotSource{RepoRoot: dir, Rev: baseSHA}, HeadSource: headSrc, RepoRoot: dir,
	})
	if err != nil {
		t.Fatalf("Review: %v", err)
	}
	if result.Verdict.Learned != 1 || len(result.ChangedRelationships()) != 1 {
		t.Fatalf("Learned=%d ChangedRelationships=%d", result.Verdict.Learned, len(result.ChangedRelationships()))
	}
	md, err := RenderMarkdown(result, MarkdownOptions{})
	if err != nil {
		t.Fatal(err)
	}
	tableAt := strings.Index(md, "### Changed system relationships")
	learnedAt := strings.Index(md, "Graph learned 1 facts")
	if tableAt < 0 || learnedAt < tableAt {
		t.Fatalf("learned section must follow the relationships table:\n%s", md)
	}
	table := md[tableAt:learnedAt]
	if !strings.Contains(table, "svca") || strings.Contains(table, "svcc") || strings.Contains(table, "svcd") {
		t.Errorf("table content wrong:\n%s", table)
	}
	if rows := strings.Count(table, "\n| ") - 2; rows != 1 { // minus header and separator
		t.Errorf("table rows = %d, want 1:\n%s", rows, table)
	}
	if !strings.Contains(md[learnedAt:], "svc/c.ts") {
		t.Errorf("learned section lacks svc/c.ts:\n%s", md)
	}

	head, err := headSrc.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	dg, err := CompactDiagram(result, head, DiagramOptions{})
	if err != nil {
		t.Fatal(err)
	}
	spec, _ := json.Marshal(dg.Spec)
	if strings.Contains(string(spec), "svcc") || strings.Contains(string(spec), "svcd") {
		t.Errorf("learned fact leaked into the diagram spec: %s", spec)
	}
	if !strings.Contains(string(spec), "svca") {
		t.Errorf("diagram spec lacks the changed system: %s", spec)
	}
}

// The synthetic target node of a merge pair has no evidence file: it must not
// veto "learned" for an edge whose file lies outside the diff, and it must
// not rescue an edge whose file is inside it.
func TestPartitionLearned_FilelessPartnerDoesNotVeto(t *testing.T) {
	changed := []gitdiff.ChangedFile{{Status: "M", Path: "src/seed.ts"}}

	e, n := pairFacts("src/other.ts", "")
	code, learned := partitionLearned(&GraphDelta{Added: []Fact{e, n}}, changed)
	if len(code.Added) != 0 || len(learned) != 1 {
		t.Fatalf("outside edge + fileless node: code.Added=%d learned=%d, want 0/1", len(code.Added), len(learned))
	}
	if len(learned[0].Change.Facts) != 2 || learned[0].File != "src/other.ts" {
		t.Errorf("learned = %+v, want the pair attributed to src/other.ts", learned[0])
	}

	e, n = pairFacts("src/seed.ts", "")
	code, learned = partitionLearned(&GraphDelta{Added: []Fact{e, n}}, changed)
	if len(code.Added) != 2 || len(learned) != 0 {
		t.Fatalf("inside edge + fileless node: code.Added=%d learned=%d, want 2/0", len(code.Added), len(learned))
	}
}
