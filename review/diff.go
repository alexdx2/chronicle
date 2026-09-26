package review

import (
	"bytes"
	"maps"
	"sort"

	"github.com/alexdx2/chronicle-core/gitdiff"
	"github.com/alexdx2/chronicle-core/snapshot"
)

// Changed is one fact present in both base and head, matched by id, whose
// semantic fields differ (see Diff's doc comment for exactly which fields
// count as semantic, per Kind).
type Changed struct {
	ID     string
	Kind   FactKind
	Fields []string
	Before Fact
	After  Fact
}

// Moved is one fact present in both base and head whose semantic fields are
// identical but whose evidence attachment changed to a different, non-empty
// set of evidence ids — the fact's own meaning didn't change, but what it's
// now sourced from did (e.g. re-extracted after a refactor).
type Moved struct {
	ID     string
	Kind   FactKind
	Before Fact
	After  Fact
}

// HunkRef names one diff hunk, by its base-side file path and OldStart/
// OldLines, that a Touched fact's base evidence range overlaps.
type HunkRef struct {
	File               string
	OldStart, OldLines int
}

// Touched is one fact, present and semantically unchanged in both base and
// head (not in Added/Removed/Changed/Moved), at least one of whose BASE
// evidence ranges overlaps a hunk in the diff between base and head — the
// fact's own meaning didn't change, but the code it was extracted from sits
// inside a changed region.
type Touched struct {
	ID    string
	Kind  FactKind
	Fact  Fact
	Hunks []HunkRef
}

// GraphDelta is Diff's result. Added/Removed/Changed/Moved/Touched list
// every NON-structural fact; a ClassStructural fact never appears in any of
// them by name — it only moves one of the three Structural* counters (see
// Diff's doc comment for exactly how a structural Moved- or Touched-shaped
// difference is counted).
type GraphDelta struct {
	Added, Removed []Fact
	Changed        []Changed
	Moved          []Moved
	Touched        []Touched

	StructuralAdded, StructuralRemoved, StructuralChanged int
}

// Diff compares base.Export and head.Export by id (node.id / edge.id).
// base and head may be nil, or have a nil Export (VersionGraph.Origin ==
// "missing") — treated as an empty snapshot, so diffing against a revision
// that predates graph adoption reports everything in the other side as
// Added or Removed rather than panicking.
//
// A fact present in only one side is Added (head only) or Removed (base
// only), unless it is ClassStructural, in which case it only increments
// StructuralAdded/StructuralRemoved.
//
// A fact present in both sides (matched by id) is compared on its "semantic
// fields": for a node, Layer, Type, Domain, Status, Attrs and Metadata; for
// an edge, Type, From, To, Derivation, DependencySource, Active and
// Metadata. Both records are re-canonicalized first via snapshot.Normalize
// (wrapped in a throwaway single-record snapshot — see task-9-report.md),
// so a mere Evidence/Alias reordering can never look like a semantic
// difference. If any semantic field differs, the fact is Changed (or, for
// ClassStructural, +1 to StructuralChanged) with Fields naming exactly which
// ones.
//
// Otherwise (semantic fields identical), Evidence is compared: if the full,
// order-normalized Evidence slices are not equal, AND both sides are
// non-empty, AND the two slides' evidence ids are not the same SET, the fact
// is Moved (ClassStructural: folded into StructuralChanged too — there is no
// dedicated StructuralMoved counter, and an evidence-set change is still a
// difference worth surfacing at the coarse structural-counter level; see
// task-9-report.md for the rejected alternative of dropping it silently
// like Structural Touched). Any other Evidence difference (one side empty,
// or the id sets match despite some other field differing) is treated the
// same as no difference at all: the fact does not enter the delta via
// Changed or Moved.
//
// A fact that reaches neither Changed nor Moved is a Touched candidate
// (ClassStructural facts are excluded from Touched entirely — "Structural
// Touched is not counted"): if any of its BASE evidence entries' (File,
// Range) overlaps a hunk for that file (matched rename-aware: a hunk's
// base-side file is its OldPath when set, else its Path — see
// task-9-report.md point 5), the fact is Touched, listing every such hunk.
// A hunk with OldLines==0 (pure insertion — zero width on the base side)
// never overlaps anything: nothing in base was actually touched at a
// zero-width point (documented judgment call; see task-9-report.md for the
// rejected alternative).
func Diff(base, head *VersionGraph, hunks []gitdiff.FileHunks) *GraphDelta {
	delta := &GraphDelta{}
	byFile := baseFileHunks(hunks)

	diffNodes(delta, exportNodes(base), exportNodes(head), byFile)
	diffEdges(delta, exportEdges(base), exportEdges(head), byFile)

	return delta
}

func exportNodes(vg *VersionGraph) []snapshot.Node {
	if vg == nil || vg.Export == nil {
		return nil
	}
	return vg.Export.Nodes
}

func exportEdges(vg *VersionGraph) []snapshot.Edge {
	if vg == nil || vg.Export == nil {
		return nil
	}
	return vg.Export.Edges
}

// baseFileHunks indexes hunks by their BASE-side file path. gitdiff.
// FileHunks.Path is the head-side (post-rename) path; OldPath is the
// base-side path when a rename occurred, empty otherwise. base.Export's
// evidence always carries base-side paths, so this is the index
// Touched-detection must probe against — a lookup by .Path alone would
// silently miss every renamed file (point 5).
func baseFileHunks(hunks []gitdiff.FileHunks) map[string][]gitdiff.Hunk {
	byFile := make(map[string][]gitdiff.Hunk, len(hunks))
	for _, fh := range hunks {
		key := fh.Path
		if fh.OldPath != "" {
			key = fh.OldPath
		}
		byFile[key] = append(byFile[key], fh.Hunks...)
	}
	return byFile
}

// unionSortedKeys returns every key present in a or b, each exactly once,
// sorted — used so Diff's output order is deterministic regardless of Go's
// randomized map iteration order.
func unionSortedKeys[T any](a, b map[string]T) []string {
	seen := make(map[string]bool, len(a)+len(b))
	keys := make([]string, 0, len(a)+len(b))
	for k := range a {
		seen[k] = true
		keys = append(keys, k)
	}
	for k := range b {
		if !seen[k] {
			seen[k] = true
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	return keys
}

func nodeFact(id string, n snapshot.Node) Fact {
	cp := n
	return Fact{ID: id, Kind: KindNode, Node: &cp}
}

func edgeFact(id string, e snapshot.Edge) Fact {
	cp := e
	return Fact{ID: id, Kind: KindEdge, Edge: &cp}
}

// route sends an Added/Removed fact either into the corresponding named
// list or into the matching Structural* counter, per Classify(f).
func route(delta *GraphDelta, f Fact, class FactClass, removed bool) {
	if class == ClassStructural {
		if removed {
			delta.StructuralRemoved++
		} else {
			delta.StructuralAdded++
		}
		return
	}
	if removed {
		delta.Removed = append(delta.Removed, f)
	} else {
		delta.Added = append(delta.Added, f)
	}
}

func routeChanged(delta *GraphDelta, c Changed, class FactClass) {
	if class == ClassStructural {
		delta.StructuralChanged++
		return
	}
	delta.Changed = append(delta.Changed, c)
}

func routeMoved(delta *GraphDelta, m Moved, class FactClass) {
	if class == ClassStructural {
		// No StructuralMoved counter exists on GraphDelta. An evidence-set
		// change is still a real difference (unlike Structural Touched,
		// which the brief explicitly says is not counted at all), so it
		// folds into StructuralChanged rather than vanishing — see Diff's
		// doc comment and task-9-report.md for the rejected alternative.
		delta.StructuralChanged++
		return
	}
	delta.Moved = append(delta.Moved, m)
}

// --- nodes -----------------------------------------------------------------

func diffNodes(delta *GraphDelta, base, head []snapshot.Node, byFile map[string][]gitdiff.Hunk) {
	baseByID := make(map[string]snapshot.Node, len(base))
	for _, n := range base {
		baseByID[n.ID] = n
	}
	headByID := make(map[string]snapshot.Node, len(head))
	for _, n := range head {
		headByID[n.ID] = n
	}

	for _, id := range unionSortedKeys(baseByID, headByID) {
		b, inBase := baseByID[id]
		h, inHead := headByID[id]

		switch {
		case inBase && !inHead:
			f := nodeFact(id, b)
			route(delta, f, Classify(f), true)
		case !inBase && inHead:
			f := nodeFact(id, h)
			route(delta, f, Classify(f), false)
		default:
			diffMatchedNodes(delta, id, b, h, byFile)
		}
	}
}

func diffMatchedNodes(delta *GraphDelta, id string, b, h snapshot.Node, byFile map[string][]gitdiff.Hunk) {
	nb := normalizedNode(b)
	nh := normalizedNode(h)
	// Layer/Type/Domain are baked into a node's ID (node_key =
	// layer:type:domain:qualified_name), so for a matched-by-id pair they
	// are always equal already — Classify's result is therefore the same
	// regardless of which side's copy is passed here.
	class := Classify(nodeFact(id, h))

	if fields := nodeSemanticDiff(nb, nh); len(fields) > 0 {
		routeChanged(delta, Changed{
			ID: id, Kind: KindNode, Fields: fields,
			Before: nodeFact(id, b), After: nodeFact(id, h),
		}, class)
		return
	}

	if evidenceIsMoved(nb.Evidence, nh.Evidence) {
		routeMoved(delta, Moved{ID: id, Kind: KindNode, Before: nodeFact(id, b), After: nodeFact(id, h)}, class)
		return
	}

	if class == ClassStructural {
		return
	}
	if refs := touchedHunks(b.Evidence, byFile); len(refs) > 0 {
		delta.Touched = append(delta.Touched, Touched{ID: id, Kind: KindNode, Fact: nodeFact(id, b), Hunks: refs})
	}
}

func nodeSemanticDiff(before, after snapshot.Node) []string {
	var fields []string
	if before.Layer != after.Layer {
		fields = append(fields, "Layer")
	}
	if before.Type != after.Type {
		fields = append(fields, "Type")
	}
	if before.Domain != after.Domain {
		fields = append(fields, "Domain")
	}
	if before.Status != after.Status {
		fields = append(fields, "Status")
	}
	if !maps.Equal(before.Attrs, after.Attrs) {
		fields = append(fields, "Attrs")
	}
	if !bytes.Equal(before.Metadata, after.Metadata) {
		fields = append(fields, "Metadata")
	}
	return fields
}

// --- edges -------------------------------------------------------------

func diffEdges(delta *GraphDelta, base, head []snapshot.Edge, byFile map[string][]gitdiff.Hunk) {
	baseByID := make(map[string]snapshot.Edge, len(base))
	for _, e := range base {
		baseByID[e.ID] = e
	}
	headByID := make(map[string]snapshot.Edge, len(head))
	for _, e := range head {
		headByID[e.ID] = e
	}

	for _, id := range unionSortedKeys(baseByID, headByID) {
		b, inBase := baseByID[id]
		h, inHead := headByID[id]

		switch {
		case inBase && !inHead:
			f := edgeFact(id, b)
			route(delta, f, Classify(f), true)
		case !inBase && inHead:
			f := edgeFact(id, h)
			route(delta, f, Classify(f), false)
		default:
			diffMatchedEdges(delta, id, b, h, byFile)
		}
	}
}

func diffMatchedEdges(delta *GraphDelta, id string, b, h snapshot.Edge, byFile map[string][]gitdiff.Hunk) {
	nb := normalizedEdge(b)
	nh := normalizedEdge(h)
	// Type/From/To are baked into an edge's ID (edge_key =
	// from_node_key->to_node_key:TYPE), so for a matched-by-id pair they are
	// always equal already — Classify's result is the same regardless of
	// which side's copy is passed here.
	class := Classify(edgeFact(id, h))

	if fields := edgeSemanticDiff(nb, nh); len(fields) > 0 {
		routeChanged(delta, Changed{
			ID: id, Kind: KindEdge, Fields: fields,
			Before: edgeFact(id, b), After: edgeFact(id, h),
		}, class)
		return
	}

	if evidenceIsMoved(nb.Evidence, nh.Evidence) {
		routeMoved(delta, Moved{ID: id, Kind: KindEdge, Before: edgeFact(id, b), After: edgeFact(id, h)}, class)
		return
	}

	if class == ClassStructural {
		return
	}
	if refs := touchedHunks(b.Evidence, byFile); len(refs) > 0 {
		delta.Touched = append(delta.Touched, Touched{ID: id, Kind: KindEdge, Fact: edgeFact(id, b), Hunks: refs})
	}
}

func edgeSemanticDiff(before, after snapshot.Edge) []string {
	var fields []string
	if before.Type != after.Type {
		fields = append(fields, "Type")
	}
	if before.From != after.From {
		fields = append(fields, "From")
	}
	if before.To != after.To {
		fields = append(fields, "To")
	}
	if before.Derivation != after.Derivation {
		fields = append(fields, "Derivation")
	}
	if before.DependencySource != after.DependencySource {
		fields = append(fields, "DependencySource")
	}
	if before.Active != after.Active {
		fields = append(fields, "Active")
	}
	if !bytes.Equal(before.Metadata, after.Metadata) {
		fields = append(fields, "Metadata")
	}
	return fields
}

// --- normalization & evidence comparison ------------------------------

// normalizedNode returns a copy of n with Evidence/Aliases sorted via
// snapshot.Normalize (wrapped in a throwaway single-node snapshot, per
// task-9-report.md), WITHOUT mutating n's own slices. n's Evidence/Aliases
// slice headers are copied by value into cp, but they still alias the SAME
// backing arrays as whatever Export slice n came from — sorting cp's copy of
// those slices in place would silently reorder base.Export/head.Export too.
// Diff must not have that side effect just from comparing its inputs, so the
// slices are copied one level deeper before normalizing.
func normalizedNode(n snapshot.Node) snapshot.Node {
	cp := n
	cp.Evidence = append([]snapshot.Evidence(nil), n.Evidence...)
	cp.Aliases = append([]snapshot.Alias(nil), n.Aliases...)
	tmp := &snapshot.Snapshot{Nodes: []snapshot.Node{cp}}
	snapshot.Normalize(tmp)
	return tmp.Nodes[0]
}

// normalizedEdge is normalizedNode's edge counterpart.
func normalizedEdge(e snapshot.Edge) snapshot.Edge {
	cp := e
	cp.Evidence = append([]snapshot.Evidence(nil), e.Evidence...)
	tmp := &snapshot.Snapshot{Edges: []snapshot.Edge{cp}}
	snapshot.Normalize(tmp)
	return tmp.Edges[0]
}

// evidenceIsMoved decides the Moved-vs-drop question for two ALREADY
// order-normalized Evidence slices, per Diff's doc comment: the fact must
// have some actual Evidence difference, both sides must carry at least one
// evidence entry, and the two sides' evidence ids must differ as a SET (not
// merely as a slice — order was already normalized away).
func evidenceIsMoved(before, after []snapshot.Evidence) bool {
	if evidenceSliceEqual(before, after) {
		return false
	}
	if len(before) == 0 || len(after) == 0 {
		return false
	}
	return !evidenceIDSetsEqual(before, after)
}

func evidenceSliceEqual(a, b []snapshot.Evidence) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if !evidenceEqual(a[i], b[i]) {
			return false
		}
	}
	return true
}

// evidenceEqual compares every field of two Evidence records. Written out
// field-by-field (rather than reflect.DeepEqual on the whole struct) so a
// nil Assertion and an empty-but-non-nil one compare equal via bytes.Equal,
// the same way Go already treats them as interchangeable everywhere else in
// this codebase (canonicalizeRaw, etc.) — reflect.DeepEqual would not, and
// that mismatch is exactly the kind of accidental false-Moved this
// comparison must not produce.
func evidenceEqual(a, b snapshot.Evidence) bool {
	return a.ID == b.ID &&
		a.Source == b.Source &&
		a.File == b.File &&
		a.Blob == b.Blob &&
		a.Range == b.Range &&
		a.Locator == b.Locator &&
		a.Extractor == b.Extractor &&
		a.ExtractorVersion == b.ExtractorVersion &&
		a.Rule == b.Rule &&
		a.Polarity == b.Polarity &&
		a.Status == b.Status &&
		a.Verification == b.Verification &&
		a.Confidence == b.Confidence &&
		bytes.Equal(a.Assertion, b.Assertion) &&
		a.AssertionKind == b.AssertionKind &&
		a.AssertionVersion == b.AssertionVersion
}

func evidenceIDSet(ev []snapshot.Evidence) map[string]bool {
	set := make(map[string]bool, len(ev))
	for _, e := range ev {
		set[e.ID] = true
	}
	return set
}

func evidenceIDSetsEqual(a, b []snapshot.Evidence) bool {
	return maps.Equal(evidenceIDSet(a), evidenceIDSet(b))
}

// --- Touched: base evidence vs. diff hunks ------------------------------

// touchedHunks returns every hunk (deduplicated, sorted by File then
// OldStart) that overlaps at least one of evidence's (File, Range) entries.
// evidence must be BASE-side evidence — see Diff's doc comment and
// baseFileHunks.
func touchedHunks(evidence []snapshot.Evidence, byFile map[string][]gitdiff.Hunk) []HunkRef {
	seen := make(map[HunkRef]bool)
	var refs []HunkRef
	for _, ev := range evidence {
		lineStart, lineEnd, _, _ := snapshot.ParseRange(ev.Range)
		if lineStart == 0 && lineEnd == 0 {
			continue // no location info: cannot overlap anything
		}
		for _, h := range byFile[ev.File] {
			if h.OldLines == 0 {
				// Pure insertion: zero-width base range. Nothing in base was
				// actually touched at a zero-width point (documented
				// judgment call — see Diff's doc comment).
				continue
			}
			hunkEnd := h.OldStart + h.OldLines - 1
			if lineStart > hunkEnd || h.OldStart > lineEnd {
				continue // disjoint ranges
			}
			ref := HunkRef{File: ev.File, OldStart: h.OldStart, OldLines: h.OldLines}
			if !seen[ref] {
				seen[ref] = true
				refs = append(refs, ref)
			}
		}
	}
	sort.Slice(refs, func(i, j int) bool {
		if refs[i].File != refs[j].File {
			return refs[i].File < refs[j].File
		}
		return refs[i].OldStart < refs[j].OldStart
	})
	return refs
}
