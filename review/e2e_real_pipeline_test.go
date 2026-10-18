package review

import (
	"context"
	"strings"
	"testing"

	"github.com/alexdx2/chronicle-core/snapshot"
	"github.com/alexdx2/chronicle-core/store"
)

// jerryClientV1/V2 are the REAL content, before and after, of
// fixtures/tom-and-jerry/arena-api/src/arena/jerry.client.ts (v1 copied
// verbatim from the tracked fixture; v2 is this test's own edit) —
// TestReview_E2E_RealPipeline_CriticalBugs commits both so gitdiff.DiffHunks
// reports REAL hunks against REAL file content, instead of a hand-typed
// gitdiff.Hunk literal the way diff_test.go's unit tests use.
//
// The edit does two things to the SAME two real CALLS_ENDPOINT facts this
// file's evidence backs in the fixture (verified directly against the
// fixture db before writing this test — see task-18-report.md):
//   - getStatus(): the URL is hoisted into a local variable, a real
//     1-line-to-2-line replacement AT line 8 (the fetch() call's own
//     evidence line) — the SAME call survives, but its own evidence line
//     moves to 9.
//   - getTraps(): the fetch call to /jerry/traps is removed entirely —
//     paired below with a real MarkStaleEdges call, exactly how the actual
//     scan pipeline records "this rescan didn't find it anymore".
const jerryClientV1 = `import { Injectable, HttpException } from '@nestjs/common';

const JERRY_API_URL = process.env.JERRY_API_URL || 'http://jerry-api:3002';

@Injectable()
export class JerryClient {
  async getStatus() {
    const response = await fetch(` + "`" + `${JERRY_API_URL}/jerry/status` + "`" + `);
    if (!response.ok) throw new HttpException('Jerry unavailable', response.status);
    return response.json();
  }

  async getTraps() {
    const response = await fetch(` + "`" + `${JERRY_API_URL}/jerry/traps` + "`" + `);
    if (!response.ok) return [];
    return response.json();
  }
}
`

const jerryClientV2 = `import { Injectable, HttpException } from '@nestjs/common';

const JERRY_API_URL = process.env.JERRY_API_URL || 'http://jerry-api:3002';

@Injectable()
export class JerryClient {
  async getStatus() {
    const url = ` + "`" + `${JERRY_API_URL}/jerry/status` + "`" + `;
    const response = await fetch(url);
    if (!response.ok) throw new HttpException('Jerry unavailable', response.status);
    return response.json();
  }

  async getTraps() {
    return [];
  }
}
`

// TestReview_E2E_RealPipeline_CriticalBugs is task-18's own "single most
// valuable" addition. Every other test in this whole plan — the golden-file
// fixtures, the unit tests for Diff/ClassifyDelta/Analyze, even the real
// GitLab CI verification — used either hand-built snapshot.Snapshot literals
// or a hand-built graph with no evidence, which is exactly why none of them
// caught any of the three Critical bugs the final whole-branch review found.
//
// This test drives the REAL tom-and-jerry fixture's own store — the actual
// scanned database, real evidence with real file/line-range provenance, real
// REQUIRES/TRIGGERS_FLOW edges DeriveFlows wrote from real endpoints —
// through the real mutation paths the scan pipeline itself uses (UpsertEdge
// re-observation, AddEvidence at a new line, MarkStaleEdges), then a real
// FromStore export of both sides, a real two-commit git repo (so
// gitdiff.DiffHunks reports REAL hunks against REAL file content, not a
// hand-typed gitdiff.Hunk), and Review() end to end — asserting the exact
// findings each of the three bugs' fixes now produces.
//
// The scenario is built entirely from real facts already in the fixture,
// mirroring both the mockup's own ("arena-api adds a topic and stops calling
// an endpoint") and the final reviewer's own empirical repro:
//
//   - Bug 1 (a rescanned edit invisible): arena-api's JerryClient.getStatus()
//     really does call GET /jerry/status, evidenced at jerry.client.ts:8.
//     The "rescan" hoists the URL into a local variable — a real
//     1-line-to-2-line edit AT that line — so the SAME call's evidence moves
//     to line 9. Before the fix this fact was Moved and invisible to
//     ClassifyDelta; it must now be Touched -> ConsumerChanged with empty
//     Downstream ("Code changed, graph unchanged").
//   - Bug 2 (a stale-marked removal invisible): arena-api's JerryClient also
//     really does call GET /jerry/traps, a real edge in the fixture. The
//     "rescan" removes that call from the real file, and the real edge is
//     stale-marked (MarkStaleEdges) — never a deleted row, exactly the shape
//     chronicle_stale_mark produces. Must produce IntegrationRemoved, not
//     nothing and not a bare ContractChanged/no-op.
//   - Bug 3 (inflated affected count): removing that CALLS_ENDPOINT edge
//     seeds a reverse-impact BFS from JerryClient. In this real fixture,
//     JerryClient is injected by arena.service AND required by 5 of
//     arena-api's own real flow nodes (get:/arena/history, get:/arena/score,
//     post:/arena/attack, post:/arena/trap, ws:/watch-battle) — verified
//     directly against the fixture db. All 6 raw entries must collapse to
//     the ONE real owner (arena-api) and then be dropped entirely as
//     self-affected (arena-api is also the system the change originates
//     from) — 0 Affected, exactly one Informational (jerry-api loses a
//     caller).
func TestReview_E2E_RealPipeline_CriticalBugs(t *testing.T) {
	st := openTomAndJerryFixture(t).Store()
	st.SetJournaling(false)

	const (
		jerryClientFile = "arena-api/src/arena/jerry.client.ts"
		statusEdgeKey   = "code:provider:tom-and-jerry:arena-api/src/arena/jerry.client->contract:endpoint:tom-and-jerry:get:/jerry/status:CALLS_ENDPOINT"
		trapsEdgeKey    = "code:provider:tom-and-jerry:arena-api/src/arena/jerry.client->contract:endpoint:tom-and-jerry:get:/jerry/traps:CALLS_ENDPOINT"
	)

	// --- export BASE, before the "rescan" ---------------------------------
	baseSnap, err := snapshot.FromStore(st, snapshot.ExportOptions{PrevSeq: 40, Registry: "sha256:test"})
	if err != nil {
		t.Fatalf("FromStore (base): %v", err)
	}

	// --- simulate a real rescan: re-observe everything except the removed
	// call, stale-mark it, and re-extract the moved call at its new line ---
	rev, err := st.CreateRevision("tom-and-jerry", "", "rescan-sha", "manual", "full", "{}")
	if err != nil {
		t.Fatalf("CreateRevision: %v", err)
	}
	edges, err := st.ListEdges(store.EdgeFilter{})
	if err != nil {
		t.Fatalf("ListEdges: %v", err)
	}
	trapsFound := false
	for _, e := range edges {
		if e.EdgeKey == trapsEdgeKey {
			trapsFound = true
			continue // the one call the rescan no longer finds
		}
		e.LastSeenRevisionID = rev
		if _, err := st.UpsertEdge(e); err != nil {
			t.Fatalf("re-observe %s: %v", e.EdgeKey, err)
		}
	}
	if !trapsFound {
		t.Fatalf("fixture has no edge %s — has the fixture changed shape?", trapsEdgeKey)
	}
	n, err := st.MarkStaleEdges("tom-and-jerry", rev)
	if err != nil {
		t.Fatalf("MarkStaleEdges: %v", err)
	}
	if n != 1 {
		t.Fatalf("MarkStaleEdges marked %d edges, want exactly 1 (%s)", n, trapsEdgeKey)
	}

	statusEdge, err := st.GetEdgeByKey(statusEdgeKey)
	if err != nil {
		t.Fatalf("GetEdgeByKey(%s): %v", statusEdgeKey, err)
	}
	if _, err := st.AddEvidence(store.EvidenceRow{
		TargetKind: "edge", EdgeID: statusEdge.EdgeID, SourceKind: "file",
		FilePath: jerryClientFile, LineStart: 9, LineEnd: 9,
		ExtractorID: "chronicle-scan", ExtractorVersion: "1.0",
		Confidence: 0.6, AssertionKind: "text_contains",
		Assertion:           `{"context":"fetch","method":"GET","substring":"/jerry/status","url":"http://jerry-api:3002/jerry/status"}`,
		ValidFromRevisionID: rev,
	}); err != nil {
		t.Fatalf("AddEvidence (moved status call): %v", err)
	}

	// --- export HEAD, after the "rescan" ----------------------------------
	headSnap, err := snapshot.FromStore(st, snapshot.ExportOptions{PrevSeq: 41, Registry: "sha256:test"})
	if err != nil {
		t.Fatalf("FromStore (head): %v", err)
	}

	// --- a real two-commit git repo: real source file + real graph.jsonl on
	// each side, so gitdiff.DiffHunks reports REAL hunks -------------------
	dir := initGitRepoT(t)
	// Out-of-scope manifest: freshness/coverage is not what this test is
	// about (that's changed_file_counts_test.go's job) — this keeps every
	// finding in this test attributable to one of the three bugs, with no
	// incidental Unknown findings from files this test doesn't scan.
	scopedManifestT(t, dir, "zzz-nomatch/**")

	writeFileT(t, dir, jerryClientFile, jerryClientV1)
	writeFileT(t, dir, ".depbot/graph.jsonl", encodeSnapshotT(t, baseSnap))
	baseSHA := commitAllT(t, dir, "base")

	writeFileT(t, dir, jerryClientFile, jerryClientV2)
	writeFileT(t, dir, ".depbot/graph.jsonl", encodeSnapshotT(t, headSnap))
	headSHA := commitAllT(t, dir, "head")

	result, err := Review(context.Background(), ReviewInput{
		BaseSource: SnapshotSource{RepoRoot: dir, Rev: baseSHA},
		HeadSource: SnapshotSource{RepoRoot: dir, Rev: headSHA},
		RepoRoot:   dir,
		Depth:      4,
		// No MakeQuerier override: exercises the real CoreImpactQuerier
		// against the real loaded graph on both sides.
	})
	if err != nil {
		t.Fatalf("Review: %v", err)
	}

	if result.Base.Commit != baseSHA || result.Head.Commit != headSHA {
		t.Errorf("Provenance commits = (%q, %q), want (%q, %q)", result.Base.Commit, result.Head.Commit, baseSHA, headSHA)
	}
	// The tom-and-jerry fixture carries one known node with a malformed key
	// ("infra:broker:battle-result"): decode drops it and the review now says
	// so. Anything else in Warnings would be a real regression.
	sawBadKey := false
	for _, w := range result.Warnings {
		if strings.Contains(w, "bad_key record(s) dropped") {
			sawBadKey = true
			continue
		}
		t.Errorf("unexpected Warning %q", w)
	}
	if !sawBadKey {
		t.Errorf("Warnings = %q, want the bad_key warning to be present", result.Warnings)
	}
	if result.Verdict.UnknownFiles != 0 {
		t.Errorf("Verdict.UnknownFiles = %d, want 0 (jerry.client.ts is deliberately out of this repo's scan scope)", result.Verdict.UnknownFiles)
	}

	// This scenario touches exactly three facts and nothing else in the whole
	// fixture: the moved status call (code:...jerry.client ->
	// contract:endpoint:...get:/jerry/status), the removed traps call
	// (code:...jerry.client -> contract:endpoint:...get:/jerry/traps), and —
	// discovered empirically running this test, not assumed up front — a
	// real, coarser CALLS_SERVICE edge (code:...jerry.client ->
	// service:...jerry-api) whose own evidence set includes BOTH line 8 and
	// line 14, so it is legitimately Touched too, by both hunks. Anything
	// beyond these three top-level findings means some OTHER edge spuriously
	// registered as Changed/Touched/Moved, which would itself be a bug in
	// either this test's setup or the fixes under test.
	if len(result.Findings) != 3 {
		t.Fatalf("Findings = %+v, want exactly 3 (two ConsumerChanged, one IntegrationRemoved)", result.Findings)
	}

	// --- Bug 2: the stale-marked removal must be reported as Removed ------
	changed := result.ChangedRelationships()
	var removedFinding *ReviewFinding
	for i := range changed {
		if changed[i].Kind == IntegrationRemoved {
			removedFinding = &changed[i]
		}
	}
	if removedFinding == nil {
		t.Fatalf("ChangedRelationships = %+v, want an IntegrationRemoved finding (Bug 2: the stale-marked GET /jerry/traps call must be reported as removed, not silently dropped or misreported as changed)", changed)
	}
	if len(removedFinding.Facts) != 1 || removedFinding.Facts[0].Edge == nil || removedFinding.Facts[0].Edge.To != "contract:endpoint:tom-and-jerry:get:/jerry/traps" {
		t.Errorf("IntegrationRemoved.Facts = %+v, want the traps CALLS_ENDPOINT edge", removedFinding.Facts)
	}
	if !removedFinding.Facts[0].Edge.Active {
		t.Errorf("IntegrationRemoved.Facts[0].Edge.Active = false, want true (Removed-shaped findings carry the BASE-side fact, from when it was still live)")
	}

	// --- Bug 3: 0 Affected, exactly 1 Informational ("jerry-api loses a
	// caller") — not the 6-system inflation the final reviewer found -------
	affected := onlySeverity(removedFinding.Downstream, SeverityAffected)
	if len(affected) != 0 {
		t.Errorf("IntegrationRemoved Downstream Affected = %+v, want EMPTY (Bug 3: arena-api's own flow nodes and arena.service must collapse to arena-api, the very system that changed, and be dropped as self-affected)", affected)
	}
	info := onlySeverity(removedFinding.Downstream, SeverityInformational)
	if len(info) != 1 {
		t.Fatalf("IntegrationRemoved Downstream Informational = %+v, want exactly 1", info)
	}
	if info[0].Reason != ReasonProviderLostConsumer {
		t.Errorf("Informational.Reason = %q, want %q", info[0].Reason, ReasonProviderLostConsumer)
	}
	if !strings.Contains(info[0].System.Name, "jerry") {
		t.Errorf("Informational.System = %+v, want jerry-api (the endpoint's own owner)", info[0].System)
	}

	if result.Verdict.SystemsAffected != 0 {
		t.Errorf("Verdict.SystemsAffected = %d, want 0 — this exact shape (arena-api's own derived flow nodes plus arena.service reachable from arena-api's own removed call) is the final reviewer's reproduction that found the report claiming \"6 systems may be affected\"", result.Verdict.SystemsAffected)
	}
	if result.Verdict.DependenciesRemoved != 1 {
		t.Errorf("Verdict.DependenciesRemoved = %d, want 1", result.Verdict.DependenciesRemoved)
	}

	// --- Bug 1: the rescanned-and-moved call must be visible, not vanished -
	unchangedTouched := result.CodeChangedGraphUnchanged()
	if len(unchangedTouched) != 2 {
		t.Fatalf("CodeChangedGraphUnchanged = %+v, want exactly 2 (the endpoint-level status call, and the coarser jerry.client->jerry-api CALLS_SERVICE edge whose evidence also covers line 8)", unchangedTouched)
	}
	var statusFinding, serviceFinding *ReviewFinding
	for i := range unchangedTouched {
		f := &unchangedTouched[i]
		if f.Kind != ConsumerChanged || len(f.Facts) != 1 || f.Facts[0].Edge == nil {
			continue
		}
		switch f.Facts[0].Edge.To {
		case "contract:endpoint:tom-and-jerry:get:/jerry/status":
			statusFinding = f
		case "service:service:tom-and-jerry:jerry-api":
			serviceFinding = f
		}
	}
	if statusFinding == nil {
		t.Fatalf("CodeChangedGraphUnchanged = %+v, want a ConsumerChanged finding for the rescanned GET /jerry/status call (Bug 1: a fact whose evidence moved into a touched hunk must not be silently dropped)", unchangedTouched)
	}
	statusEvidence := statusFinding.Facts[0].Edge.Evidence
	if len(statusEvidence) != 1 || statusEvidence[0].Range != "8-8" {
		t.Errorf("ConsumerChanged fact evidence = %+v, want exactly the BASE-side evidence at line 8 (Touched.Fact is always the base-side fact)", statusEvidence)
	}
	if len(statusFinding.Downstream) != 0 {
		t.Errorf("status ConsumerChanged Downstream = %+v, want empty (Bug 3: it must self-filter to arena-api the same way the IntegrationRemoved finding does)", statusFinding.Downstream)
	}

	// The coarser CALLS_SERVICE edge (jerry.client -> jerry-api service) is a
	// real fact this fixture already has, whose own evidence set spans BOTH
	// line 8 and line 14 — legitimately Touched by both hunks. Its Downstream
	// must self-filter to empty for exactly the same Bug 3 reason.
	if serviceFinding == nil {
		t.Fatalf("CodeChangedGraphUnchanged = %+v, want a second ConsumerChanged finding for jerry.client's coarser CALLS_SERVICE edge to jerry-api", unchangedTouched)
	}
	if len(serviceFinding.Downstream) != 0 {
		t.Errorf("service-level ConsumerChanged Downstream = %+v, want empty", serviceFinding.Downstream)
	}
	// Confirm it did NOT also leak into Moved (the two buckets are mutually
	// exclusive; a fact promoted to Touched must not linger in Moved too).
	for _, m := range result.Delta.Moved {
		if m.ID == statusEdgeKey {
			t.Errorf("GraphDelta.Moved contains %s, want it exclusively in Touched", statusEdgeKey)
		}
	}
}
