package review

import (
	"fmt"
	"sort"
	"strings"

	"github.com/alexdx2/chronicle-core/gitdiff"
	"github.com/alexdx2/chronicle-core/registry"
	"github.com/alexdx2/chronicle-core/snapshot"
)

// IntegrationMoved is one integration whose caller changed inside the merge
// request: the base graph had A -> T, the head graph has B -> T (same edge
// type, same target T), and the code of both A and B is in the diff. It is
// reported as one informational finding instead of "integration added" plus
// "dependency removed": what the system depends on did not change.
const IntegrationMoved ChangeKind = "integration_moved"

// ReasonIntegrationMoved is the Reason of an IntegrationMoved finding.
const ReasonIntegrationMoved = "integration_moved"

// movableEdgeTypes are the sync call edges a caller can move between files
// without the dependency itself changing.
var movableEdgeTypes = map[string]bool{"CALLS_SERVICE": true, "CALLS_ENDPOINT": true}

// MovedPair is an added and a removed call edge of one moved integration.
type MovedPair struct {
	Added, Removed Fact
}

// changedPathSet is every path a diff names, old paths included.
func changedPathSet(changed []gitdiff.ChangedFile) map[string]bool {
	m := make(map[string]bool, len(changed)*2)
	for _, cf := range changed {
		m[cf.Path] = true
		if cf.OldPath != "" {
			m[cf.OldPath] = true
		}
	}
	return m
}

func factInDiff(f Fact, inDiff map[string]bool) bool {
	for _, p := range factFiles(f) {
		if inDiff[p] {
			return true
		}
	}
	return false
}

// MovedIntegrationPairs pairs each removed call edge with an added one of the
// same type and target, both with evidence in a changed file. Pairing is one
// to one and deterministic (sorted by edge id); an edge no counterpart claims
// stays out. The indices are into added and removed.
//
// Both callers must also belong to one system (the collapse the review names
// systems with: addedSys resolves a head node, removedSys a base node), or,
// when neither collapses to a service, to one domain. A nil resolver or an
// unresolved caller never pairs: two services of one monorepo calling the
// same target are an addition and a removal, not a move.
func MovedIntegrationPairs(added, removed []Fact, changed []gitdiff.ChangedFile, addedSys, removedSys func(nodeKey string) SystemRef) (pairs [][2]int) {
	inDiff := changedPathSet(changed)
	eligible := func(f Fact) bool {
		return f.Kind == KindEdge && f.Edge != nil && f.Edge.To != "" && movableEdgeTypes[f.Edge.Type] && factInDiff(f, inDiff)
	}
	order := func(facts []Fact) []int {
		var idx []int
		for i, f := range facts {
			if eligible(f) {
				idx = append(idx, i)
			}
		}
		sort.SliceStable(idx, func(a, b int) bool { return facts[idx[a]].Edge.From < facts[idx[b]].Edge.From })
		return idx
	}
	used := map[int]bool{}
	for _, ri := range order(removed) {
		r := removed[ri].Edge
		for _, ai := range order(added) {
			a := added[ai].Edge
			if used[ai] || a.Type != r.Type || a.To != r.To || a.From == r.From {
				continue
			}
			if !sameOwner(addedSys, removedSys, a.From, r.From) {
				continue
			}
			used[ai] = true
			pairs = append(pairs, [2]int{ai, ri})
			break
		}
	}
	return pairs
}

// sameOwner reports whether the added caller (head side) and the removed
// caller (base side) collapse to the same system, or, when neither is a
// service, to the same domain.
func sameOwner(addedSys, removedSys func(string) SystemRef, addedFrom, removedFrom string) bool {
	if addedSys == nil || removedSys == nil {
		return false
	}
	a, r := addedSys(addedFrom), removedSys(removedFrom)
	if a.Key == "" || r.Key == "" {
		return false
	}
	if a.Key == r.Key {
		return true
	}
	return a.Kind != kindService && r.Kind != kindService && a.Domain != "" && a.Domain == r.Domain
}

func collapser(q ImpactQuerier) func(string) SystemRef {
	if q == nil {
		return nil
	}
	return func(key string) SystemRef { return safeCollapse(q, key) }
}

// SnapshotCollapse returns the system collapse of snap's graph, the same one
// the review uses for its findings. A nil or unloadable snapshot yields a
// resolver that resolves nothing. release must be called when done.
func SnapshotCollapse(snap *snapshot.Snapshot) (collapse func(string) SystemRef, release func()) {
	none := func(string) SystemRef { return SystemRef{} }
	if snap == nil {
		return none, func() {}
	}
	reg, err := registry.LoadDefaults()
	if err != nil {
		return none, func() {}
	}
	g, closer, err := snapshot.Load(snap, reg, snapshot.LoadOptions{})
	if err != nil {
		return none, func() {}
	}
	q := CoreImpactQuerier{Graph: g}
	return collapser(q), func() { _ = closer() }
}

// splitMovedIntegrations removes the moved pairs from d (a copy is returned,
// d is not modified) and returns them as IntegrationMoved Changes seeded on
// head, with the new caller as the source.
func splitMovedIntegrations(d *GraphDelta, changed []gitdiff.ChangedFile, baseQ, headQ ImpactQuerier) (*GraphDelta, []Change) {
	if d == nil {
		return nil, nil
	}
	pairs := MovedIntegrationPairs(d.Added, d.Removed, changed, collapser(headQ), collapser(baseQ))
	if len(pairs) == 0 {
		return d, nil
	}
	dropA, dropR := map[int]bool{}, map[int]bool{}
	var moved []Change
	for _, p := range pairs {
		a, r := d.Added[p[0]], d.Removed[p[1]]
		dropA[p[0]], dropR[p[1]] = true, true
		moved = append(moved, Change{
			Kind: IntegrationMoved, SourceID: a.Edge.From, Counterpart: a.Edge.To,
			Seed: seedHead, Facts: []Fact{r, a},
		})
	}
	out := *d
	out.Added, out.Removed = nil, nil
	for i, f := range d.Added {
		if !dropA[i] {
			out.Added = append(out.Added, f)
		}
	}
	for i, f := range d.Removed {
		if !dropR[i] {
			out.Removed = append(out.Removed, f)
		}
	}
	sort.SliceStable(moved, func(i, j int) bool {
		if moved[i].SourceID != moved[j].SourceID {
			return moved[i].SourceID < moved[j].SourceID
		}
		return moved[i].Counterpart < moved[j].Counterpart
	})
	return &out, moved
}

// movedFindings turns IntegrationMoved Changes into informational findings.
func movedFindings(moved []Change, headQ ImpactQuerier) []ReviewFinding {
	out := make([]ReviewFinding, 0, len(moved))
	for _, c := range moved {
		var files []string
		for _, f := range c.Facts {
			files = appendUnique(files, factFiles(f)...)
		}
		file := ""
		if len(files) > 0 {
			file = files[0]
		}
		out = append(out, ReviewFinding{
			Severity:    SeverityInformational,
			Kind:        IntegrationMoved,
			System:      safeCollapse(headQ, c.SourceID),
			File:        file,
			Reason:      ReasonIntegrationMoved,
			Counterpart: c.Counterpart,
			Facts:       c.Facts,
			Evidence:    flattenEvidence(c.Facts),
		})
	}
	return out
}

// MovedIntegrations lists the moved-integration findings of r.
func (r *ReviewResult) MovedIntegrations() []ReviewFinding {
	var out []ReviewFinding
	for _, f := range r.Findings {
		if f.Severity == SeverityInformational && f.Reason == ReasonIntegrationMoved {
			out = append(out, f)
		}
	}
	return out
}

// movedSection is the collapsed "Moved integrations" block.
func movedSection(r *ReviewResult, maxRows int) string {
	moved := r.MovedIntegrations()
	if len(moved) == 0 {
		return ""
	}
	rows := make([]mdRow, 0, len(moved))
	for _, f := range moved {
		var from, to string
		if len(f.Facts) == 2 && f.Facts[0].Edge != nil && f.Facts[1].Edge != nil {
			from, to = f.Facts[0].Edge.From, f.Facts[1].Edge.From // [removed, added]
		}
		if to == "" {
			to = f.System.Name
		}
		rows = append(rows, mdRow{
			escapeText(to) + " " + edgeVerb(factEdgeType(f)) + " " + escapeText(f.Counterpart),
			"was " + escapeText(from),
		})
	}
	var b strings.Builder
	b.WriteString("<details>\n<summary>")
	b.WriteString(fmt.Sprintf("%d", len(moved)) + " " + pluralize(len(moved), "integration moved", "integrations moved"))
	b.WriteString(" inside this change (same target, different caller)</summary>\n\n")
	b.WriteString(mdTable([]string{"Integration", "Before"}, rows, maxRows))
	b.WriteString("\n</details>\n")
	return b.String()
}
