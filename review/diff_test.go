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

// TestDiff_Touched_PureInsertionNoIntersect: a hunk with OldLines==0 is a
// pure insertion — zero width on the base side. Documented judgment call:
// nothing in base was actually touched at a zero-width point, so this must
// NOT produce a Touched fact even though OldStart falls inside the
// evidence's range.
func TestDiff_Touched_PureInsertionNoIntersect(t *testing.T) {
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

	if len(delta.Touched) != 0 {
		t.Errorf("Touched = %+v, want none (OldLines==0 pure insertion must not intersect)", delta.Touched)
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
