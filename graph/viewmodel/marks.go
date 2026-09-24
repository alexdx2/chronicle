package viewmodel

import (
	"fmt"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/alexdx2/chronicle-core/graph/salience"
	"github.com/alexdx2/chronicle-core/store"
)

// Agent marks annotate elements a view already draws: what breaks, what
// changes, where to look. Marks never change view membership. They may
// promote an already selected-but-hidden node to a visible render mode.

// MarkKinds is the closed vocabulary of marks, strongest first.
var MarkKinds = []string{"breaks", "changed", "focus", "note"}

const (
	// MaxMarks bounds one view's marks, after every source is merged.
	MaxMarks = 50
	// MaxMarkNote bounds a note, in characters, after CleanText.
	MaxMarkNote = 200
)

// Mark is one requested annotation of a node or edge key.
type Mark struct {
	Kind string `json:"kind"`
	Note string `json:"note,omitempty"`
}

// MarkRef is a placed mark: the key it annotates and how that key reads.
type MarkRef struct {
	Key  string `json:"key"`
	Name string `json:"name"`
	Kind string `json:"kind"`
	Note string `json:"note,omitempty"`
}

// MarkRank orders kinds by strength: breaks 4 … note 1, unknown 0.
func MarkRank(kind string) int {
	for i, k := range MarkKinds {
		if k == kind {
			return len(MarkKinds) - i
		}
	}
	return 0
}

// StrongestMark is the strongest kind among refs ("" when there are none).
func StrongestMark(refs []MarkRef) string {
	best := ""
	for _, r := range refs {
		if MarkRank(r.Kind) > MarkRank(best) {
			best = r.Kind
		}
	}
	return best
}

// CleanText makes untrusted text safe to carry as a one-line label: line
// breaks and tabs become spaces, control and format characters (bidi
// overrides included) are dropped, runs of spaces collapse, ends are trimmed.
func CleanText(s string) string {
	var b strings.Builder
	pendingSpace := false
	for _, r := range s {
		switch {
		case r == ' ' || r == '\n' || r == '\r' || r == '\t':
			pendingSpace = true
			continue
		case unicode.IsControl(r) || unicode.Is(unicode.Cf, r):
			continue
		}
		if pendingSpace && b.Len() > 0 {
			b.WriteByte(' ')
		}
		pendingSpace = false
		b.WriteRune(r)
	}
	return b.String()
}

// NormalizeMarks validates a marks map and returns a cleaned copy: kinds
// from MarkKinds (case-insensitive), at most MaxMarks entries, notes through
// CleanText and at most MaxMarkNote characters. Nil when empty.
func NormalizeMarks(in map[string]Mark) (map[string]Mark, error) {
	if len(in) == 0 {
		return nil, nil
	}
	if len(in) > MaxMarks {
		return nil, fmt.Errorf("marks: at most %d per view (got %d)", MaxMarks, len(in))
	}
	out := make(map[string]Mark, len(in))
	for key, m := range in {
		k := strings.TrimSpace(key)
		if k == "" {
			return nil, fmt.Errorf("marks: empty key")
		}
		kind := strings.ToLower(strings.TrimSpace(m.Kind))
		if MarkRank(kind) == 0 {
			return nil, fmt.Errorf("marks[%s]: kind %q is not one of %s", k, m.Kind, strings.Join(MarkKinds, "|"))
		}
		note := CleanText(m.Note)
		if n := utf8.RuneCountInString(note); n > MaxMarkNote {
			return nil, fmt.Errorf("marks[%s]: note has %d characters, at most %d", k, n, MaxMarkNote)
		}
		out[k] = Mark{Kind: kind, Note: note}
	}
	return out, nil
}

// MarkPlacer tells PlaceMarks what the View alone cannot know.
type MarkPlacer struct {
	// Fold returns the key of the element a node folds into when it is not
	// drawn itself — the component exposing an endpoint pill, the card or
	// frame of a group member; "" when it folds nowhere.
	Fold func(key string) string
	// Name is the display name of a node or edge key ("" when unknown).
	Name func(key string) string
	// Exists reports whether the graph has the key (node or edge) at all.
	Exists func(key string) bool
}

// PlaceMarks puts marks on what v already draws: a drawn node or frame
// carries its own mark, a folded node lends its mark to the card or frame
// it folds into, an edge's mark sits on the arrow whose collapsed_from holds
// it. A key the graph has but the view does not draw goes to v.Unplaced, a
// key the graph lacks to v.Missing.
func PlaceMarks(v *View, marks map[string]Mark, p MarkPlacer) {
	if len(marks) == 0 {
		return
	}
	nodeIdx := make(map[string]int, len(v.Nodes))
	for i := range v.Nodes {
		nodeIdx[v.Nodes[i].Key] = i
	}
	groupIdx := make(map[string]int, len(v.Groups))
	for i := range v.Groups {
		groupIdx[v.Groups[i].Key] = i
	}
	arrowIdx := make(map[string]int)
	for i := range v.Edges {
		for _, k := range v.Edges[i].CollapsedFrom {
			if _, seen := arrowIdx[k]; !seen {
				arrowIdx[k] = i
			}
		}
	}
	keys := make([]string, 0, len(marks))
	for k := range marks {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, key := range keys {
		m := marks[key]
		ref := MarkRef{Key: key, Name: key, Kind: m.Kind, Note: m.Note}
		if p.Name != nil {
			if n := p.Name(key); n != "" {
				ref.Name = n
			}
		}
		placed := false
		if strings.Contains(key, "->") {
			if i, ok := arrowIdx[key]; ok {
				v.Edges[i].Marks = append(v.Edges[i].Marks, ref)
				placed = true
			}
		} else {
			placed = placeNodeMark(v, key, ref, nodeIdx, groupIdx, p)
		}
		if placed {
			continue
		}
		if p.Exists != nil && p.Exists(key) {
			v.Unplaced = append(v.Unplaced, key)
		} else {
			v.Missing = append(v.Missing, key)
		}
	}
	for i := range v.Nodes {
		sortMarkRefs(v.Nodes[i].Marks)
	}
	for i := range v.Groups {
		sortMarkRefs(v.Groups[i].Marks)
	}
	for i := range v.Edges {
		sortMarkRefs(v.Edges[i].Marks)
	}
}

// placeNodeMark follows key to the element that draws it (at most four
// folds) and attaches ref there; a hidden node carrying a mark is promoted.
func placeNodeMark(v *View, key string, ref MarkRef, nodeIdx, groupIdx map[string]int, p MarkPlacer) bool {
	rep := key
	for hop := 0; hop < 4 && rep != ""; hop++ {
		if i, ok := nodeIdx[rep]; ok {
			n := &v.Nodes[i]
			if n.RenderMode == string(salience.RenderHidden) {
				promoteMarked(v, n)
			}
			n.Marks = append(n.Marks, ref)
			return true
		}
		if i, ok := groupIdx[rep]; ok {
			v.Groups[i].Marks = append(v.Groups[i].Marks, ref)
			return true
		}
		if p.Fold == nil {
			return false
		}
		rep = p.Fold(rep)
	}
	return false
}

// promoteMarked makes a hidden node visible because a mark points at it and
// takes it out of the hidden summary.
func promoteMarked(v *View, n *VNode) {
	reason := n.SalienceReason
	if reason == "" {
		reason = "policy"
	}
	for i := range v.Hidden {
		h := &v.Hidden[i]
		if h.Layer == n.Layer && h.Type == n.Type && h.Reason == reason {
			h.Count--
			if h.Count <= 0 {
				v.Hidden = append(v.Hidden[:i], v.Hidden[i+1:]...)
			}
			break
		}
	}
	n.RenderMode = string(salience.RenderBox)
	n.SalienceReason = "mark"
}

func sortMarkRefs(refs []MarkRef) {
	sort.SliceStable(refs, func(i, j int) bool {
		if ri, rj := MarkRank(refs[i].Kind), MarkRank(refs[j].Kind); ri != rj {
			return ri > rj
		}
		return refs[i].Key < refs[j].Key
	})
}

// PlacedMarks lists every mark the view carries — on cards, frames and
// arrows — sorted by key.
func (v *View) PlacedMarks() []MarkRef {
	out := []MarkRef{}
	for _, n := range v.Nodes {
		out = append(out, n.Marks...)
	}
	for _, g := range v.Groups {
		out = append(out, g.Marks...)
	}
	for _, e := range v.Edges {
		out = append(out, e.Marks...)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out
}

// buildViewPlacer answers PlaceMarks from BuildView's indices: endpoint pills
// fold into their exposer, group members into their group, names and
// existence come from the loaded graph (then the store, for keys this view
// never loaded — another domain, a hidden layer).
func buildViewPlacer(st *store.Store, nodeByKey map[string]*store.NodeRow, nodeByID map[int64]*store.NodeRow,
	attached map[int64]int64, memberGroup map[int64]string, edges []store.EdgeRow) MarkPlacer {
	edgeByKey := make(map[string]*store.EdgeRow, len(edges))
	for i := range edges {
		edgeByKey[edges[i].EdgeKey] = &edges[i]
	}
	return MarkPlacer{
		Fold: func(key string) string {
			n := nodeByKey[key]
			if n == nil {
				return ""
			}
			if ex, ok := attached[n.NodeID]; ok {
				if xn := nodeByID[ex]; xn != nil {
					return xn.NodeKey
				}
			}
			return memberGroup[n.NodeID]
		},
		Name: func(key string) string {
			if n := nodeByKey[key]; n != nil {
				return n.Name
			}
			if e := edgeByKey[key]; e != nil {
				from, to := nodeByID[e.FromNodeID], nodeByID[e.ToNodeID]
				if from != nil && to != nil {
					return from.Name + " → " + to.Name
				}
			}
			if n, err := st.GetNodeByKey(key); err == nil {
				return n.Name
			}
			return ""
		},
		Exists: func(key string) bool {
			if nodeByKey[key] != nil || edgeByKey[key] != nil {
				return true
			}
			_, err := st.GetNodeByKey(key)
			return err == nil
		},
	}
}
