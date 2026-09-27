package review

import (
	"encoding/json"
	"testing"

	"github.com/alexdx2/chronicle-core/gitdiff"
	"github.com/alexdx2/chronicle-core/snapshot"
)

// vgFor wraps nodes/edges into a *VersionGraph carrying nothing but an
// Export — every diff_test.go fixture is synthetic, built directly as Go
// literals with no git and no *graph.Graph involved (per the brief: "на
// синтетических *snapshot.Snapshot").
func vgFor(nodes []snapshot.Node, edges []snapshot.Edge) *VersionGraph {
	return &VersionGraph{Export: &snapshot.Snapshot{Nodes: nodes, Edges: edges}}
}

func factIDs(facts []Fact) map[string]bool {
	out := make(map[string]bool, len(facts))
	for _, f := range facts {
		out[f.ID] = true
	}
	return out
}

// --- Diff: Added / Removed ------------------------------------------------

// TestDiff_AddedRemoved: a contract node present only in base is Removed; a
// contract node present only in head is Added. Both are ClassContract
// (non-structural), so they must be LISTED, not merely counted.
func TestDiff_AddedRemoved(t *testing.T) {
	base := vgFor([]snapshot.Node{
		{ID: "contract:endpoint:d:old", Layer: "contract", Type: "endpoint"},
	}, nil)
	head := vgFor([]snapshot.Node{
		{ID: "contract:endpoint:d:new", Layer: "contract", Type: "endpoint"},
	}, nil)

	delta := Diff(base, head, nil)

	if len(delta.Removed) != 1 || delta.Removed[0].ID != "contract:endpoint:d:old" {
		t.Errorf("Removed = %+v, want exactly [contract:endpoint:d:old]", delta.Removed)
	}
	if len(delta.Added) != 1 || delta.Added[0].ID != "contract:endpoint:d:new" {
		t.Errorf("Added = %+v, want exactly [contract:endpoint:d:new]", delta.Added)
	}
	if delta.StructuralAdded != 0 || delta.StructuralRemoved != 0 {
		t.Errorf("Structural counters = added:%d removed:%d, want 0/0 (these are ClassContract, not structural)", delta.StructuralAdded, delta.StructuralRemoved)
	}
}

// --- Diff: Changed ---------------------------------------------------------

// TestDiff_ChangedFields: an edge's Metadata differs between base and head —
// everything else identical — must land in Changed with Fields == ["Metadata"].
func TestDiff_ChangedFields(t *testing.T) {
	base := vgFor(nil, []snapshot.Edge{
		{ID: "a->b:CALLS_SERVICE", Type: "CALLS_SERVICE", From: "a", To: "b", Metadata: json.RawMessage(`{"x":1}`)},
	})
	head := vgFor(nil, []snapshot.Edge{
		{ID: "a->b:CALLS_SERVICE", Type: "CALLS_SERVICE", From: "a", To: "b", Metadata: json.RawMessage(`{"x":2}`)},
	})

	delta := Diff(base, head, nil)

	if len(delta.Changed) != 1 {
		t.Fatalf("Changed = %+v, want exactly one entry", delta.Changed)
	}
	c := delta.Changed[0]
	if c.ID != "a->b:CALLS_SERVICE" || c.Kind != KindEdge {
		t.Errorf("Changed[0] = %+v, want ID a->b:CALLS_SERVICE, Kind edge", c)
	}
	if len(c.Fields) != 1 || c.Fields[0] != "Metadata" {
		t.Errorf("Changed[0].Fields = %v, want exactly [Metadata]", c.Fields)
	}
	if len(delta.Added) != 0 || len(delta.Removed) != 0 || len(delta.Moved) != 0 {
		t.Errorf("delta = %+v, want only Changed populated", delta)
	}
}

// --- Diff: Moved -------------------------------------------------------

// TestDiff_Moved: the same node, semantic fields identical, but its evidence
// was replaced by a different evidence id at a different Blob/Range — must
// land in Moved, not Changed, and not silently dropped.
func TestDiff_Moved(t *testing.T) {
	base := vgFor([]snapshot.Node{
		{
			ID: "contract:endpoint:d:foo", Layer: "contract", Type: "endpoint",
			Evidence: []snapshot.Evidence{{ID: "ev1", File: "a.ts", Range: "10-12", Blob: "blobA"}},
		},
	}, nil)
	head := vgFor([]snapshot.Node{
		{
			ID: "contract:endpoint:d:foo", Layer: "contract", Type: "endpoint",
			Evidence: []snapshot.Evidence{{ID: "ev2", File: "a.ts", Range: "20-22", Blob: "blobB"}},
		},
	}, nil)

	delta := Diff(base, head, nil)

	if len(delta.Moved) != 1 {
		t.Fatalf("Moved = %+v, want exactly one entry", delta.Moved)
	}
	m := delta.Moved[0]
	if m.ID != "contract:endpoint:d:foo" || m.Kind != KindNode {
		t.Errorf("Moved[0] = %+v, want ID contract:endpoint:d:foo, Kind node", m)
	}
	if len(delta.Changed) != 0 || len(delta.Added) != 0 || len(delta.Removed) != 0 {
		t.Errorf("delta = %+v, want only Moved populated", delta)
	}
}

// TestDiff_MovedButTouched_BecomesTouched is task-18-report.md's Bug 1 (cause
// 1a) reproduction: a fact whose evidence moved to a new line (a real rescan
// re-extracting the SAME call after an edit shifted it — e.g. hoisting a URL
// into a local variable pushed the fetch() call from line 8 to line 9) AND
// whose OLD (base) evidence line sits inside a real hunk of this diff. Before
// this fix, evidenceIsMoved short-circuited the whole check straight to
// Moved and RETURNED — this fact never even reached touchedHunks, so a human
// who rescanned and genuinely touched that exact code got nothing in the
// finding pipeline (ClassifyDelta never reads d.Moved). It must now be
// Touched, not Moved: the hunk directly overlaps the base evidence's own
// line 8 (a real 1-line-to-2-line replacement, not a pure insertion — this
// case does not need Bug 1b's insertion-strictly-inside fix at all).
func TestDiff_MovedButTouched_BecomesTouched(t *testing.T) {
	base := vgFor(nil, []snapshot.Edge{{
		ID: "jerry.client->status:CALLS_ENDPOINT", Type: "CALLS_ENDPOINT", From: "jerry.client", To: "status",
		Evidence: []snapshot.Evidence{{ID: "ev-line8", File: "jerry.client.ts", Range: "8-8"}},
	}})
	head := vgFor(nil, []snapshot.Edge{{
		ID: "jerry.client->status:CALLS_ENDPOINT", Type: "CALLS_ENDPOINT", From: "jerry.client", To: "status",
		Evidence: []snapshot.Evidence{{ID: "ev-line9", File: "jerry.client.ts", Range: "9-9"}},
	}})
	// One old line (the fetch call) replaced by two new lines (URL hoisted to
	// its own line, then the call) — a real, non-insertion hunk overlapping
	// old line 8 exactly.
	hunks := []gitdiff.FileHunks{
		{Path: "jerry.client.ts", Hunks: []gitdiff.Hunk{{OldStart: 8, OldLines: 1, NewStart: 8, NewLines: 2}}},
	}

	delta := Diff(base, head, hunks)

	if len(delta.Moved) != 0 {
		t.Errorf("Moved = %+v, want empty (this fact must be reclassified as Touched)", delta.Moved)
	}
	if len(delta.Touched) != 1 || delta.Touched[0].ID != "jerry.client->status:CALLS_ENDPOINT" {
		t.Fatalf("Touched = %+v, want exactly [jerry.client->status:CALLS_ENDPOINT]", delta.Touched)
	}
	if got := delta.Touched[0].Fact; got.Edge == nil || len(got.Edge.Evidence) != 1 || got.Edge.Evidence[0].ID != "ev-line8" {
		t.Errorf("Touched[0].Fact = %+v, want the BASE-side fact (evidence ev-line8)", got)
	}
}

// TestDiff_MovedNotTouched_StaysMoved: evidence moved, but the diff's only
// hunk is nowhere near the fact's base evidence line — this fact "moved for
// reasons the diff being reviewed didn't cause" (Moved's own doc comment)
// and must stay Moved alone, not be promoted to Touched just because SOME
// hunk exists somewhere in the same file.
func TestDiff_MovedNotTouched_StaysMoved(t *testing.T) {
	base := vgFor(nil, []snapshot.Edge{{
		ID: "a->b:CALLS_SERVICE", Type: "CALLS_SERVICE", From: "a", To: "b",
		Evidence: []snapshot.Evidence{{ID: "ev1", File: "svc.ts", Range: "8-8"}},
	}})
	head := vgFor(nil, []snapshot.Edge{{
		ID: "a->b:CALLS_SERVICE", Type: "CALLS_SERVICE", From: "a", To: "b",
		Evidence: []snapshot.Evidence{{ID: "ev2", File: "svc.ts", Range: "9-9"}},
	}})
	// A real hunk exists in the same file, but far from line 8.
	hunks := []gitdiff.FileHunks{
		{Path: "svc.ts", Hunks: []gitdiff.Hunk{{OldStart: 100, OldLines: 1, NewStart: 100, NewLines: 1}}},
	}

	delta := Diff(base, head, hunks)

	if len(delta.Touched) != 0 {
		t.Errorf("Touched = %+v, want empty (the only hunk is nowhere near this fact's evidence)", delta.Touched)
	}
	if len(delta.Moved) != 1 || delta.Moved[0].ID != "a->b:CALLS_SERVICE" {
		t.Fatalf("Moved = %+v, want exactly [a->b:CALLS_SERVICE]", delta.Moved)
	}
}

// --- Diff: Touched -----------------------------------------------------

// TestDiff_Touched: an edge identical in base and head (same semantic
// fields, same evidence) whose base evidence Range overlaps a hunk in the
// diff between base and head — must be reported as Touched.
func TestDiff_Touched(t *testing.T) {
	edge := snapshot.Edge{
		ID: "a->b:CALLS_SERVICE", Type: "CALLS_SERVICE", From: "a", To: "b",
		Evidence: []snapshot.Evidence{{ID: "ev1", File: "svc.ts", Range: "10-15"}},
	}
	base := vgFor(nil, []snapshot.Edge{edge})
	head := vgFor(nil, []snapshot.Edge{edge})

	hunks := []gitdiff.FileHunks{
		{Path: "svc.ts", Hunks: []gitdiff.Hunk{{OldStart: 12, OldLines: 2, NewStart: 12, NewLines: 2}}},
	}

	delta := Diff(base, head, hunks)

	if len(delta.Touched) != 1 {
		t.Fatalf("Touched = %+v, want exactly one entry", delta.Touched)
	}
	tc := delta.Touched[0]
	if tc.ID != "a->b:CALLS_SERVICE" || tc.Kind != KindEdge {
		t.Errorf("Touched[0] = %+v, want ID a->b:CALLS_SERVICE, Kind edge", tc)
	}
	if len(tc.Hunks) != 1 || tc.Hunks[0] != (HunkRef{File: "svc.ts", OldStart: 12, OldLines: 2}) {
		t.Errorf("Touched[0].Hunks = %+v, want exactly [{svc.ts 12 2}]", tc.Hunks)
	}
	if len(delta.Changed) != 0 || len(delta.Moved) != 0 || len(delta.Added) != 0 || len(delta.Removed) != 0 {
		t.Errorf("delta = %+v, want only Touched populated", delta)
	}
}

// TestDiff_Touched_RenamedFile: base evidence lives at the file's BASE-side
// path; the hunk reports the HEAD-side (post-rename) path in .Path and the
// base-side path in .OldPath. Matching evidence against .Path alone would
// silently miss this (point 5) — OldPath must be tried first.
func TestDiff_Touched_RenamedFile(t *testing.T) {
	edge := snapshot.Edge{
		ID: "a->b:CALLS_SERVICE", Type: "CALLS_SERVICE", From: "a", To: "b",
		Evidence: []snapshot.Evidence{{ID: "ev1", File: "old_name.ts", Range: "10-15"}},
	}
	base := vgFor(nil, []snapshot.Edge{edge})
	head := vgFor(nil, []snapshot.Edge{edge})

	hunks := []gitdiff.FileHunks{
		{Path: "new_name.ts", OldPath: "old_name.ts", Hunks: []gitdiff.Hunk{{OldStart: 12, OldLines: 2, NewStart: 12, NewLines: 2}}},
	}

	delta := Diff(base, head, hunks)

	if len(delta.Touched) != 1 {
		t.Fatalf("Touched = %+v, want exactly one entry (rename-aware match on OldPath)", delta.Touched)
	}
	if delta.Touched[0].Hunks[0].File != "old_name.ts" {
		t.Errorf("Touched[0].Hunks[0].File = %q, want %q (the base-side name evidence itself carries)", delta.Touched[0].Hunks[0].File, "old_name.ts")
	}
}

// TestDiff_Touched_PureInsertionInsideRangeIntersects: a hunk with
// OldLines==0 is a pure insertion — zero width on the base side, sitting
// between base lines OldStart and OldStart+1. task-18-report.md's Bug 1b fix:
// when that point falls STRICTLY inside a fact's evidence range (a real
// evidence line on both sides of it), the fact IS touched — a line inserted
// literally in the middle of a fact's own code is an edit to that fact's
// code by insertion, not a no-op. This reverses the older, narrower reading
// of "an empty interval cannot overlap a non-empty range" pinned by this
// test's own previous form (TestDiff_Touched_PureInsertionNoIntersect),
// which reasoned about a boundary insertion and is still correct for that
// case — see TestDiff_Touched_PureInsertionAtBoundaryNoIntersect below.
func TestDiff_Touched_PureInsertionInsideRangeIntersects(t *testing.T) {
	edge := snapshot.Edge{
		ID: "a->b:CALLS_SERVICE", Type: "CALLS_SERVICE", From: "a", To: "b",
		Evidence: []snapshot.Evidence{{ID: "ev1", File: "svc.ts", Range: "10-15"}},
	}
	base := vgFor(nil, []snapshot.Edge{edge})
	head := vgFor(nil, []snapshot.Edge{edge})

	hunks := []gitdiff.FileHunks{
		{Path: "svc.ts", Hunks: []gitdiff.Hunk{{OldStart: 12, OldLines: 0, NewStart: 12, NewLines: 3}}},
	}

	delta := Diff(base, head, hunks)

	if len(delta.Touched) != 1 || delta.Touched[0].ID != "a->b:CALLS_SERVICE" {
		t.Fatalf("Touched = %+v, want exactly [a->b:CALLS_SERVICE] (insertion at line 12 sits strictly inside the 10-15 range)", delta.Touched)
	}
	if got := delta.Touched[0].Hunks[0]; got.OldStart != 12 || got.OldLines != 0 {
		t.Errorf("Touched[0].Hunks[0] = %+v, want {OldStart:12 OldLines:0}", got)
	}
}

// TestDiff_Touched_PureInsertionAtBoundaryNoIntersect: an insertion
// immediately before a range's first line, or immediately after its last
// line, is a BOUNDARY insertion — still not a touch, per Diff's own doc
// comment (task-18-report.md's Bug 1b fix narrows the old blanket rule to
// exactly this case, it does not remove it). Also covers a single-line range
// (10-10, via the OldStart==10 sub-case), which can never be touched by a
// pure insertion at all: there is no "inside" a single line to insert into.
func TestDiff_Touched_PureInsertionAtBoundaryNoIntersect(t *testing.T) {
	cases := []struct {
		name     string
		rng      string
		oldStart int
	}{
		{"immediately before range start", "10-15", 9},
		{"immediately after range end", "10-15", 15},
		{"single-line range, insertion after its only line", "10-10", 10},
		{"single-line range, insertion before its only line", "10-10", 9},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			edge := snapshot.Edge{
				ID: "a->b:CALLS_SERVICE", Type: "CALLS_SERVICE", From: "a", To: "b",
				Evidence: []snapshot.Evidence{{ID: "ev1", File: "svc.ts", Range: tc.rng}},
			}
			base := vgFor(nil, []snapshot.Edge{edge})
			head := vgFor(nil, []snapshot.Edge{edge})

			hunks := []gitdiff.FileHunks{
				{Path: "svc.ts", Hunks: []gitdiff.Hunk{{OldStart: tc.oldStart, OldLines: 0, NewStart: tc.oldStart, NewLines: 1}}},
			}

			delta := Diff(base, head, hunks)

			if len(delta.Touched) != 0 {
				t.Errorf("Touched = %+v, want none (boundary/single-line insertion must not intersect)", delta.Touched)
			}
		})
	}
}

// --- Diff: nil-safety for a missing VersionGraph ----------------------

// TestDiff_MissingOrigin_NilSafe: Origin:"missing" (ErrNoVersion — a
// revision whose history never committed a graph, or a repo predating graph
// adoption) is a first-class VersionGraph state this same task produces:
// Graph and Export are both nil. Diff must treat a nil Export as an empty
// snapshot rather than panicking — diffing a missing base against a
// populated head reports every head fact as Added (or StructuralAdded), and
// symmetrically for a missing head.
func TestDiff_MissingOrigin_NilSafe(t *testing.T) {
	missing := &VersionGraph{Origin: OriginMissing}
	populated := vgFor([]snapshot.Node{
		{ID: "contract:endpoint:d:foo", Layer: "contract", Type: "endpoint"},
	}, []snapshot.Edge{
		{ID: "a->b:CONTAINS", Type: "CONTAINS", From: "a", To: "b"},
	})

	delta := Diff(missing, populated, nil)
	if len(delta.Added) != 1 || delta.Added[0].ID != "contract:endpoint:d:foo" {
		t.Errorf("Added = %+v, want exactly [contract:endpoint:d:foo] (missing base -> everything in head is Added)", delta.Added)
	}
	if delta.StructuralAdded != 1 {
		t.Errorf("StructuralAdded = %d, want 1 (the CONTAINS edge)", delta.StructuralAdded)
	}
	if len(delta.Removed) != 0 || delta.StructuralRemoved != 0 {
		t.Errorf("Removed/StructuralRemoved = %+v/%d, want empty/0", delta.Removed, delta.StructuralRemoved)
	}

	delta2 := Diff(populated, missing, nil)
	if len(delta2.Removed) != 1 || delta2.Removed[0].ID != "contract:endpoint:d:foo" {
		t.Errorf("Removed = %+v, want exactly [contract:endpoint:d:foo] (missing head -> everything in base is Removed)", delta2.Removed)
	}
	if delta2.StructuralRemoved != 1 {
		t.Errorf("StructuralRemoved = %d, want 1 (the CONTAINS edge)", delta2.StructuralRemoved)
	}
	if len(delta2.Added) != 0 || delta2.StructuralAdded != 0 {
		t.Errorf("Added/StructuralAdded = %+v/%d, want empty/0", delta2.Added, delta2.StructuralAdded)
	}

	// A literal nil *VersionGraph (not just a nil Export) must be just as
	// safe — exportNodes/exportEdges check vg == nil before vg.Export.
	if delta3 := Diff(nil, nil, nil); delta3 == nil ||
		len(delta3.Added) != 0 || len(delta3.Removed) != 0 || len(delta3.Changed) != 0 ||
		len(delta3.Moved) != 0 || len(delta3.Touched) != 0 {
		t.Errorf("Diff(nil, nil, nil) = %+v, want an empty, non-nil GraphDelta", delta3)
	}
}

// --- Diff: structural facts are counted, never listed ----------------------

// TestDiff_StructuralCountedNotListed: a CONTAINS edge added in head is
// ClassStructural — it must increment StructuralAdded and must NOT appear
// in Added.
func TestDiff_StructuralCountedNotListed(t *testing.T) {
	base := vgFor(nil, nil)
	head := vgFor(nil, []snapshot.Edge{
		{ID: "a->b:CONTAINS", Type: "CONTAINS", From: "a", To: "b"},
	})

	delta := Diff(base, head, nil)

	if delta.StructuralAdded != 1 {
		t.Errorf("StructuralAdded = %d, want 1", delta.StructuralAdded)
	}
	if len(delta.Added) != 0 {
		t.Errorf("Added = %+v, want empty (CONTAINS is structural — counted, not listed)", delta.Added)
	}
}

// --- Classify ----------------------------------------------------------

// TestClassify_AllKinds pins one fact per FactClass from the brief's own
// classification table, plus the two explicit node exceptions
// (data:model/data:field -> ClassContract, data:enum stays ClassData).
func TestClassify_AllKinds(t *testing.T) {
	edgeFactOf := func(edgeType string) Fact {
		e := snapshot.Edge{ID: "x->y:" + edgeType, Type: edgeType, From: "x", To: "y"}
		return Fact{ID: e.ID, Kind: KindEdge, Edge: &e}
	}
	nodeFactOf := func(layer, typ string) Fact {
		n := snapshot.Node{ID: layer + ":" + typ + ":d:n", Layer: layer, Type: typ}
		return Fact{ID: n.ID, Kind: KindNode, Node: &n}
	}

	cases := []struct {
		name string
		fact Fact
		want FactClass
	}{
		{"CALLS_SERVICE", edgeFactOf("CALLS_SERVICE"), ClassBehaviorSync},
		{"CALLS_ENDPOINT", edgeFactOf("CALLS_ENDPOINT"), ClassBehaviorSync},
		{"INJECTS", edgeFactOf("INJECTS"), ClassBehaviorSync},

		{"PUBLISHES_TOPIC", edgeFactOf("PUBLISHES_TOPIC"), ClassBehaviorAsync},
		{"CONSUMES_TOPIC", edgeFactOf("CONSUMES_TOPIC"), ClassBehaviorAsync},

		{"USES_MODEL", edgeFactOf("USES_MODEL"), ClassData},
		{"REFERENCES_MODEL", edgeFactOf("REFERENCES_MODEL"), ClassData},
		{"READS_FIELD", edgeFactOf("READS_FIELD"), ClassData},
		{"WRITES_FIELD", edgeFactOf("WRITES_FIELD"), ClassData},
		{"data:enum node", nodeFactOf("data", "enum"), ClassData},

		{"TRIGGERS_FLOW", edgeFactOf("TRIGGERS_FLOW"), ClassFlow},
		{"REQUIRES", edgeFactOf("REQUIRES"), ClassFlow},

		{"EXPOSES_ENDPOINT", edgeFactOf("EXPOSES_ENDPOINT"), ClassContract},
		{"HAS_FIELD", edgeFactOf("HAS_FIELD"), ClassContract},
		{"contract layer node", nodeFactOf("contract", "endpoint"), ClassContract},
		{"data:model node", nodeFactOf("data", "model"), ClassContract},
		{"data:field node", nodeFactOf("data", "field"), ClassContract},

		{"CONTAINS", edgeFactOf("CONTAINS"), ClassStructural},
		{"code layer node", nodeFactOf("code", "module"), ClassStructural},
		{"service layer node", nodeFactOf("service", "service"), ClassStructural},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := Classify(c.fact); got != c.want {
				t.Errorf("Classify(%s) = %s, want %s", c.name, got, c.want)
			}
		})
	}
}
