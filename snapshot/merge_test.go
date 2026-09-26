package snapshot

import "testing"

// --- fixtures ---------------------------------------------------------

func mergeNode(id string, evidenceFile string) Node {
	n := Node{Kind: "node", ID: id, Layer: "code", Type: "module", Domain: "orders", Name: id, Status: "active"}
	if evidenceFile != "" {
		n.Evidence = []Evidence{{ID: id + "#ev1", Source: "code", File: evidenceFile, Blob: "blob1", Extractor: "chronicle-scan"}}
	}
	return n
}

func mergeEdge(id, from, to string, evidenceFile string) Edge {
	e := Edge{Kind: "edge", ID: id, Type: "CALLS", From: from, To: to, Derivation: "hard", Active: true}
	if evidenceFile != "" {
		e.Evidence = []Evidence{{ID: id + "#ev1", Source: "code", File: evidenceFile, Blob: "blob1", Extractor: "chronicle-scan"}}
	}
	return e
}

func hasNode(s *Snapshot, id string) bool {
	for _, n := range s.Nodes {
		if n.ID == id {
			return true
		}
	}
	return false
}

func findNode(s *Snapshot, id string) (Node, bool) {
	for _, n := range s.Nodes {
		if n.ID == id {
			return n, true
		}
	}
	return Node{}, false
}

func findEdge(s *Snapshot, id string) (Edge, bool) {
	for _, e := range s.Edges {
		if e.ID == id {
			return e, true
		}
	}
	return Edge{}, false
}

func findCoverage(s *Snapshot, path string) (Coverage, bool) {
	for _, c := range s.Coverage {
		if c.Path == path {
			return c, true
		}
	}
	return Coverage{}, false
}

// --- tests --------------------------------------------------------------

// TestMerge_IndependentChanges: base/ours/theirs with non-overlapping
// additions — both sets end up in the result untouched.
func TestMerge_IndependentChanges(t *testing.T) {
	base := &Snapshot{
		Header: Header{Kind: "header", Format: Format, Version: Version, FactSchema: FactSchema, Seq: 3},
		Nodes:  []Node{mergeNode("code:module:orders:shared", "")},
	}
	ours := &Snapshot{
		Header: Header{Kind: "header", Format: Format, Version: Version, FactSchema: FactSchema, Seq: 4},
		Nodes:  []Node{mergeNode("code:module:orders:shared", ""), mergeNode("code:module:orders:ours-only", "")},
	}
	theirs := &Snapshot{
		Header: Header{Kind: "header", Format: Format, Version: Version, FactSchema: FactSchema, Seq: 5},
		Nodes:  []Node{mergeNode("code:module:orders:shared", ""), mergeNode("code:module:orders:theirs-only", "")},
	}

	got := Merge(base, ours, theirs)

	if len(got.Nodes) != 3 {
		t.Fatalf("want 3 nodes (shared + ours-only + theirs-only), got %d: %+v", len(got.Nodes), got.Nodes)
	}
	for _, id := range []string{"code:module:orders:shared", "code:module:orders:ours-only", "code:module:orders:theirs-only"} {
		if !hasNode(got, id) {
			t.Errorf("missing node %q in merged result", id)
		}
	}

	// Header.Seq = max(ours.Seq, theirs.Seq) + 1 = max(4,5)+1 = 6.
	if got.Header.Seq != 6 {
		t.Errorf("Header.Seq = %d, want 6 (max(ours=4, theirs=5)+1)", got.Header.Seq)
	}
}

// TestMerge_SameChange: both sides added the identical record — the result
// keeps exactly one copy, not two.
func TestMerge_SameChange(t *testing.T) {
	base := &Snapshot{Header: Header{Format: Format, Version: Version, FactSchema: FactSchema}}
	added := mergeNode("code:module:orders:new", "")
	ours := &Snapshot{Header: Header{Format: Format, Version: Version, FactSchema: FactSchema, Seq: 1}, Nodes: []Node{added}}
	theirs := &Snapshot{Header: Header{Format: Format, Version: Version, FactSchema: FactSchema, Seq: 1}, Nodes: []Node{added}}

	got := Merge(base, ours, theirs)

	count := 0
	for _, n := range got.Nodes {
		if n.ID == "code:module:orders:new" {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("want exactly 1 copy of the identically-added node, got %d", count)
	}
}

// TestMerge_Conflict: both sides changed the same node differently — the
// result contains Ours' version, and the Coverage for the file evidenced by
// EITHER version has Blob=="" (stale).
func TestMerge_Conflict(t *testing.T) {
	const id = "code:module:orders:disputed"
	base := &Snapshot{
		Header: Header{Format: Format, Version: Version, FactSchema: FactSchema},
		Nodes:  []Node{mergeNode(id, "orders/disputed.go")},
		Coverage: []Coverage{
			{Kind: "coverage", Path: "orders/disputed.go", Blob: "blob1"},
		},
	}

	oursNode := mergeNode(id, "orders/disputed.go")
	oursNode.Name = "OursName"
	oursNode.Evidence[0].Blob = "blob-ours"

	theirsNode := mergeNode(id, "orders/disputed-theirs.go")
	theirsNode.Name = "TheirsName"

	ours := &Snapshot{
		Header: Header{Format: Format, Version: Version, FactSchema: FactSchema, Seq: 1},
		Nodes:  []Node{oursNode},
		Coverage: []Coverage{
			{Kind: "coverage", Path: "orders/disputed.go", Blob: "blob-ours"},
		},
	}
	theirs := &Snapshot{
		Header: Header{Format: Format, Version: Version, FactSchema: FactSchema, Seq: 1},
		Nodes:  []Node{theirsNode},
		Coverage: []Coverage{
			{Kind: "coverage", Path: "orders/disputed-theirs.go", Blob: "blob-theirs"},
		},
	}

	got := Merge(base, ours, theirs)

	n, ok := findNode(got, id)
	if !ok {
		t.Fatalf("disputed node %q missing from merge result", id)
	}
	if n.Name != "OursName" {
		t.Errorf("conflict must resolve to Ours: Name = %q, want %q", n.Name, "OursName")
	}

	// Both versions' evidenced files must be marked stale (Blob=="").
	for _, path := range []string{"orders/disputed.go", "orders/disputed-theirs.go"} {
		cov, ok := findCoverage(got, path)
		if !ok {
			t.Fatalf("expected a Coverage entry for %q after conflict, found none", path)
		}
		if cov.Blob != "" {
			t.Errorf("Coverage(%q).Blob = %q, want \"\" (stale) after disputed node evidence", path, cov.Blob)
		}
	}
}

// TestMerge_DeleteVsChange: theirs deleted an edge, ours changed its
// metadata — Ours wins (the edge remains), and the file the ours-version's
// evidence points at becomes stale.
func TestMerge_DeleteVsChange(t *testing.T) {
	const edgeID = "code:module:orders:a->code:module:orders:b:CALLS"
	nodeA := mergeNode("code:module:orders:a", "")
	nodeB := mergeNode("code:module:orders:b", "")
	baseEdge := mergeEdge(edgeID, nodeA.ID, nodeB.ID, "orders/a.go")

	base := &Snapshot{
		Header: Header{Format: Format, Version: Version, FactSchema: FactSchema},
		Nodes:  []Node{nodeA, nodeB},
		Edges:  []Edge{baseEdge},
		Coverage: []Coverage{
			{Kind: "coverage", Path: "orders/a.go", Blob: "blob1"},
		},
	}

	oursEdge := mergeEdge(edgeID, nodeA.ID, nodeB.ID, "orders/a.go")
	oursEdge.Context = "changed-by-ours"
	oursEdge.Evidence[0].Blob = "blob-ours"

	ours := &Snapshot{
		Header: Header{Format: Format, Version: Version, FactSchema: FactSchema, Seq: 2},
		Nodes:  []Node{nodeA, nodeB},
		Edges:  []Edge{oursEdge},
		Coverage: []Coverage{
			{Kind: "coverage", Path: "orders/a.go", Blob: "blob-ours"},
		},
	}
	// theirs deleted the edge entirely (nodes stay, edge is gone).
	theirs := &Snapshot{
		Header: Header{Format: Format, Version: Version, FactSchema: FactSchema, Seq: 1},
		Nodes:  []Node{nodeA, nodeB},
		Edges:  []Edge{},
		Coverage: []Coverage{
			{Kind: "coverage", Path: "orders/a.go", Blob: "blob1"},
		},
	}

	got := Merge(base, ours, theirs)

	e, ok := findEdge(got, edgeID)
	if !ok {
		t.Fatalf("edge %q should remain (Ours wins over Theirs' delete), but is absent", edgeID)
	}
	if e.Context != "changed-by-ours" {
		t.Errorf("edge Context = %q, want ours' \"changed-by-ours\"", e.Context)
	}

	cov, ok := findCoverage(got, "orders/a.go")
	if !ok {
		t.Fatalf("expected a Coverage entry for orders/a.go, found none")
	}
	if cov.Blob != "" {
		t.Errorf("Coverage(orders/a.go).Blob = %q, want \"\" (stale) — delete-vs-change is a conflict too", cov.Blob)
	}
}
