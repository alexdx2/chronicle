package reviewcheck

import (
	"testing"

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
