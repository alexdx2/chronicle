// Package reviewcheck compares a Chronicle MR review against the
// deterministic reference delta of the same change: which contract, call,
// topic, dependency and package changes the code really made, and which of
// them the review named; and, in reverse, which review delta items no
// reference change explains.
//
// It is a comparer, not part of the independent reference path: unlike the
// rest of truth/..., it may import review/ and snapshot/ (but not graph/ or
// scan/ directly).
package reviewcheck

import (
	"sort"
	"strings"

	"github.com/alexdx2/chronicle-core/review"
	"github.com/alexdx2/chronicle-core/snapshot"
	"github.com/alexdx2/chronicle-core/truth"
	"github.com/alexdx2/chronicle-core/truth/compare"
)

// Status says what the review did with one reference delta fact.
type Status string

const (
	// Reported: the review named the fact's graph node (or an edge touching
	// it) in its delta or in a finding.
	Reported Status = "reported"
	// MissedByReview: the node is in the graph, the review did not name it.
	MissedByReview Status = "missed_by_review"
	// NotInGraph: the graph snapshot has no node for the fact (a scan gap).
	NotInGraph Status = "not_in_graph"
	// NotTracked: the review does not list this kind of change (unless a
	// finding names the node: then it is Reported). Every delta
	// fact of the classes deps and packages that has a graph node gets this
	// status, never missed_by_review: review.Diff routes ClassStructural facts
	// (code:package nodes, DEPENDS_ON edges) only into the
	// GraphDelta.StructuralAdded/StructuralRemoved counters (review/diff.go,
	// route(), line 225-232) and never into Delta.Added/Removed, so the
	// review cannot name them. not_in_graph takes precedence (no node at all).
	NotTracked Status = "not_tracked"
)

// Change direction of a delta fact.
const (
	Added   = "added"
	Removed = "removed"
)

// Classes are the reference classes a review is checked on. Services are
// excluded on purpose: a change of the service set is rare and noisy.
var Classes = []truth.Class{truth.ClassEndpoints, truth.ClassHTTP, truth.ClassChannels, truth.ClassDeps, truth.ClassPackages}

// Input is everything Check needs. Base/Head are reference reports at the base
// and head revisions; BaseSnap/HeadSnap the graph snapshots at the same
// revisions (nil is tolerated: facts become not_in_graph).
type Input struct {
	Base, Head         *truth.Report
	BaseSnap, HeadSnap *snapshot.Snapshot
	Review             *review.ReviewResult
}

// DeltaFact is one reference fact present on only one side of the change.
type DeltaFact struct {
	Fact    truth.Fact `json:"fact"`
	Change  string     `json:"change"` // added | removed
	Status  Status     `json:"status"`
	NodeKey string     `json:"node_key,omitempty"`
}

// Unexplained is a review delta item (an added or removed endpoint, topic,
// package, call or topic edge, or dependency edge to a package) that no
// reference delta fact accounts for: a candidate false positive.
type Unexplained struct {
	ID     string `json:"id"`
	Kind   string `json:"kind"`   // node | edge
	Type   string `json:"type"`   // node type or edge type
	Change string `json:"change"` // added | removed
}

// ClassCount is the per-class tally of statuses.
type ClassCount struct {
	Class      truth.Class `json:"class"`
	Reported   int         `json:"reported"`
	Missed     int         `json:"missed"`
	NotInGraph int         `json:"not_in_graph"`
	NotTracked int         `json:"not_tracked"`
}

// Result is the outcome of Check. Recall is nil when reported+missed is 0.
type Result struct {
	Facts       []DeltaFact   `json:"facts"`
	Unexplained []Unexplained `json:"unexplained"`
	ByClass     []ClassCount  `json:"by_class"`
	Reported    int           `json:"reported"`
	Missed      int           `json:"missed"`
	NotInGraph  int           `json:"not_in_graph"`
	NotTracked  int           `json:"not_tracked"`
	// ReviewStructuralAdded/Removed are the review's own counters of
	// structural facts (all of them, not only packages and DEPENDS_ON) added
	// and removed, so a reader can compare them with the not_tracked count.
	ReviewStructuralAdded   int `json:"review_structural_added"`
	ReviewStructuralRemoved int `json:"review_structural_removed"`
	// Recall counts reported and missed only.
	Recall *float64 `json:"recall"`
}

type factKey struct {
	class truth.Class
	key   string
}

func classSet() map[truth.Class]bool {
	m := map[truth.Class]bool{}
	for _, c := range Classes {
		m[c] = true
	}
	return m
}

func index(rep *truth.Report, in map[truth.Class]bool) map[factKey]truth.Fact {
	m := map[factKey]truth.Fact{}
	if rep == nil {
		return m
	}
	for _, f := range rep.Facts {
		if !in[f.Class] {
			continue
		}
		k := factKey{f.Class, f.Key}
		if _, ok := m[k]; !ok {
			m[k] = f
		}
	}
	return m
}

// nodeKeys maps each fact (by class and key) to its graph node key via the
// compare package; empty when the fact is not confirmed in the snapshot.
func nodeKeys(rep *truth.Report, snap *snapshot.Snapshot) map[factKey]string {
	out := map[factKey]string{}
	for _, jf := range compare.Compare(rep, snap).Facts {
		if jf.Status != truth.Confirmed && jf.Status != truth.ConfirmedLoose {
			continue
		}
		k := factKey{jf.Class, jf.Key}
		if _, ok := out[k]; !ok && jf.GraphNodeKey != "" {
			out[k] = jf.GraphNodeKey
		}
	}
	return out
}

// named is the set of node keys a review named, per direction.
type named struct {
	added, removed map[string]bool
	// findings holds the node keys named in review findings only.
	findings map[string]bool
}

func newNamed() *named {
	return &named{map[string]bool{}, map[string]bool{}, map[string]bool{}}
}

func addFact(set map[string]bool, f review.Fact) {
	if f.ID != "" {
		set[f.ID] = true
	}
	if f.Node != nil && f.Node.ID != "" {
		set[f.Node.ID] = true
	}
	if f.Edge != nil {
		if f.Edge.From != "" {
			set[f.Edge.From] = true
		}
		if f.Edge.To != "" {
			set[f.Edge.To] = true
		}
	}
}

func collectFindings(fs []review.ReviewFinding, n *named) {
	for _, f := range fs {
		for _, fact := range f.Facts {
			addFact(n.added, fact)
			addFact(n.removed, fact)
			addFact(n.findings, fact)
		}
		collectFindings(f.Downstream, n)
	}
}

func reviewNamed(r *review.ReviewResult) *named {
	n := newNamed()
	if r == nil {
		return n
	}
	if d := r.Delta; d != nil {
		for _, f := range d.Added {
			addFact(n.added, f)
		}
		for _, f := range d.Removed {
			addFact(n.removed, f)
		}
		for _, c := range d.Changed {
			for _, f := range []review.Fact{c.Before, c.After} {
				addFact(n.added, f)
				addFact(n.removed, f)
			}
		}
	}
	collectFindings(r.Findings, n)
	return n
}

// Check compares the reference delta with the review.
func Check(in Input) Result {
	cls := classSet()
	base, head := index(in.Base, cls), index(in.Head, cls)
	headKeys := nodeKeys(in.Head, in.HeadSnap)
	baseKeys := nodeKeys(in.Base, in.BaseSnap)
	nm := reviewNamed(in.Review)

	res := Result{Facts: []DeltaFact{}, Unexplained: []Unexplained{}, ByClass: []ClassCount{}}
	mappedAdded, mappedRemoved := map[string]bool{}, map[string]bool{}
	judge := func(f truth.Fact, change string, keys map[factKey]string, set, mapped map[string]bool) {
		df := DeltaFact{Fact: f, Change: change}
		if nk := keys[factKey{f.Class, f.Key}]; nk == "" {
			df.Status = NotInGraph
		} else {
			df.NodeKey = nk
			mapped[nk] = true
			if f.Class == truth.ClassDeps || f.Class == truth.ClassPackages {
				// Deps and packages are not in the review delta, but a
				// finding may still name the node.
				if nm.findings[nk] {
					df.Status = Reported
				} else {
					df.Status = NotTracked
				}
			} else if set[nk] {
				df.Status = Reported
			} else {
				df.Status = MissedByReview
			}
		}
		res.Facts = append(res.Facts, df)
	}
	for k, f := range head {
		if _, ok := base[k]; !ok {
			judge(f, Added, headKeys, nm.added, mappedAdded)
		}
	}
	for k, f := range base {
		if _, ok := head[k]; !ok {
			judge(f, Removed, baseKeys, nm.removed, mappedRemoved)
		}
	}

	order := map[truth.Class]int{}
	for i, c := range Classes {
		order[c] = i
	}
	sort.Slice(res.Facts, func(i, j int) bool {
		a, b := res.Facts[i], res.Facts[j]
		if a.Fact.Class != b.Fact.Class {
			return order[a.Fact.Class] < order[b.Fact.Class]
		}
		if a.Fact.Key != b.Fact.Key {
			return a.Fact.Key < b.Fact.Key
		}
		return a.Change < b.Change
	})

	counts := map[truth.Class]*ClassCount{}
	for _, c := range Classes {
		counts[c] = &ClassCount{Class: c}
	}
	for _, f := range res.Facts {
		cc := counts[f.Fact.Class]
		switch f.Status {
		case Reported:
			res.Reported++
			cc.Reported++
		case MissedByReview:
			res.Missed++
			cc.Missed++
		case NotTracked:
			res.NotTracked++
			cc.NotTracked++
		default:
			res.NotInGraph++
			cc.NotInGraph++
		}
	}
	for _, c := range Classes {
		res.ByClass = append(res.ByClass, *counts[c])
	}
	if d := res.Reported + res.Missed; d > 0 {
		r := float64(res.Reported) / float64(d)
		res.Recall = &r
	}

	if in.Review != nil && in.Review.Delta != nil {
		d := in.Review.Delta
		res.ReviewStructuralAdded, res.ReviewStructuralRemoved = d.StructuralAdded, d.StructuralRemoved
		types := nodeTypes(in.BaseSnap, in.HeadSnap)
		// A package that is a dependency in the head reference (in any
		// manifest) is not a false positive even when the delta has no fact
		// for it: the dependency already existed elsewhere.
		for k := range head {
			if k.class == truth.ClassDeps {
				if nk := headKeys[k]; nk != "" {
					mappedAdded[nk], mappedRemoved[nk] = true, true
				}
			}
		}
		explainByHTTP(d.Added, httpFacts(head, base, true), in.HeadSnap, mappedAdded)
		explainByHTTP(d.Removed, httpFacts(head, base, false), in.BaseSnap, mappedRemoved)
		changed := map[string]bool{}
		for _, c := range in.Review.ChangedFiles {
			changed[strings.TrimPrefix(strings.ReplaceAll(c.Path, "\\", "/"), "./")] = true
		}
		explainByChangedCaller(d.Added, changed, headHTTPFacts(head), in.HeadSnap, mappedAdded)
		explainByCallerService(d.Added, headHTTPFacts(head), in.HeadSnap, mappedAdded)
		explainByCallerService(d.Removed, headHTTPFacts(base), in.BaseSnap, mappedRemoved)
		headSys, relHead := review.SnapshotCollapse(in.HeadSnap)
		baseSys, relBase := review.SnapshotCollapse(in.BaseSnap)
		defer relHead()
		defer relBase()
		// A caller moved inside the change (A -> T removed, B -> T added, both
		// files in the diff) is one moved integration, not two unexplained edges.
		for _, p := range review.MovedIntegrationPairs(d.Added, d.Removed, in.Review.ChangedFiles, headSys, baseSys) {
			a, r := d.Added[p[0]].Edge, d.Removed[p[1]].Edge
			mappedAdded[a.From], mappedAdded[a.To] = true, true
			mappedRemoved[r.From], mappedRemoved[r.To] = true, true
		}
		// An added topic edge from another node of a system already on the
		// topic in base is informational in the review, not a change.
		sameSys := map[int]bool{}
		for _, i := range review.SameSystemTopicEdges(d.Added, in.BaseSnap, headSys, baseSys) {
			sameSys[i] = true
		}
		for i, f := range d.Added {
			if sameSys[i] {
				continue
			}
			if u, ok := unexplained(f, Added, mappedAdded, types); ok {
				res.Unexplained = append(res.Unexplained, u)
			}
		}
		for _, f := range d.Removed {
			if u, ok := unexplained(f, Removed, mappedRemoved, types); ok {
				res.Unexplained = append(res.Unexplained, u)
			}
		}
	}
	sort.Slice(res.Unexplained, func(i, j int) bool {
		a, b := res.Unexplained[i], res.Unexplained[j]
		if a.ID != b.ID {
			return a.ID < b.ID
		}
		return a.Change < b.Change
	})
	return res
}

// nodeTypes maps node key to "layer:type" for both snapshots.
func nodeTypes(snaps ...*snapshot.Snapshot) map[string]string {
	m := map[string]string{}
	for _, s := range snaps {
		if s == nil {
			continue
		}
		for _, n := range s.Nodes {
			if n.Layer != "" || n.Type != "" {
				m[n.ID] = n.Layer + ":" + n.Type
			}
		}
	}
	return m
}

// layerType returns "layer:type" for a node key, from the snapshots when
// known, else from the key's first two ':'-separated parts.
func layerType(id string, types map[string]string) string {
	if t, ok := types[id]; ok {
		return t
	}
	parts := strings.SplitN(id, ":", 3)
	if len(parts) >= 2 {
		return parts[0] + ":" + parts[1]
	}
	return ""
}

var edgeTypes = map[string]bool{
	"CALLS_SERVICE": true, "CALLS_ENDPOINT": true, "PUBLISHES_TOPIC": true, "CONSUMES_TOPIC": true,
}

var nodeLayerTypes = map[string]bool{"contract:endpoint": true, "contract:topic": true, "code:package": true}

func unexplained(f review.Fact, change string, mapped map[string]bool, types map[string]string) (Unexplained, bool) {
	switch {
	case f.Edge != nil:
		e := f.Edge
		relevant := edgeTypes[e.Type] || (e.Type == "DEPENDS_ON" && layerType(e.To, types) == "code:package")
		if !relevant || mapped[e.From] || mapped[e.To] {
			return Unexplained{}, false
		}
		id := f.ID
		if id == "" {
			id = e.ID
		}
		return Unexplained{ID: id, Kind: "edge", Type: e.Type, Change: change}, true
	case f.Node != nil:
		id := f.ID
		if id == "" {
			id = f.Node.ID
		}
		lt := f.Node.Layer + ":" + f.Node.Type
		if !nodeLayerTypes[lt] || mapped[id] {
			return Unexplained{}, false
		}
		return Unexplained{ID: id, Kind: "node", Type: f.Node.Type, Change: change}, true
	}
	return Unexplained{}, false
}

// httpFacts returns the http facts present only in head (added) or only in
// base (removed).
func httpFacts(head, base map[factKey]truth.Fact, added bool) []truth.Fact {
	from, other := head, base
	if !added {
		from, other = base, head
	}
	var out []truth.Fact
	for k, f := range from {
		if f.Class != truth.ClassHTTP {
			continue
		}
		if _, ok := other[k]; !ok {
			out = append(out, f)
		}
	}
	return out
}

// explainByHTTP marks CALLS_SERVICE / CALLS_ENDPOINT edges of the review
// delta that an http fact explains, and their target nodes, as mapped. An edge
// is explained when its from-node lives in the fact's file; a CALLS_ENDPOINT
// edge also when its target endpoint's path matches the fact path.
func explainByHTTP(facts []review.Fact, hf []truth.Fact, snap *snapshot.Snapshot, mapped map[string]bool) {
	if len(hf) == 0 {
		return
	}
	files := map[string]string{}
	if snap != nil {
		for _, n := range snap.Nodes {
			files[n.ID] = n.File
		}
	}
	for _, f := range facts {
		e := f.Edge
		if e == nil || (e.Type != "CALLS_SERVICE" && e.Type != "CALLS_ENDPOINT") {
			continue
		}
		for _, h := range hf {
			file := h.Primary.File
			ok := file != "" && fromInFile(e.From, files[e.From], file)
			if !ok && e.Type == "CALLS_ENDPOINT" && h.Attrs["path"] != "" {
				if p := endpointPath(e.To); p != "" && truth.CanonPath(p) == truth.CanonPath(h.Attrs["path"]) {
					ok = true
				}
			}
			if ok {
				mapped[e.From] = true
				mapped[e.To] = true
				break
			}
		}
	}
}

func fromInFile(key, nodeFile, file string) bool {
	norm := func(p string) string { return strings.TrimPrefix(strings.ReplaceAll(p, "\\", "/"), "./") }
	if nodeFile != "" {
		return norm(nodeFile) == norm(file)
	}
	noExt := norm(file)
	if i := strings.LastIndex(noExt, "."); i > strings.LastIndex(noExt, "/") && i > 0 {
		noExt = noExt[:i]
	}
	return strings.Contains(key, noExt)
}

// endpointPath extracts the path of a contract:endpoint node key
// (contract:endpoint:<domain>:<method>:<path>).
func endpointPath(key string) string {
	if !strings.HasPrefix(key, "contract:endpoint:") {
		return ""
	}
	if i := strings.Index(key, "/"); i >= 0 {
		return key[i:]
	}
	return ""
}

// headHTTPFacts returns every http fact of the head reference with a known
// host (the reference has no per-call class, so an unchanged fact still
// stands for a call added next to it).
func headHTTPFacts(head map[factKey]truth.Fact) []truth.Fact {
	var out []truth.Fact
	for _, f := range head {
		if f.Class != truth.ClassHTTP {
			continue
		}
		known := f.Attrs["host"] != "" || f.Attrs["var"] != "" || (f.Key != "" && f.Key != "var:")
		if known {
			out = append(out, f)
		}
	}
	return out
}

// explainByChangedCaller explains an added CALLS_ENDPOINT edge whose caller
// file changed in the MR and carries an http fact in the head reference, even
// when that fact (host) is not new: one more call in a file that already
// reaches the host leaves the reference fact unchanged.
func explainByChangedCaller(facts []review.Fact, changedSet map[string]bool, hf []truth.Fact, snap *snapshot.Snapshot, mapped map[string]bool) {
	if len(hf) == 0 || len(changedSet) == 0 {
		return
	}
	norm := func(p string) string { return strings.TrimPrefix(strings.ReplaceAll(p, "\\", "/"), "./") }
	files := map[string]string{}
	if snap != nil {
		for _, n := range snap.Nodes {
			files[n.ID] = n.File
		}
	}
	nodes := map[string]snapshot.Node{}
	if snap != nil {
		for _, n := range snap.Nodes {
			nodes[n.ID] = n
		}
	}
	idents := map[string]map[string]bool{} // file -> distinct hosts (var name when the host is unknown)
	for _, h := range hf {
		file := norm(h.Primary.File)
		if idents[file] == nil {
			idents[file] = map[string]bool{}
		}
		idents[file][factIdent(h)] = true
	}
	for _, f := range facts {
		e := f.Edge
		if e == nil || e.Type != "CALLS_ENDPOINT" {
			continue
		}
		th := targetHost(nodes[e.To])
		for _, h := range hf {
			file := norm(h.Primary.File)
			if file == "" || !changedSet[file] || !fromInFile(e.From, files[e.From], file) {
				continue
			}
			if th != "" {
				if factHost(h) != th {
					continue
				}
			} else if len(idents[file]) != 1 {
				continue
			}
			mapped[e.From] = true
			mapped[e.To] = true
			break
		}
	}
}

// factHost is the canonical host an http fact names ("" for a fact known only
// by its variable).
func factHost(f truth.Fact) string {
	if h := truth.CanonHost(f.Attrs["host"]); h != "" {
		return h
	}
	if f.Key != "" && !strings.HasPrefix(f.Key, "var:") {
		return truth.CanonHost(f.Key)
	}
	return ""
}

// factIdent names what an http fact reaches: its host, else its variable.
func factIdent(f truth.Fact) string {
	if h := factHost(f); h != "" {
		return h
	}
	if v := f.Attrs["var"]; v != "" {
		return "var:" + v
	}
	return f.Key
}

// targetHost is the canonical host recorded on a call target node (the host
// attribute, else a dotted alias), "" when the node names none.
func targetHost(n snapshot.Node) string {
	if h := truth.CanonHost(n.Attrs["host"]); h != "" {
		return h
	}
	for _, a := range n.Aliases {
		if strings.Contains(a.Alias, ".") && !strings.ContainsAny(a.Alias, " /") {
			return truth.CanonHost(a.Alias)
		}
	}
	return ""
}

// explainByCallerService explains a CALLS_ENDPOINT edge of the review delta
// whose caller node also has a CALLS_SERVICE edge to an external system named
// after the host of an http fact (hf, from the same side as snap). The host
// fact lives in configuration, not in the client file, so a new call in the
// client changes no reference fact.
func explainByCallerService(facts []review.Fact, hf []truth.Fact, snap *snapshot.Snapshot, mapped map[string]bool) {
	if len(hf) == 0 || snap == nil {
		return
	}
	hosts := map[string]bool{}
	for _, h := range hf {
		if x := factHost(h); x != "" {
			hosts[x] = true
		}
	}
	external := map[string]bool{} // external node whose name is a known host
	for _, n := range snap.Nodes {
		if n.Type == "external_system" && hosts[truth.CanonHost(n.Name)] {
			external[n.ID] = true
		}
	}
	callers := map[string]bool{}
	for _, e := range snap.Edges {
		if e.Type == "CALLS_SERVICE" && e.Active && external[e.To] {
			callers[e.From] = true
		}
	}
	for _, f := range facts {
		if e := f.Edge; e != nil && e.Type == "CALLS_ENDPOINT" && callers[e.From] {
			mapped[e.From] = true
			mapped[e.To] = true
		}
	}
}
