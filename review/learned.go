package review

import (
	"sort"

	"github.com/alexdx2/chronicle-core/gitdiff"
	"github.com/alexdx2/chronicle-core/snapshot"
)

// LearnedFact is a Change whose every evidence file lies outside the diff:
// the head graph knows it (or no longer knows it) because a model re-read a
// graph neighbour, not because the merge request touched anything. It is
// reported as information, never as a change of this merge request.
type LearnedFact struct {
	// Change is the classification the fact would have received had it been
	// a code change (its Kind names the finding).
	Change Change
	// File is the first evidence file; Files is every evidence file of the
	// underlying fact(s), in order of first appearance.
	File  string
	Files []string
}

// partitionLearned splits d into the part that is a change of this diff
// (code) and the facts learned from files outside it (learned).
//
// A fact in Added, Removed or Changed is learned when it carries at least
// one evidence file and none of its evidence files (head side for Added,
// base side for Removed, both sides for Changed) is in changed. A changed
// file counts by Path and by OldPath; a deleted file is in the diff like
// any other. A fact with no file-bearing evidence at all stays in code,
// exactly as before. Moved and Touched are untouched: Touched is by
// construction evidence inside the diff's hunks, and Moved never produces a
// Change. d itself is not modified.
func partitionLearned(d *GraphDelta, changed []gitdiff.ChangedFile) (code *GraphDelta, learned []LearnedFact) {
	if d == nil {
		return nil, nil
	}
	inDiff := make(map[string]bool, len(changed)*2)
	for _, cf := range changed {
		inDiff[cf.Path] = true
		if cf.OldPath != "" {
			inDiff[cf.OldPath] = true
		}
	}
	isLearned := func(files []string) bool {
		if len(files) == 0 {
			return false
		}
		for _, f := range files {
			if inDiff[f] {
				return false
			}
		}
		return true
	}

	code = &GraphDelta{
		Moved:             d.Moved,
		Touched:           d.Touched,
		StructuralAdded:   d.StructuralAdded,
		StructuralRemoved: d.StructuralRemoved,
		StructuralChanged: d.StructuralChanged,
	}
	ld := &GraphDelta{}
	// A merge pair (edge + its new/removed target node) is one unit: it is
	// learned only if both members are, otherwise the whole pair stays code.
	split := func(facts []Fact) (c, l []Fact) {
		partner := make(map[int]int)
		for _, p := range mergePairs(facts, map[int]bool{}) {
			partner[p.edge], partner[p.node] = p.node, p.edge
		}
		for i, f := range facts {
			learnedFact := isLearned(factFiles(f))
			if j, ok := partner[i]; ok {
				learnedFact = learnedFact && isLearned(factFiles(facts[j]))
			}
			if learnedFact {
				l = append(l, f)
			} else {
				c = append(c, f)
			}
		}
		return c, l
	}
	code.Added, ld.Added = split(d.Added)
	code.Removed, ld.Removed = split(d.Removed)
	for _, c := range d.Changed {
		if isLearned(appendUnique(factFiles(c.Before), factFiles(c.After)...)) {
			ld.Changed = append(ld.Changed, c)
		} else {
			code.Changed = append(code.Changed, c)
		}
	}

	for _, ch := range ClassifyDelta(ld) {
		var files []string
		for _, f := range ch.Facts {
			files = appendUnique(files, factFiles(f)...)
		}
		if len(files) == 0 {
			continue
		}
		learned = append(learned, LearnedFact{Change: ch, File: files[0], Files: files})
	}
	sort.SliceStable(learned, func(i, j int) bool {
		if learned[i].File != learned[j].File {
			return learned[i].File < learned[j].File
		}
		return learnedText(learned[i]) < learnedText(learned[j])
	})
	return code, learned
}

// learnedText is the sort key of a learned fact within one file: the kind,
// then the ids of its facts.
func learnedText(l LearnedFact) string {
	s := string(l.Change.Kind)
	for _, f := range l.Change.Facts {
		s += "|" + f.ID
	}
	return s
}

// factFiles lists the distinct, non-empty evidence files of one fact.
func factFiles(f Fact) []string {
	var evs []snapshot.Evidence
	switch {
	case f.Edge != nil:
		evs = f.Edge.Evidence
	case f.Node != nil:
		evs = f.Node.Evidence
	}
	var out []string
	for _, e := range evs {
		out = appendUnique(out, e.File)
	}
	return out
}

func appendUnique(dst []string, add ...string) []string {
	for _, a := range add {
		if a == "" {
			continue
		}
		dup := false
		for _, d := range dst {
			if d == a {
				dup = true
				break
			}
		}
		if !dup {
			dst = append(dst, a)
		}
	}
	return dst
}

// learnedFindings turns learned facts into informational findings.
func learnedFindings(learned []LearnedFact, baseQ, headQ ImpactQuerier) []ReviewFinding {
	out := make([]ReviewFinding, 0, len(learned))
	for _, l := range learned {
		q := headQ
		if l.Change.Seed == seedBase {
			q = baseQ
		}
		out = append(out, ReviewFinding{
			Severity:    SeverityInformational,
			Kind:        l.Change.Kind,
			System:      safeCollapse(q, l.Change.SourceID),
			File:        l.File,
			Reason:      ReasonLearnedOutsideDiff,
			Counterpart: l.Change.Counterpart,
			Facts:       l.Change.Facts,
			Evidence:    flattenEvidence(l.Change.Facts),
		})
	}
	return out
}

// LearnedFacts lists the learned findings of r in their report order.
func (r *ReviewResult) LearnedFacts() []ReviewFinding {
	var out []ReviewFinding
	for _, f := range r.Findings {
		if f.Severity == SeverityInformational && f.Reason == ReasonLearnedOutsideDiff {
			out = append(out, f)
		}
	}
	return out
}
