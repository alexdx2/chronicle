package reviewcheck

import (
	"sort"
	"testing"

	"github.com/alexdx2/chronicle-core/gitdiff"
	"github.com/alexdx2/chronicle-core/review"
	"github.com/alexdx2/chronicle-core/snapshot"
	"github.com/alexdx2/chronicle-core/truth"
)

func ep(key string) truth.Fact {
	return truth.Fact{Class: truth.ClassEndpoints, Key: key, Primary: truth.Evidence{File: "a.ts", Line: 1}, Sources: []string{"t"}}
}

func epSnap(keys ...string) *snapshot.Snapshot {
	s := &snapshot.Snapshot{Header: snapshot.Header{Kind: "header", Format: snapshot.Format, Version: snapshot.Version}}
	for _, k := range keys {
		s.Nodes = append(s.Nodes, snapshot.Node{ID: "contract:endpoint:d:" + k, Layer: "contract", Type: "endpoint", Name: k})
	}
	return s
}

func nodeFact(id, layer, typ string) review.Fact {
	return review.Fact{ID: id, Kind: review.KindNode, Node: &snapshot.Node{ID: id, Layer: layer, Type: typ}}
}

func edgeFact(from, to, typ string) review.Fact {
	id := from + "->" + to + ":" + typ
	return review.Fact{ID: id, Kind: review.KindEdge, Edge: &snapshot.Edge{ID: id, From: from, To: to, Type: typ}}
}

func TestStatuses(t *testing.T) {
	head := &truth.Report{Facts: []truth.Fact{ep("GET /a"), ep("POST /b"), ep("PUT /c"), ep("GET /kept")}}
	base := &truth.Report{Facts: []truth.Fact{ep("GET /kept"), ep("DELETE /gone")}}
	headSnap := epSnap("GET:/a", "POST:/b", "GET:/kept")
	baseSnap := epSnap("GET:/kept", "DELETE:/gone")
	rv := &review.ReviewResult{Delta: &review.GraphDelta{
		Added:   []review.Fact{nodeFact("contract:endpoint:d:GET:/a", "contract", "endpoint")},
		Removed: []review.Fact{edgeFact("code:x:d:h", "contract:endpoint:d:DELETE:/gone", "CALLS_ENDPOINT")},
	}}
	res := Check(Input{Base: base, Head: head, BaseSnap: baseSnap, HeadSnap: headSnap, Review: rv})
	got := map[string]Status{}
	for _, f := range res.Facts {
		got[f.Change+" "+f.Fact.Key] = f.Status
	}
	want := map[string]Status{
		"added GET /a": Reported, "added POST /b": MissedByReview, "added PUT /c": NotInGraph,
		"removed DELETE /gone": Reported,
	}
	if len(got) != len(want) {
		t.Fatalf("got %v", got)
	}
	for k, w := range want {
		if got[k] != w {
			t.Errorf("%s = %q want %q", k, got[k], w)
		}
	}
	if res.Reported != 2 || res.Missed != 1 || res.NotInGraph != 1 {
		t.Errorf("counts %+v", res)
	}
	if res.Recall == nil || *res.Recall < 0.66 || *res.Recall > 0.67 {
		t.Errorf("recall %v", res.Recall)
	}
	if len(res.Unexplained) != 0 {
		t.Errorf("unexplained %+v", res.Unexplained)
	}
}

func TestDirectionMatters(t *testing.T) {
	// The review names the node as removed, but the fact was added.
	head := &truth.Report{Facts: []truth.Fact{ep("GET /a")}}
	rv := &review.ReviewResult{Delta: &review.GraphDelta{
		Removed: []review.Fact{nodeFact("contract:endpoint:d:GET:/a", "contract", "endpoint")},
	}}
	res := Check(Input{Base: &truth.Report{}, Head: head, HeadSnap: epSnap("GET:/a"), Review: rv})
	if len(res.Facts) != 1 || res.Facts[0].Status != MissedByReview {
		t.Fatalf("%+v", res.Facts)
	}
}

func TestFindingFactsAndChanged(t *testing.T) {
	head := &truth.Report{Facts: []truth.Fact{ep("GET /a"), ep("GET /b")}}
	rv := &review.ReviewResult{
		Findings: []review.ReviewFinding{{Facts: []review.Fact{nodeFact("contract:endpoint:d:GET:/a", "contract", "endpoint")}}},
		Delta: &review.GraphDelta{Changed: []review.Changed{{ID: "x",
			Before: nodeFact("contract:endpoint:d:GET:/b", "contract", "endpoint"),
			After:  nodeFact("contract:endpoint:d:GET:/b", "contract", "endpoint")}}},
	}
	res := Check(Input{Base: &truth.Report{}, Head: head, HeadSnap: epSnap("GET:/a", "GET:/b"), Review: rv})
	if res.Reported != 2 {
		t.Fatalf("%+v", res)
	}
}

func TestUnexplained(t *testing.T) {
	head := &truth.Report{Facts: []truth.Fact{ep("GET /a")}}
	rv := &review.ReviewResult{Delta: &review.GraphDelta{
		Added: []review.Fact{
			nodeFact("contract:endpoint:d:GET:/a", "contract", "endpoint"),         // explained
			nodeFact("contract:endpoint:d:GET:/zzz", "contract", "endpoint"),       // spurious
			nodeFact("contract:topic:d:ghost", "contract", "topic"),                // spurious
			nodeFact("code:package:d:npm:x", "code", "package"),                    // spurious
			nodeFact("code:provider:d:p", "code", "provider"),                      // ignored: other code node
			nodeFact("data:model:d:m", "data", "model"),                            // ignored class
			edgeFact("code:p:d:c", "contract:endpoint:d:GET:/a", "CALLS_ENDPOINT"), // explained via endpoint
			edgeFact("code:p:d:c", "service:service:d:s", "CALLS_SERVICE"),         // spurious
			edgeFact("service:service:d:s", "code:package:d:npm:y", "DEPENDS_ON"),  // spurious
			edgeFact("service:service:d:s", "service:service:d:t", "DEPENDS_ON"),   // ignored: not to a package
		},
		Removed: []review.Fact{nodeFact("contract:topic:d:old", "contract", "topic")},
	}}
	res := Check(Input{Base: &truth.Report{}, Head: head, HeadSnap: epSnap("GET:/a"), Review: rv})
	got := map[string]bool{}
	for _, u := range res.Unexplained {
		got[u.Change+" "+u.ID] = true
	}
	want := []string{
		"added contract:endpoint:d:GET:/zzz", "added contract:topic:d:ghost", "added code:package:d:npm:x",
		"added code:p:d:c->service:service:d:s:CALLS_SERVICE", "added service:service:d:s->code:package:d:npm:y:DEPENDS_ON",
		"removed contract:topic:d:old",
	}
	if len(got) != len(want) {
		t.Fatalf("got %v", got)
	}
	for _, w := range want {
		if !got[w] {
			t.Errorf("missing %s in %v", w, got)
		}
	}
	for i := 1; i < len(res.Unexplained); i++ {
		if res.Unexplained[i-1].ID > res.Unexplained[i].ID {
			t.Errorf("not sorted: %+v", res.Unexplained)
		}
	}
}

func TestEmptyAndNil(t *testing.T) {
	res := Check(Input{})
	if res.Recall != nil || len(res.Facts) != 0 || res.Facts == nil || res.Unexplained == nil {
		t.Fatalf("%+v", res)
	}
	// Only not_in_graph facts: no recall either.
	res = Check(Input{Head: &truth.Report{Facts: []truth.Fact{ep("GET /a")}}})
	if res.NotInGraph != 1 || res.Recall != nil {
		t.Fatalf("%+v", res)
	}
}

func TestServicesIgnoredAndDeterministic(t *testing.T) {
	svc := truth.Fact{Class: truth.ClassServices, Key: "svc"}
	head := &truth.Report{Facts: []truth.Fact{svc, ep("GET /b"), ep("GET /a")}}
	a := Check(Input{Head: head})
	head2 := &truth.Report{Facts: []truth.Fact{ep("GET /a"), ep("GET /b"), svc}}
	b := Check(Input{Head: head2})
	if len(a.Facts) != 2 || a.Facts[0].Fact.Key != "GET /a" || b.Facts[0].Fact.Key != "GET /a" {
		t.Fatalf("%+v", a.Facts)
	}
}

func TestDepsPackagesNotTracked(t *testing.T) {
	dep := truth.Fact{Class: truth.ClassDeps, Key: "npm:lodash"}
	pkg := truth.Fact{Class: truth.ClassPackages, Key: "npm:mine"}
	gone := truth.Fact{Class: truth.ClassDeps, Key: "npm:nograph"}
	s := &snapshot.Snapshot{Nodes: []snapshot.Node{
		{ID: "code:package:d:npm:lodash", Layer: "code", Type: "package", Name: "lodash"},
		{ID: "code:package:d:npm:mine", Layer: "code", Type: "package", Name: "mine"},
	}, Edges: []snapshot.Edge{
		{ID: "e1", Type: "DEPENDS_ON", From: "service:service:d:s", To: "code:package:d:npm:lodash", Active: true},
		{ID: "e2", Type: "PUBLISHES", From: "service:service:d:s", To: "code:package:d:npm:mine", Active: true},
	}}
	rv := &review.ReviewResult{Delta: &review.GraphDelta{StructuralAdded: 3, StructuralRemoved: 1}}
	res := Check(Input{Base: &truth.Report{}, Head: &truth.Report{Facts: []truth.Fact{dep, pkg, gone}}, HeadSnap: s, Review: rv})
	got := map[string]Status{}
	for _, f := range res.Facts {
		got[f.Fact.Key] = f.Status
	}
	if got["npm:lodash"] != NotTracked || got["npm:mine"] != NotTracked || got["npm:nograph"] != NotInGraph {
		t.Fatalf("%v", got)
	}
	if res.NotTracked != 2 || res.NotInGraph != 1 || res.Recall != nil {
		t.Fatalf("%+v", res)
	}
	if res.ReviewStructuralAdded != 3 || res.ReviewStructuralRemoved != 1 {
		t.Fatalf("%+v", res)
	}
}

func httpFact(file, path string) truth.Fact {
	return truth.Fact{Class: truth.ClassHTTP, Key: "var:BADGES_API_URL", Attrs: map[string]string{"path": path},
		Primary: truth.Evidence{File: file, Line: 3}, Sources: []string{"t"}}
}

func httpSnap(file string) *snapshot.Snapshot {
	return &snapshot.Snapshot{Nodes: []snapshot.Node{
		{ID: "service:external_system:d:badges-api", Layer: "service", Type: "external_system"},
		{ID: "code:provider:d:analytics-api/src/analytics/badge-client", Layer: "code", Type: "provider", File: file},
		{ID: "code:provider:d:other/thing", Layer: "code", Type: "provider", File: "other/thing.ts"},
	}}
}

func TestHTTPFactExplainsCallEdges(t *testing.T) {
	file := "analytics-api/src/analytics/badge.client.ts"
	from := "code:provider:d:analytics-api/src/analytics/badge-client"
	ep := "contract:endpoint:d:get:/badges/:userid"
	hf := httpFact(file, "/badges/${userId}")
	snap := httpSnap(file)
	svc := "service:external_system:d:badges-api"
	rv := &review.ReviewResult{Delta: &review.GraphDelta{Added: []review.Fact{
		edgeFact(from, ep, "CALLS_ENDPOINT"),
		nodeFact(ep, "contract", "endpoint"),
		nodeFact(svc, "service", "external_system"),
		edgeFact("code:provider:d:other/thing", "contract:endpoint:d:post:/zzz", "CALLS_ENDPOINT"),
	}}}
	// The same-path endpoint edge from another file is explained by path; use
	// a different path to prove a foreign file stays unexplained.
	res := Check(Input{Base: &truth.Report{}, Head: &truth.Report{Facts: []truth.Fact{hf}}, HeadSnap: snap, Review: rv})
	if len(res.Unexplained) != 1 || res.Unexplained[0].ID != "code:provider:d:other/thing->contract:endpoint:d:post:/zzz:CALLS_ENDPOINT" {
		t.Fatalf("%+v", res.Unexplained)
	}
}

func TestHTTPFactRecallUnchanged(t *testing.T) {
	file := "a/b.ts"
	hf := httpFact(file, "/x/${id}")
	snap := httpSnap(file)
	with := Check(Input{Base: &truth.Report{}, Head: &truth.Report{Facts: []truth.Fact{hf}}, HeadSnap: snap,
		Review: &review.ReviewResult{Delta: &review.GraphDelta{Added: []review.Fact{
			nodeFact("service:external_system:d:badges-api", "service", "external_system")}}}})
	if with.Reported+with.Missed+with.NotInGraph != 1 {
		t.Fatalf("%+v", with)
	}
}

func TestHTTPRemovedFactExplainsRemovedEdge(t *testing.T) {
	file := "analytics-api/src/analytics/badge.client.ts"
	from := "code:provider:d:analytics-api/src/analytics/badge-client"
	rv := &review.ReviewResult{Delta: &review.GraphDelta{Removed: []review.Fact{
		edgeFact(from, "service:external_system:d:badges-api", "CALLS_SERVICE")}}}
	res := Check(Input{Base: &truth.Report{Facts: []truth.Fact{httpFact(file, "/badges/:id")}}, Head: &truth.Report{},
		BaseSnap: httpSnap(file), Review: rv})
	if len(res.Unexplained) != 0 {
		t.Fatalf("%+v", res.Unexplained)
	}
	// Same edge reported as added is not explained by a removed fact.
	rv2 := &review.ReviewResult{Delta: &review.GraphDelta{Added: rv.Delta.Removed}}
	res = Check(Input{Base: &truth.Report{Facts: []truth.Fact{httpFact(file, "/badges/:id")}}, Head: &truth.Report{},
		BaseSnap: httpSnap(file), HeadSnap: httpSnap(file), Review: rv2})
	if len(res.Unexplained) != 1 {
		t.Fatalf("%+v", res.Unexplained)
	}
}

func pkgSnap() *snapshot.Snapshot {
	return &snapshot.Snapshot{Nodes: []snapshot.Node{
		{ID: "code:package:d:npm:lodash", Layer: "code", Type: "package", Name: "lodash"},
		{ID: "code:package:d:npm:mine", Layer: "code", Type: "package", Name: "mine"},
	}, Edges: []snapshot.Edge{
		{ID: "e1", Type: "DEPENDS_ON", From: "service:service:d:s", To: "code:package:d:npm:lodash", Active: true},
		{ID: "e2", Type: "PUBLISHES", From: "service:service:d:s", To: "code:package:d:npm:mine", Active: true},
	}}
}

func TestFindingNamingDepsNodeIsReported(t *testing.T) {
	dep := truth.Fact{Class: truth.ClassDeps, Key: "npm:lodash"}
	pkg := truth.Fact{Class: truth.ClassPackages, Key: "npm:mine"}
	rv := &review.ReviewResult{Findings: []review.ReviewFinding{
		{Facts: []review.Fact{nodeFact("code:package:d:npm:lodash", "code", "package")}},
	}}
	res := Check(Input{Base: &truth.Report{}, Head: &truth.Report{Facts: []truth.Fact{dep, pkg}}, HeadSnap: pkgSnap(), Review: rv})
	got := map[string]Status{}
	for _, f := range res.Facts {
		got[f.Fact.Key] = f.Status
	}
	if got["npm:lodash"] != Reported || got["npm:mine"] != NotTracked {
		t.Fatalf("%v", got)
	}
	if res.Reported != 1 || res.NotTracked != 1 {
		t.Fatalf("%+v", res)
	}
}

func TestDependsOnExistingHeadDepNotUnexplained(t *testing.T) {
	// lodash is a head dependency (from another manifest) with no delta fact.
	headDep := truth.Fact{Class: truth.ClassDeps, Key: "npm:lodash"}
	base := &truth.Report{Facts: []truth.Fact{headDep}}
	head := &truth.Report{Facts: []truth.Fact{headDep}}
	e := edgeFact("service:service:d:s", "code:package:d:npm:lodash", "DEPENDS_ON")
	other := edgeFact("service:service:d:s", "code:package:d:npm:mine", "DEPENDS_ON")
	rv := &review.ReviewResult{Delta: &review.GraphDelta{Added: []review.Fact{e, other}}}
	res := Check(Input{Base: base, Head: head, BaseSnap: pkgSnap(), HeadSnap: pkgSnap(), Review: rv})
	if len(res.Unexplained) != 1 || res.Unexplained[0].ID != other.ID {
		t.Fatalf("%+v", res.Unexplained)
	}
}

// The http fact exists in base and head (host unchanged); a call added in the
// same changed file is still explained, one in an untouched file is not.
func TestUnchangedHTTPFactExplainsAddedCallInChangedFile(t *testing.T) {
	file := "analytics-api/src/analytics/badge.client.ts"
	from := "code:provider:d:analytics-api/src/analytics/badge-client"
	hf := httpFact(file, "")
	snap := httpSnap(file)
	other := edgeFact("code:provider:d:other/thing", "contract:endpoint:d:post:/zzz", "CALLS_ENDPOINT")
	mk := func(changed ...gitdiff.ChangedFile) *review.ReviewResult {
		return &review.ReviewResult{ChangedFiles: changed, Delta: &review.GraphDelta{Added: []review.Fact{
			edgeFact(from, "contract:endpoint:d:post:/badges/new", "CALLS_ENDPOINT"), other,
		}}}
	}
	in := Input{Base: &truth.Report{Facts: []truth.Fact{hf}}, Head: &truth.Report{Facts: []truth.Fact{hf}}, BaseSnap: snap, HeadSnap: snap}
	in.Review = mk(gitdiff.ChangedFile{Path: file, Status: "M"})
	res := Check(in)
	if len(res.Unexplained) != 1 || res.Unexplained[0].ID != other.ID {
		t.Fatalf("changed caller: %+v", res.Unexplained)
	}
	in.Review = mk(gitdiff.ChangedFile{Path: "elsewhere.ts", Status: "M"})
	if res := Check(in); len(res.Unexplained) != 2 {
		t.Fatalf("caller file not changed must stay unexplained: %+v", res.Unexplained)
	}
}

// ownedSnap is a graph of services, each containing the named code nodes.
func ownedSnap(owners map[string][]string) *snapshot.Snapshot {
	s := &snapshot.Snapshot{Header: snapshot.Header{Kind: "header", Format: snapshot.Format, Version: snapshot.Version}}
	var svcs []string
	for svc := range owners {
		svcs = append(svcs, svc)
	}
	sort.Strings(svcs)
	for _, svc := range svcs {
		sid := "service:service:d:" + svc
		s.Nodes = append(s.Nodes, snapshot.Node{Kind: "node", ID: sid, Layer: "service", Type: "service", Domain: "d", Name: svc, Status: "active"})
		for _, c := range owners[svc] {
			s.Nodes = append(s.Nodes, snapshot.Node{Kind: "node", ID: c, Layer: "code", Type: "provider", Domain: "d", Name: c, Status: "active"})
			s.Edges = append(s.Edges, snapshot.Edge{Kind: "edge", ID: sid + "->" + c + ":CONTAINS", From: sid, To: c, Type: "CONTAINS", Derivation: "hard", Active: true})
		}
	}
	s.Nodes = append(s.Nodes, snapshot.Node{Kind: "node", ID: "service:external_system:d:pricing-api", Layer: "service", Type: "external_system", Domain: "d", Name: "pricing-api", Status: "active"})
	return s
}

func movedReview(a, b string) *review.ReviewResult {
	withFile := func(f review.Fact, file string) review.Fact {
		f.Edge.Evidence = []snapshot.Evidence{{ID: "e-" + file, File: file, Range: "1-1"}}
		return f
	}
	return &review.ReviewResult{
		ChangedFiles: []gitdiff.ChangedFile{{Path: "a.cs"}, {Path: "b.cs"}},
		Delta: &review.GraphDelta{
			Added:   []review.Fact{withFile(edgeFact(b, "service:external_system:d:pricing-api", "CALLS_SERVICE"), "b.cs")},
			Removed: []review.Fact{withFile(edgeFact(a, "service:external_system:d:pricing-api", "CALLS_SERVICE"), "a.cs")},
		}}
}

func TestMovedIntegrationIsNotUnexplained(t *testing.T) {
	a, b := "code:p:d:a", "code:p:d:b"
	snap := ownedSnap(map[string][]string{"sales": {a, b}})
	rv := movedReview(a, b)
	in := Input{Base: &truth.Report{}, Head: &truth.Report{}, BaseSnap: snap, HeadSnap: snap, Review: rv}
	if res := Check(in); len(res.Unexplained) != 0 {
		t.Errorf("moved pair unexplained: %+v", res.Unexplained)
	}
	// without the second changed file the edges are unexplained again
	rv.ChangedFiles = rv.ChangedFiles[:1]
	if res := Check(in); len(res.Unexplained) != 2 {
		t.Errorf("unpaired: %+v", res.Unexplained)
	}
}

// Two services of one monorepo: a removal and an addition, not a move.
func TestMovedIntegrationAcrossServicesStaysUnexplained(t *testing.T) {
	a, b := "code:p:d:a", "code:p:d:b"
	snap := ownedSnap(map[string][]string{"sales": {a}, "billing": {b}})
	in := Input{Base: &truth.Report{}, Head: &truth.Report{}, BaseSnap: snap, HeadSnap: snap, Review: movedReview(a, b)}
	if res := Check(in); len(res.Unexplained) != 2 {
		t.Errorf("cross-service pair explained: %+v", res.Unexplained)
	}
}

// An unchanged http fact explains an added call only when it is about the
// call's target host.
func TestChangedCallerExplainsOnlyMatchingHost(t *testing.T) {
	file := "analytics-api/src/analytics/badge.client.ts"
	from := "code:provider:d:analytics-api/src/analytics/badge-client"
	fact := func(host string) truth.Fact {
		f := httpFact(file, "")
		f.Key, f.Attrs = host, map[string]string{"host": host}
		return f
	}
	snap := httpSnap(file)
	snap.Nodes = append(snap.Nodes,
		snapshot.Node{ID: "contract:endpoint:d:post:/a", Layer: "contract", Type: "endpoint", Attrs: map[string]string{"host": "a.example.com"}},
		snapshot.Node{ID: "contract:endpoint:d:post:/nohost", Layer: "contract", Type: "endpoint"})
	run := func(target string, hosts ...string) int {
		var facts []truth.Fact
		for _, h := range hosts {
			facts = append(facts, fact(h))
		}
		rv := &review.ReviewResult{ChangedFiles: []gitdiff.ChangedFile{{Path: file, Status: "M"}},
			Delta: &review.GraphDelta{Added: []review.Fact{edgeFact(from, target, "CALLS_ENDPOINT")}}}
		rep := &truth.Report{Facts: facts}
		return len(Check(Input{Base: rep, Head: rep, BaseSnap: snap, HeadSnap: snap, Review: rv}).Unexplained)
	}
	if n := run("contract:endpoint:d:post:/a", "a.example.com", "b.example.com"); n != 0 {
		t.Errorf("matching host: %d unexplained", n)
	}
	if n := run("contract:endpoint:d:post:/a", "b.example.com"); n != 1 {
		t.Errorf("other host explained the call: %d unexplained", n)
	}
	if n := run("contract:endpoint:d:post:/nohost", "b.example.com"); n != 0 {
		t.Errorf("hostless target, one host in file: %d unexplained", n)
	}
	if n := run("contract:endpoint:d:post:/nohost", "a.example.com", "b.example.com"); n != 1 {
		t.Errorf("hostless target, two hosts in file: %d unexplained", n)
	}
}

// A new call in a client whose host fact lives in configuration: the caller
// also calls the external named after the host, so the edge is explained.
func TestCallerServiceHostExplainsCallEndpoint(t *testing.T) {
	from := "code:provider:d:sales-api/services/pricing-client"
	snap := &snapshot.Snapshot{
		Nodes: []snapshot.Node{
			{ID: from, Layer: "code", Type: "provider", File: "Services/PricingClient.cs"},
			{ID: "service:external_system:d:pricing-api", Layer: "service", Type: "external_system", Name: "pricing-api"},
			{ID: "service:external_system:d:other-api", Layer: "service", Type: "external_system", Name: "other-api"},
		},
		Edges: []snapshot.Edge{
			{ID: "c1", From: from, To: "service:external_system:d:pricing-api", Type: "CALLS_SERVICE", Active: true},
		},
	}
	cfg := truth.Fact{Class: truth.ClassHTTP, Key: "pricing-api", Attrs: map[string]string{"host": "http://pricing-api"},
		Primary: truth.Evidence{File: "appsettings.json", Line: 4}, Sources: []string{"t"}}
	rep := &truth.Report{Facts: []truth.Fact{cfg}}
	ep := "contract:endpoint:d:get:/prices/{sku}/history"
	foreign := "code:provider:d:sales-api/other"
	mk := func(added, removed []review.Fact) Input {
		return Input{Base: rep, Head: rep, BaseSnap: snap, HeadSnap: snap,
			Review: &review.ReviewResult{Delta: &review.GraphDelta{Added: added, Removed: removed}}}
	}
	if res := Check(mk([]review.Fact{edgeFact(from, ep, "CALLS_ENDPOINT")}, nil)); len(res.Unexplained) != 0 {
		t.Errorf("added: %+v", res.Unexplained)
	}
	if res := Check(mk(nil, []review.Fact{edgeFact(from, ep, "CALLS_ENDPOINT")})); len(res.Unexplained) != 0 {
		t.Errorf("removed: %+v", res.Unexplained)
	}
	if res := Check(mk([]review.Fact{edgeFact(foreign, ep, "CALLS_ENDPOINT")}, nil)); len(res.Unexplained) != 1 {
		t.Errorf("caller without the service edge explained: %+v", res.Unexplained)
	}
	// host not in the reference: stays unexplained
	in := mk([]review.Fact{edgeFact(from, ep, "CALLS_ENDPOINT")}, nil)
	in.Head = &truth.Report{}
	if res := Check(in); len(res.Unexplained) != 1 {
		t.Errorf("no host fact: %+v", res.Unexplained)
	}
}

func TestSameSystemTopicEdgeNotUnexplained(t *testing.T) {
	a, b := "code:p:d:a", "code:p:d:b"
	topic := "contract:topic:d:price-changed"
	snap := ownedSnap(map[string][]string{"pricing": {a, b}, "billing": {"code:p:d:c"}})
	snap.Nodes = append(snap.Nodes, snapshot.Node{Kind: "node", ID: topic, Layer: "contract", Type: "topic", Domain: "d", Name: "price-changed", Status: "active"})
	snap.Edges = append(snap.Edges, snapshot.Edge{Kind: "edge", Derivation: "hard", ID: "pub", From: a, To: topic, Type: "PUBLISHES_TOPIC", Active: true})
	run := func(from string) int {
		rv := &review.ReviewResult{Delta: &review.GraphDelta{Added: []review.Fact{edgeFact(from, topic, "PUBLISHES_TOPIC")}}}
		res := Check(Input{Base: &truth.Report{}, Head: &truth.Report{}, BaseSnap: snap, HeadSnap: snap, Review: rv})
		return len(res.Unexplained)
	}
	if n := run(b); n != 0 {
		t.Errorf("same system: %d unexplained", n)
	}
	if n := run("code:p:d:c"); n != 1 {
		t.Errorf("new system: %d unexplained", n)
	}
}
