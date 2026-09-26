package snapshot

import (
	"reflect"
	"sort"
)

// Merge performs a keyed three-way merge of base, ours and theirs: nodes and
// edges are matched by ID, terms by ID, and coverage by Path.
//
//   - A record changed on only one side relative to base is taken from that
//     side. A one-sided delete counts as "the change" on that side: if the
//     other side left the record untouched, the delete wins and the record
//     is absent from the result.
//   - A record changed identically on both sides collapses to one copy.
//   - A record changed DIFFERENTLY on both sides — including one side
//     deleting it while the other modified it — is a conflict: Ours wins.
//     Every file named by an Evidence.File in EITHER side's version of that
//     disputed record has its Coverage.Blob cleared (regardless of which
//     version wins), so the file reads as Stale (package freshness) and the
//     next scan re-verifies it rather than the graph resting on a fact
//     neither side agreed on — even the surviving side's own evidence is
//     re-checked, since Ours is a tie-break, not a verification.
//
// Header.Seq is max(ours.Seq, theirs.Seq) + 1. Format/Version/FactSchema are
// this package's current constants (the merge result is written by this
// code, not copied from either input); Domains is the sorted union of both
// sides' domains; Registry prefers ours, falling back to theirs.
//
// Merge normalizes base, ours and theirs in place (Normalize) before
// comparing — like Encode, this is a side effect of computing a canonical
// result, and it means nested-slice order (evidence, aliases) never causes a
// false conflict between two sides that actually agree.
func Merge(base, ours, theirs *Snapshot) *Snapshot {
	Normalize(base)
	Normalize(ours)
	Normalize(theirs)

	nodes, disputedNodes := mergeKeyed(nodeMap(base.Nodes), nodeMap(ours.Nodes), nodeMap(theirs.Nodes))
	edges, disputedEdges := mergeKeyed(edgeMap(base.Edges), edgeMap(ours.Edges), edgeMap(theirs.Edges))
	terms, _ := mergeKeyed(termMap(base.Terms), termMap(ours.Terms), termMap(theirs.Terms))
	coverage, _ := mergeKeyed(coverageMap(base.Coverage), coverageMap(ours.Coverage), coverageMap(theirs.Coverage))

	staleFiles := map[string]bool{}
	for _, sides := range disputedNodes {
		for _, n := range sides {
			collectEvidenceFiles(n.Evidence, staleFiles)
		}
	}
	for _, sides := range disputedEdges {
		for _, e := range sides {
			collectEvidenceFiles(e.Evidence, staleFiles)
		}
	}
	for path := range staleFiles {
		if c, ok := coverage[path]; ok {
			c.Blob = ""
			coverage[path] = c
		} else {
			// No existing Coverage entry for this path: create one with an
			// empty blob so the file reads Stale rather than Unscanned —
			// disputed evidence pointed at it, so it needs re-verification
			// even though nothing previously recorded coverage of it.
			coverage[path] = Coverage{Kind: "coverage", Path: path}
		}
	}

	seq := ours.Header.Seq
	if theirs.Header.Seq > seq {
		seq = theirs.Header.Seq
	}
	registry := ours.Header.Registry
	if registry == "" {
		registry = theirs.Header.Registry
	}

	merged := &Snapshot{
		Header: Header{
			Kind:       "header",
			Format:     Format,
			Version:    Version,
			FactSchema: FactSchema,
			Seq:        seq + 1,
			Domains:    unionSortedStrings(ours.Header.Domains, theirs.Header.Domains),
			Registry:   registry,
		},
		Nodes:    mapValues(nodes),
		Edges:    mapValues(edges),
		Terms:    mapValues(terms),
		Coverage: mapValues(coverage),
	}

	Normalize(merged)
	merged.Header.Counts = Counts{
		Node:     len(merged.Nodes),
		Edge:     len(merged.Edges),
		Term:     len(merged.Terms),
		Coverage: len(merged.Coverage),
	}
	return merged
}

func collectEvidenceFiles(ev []Evidence, into map[string]bool) {
	for _, e := range ev {
		if e.File != "" {
			into[e.File] = true
		}
	}
}

func nodeMap(ns []Node) map[string]Node {
	m := make(map[string]Node, len(ns))
	for _, n := range ns {
		m[n.ID] = n
	}
	return m
}

func edgeMap(es []Edge) map[string]Edge {
	m := make(map[string]Edge, len(es))
	for _, e := range es {
		m[e.ID] = e
	}
	return m
}

func termMap(ts []Term) map[string]Term {
	m := make(map[string]Term, len(ts))
	for _, t := range ts {
		m[t.ID] = t
	}
	return m
}

func coverageMap(cs []Coverage) map[string]Coverage {
	m := make(map[string]Coverage, len(cs))
	for _, c := range cs {
		m[c.Path] = c
	}
	return m
}

func mapValues[T any](m map[string]T) []T {
	out := make([]T, 0, len(m))
	for _, v := range m {
		out = append(out, v)
	}
	return out
}

// mergeKeyed applies the keyed three-way merge rule described on Merge's doc
// comment to one record map (node/edge/term by ID, coverage by Path — the
// caller picks the key when it builds base/ours/theirs). It returns the
// merged-by-key result and, for every key that was a genuine conflict
// (changed differently on both sides, including one side deleting while the
// other changed it), the non-absent version(s) that were in dispute — the
// caller uses these to find evidence files to mark stale. A key absent from
// the result map was deleted, either by agreement or because Ours' side of a
// delete-vs-change conflict was itself the delete.
func mergeKeyed[T any](base, ours, theirs map[string]T) (result map[string]T, disputed map[string][]T) {
	result = make(map[string]T)
	disputed = make(map[string][]T)

	for _, k := range unionKeys(base, ours, theirs) {
		b, bOK := base[k]
		o, oOK := ours[k]
		t, tOK := theirs[k]

		if !oOK && !tOK {
			continue // gone from both sides (independently or together) — absent, no conflict
		}

		if !bOK {
			// Didn't exist in base: an addition on one or both sides.
			switch {
			case oOK && !tOK:
				result[k] = o
			case !oOK && tOK:
				result[k] = t
			default: // added on both sides
				if reflect.DeepEqual(o, t) {
					result[k] = o
				} else {
					result[k] = o // conflict: Ours wins
					disputed[k] = []T{o, t}
				}
			}
			continue
		}

		// Existed in base already: figure out which side(s) changed it.
		oChanged := !oOK || !reflect.DeepEqual(o, b)
		tChanged := !tOK || !reflect.DeepEqual(t, b)

		switch {
		case !oChanged && !tChanged:
			result[k] = b

		case oChanged && !tChanged:
			if oOK {
				result[k] = o
			}
			// else: ours deleted, theirs left it untouched — stays deleted.

		case !oChanged && tChanged:
			if tOK {
				result[k] = t
			}
			// else: theirs deleted, ours left it untouched — stays deleted.

		default: // both changed
			if oOK && tOK && reflect.DeepEqual(o, t) {
				result[k] = o
				continue
			}
			var sides []T
			if oOK {
				result[k] = o // Ours wins: present, ours' value.
				sides = append(sides, o)
			}
			// !oOK here means Ours' change WAS a delete, so Ours winning
			// means the record stays deleted — no result[k] assignment.
			if tOK {
				sides = append(sides, t)
			}
			disputed[k] = sides
		}
	}
	return result, disputed
}

// unionKeys returns every key present in any of maps, each exactly once.
// Order is unspecified — every caller either merges into another map (later
// sorted by Normalize) or reduces the result into a plain set.
func unionKeys[T any](maps ...map[string]T) []string {
	seen := make(map[string]bool)
	var keys []string
	for _, m := range maps {
		for k := range m {
			if !seen[k] {
				seen[k] = true
				keys = append(keys, k)
			}
		}
	}
	return keys
}

// unionSortedStrings returns the sorted, deduplicated union of a and b.
func unionSortedStrings(a, b []string) []string {
	seen := make(map[string]bool, len(a)+len(b))
	var out []string
	for _, s := range a {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	for _, s := range b {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	sort.Strings(out)
	return out
}
