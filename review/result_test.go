package review

import (
	"context"
	"testing"

	"github.com/alexdx2/chronicle-core/gitutil"
	"github.com/alexdx2/chronicle-core/snapshot"
)

// fakeGraphSource implements GraphSource by returning a hand-built
// *VersionGraph verbatim — used for the missing-graph tests, which need no
// filesystem or git at all (Review returns before either side's Commit/
// RepoRoot is ever touched).
type fakeGraphSource struct {
	vg  *VersionGraph
	err error
}

func (f fakeGraphSource) Load(ctx context.Context) (*VersionGraph, error) { return f.vg, f.err }

func noopCloseVG() error { return nil }

// --- Origin: missing ------------------------------------------------------

// TestReview_NoGraphOnHead: HeadSource returns Origin:"missing" -> err==nil,
// a single warning with the brief's own exact text, empty Findings, and
// Verdict.NoSystemChange hardcoded false (not computed from empty buckets).
func TestReview_NoGraphOnHead(t *testing.T) {
	head := &VersionGraph{Origin: OriginMissing, Close: noopCloseVG}
	base := &VersionGraph{Origin: OriginMissing, Close: noopCloseVG}

	result, err := Review(context.Background(), ReviewInput{
		BaseSource: fakeGraphSource{vg: base},
		HeadSource: fakeGraphSource{vg: head},
	})
	if err != nil {
		t.Fatalf("Review: %v", err)
	}
	if len(result.Warnings) != 1 || result.Warnings[0] != headMissingGraphMessage {
		t.Errorf("Warnings = %+v, want exactly [%q]", result.Warnings, headMissingGraphMessage)
	}
	if len(result.Findings) != 0 {
		t.Errorf("Findings = %+v, want empty", result.Findings)
	}
	if result.Verdict.NoSystemChange {
		t.Error("Verdict.NoSystemChange = true, want false (hardcoded per the brief, not computed from empty buckets)")
	}
}

// TestReview_NoGraphOnBase: head is checked first (per Review's own doc
// comment) — this proves the base-side branch is still reachable, and gets
// its OWN distinct message, when head is fine but base predates the graph.
func TestReview_NoGraphOnBase(t *testing.T) {
	head := &VersionGraph{Origin: OriginSnapshot, Close: noopCloseVG, Export: &snapshot.Snapshot{}}
	base := &VersionGraph{Origin: OriginMissing, Close: noopCloseVG}

	result, err := Review(context.Background(), ReviewInput{
		BaseSource: fakeGraphSource{vg: base},
		HeadSource: fakeGraphSource{vg: head},
	})
	if err != nil {
		t.Fatalf("Review: %v", err)
	}
	if len(result.Warnings) != 1 || result.Warnings[0] != baseMissingGraphMessage {
		t.Errorf("Warnings = %+v, want exactly [%q]", result.Warnings, baseMissingGraphMessage)
	}
	if result.Verdict.NoSystemChange {
		t.Error("Verdict.NoSystemChange = true, want false")
	}
}

// --- Verdict from real Diff/ClassifyDelta, fake ImpactQuerier -------------

// scopedManifestT writes a manifest whose ONE domain's scan scope is
// includePattern only — deliberately narrower than graph_test.go's own
// writeManifestT ("**"), which (confirmed directly: manifest.MatchGlob has
// no special-casing for dotfiles) would also put .depbot/graph.jsonl itself
// in scan scope. Since graph.jsonl changes on every commit these tests make
// by construction, "**" would make EVERY such test see a permanent, spurious
// Unknown finding for the graph file having no Coverage of itself. Scoping
// to the tests' own synthetic source paths avoids that entirely.
func scopedManifestT(t *testing.T, dir, includePattern string) {
	t.Helper()
	writeFileT(t, dir, ".depbot/chronicle.domain.yaml", ""+
		"domains:\n"+
		"  - name: d\n"+
		"    scan:\n"+
		"      include:\n"+
		"        - \""+includePattern+"\"\n")
}

// TestReview_Verdict_NoSystemChange: the only change between base and head
// is a Touched INJECTS edge (identical fact both sides, evidence range
// overlapping a real hunk) — classify.go's own rule turns that into
// ConsumerChanged with an empty Downstream (the fake querier returns no
// impacts), so ChangedRelationships/WhoMayBeAffected must both be empty and
// NoSystemChange must be true.
func TestReview_Verdict_NoSystemChange(t *testing.T) {
	dir := initGitRepoT(t)
	scopedManifestT(t, dir, "svc/**")

	writeFileT(t, dir, "svc/a.ts", "v1\n")
	blob1, err := gitutil.FileBlobSHA(dir, "svc/a.ts")
	if err != nil {
		t.Fatalf("FileBlobSHA: %v", err)
	}
	svcA := snapshot.Node{Kind: "node", ID: "service:service:d:svca", Layer: "service", Type: "service", Domain: "d", Name: "svcA", Status: "active"}
	svcB := snapshot.Node{Kind: "node", ID: "service:service:d:svcb", Layer: "service", Type: "service", Domain: "d", Name: "svcB", Status: "active"}
	edge := snapshot.Edge{
		Kind: "edge", ID: svcA.ID + "->" + svcB.ID + ":INJECTS", Type: "INJECTS", From: svcA.ID, To: svcB.ID,
		Active: true, Derivation: "hard",
		Evidence: []snapshot.Evidence{{File: "svc/a.ts", Range: "1-1", Extractor: "fact-schema"}},
	}
	baseSnap := &snapshot.Snapshot{
		Header:   snapshot.Header{Kind: "header", Format: snapshot.Format, Version: snapshot.Version, Seq: 1, FactSchema: snapshot.FactSchema, Domains: []string{"d"}},
		Nodes:    []snapshot.Node{svcA, svcB},
		Edges:    []snapshot.Edge{edge},
		Coverage: []snapshot.Coverage{{Kind: "coverage", Path: "svc/a.ts", Blob: blob1, Extractors: []snapshot.ExtractorRun{{ID: "fact-schema", Version: "v1", Status: "ok"}}}},
	}
	writeFileT(t, dir, ".depbot/graph.jsonl", encodeSnapshotT(t, baseSnap))
	baseSHA := commitAllT(t, dir, "base")

	writeFileT(t, dir, "svc/a.ts", "v2\n")
	blob2, err := gitutil.FileBlobSHA(dir, "svc/a.ts")
	if err != nil {
		t.Fatalf("FileBlobSHA: %v", err)
	}
	headSnap := &snapshot.Snapshot{
		Header:   snapshot.Header{Kind: "header", Format: snapshot.Format, Version: snapshot.Version, Seq: 2, FactSchema: snapshot.FactSchema, Domains: []string{"d"}},
		Nodes:    []snapshot.Node{svcA, svcB},
		Edges:    []snapshot.Edge{edge}, // identical fact: unchanged
		Coverage: []snapshot.Coverage{{Kind: "coverage", Path: "svc/a.ts", Blob: blob2, Extractors: []snapshot.ExtractorRun{{ID: "fact-schema", Version: "v1", Status: "ok"}}}},
	}
	writeFileT(t, dir, ".depbot/graph.jsonl", encodeSnapshotT(t, headSnap))
	headSHA := commitAllT(t, dir, "head")

	result, err := Review(context.Background(), ReviewInput{
		BaseSource:  SnapshotSource{RepoRoot: dir, Rev: baseSHA},
		HeadSource:  SnapshotSource{RepoRoot: dir, Rev: headSHA},
		RepoRoot:    dir,
		Depth:       4,
		MakeQuerier: func(*VersionGraph) ImpactQuerier { return fakeImpactQuerier{} },
	})
	if err != nil {
		t.Fatalf("Review: %v", err)
	}

	if got := result.ChangedRelationships(); len(got) != 0 {
		t.Errorf("ChangedRelationships = %+v, want empty", got)
	}
	if got := result.WhoMayBeAffected(); len(got) != 0 {
		t.Errorf("WhoMayBeAffected = %+v, want empty", got)
	}
	if got := result.CodeChangedGraphUnchanged(); len(got) != 1 {
		t.Errorf("CodeChangedGraphUnchanged = %+v, want exactly 1 (the touched INJECTS -> ConsumerChanged)", got)
	}
	if result.Verdict.UnknownFiles != 0 {
		t.Errorf("Verdict.UnknownFiles = %d, want 0", result.Verdict.UnknownFiles)
	}
	if !result.Verdict.NoSystemChange {
		t.Errorf("Verdict.NoSystemChange = false, want true; findings=%+v", result.Findings)
	}
}

// TestReview_Verdict_Counts: synthetic data mirroring the mockup — 1
// integration added, 1 removed, 3 systems affected (2 from the added
// integration's QueryImpact + 1 from the removed one's, all distinct keys),
// 1 unknown file (a brand-new file with no Coverage) — every Verdict number
// must match, computed from the very buckets a report would render.
func TestReview_Verdict_Counts(t *testing.T) {
	dir := initGitRepoT(t)
	scopedManifestT(t, dir, "svc/**")

	svcA := snapshot.Node{Kind: "node", ID: "service:service:d:svca", Layer: "service", Type: "service", Domain: "d", Name: "svcA", Status: "active"}
	svcB := snapshot.Node{Kind: "node", ID: "service:service:d:svcb", Layer: "service", Type: "service", Domain: "d", Name: "svcB", Status: "active"}
	svcC := snapshot.Node{Kind: "node", ID: "service:service:d:svcc", Layer: "service", Type: "service", Domain: "d", Name: "svcC", Status: "active"}
	svcD := snapshot.Node{Kind: "node", ID: "service:service:d:svcd", Layer: "service", Type: "service", Domain: "d", Name: "svcD", Status: "active"}
	allSvcNodes := []snapshot.Node{svcA, svcB, svcC, svcD}

	addedEdge := snapshot.Edge{Kind: "edge", ID: svcA.ID + "->" + svcB.ID + ":CALLS_SERVICE", Type: "CALLS_SERVICE", From: svcA.ID, To: svcB.ID, Active: true, Derivation: "hard"}
	removedEdge := snapshot.Edge{Kind: "edge", ID: svcC.ID + "->" + svcD.ID + ":CALLS_SERVICE", Type: "CALLS_SERVICE", From: svcC.ID, To: svcD.ID, Active: true, Derivation: "hard"}

	baseSnap := &snapshot.Snapshot{
		Header: snapshot.Header{Kind: "header", Format: snapshot.Format, Version: snapshot.Version, Seq: 1, FactSchema: snapshot.FactSchema, Domains: []string{"d"}},
		Nodes:  allSvcNodes,
		Edges:  []snapshot.Edge{removedEdge},
	}
	writeFileT(t, dir, ".depbot/graph.jsonl", encodeSnapshotT(t, baseSnap))
	baseSHA := commitAllT(t, dir, "base")

	headSnap := &snapshot.Snapshot{
		Header: snapshot.Header{Kind: "header", Format: snapshot.Format, Version: snapshot.Version, Seq: 2, FactSchema: snapshot.FactSchema, Domains: []string{"d"}},
		Nodes:  allSvcNodes,
		Edges:  []snapshot.Edge{addedEdge},
	}
	writeFileT(t, dir, ".depbot/graph.jsonl", encodeSnapshotT(t, headSnap))
	writeFileT(t, dir, "svc/unknown.ts", "new file, never scanned\n") // brand new -> Unscanned on head, absent on base
	headSHA := commitAllT(t, dir, "head")

	// systemA/systemC are svcA/svcC's OWN collapsed identity — deliberately
	// distinct from every system they impact (systemX/Y, systemZ). Reusing
	// one of the impacted systems as the origin's own identity would trip
	// the "never both changed and affected" self-collision filter
	// (dropSelfAffected, task-18-report.md's Bug 3 fix) and silently drop a
	// system this test intends to count — this fixture's own narrative is 3
	// DISTINCT affected systems, none of which is the origin itself.
	systemA := SystemRef{Kind: kindService, Key: "sys:a", Name: "A"}
	systemC := SystemRef{Kind: kindService, Key: "sys:c", Name: "C"}
	systemX := SystemRef{Kind: kindService, Key: "sys:x", Name: "X"}
	systemY := SystemRef{Kind: kindService, Key: "sys:y", Name: "Y"}
	systemZ := SystemRef{Kind: kindService, Key: "sys:z", Name: "Z"}
	systemB := SystemRef{Kind: kindService, Key: "sys:b", Name: "B"}
	systemD := SystemRef{Kind: kindService, Key: "sys:d", Name: "D"}

	headFake := fakeImpactQuerier{
		impacts: map[string][]SystemImpact{
			svcA.ID: {
				{System: systemX, Path: []PathStep{{Node: "x", Edge: "CALLS_SERVICE"}}},
				{System: systemY, Path: []PathStep{{Node: "y", Edge: "CALLS_SERVICE"}}},
			},
		},
		collapse: map[string]SystemRef{svcA.ID: systemA, svcB.ID: systemB},
	}
	baseFake := fakeImpactQuerier{
		impacts: map[string][]SystemImpact{
			svcC.ID: {{System: systemZ, Path: []PathStep{{Node: "z", Edge: "CALLS_SERVICE"}}}},
		},
		collapse: map[string]SystemRef{svcC.ID: systemC, svcD.ID: systemD},
	}

	result, err := Review(context.Background(), ReviewInput{
		BaseSource: SnapshotSource{RepoRoot: dir, Rev: baseSHA},
		HeadSource: SnapshotSource{RepoRoot: dir, Rev: headSHA},
		RepoRoot:   dir,
		Depth:      4,
		MakeQuerier: func(vg *VersionGraph) ImpactQuerier {
			if vg.Commit == baseSHA {
				return baseFake
			}
			return headFake
		},
	})
	if err != nil {
		t.Fatalf("Review: %v", err)
	}

	v := result.Verdict
	if v.IntegrationsAdded != 1 {
		t.Errorf("IntegrationsAdded = %d, want 1", v.IntegrationsAdded)
	}
	if v.DependenciesRemoved != 1 {
		t.Errorf("DependenciesRemoved = %d, want 1", v.DependenciesRemoved)
	}
	if v.SystemsAffected != 3 {
		t.Errorf("SystemsAffected = %d, want 3; WhoMayBeAffected=%+v", v.SystemsAffected, result.WhoMayBeAffected())
	}
	if v.UnknownFiles != 1 {
		t.Errorf("UnknownFiles = %d, want 1; findings=%+v", v.UnknownFiles, result.Findings)
	}
	if v.Repositories != 1 {
		t.Errorf("Repositories = %d, want 1 (all 3 affected systems have Repo=\"\", counted as one value)", v.Repositories)
	}
	if v.NoSystemChange {
		t.Error("NoSystemChange = true, want false")
	}
}

// --- End-to-end on a real graph engine -------------------------------------

// TestReview_E2E_Fixture exercises the full real pipeline — SnapshotSource
// over two real git commits, real Diff/ClassifyDelta, and (no MakeQuerier
// override) the REAL CoreImpactQuerier over a REAL *graph.Graph — on a
// small hand-built graph in the same shape/spirit as the tom-and-jerry
// fixture's battle-results pipeline (arena-api publishes, scoreboard-api
// consumes), the same scenario task-16's DoD exercises through the CLI/
// markdown, here driven directly through Review with no markdown/CLI
// involved (see task-11-report.md for why this test builds its own tiny
// two-commit repo rather than depending on the real fixture's git history:
// fixtures/tom-and-jerry has no committed .depbot/graph.jsonl of its own to
// diff across two real commits).
//
// The change: arena-api stops publishing to battle-results (head removes
// the PUBLISHES_TOPIC edge — scoreboard-api's consumer, unchanged on both
// sides, is still subscribed, so this is a real "who loses a producer"
// signal) and spectators-api starts calling scoreboard-api directly (head
// adds a CALLS_SERVICE edge). Traced by hand against the real ownership
// rules (see impact.go/viewmodel's path-prefix and name-segment
// attribution): IntegrationsAdded=1, DependenciesRemoved=1,
// SystemsAffected=1 (scoreboard-api, via the removed publish's
// TopicSubscribers walk on base — the added call's own QueryImpact(caller)
// is empty, nothing points at a brand-new caller node).
func TestReview_E2E_Fixture(t *testing.T) {
	dir := initGitRepoT(t)
	// Scope matches nothing this test creates: this is a pure Added/Removed
	// scenario with no Touched facts, so no source files or Coverage are
	// needed at all — an always-empty scan scope keeps Verdict.UnknownFiles
	// at 0 without having to reason about it further.
	scopedManifestT(t, dir, "zzz-nomatch/**")

	svcArena := snapshot.Node{Kind: "node", ID: "service:service:tomandjerry:arena-api", Layer: "service", Type: "service", Domain: "tomandjerry", Name: "arena-api", Status: "active"}
	svcScoreboard := snapshot.Node{Kind: "node", ID: "service:service:tomandjerry:scoreboard-api", Layer: "service", Type: "service", Domain: "tomandjerry", Name: "scoreboard-api", Status: "active"}
	svcSpectators := snapshot.Node{Kind: "node", ID: "service:service:tomandjerry:spectators-api", Layer: "service", Type: "service", Domain: "tomandjerry", Name: "spectators-api", Status: "active"}
	producer := snapshot.Node{Kind: "node", ID: "code:provider:tomandjerry:arena-api/producer", Layer: "code", Type: "provider", Domain: "tomandjerry", Name: "BattleResultProducer", File: "arena-api/producer.ts", Status: "active"}
	consumer := snapshot.Node{Kind: "node", ID: "code:provider:tomandjerry:scoreboard-api/consumer", Layer: "code", Type: "provider", Domain: "tomandjerry", Name: "BattleResultConsumer", File: "scoreboard-api/consumer.ts", Status: "active"}
	caller := snapshot.Node{Kind: "node", ID: "code:provider:tomandjerry:spectators-api/caller", Layer: "code", Type: "provider", Domain: "tomandjerry", Name: "ScoreCaller", File: "spectators-api/caller.ts", Status: "active"}
	topic := snapshot.Node{Kind: "node", ID: "contract:topic:tomandjerry:battle-results", Layer: "contract", Type: "topic", Domain: "tomandjerry", Name: "battle-results", Status: "active"}
	nodes := []snapshot.Node{svcArena, svcScoreboard, svcSpectators, producer, consumer, caller, topic}

	publishEdge := snapshot.Edge{Kind: "edge", ID: producer.ID + "->" + topic.ID + ":PUBLISHES_TOPIC", Type: "PUBLISHES_TOPIC", From: producer.ID, To: topic.ID, Active: true, Derivation: "hard"}
	consumeEdge := snapshot.Edge{Kind: "edge", ID: consumer.ID + "->" + topic.ID + ":CONSUMES_TOPIC", Type: "CONSUMES_TOPIC", From: consumer.ID, To: topic.ID, Active: true, Derivation: "hard"}
	newCallEdge := snapshot.Edge{Kind: "edge", ID: caller.ID + "->" + svcScoreboard.ID + ":CALLS_SERVICE", Type: "CALLS_SERVICE", From: caller.ID, To: svcScoreboard.ID, Active: true, Derivation: "hard"}

	baseSnap := &snapshot.Snapshot{
		Header: snapshot.Header{Kind: "header", Format: snapshot.Format, Version: snapshot.Version, Seq: 1, FactSchema: snapshot.FactSchema, Domains: []string{"d"}},
		Nodes:  nodes,
		Edges:  []snapshot.Edge{publishEdge, consumeEdge},
	}
	writeFileT(t, dir, ".depbot/graph.jsonl", encodeSnapshotT(t, baseSnap))
	baseSHA := commitAllT(t, dir, "base")

	headSnap := &snapshot.Snapshot{
		Header: snapshot.Header{Kind: "header", Format: snapshot.Format, Version: snapshot.Version, Seq: 2, FactSchema: snapshot.FactSchema, Domains: []string{"d"}},
		Nodes:  nodes,
		Edges:  []snapshot.Edge{consumeEdge, newCallEdge}, // publishEdge removed, newCallEdge added
	}
	writeFileT(t, dir, ".depbot/graph.jsonl", encodeSnapshotT(t, headSnap))
	headSHA := commitAllT(t, dir, "head")

	result, err := Review(context.Background(), ReviewInput{
		BaseSource: SnapshotSource{RepoRoot: dir, Rev: baseSHA},
		HeadSource: SnapshotSource{RepoRoot: dir, Rev: headSHA},
		RepoRoot:   dir,
		Depth:      4,
		// No MakeQuerier override: exercises the real CoreImpactQuerier.
	})
	if err != nil {
		t.Fatalf("Review: %v", err)
	}

	if result.Base.Commit != baseSHA || result.Head.Commit != headSHA {
		t.Errorf("Provenance commits = (%q, %q), want (%q, %q)", result.Base.Commit, result.Head.Commit, baseSHA, headSHA)
	}
	if result.Base.Graph == nil || result.Head.Graph == nil {
		t.Error("Provenance.Graph is nil on one side, want both populated (Origin snapshot on both)")
	}

	changed := result.ChangedRelationships()
	if len(changed) != 2 {
		t.Fatalf("ChangedRelationships = %+v, want exactly 2 (1 added + 1 removed)", changed)
	}
	var addedFinding, removedFinding *ReviewFinding
	for i := range changed {
		switch changed[i].Kind {
		case IntegrationAdded:
			addedFinding = &changed[i]
		case IntegrationRemoved:
			removedFinding = &changed[i]
		}
	}
	if addedFinding == nil || removedFinding == nil {
		t.Fatalf("ChangedRelationships = %+v, want one IntegrationAdded and one IntegrationRemoved", changed)
	}

	// The removed publish must carry an Affected downstream for
	// scoreboard-api (still subscribed on base, per TopicSubscribers).
	removedAffected := onlySeverity(removedFinding.Downstream, SeverityAffected)
	if len(removedAffected) != 1 || removedAffected[0].System.Key != svcScoreboard.ID {
		t.Errorf("IntegrationRemoved Downstream Affected = %+v, want exactly [scoreboard-api]", removedAffected)
	}

	// The added call's own QueryImpact(caller) is empty (nothing points at
	// a brand-new caller node) — only the Informational counterpart entry
	// should be present, collapsed to scoreboard-api itself (self-collapse:
	// the edge's own To IS the service node).
	addedInfo := onlySeverity(addedFinding.Downstream, SeverityInformational)
	if len(addedInfo) != 1 || addedInfo[0].System.Key != svcScoreboard.ID || addedInfo[0].Reason != ReasonProviderGainsCaller {
		t.Errorf("IntegrationAdded Downstream Informational = %+v, want [{scoreboard-api, provider_gains_caller}]", addedInfo)
	}
	if len(onlySeverity(addedFinding.Downstream, SeverityAffected)) != 0 {
		t.Errorf("IntegrationAdded Downstream Affected = %+v, want empty (nothing depends on a brand-new caller)", onlySeverity(addedFinding.Downstream, SeverityAffected))
	}

	v := result.Verdict
	if v.IntegrationsAdded != 1 {
		t.Errorf("Verdict.IntegrationsAdded = %d, want 1", v.IntegrationsAdded)
	}
	if v.DependenciesRemoved != 1 {
		t.Errorf("Verdict.DependenciesRemoved = %d, want 1", v.DependenciesRemoved)
	}
	if v.SystemsAffected != 1 {
		t.Errorf("Verdict.SystemsAffected = %d, want 1 (scoreboard-api only); WhoMayBeAffected=%+v", v.SystemsAffected, result.WhoMayBeAffected())
	}
	if v.UnknownFiles != 0 {
		t.Errorf("Verdict.UnknownFiles = %d, want 0", v.UnknownFiles)
	}
	if v.NoSystemChange {
		t.Error("Verdict.NoSystemChange = true, want false")
	}
	if len(result.Warnings) != 0 {
		t.Errorf("Warnings = %+v, want empty (both sides have a real graph)", result.Warnings)
	}
}
