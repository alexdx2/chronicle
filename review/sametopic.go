package review

import (
	"sort"

	"github.com/alexdx2/chronicle-core/snapshot"
)

// SameSystemTopic is a PUBLISHES_TOPIC / CONSUMES_TOPIC edge added by a node
// of a system that already had an edge of the same type on the same topic in
// base. The system's relationship to the topic did not change: only the node
// the extractor attributes it to did. Informational, never a change.
const SameSystemTopic ChangeKind = "same_system_topic"

// ReasonSameSystemTopic is the Reason of a SameSystemTopic finding.
const ReasonSameSystemTopic = "same_system_topic"

// SameSystemTopicEdges returns the indices into added of topic edges whose
// topic already had an active edge of the same type in base from a node that
// collapses to the same system as the added edge's node (addedSys resolves a
// head node, baseSys a base node). A nil resolver or an unresolved node never
// matches.
func SameSystemTopicEdges(added []Fact, base *snapshot.Snapshot, addedSys, baseSys func(string) SystemRef) []int {
	if base == nil || addedSys == nil || baseSys == nil {
		return nil
	}
	type key struct{ typ, topic string }
	baseSystems := map[key]map[string]bool{}
	for _, e := range base.Edges {
		if !e.Active || (e.Type != "PUBLISHES_TOPIC" && e.Type != "CONSUMES_TOPIC") {
			continue
		}
		sys := baseSys(e.From).Key
		if sys == "" {
			continue
		}
		k := key{e.Type, e.To}
		if baseSystems[k] == nil {
			baseSystems[k] = map[string]bool{}
		}
		baseSystems[k][sys] = true
	}
	var out []int
	for i, f := range added {
		if f.Kind != KindEdge || f.Edge == nil || (f.Edge.Type != "PUBLISHES_TOPIC" && f.Edge.Type != "CONSUMES_TOPIC") {
			continue
		}
		if sys := addedSys(f.Edge.From).Key; sys != "" && baseSystems[key{f.Edge.Type, f.Edge.To}][sys] {
			out = append(out, i)
		}
	}
	return out
}

// splitSameSystemTopic removes same-system topic edges from d (a copy is
// returned) and returns them as SameSystemTopic Changes seeded on head.
func splitSameSystemTopic(d *GraphDelta, base *snapshot.Snapshot, baseQ, headQ ImpactQuerier) (*GraphDelta, []Change) {
	if d == nil {
		return nil, nil
	}
	idx := SameSystemTopicEdges(d.Added, base, collapser(headQ), collapser(baseQ))
	if len(idx) == 0 {
		return d, nil
	}
	drop := map[int]bool{}
	var out []Change
	for _, i := range idx {
		drop[i] = true
		f := d.Added[i]
		out = append(out, Change{Kind: SameSystemTopic, SourceID: f.Edge.From, Counterpart: f.Edge.To, Seed: seedHead, Facts: []Fact{f}})
	}
	cp := *d
	cp.Added = nil
	for i, f := range d.Added {
		if !drop[i] {
			cp.Added = append(cp.Added, f)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].SourceID != out[j].SourceID {
			return out[i].SourceID < out[j].SourceID
		}
		return out[i].Counterpart < out[j].Counterpart
	})
	return &cp, out
}

// sameSystemTopicFindings turns SameSystemTopic Changes into informational
// findings ("another node of the same system on this topic").
func sameSystemTopicFindings(changes []Change, headQ ImpactQuerier) []ReviewFinding {
	out := make([]ReviewFinding, 0, len(changes))
	for _, c := range changes {
		file := ""
		if files := factFiles(c.Facts[0]); len(files) > 0 {
			file = files[0]
		}
		out = append(out, ReviewFinding{
			Severity:    SeverityInformational,
			Kind:        c.Kind,
			System:      safeCollapse(headQ, c.SourceID),
			File:        file,
			Reason:      ReasonSameSystemTopic,
			Counterpart: c.Counterpart,
			Facts:       c.Facts,
			Evidence:    flattenEvidence(c.Facts),
		})
	}
	return out
}
