package compare

import (
	"bytes"
	"testing"

	"github.com/alexdx2/chronicle-core/snapshot"
	"github.com/alexdx2/chronicle-core/truth"
)

func mkSnap(t *testing.T, coverage ...string) *snapshot.Snapshot {
	t.Helper()
	return mkSnapWith(t, nil, nil, coverage...)
}

func mkSnapWith(t *testing.T, extraNodes []snapshot.Node, extraEdges []snapshot.Edge, coverage ...string) *snapshot.Snapshot {
	t.Helper()
	n := func(id, typ, name string, al ...snapshot.Alias) snapshot.Node {
		return snapshot.Node{Kind: "node", ID: id, Type: typ, Domain: "d", Name: name, Status: "active", Aliases: al}
	}
	s := &snapshot.Snapshot{
		Header: snapshot.Header{Kind: "header", Format: snapshot.Format, Version: snapshot.Version, FactSchema: snapshot.FactSchema, Domains: []string{"d"}},
		Nodes: []snapshot.Node{
			n("contract:endpoint:d:get:/api/orders/{id}", "endpoint", "GET /api/orders/{id}"),
			n("contract:endpoint:d:post:/orders", "endpoint", "POST /orders"),
			n("contract:endpoint:d:get:/api/extra", "endpoint", "GET /api/extra"),
			n("contract:topic:d:order-created", "topic", "order-created"),
			n("code:package:d:npm:express", "package", "express"),
			n("service:external_system:d:tom-api", "external_system", "tom-api"),
			n("service:service:d:orders-api", "service", "orders-api"),
		},
		Edges: []snapshot.Edge{
			{Kind: "edge", ID: "e1", Type: "PUBLISHES_TOPIC", From: "service:service:d:orders-api", To: "contract:topic:d:order-created", Active: true},
			{Kind: "edge", ID: "e2", Type: "DEPENDS_ON", From: "service:service:d:orders-api", To: "code:package:d:npm:express", Active: true},
			{Kind: "edge", ID: "e3", Type: "CALLS_SERVICE", From: "service:service:d:orders-api", To: "service:external_system:d:tom-api", Active: true},
		},
	}
	s.Nodes = append(s.Nodes, extraNodes...)
	s.Edges = append(s.Edges, extraEdges...)
	for i := range s.Edges {
		e := &s.Edges[i]
		e.ID = e.From + "->" + e.To + ":" + e.Type
	}
	for _, c := range coverage {
		s.Coverage = append(s.Coverage, snapshot.Coverage{Kind: "coverage", Path: c})
	}
	var buf bytes.Buffer
	if err := snapshot.Encode(&buf, s); err != nil {
		t.Fatal(err)
	}
	out, warns, err := snapshot.Decode(&buf, snapshot.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	if len(warns) != 0 {
		t.Fatalf("decode warnings: %v", warns)
	}
	if len(out.Edges) != len(s.Edges) {
		t.Fatalf("edges lost: %d vs %d", len(out.Edges), len(s.Edges))
	}
	return out
}

func fact(c truth.Class, key, file string, attrs map[string]string, src ...string) truth.Fact {
	if len(src) == 0 {
		src = []string{"r"}
	}
	return truth.Fact{Class: c, Key: key, Attrs: attrs, Primary: truth.Evidence{File: file}, Sources: src}
}

func statusOf(v *truth.Verdict, key string) truth.Status {
	for _, f := range v.Facts {
		if f.Key == key {
			return f.Status
		}
	}
	return "?"
}

func TestCompareStatuses(t *testing.T) {
	rep := &truth.Report{Facts: []truth.Fact{
		fact(truth.ClassEndpoints, "GET /api/orders/{}", "src/a.cs", nil),
		fact(truth.ClassEndpoints, "GET /orders/{}", "src/a.cs", nil),
		fact(truth.ClassEndpoints, "ANY /orders", "src/a.cs", nil),
		fact(truth.ClassEndpoints, "GET /nope", "src/a.cs", nil),
		fact(truth.ClassEndpoints, "GET /elsewhere", "src/zzz.cs", nil),
		fact(truth.ClassDeps, "npm:express", "package.json", map[string]string{"ecosystem": "npm", "name": "express"}),
		fact(truth.ClassDeps, "nuget:microsoft.extensions.logging", "x.csproj", map[string]string{"ecosystem": "nuget", "name": "Microsoft.Extensions.Logging"}),
		fact(truth.ClassHTTP, "var:TOM_API_URL", "src/b.ts", map[string]string{"var": "TOM_API_URL"}),
		fact(truth.ClassHTTP, "tom-api", "src/b.ts", map[string]string{"host": "tom-api"}),
		fact(truth.ClassChannels, "topic:order.created#produce", "src/a.cs", map[string]string{"name": "order.created", "kind": "topic", "side": "produce"}),
		fact(truth.ClassServices, "own:ordersapi", "Dockerfile", map[string]string{"name": "orders-api", "kind": "own"}),
	}}
	v := Compare(rep, mkSnap(t, "src/a.cs", "src/b.ts"))
	want := map[string]truth.Status{
		"GET /api/orders/{}":                 truth.Confirmed,
		"GET /orders/{}":                     truth.ConfirmedLoose,
		"ANY /orders":                        truth.Confirmed,
		"GET /nope":                          truth.Missing,
		"GET /elsewhere":                     truth.OutOfScope,
		"npm:express":                        truth.Confirmed,
		"nuget:microsoft.extensions.logging": truth.Ignored,
		"var:TOM_API_URL":                    truth.Confirmed,
		"tom-api":                            truth.Confirmed,
		"topic:order.created#produce":        truth.Confirmed,
		"own:ordersapi":                      truth.Confirmed,
	}
	for k, w := range want {
		if g := statusOf(v, k); g != w {
			t.Errorf("%s: got %s want %s", k, g, w)
		}
	}
	if len(v.Uncovered) != 1 || v.Uncovered[0].NodeKey != "contract:endpoint:d:get:/api/extra" {
		t.Errorf("uncovered = %+v", v.Uncovered)
	}
	if len(v.Classes) != 5 || v.Classes[0].Class != truth.ClassEndpoints {
		t.Errorf("classes = %+v", v.Classes)
	}
	if v.Classes[0].Uncovered != 1 || v.Classes[0].Known != 5 {
		t.Errorf("endpoint counts = %+v", v.Classes[0].Counts)
	}
}

func TestCompareEmptyCoverage(t *testing.T) {
	rep := &truth.Report{Facts: []truth.Fact{fact(truth.ClassEndpoints, "GET /nope", "src/q.cs", nil)}}
	v := Compare(rep, mkSnap(t))
	if statusOf(v, "GET /nope") != truth.Missing {
		t.Error("want missing")
	}
	n := 0
	for _, w := range v.Warnings {
		if w == "snapshot has no coverage records; scope unknown" {
			n++
		}
	}
	if n != 1 {
		t.Errorf("warnings = %v", v.Warnings)
	}
}

func TestCompareNilSnapshot(t *testing.T) {
	rep := &truth.Report{Facts: []truth.Fact{fact(truth.ClassEndpoints, "GET /x", "a", nil)}}
	v := Compare(rep, nil)
	if statusOf(v, "GET /x") != truth.Missing || len(v.Warnings) == 0 || v.Warnings[0] != "no snapshot" {
		t.Errorf("%+v", v)
	}
	Compare(nil, &snapshot.Snapshot{})
}

func TestSummariesNumeric(t *testing.T) {
	var facts []truth.JudgedFact
	add := func(n int, s truth.Status) {
		for i := 0; i < n; i++ {
			facts = append(facts, truth.JudgedFact{Fact: truth.Fact{Class: truth.ClassEndpoints, Sources: []string{"x"}}, Status: s})
		}
	}
	add(8, truth.Confirmed)
	add(2, truth.Missing)
	add(90, truth.OutOfScope)
	cs := Summaries(facts, nil)
	if len(cs) != 5 {
		t.Fatal(len(cs))
	}
	e := cs[0]
	if e.Known != 100 || e.Coverage != 0.08 || e.Scope != 0.10 || e.Recall != 0.8 {
		t.Errorf("%+v", e)
	}
	if cs[1].HasData {
		t.Error("empty class must have no data")
	}
}

func TestSummariesBySource(t *testing.T) {
	facts := []truth.JudgedFact{
		{Fact: truth.Fact{Class: truth.ClassDeps, Sources: []string{"b", "a"}}, Status: truth.Confirmed},
		{Fact: truth.Fact{Class: truth.ClassDeps, Sources: []string{"b"}}, Status: truth.Missing},
	}
	d := Summaries(facts, nil)[3]
	if len(d.BySource) != 2 || d.BySource[0].Source != "a" || d.BySource[0].Known != 1 || d.BySource[1].Known != 2 {
		t.Errorf("%+v", d.BySource)
	}
	if d.Known != 2 {
		t.Errorf("class counted a fact once: %d", d.Known)
	}
}

func nd(id, typ, name string) snapshot.Node {
	return snapshot.Node{Kind: "node", ID: id, Type: typ, Domain: "d", Name: name, Status: "active"}
}

func ed(typ, from, to string) snapshot.Edge {
	return snapshot.Edge{Kind: "edge", Type: typ, From: from, To: to, Active: true}
}

func one(t *testing.T, snap *snapshot.Snapshot, f truth.Fact) truth.Status {
	t.Helper()
	return Compare(&truth.Report{Facts: []truth.Fact{f}}, snap).Facts[0].Status
}

func TestHTTPHostMatching(t *testing.T) {
	snap := mkSnapWith(t, []snapshot.Node{nd("service:service:d:api", "service", "api")}, nil, "src/b.ts")
	f := fact(truth.ClassHTTP, "api.stripe.com", "src/b.ts", map[string]string{"host": "api.stripe.com"})
	if s := one(t, snap, f); s != truth.Missing {
		t.Errorf("api.stripe.com vs service api: %s", s)
	}
	host := truth.CanonHost("http://tom-api:3001")
	f = fact(truth.ClassHTTP, host, "src/b.ts", map[string]string{"host": host})
	if s := one(t, snap, f); s != truth.Confirmed {
		t.Errorf("literal host: %s", s)
	}
}

func TestNoEndpointUnderHTTPUncovered(t *testing.T) {
	snap := mkSnapWith(t, nil, []snapshot.Edge{ed("CALLS_ENDPOINT", "service:service:d:orders-api", "contract:endpoint:d:get:/api/extra")}, "src/a.cs")
	v := Compare(&truth.Report{}, snap)
	for _, u := range v.Uncovered {
		if u.Class == truth.ClassHTTP && u.NodeKey == "contract:endpoint:d:get:/api/extra" {
			t.Errorf("endpoint under http uncovered")
		}
	}
}

func TestChannelSides(t *testing.T) {
	snap := mkSnapWith(t, []snapshot.Node{
		nd("contract:topic:d:lonely", "topic", "lonely"),
		nd("contract:topic:d:pubonly", "topic", "pubonly"),
	}, []snapshot.Edge{ed("PUBLISHES_TOPIC", "service:service:d:orders-api", "contract:topic:d:pubonly")}, "src/a.cs")
	ch := func(name, side string) truth.Fact {
		return fact(truth.ClassChannels, "topic:"+name+"#"+side, "src/a.cs", map[string]string{"name": name, "kind": "topic", "side": side})
	}
	cases := []struct {
		name, side string
		want       truth.Status
	}{
		{"lonely", "consume", truth.Missing},
		{"lonely", "declare", truth.Confirmed},
		{"pubonly", "produce", truth.Confirmed},
		{"pubonly", "consume", truth.Missing},
	}
	for _, c := range cases {
		if g := one(t, snap, ch(c.name, c.side)); g != c.want {
			t.Errorf("%s/%s: got %s want %s", c.name, c.side, g, c.want)
		}
	}
}

func TestDepRules(t *testing.T) {
	snap := mkSnapWith(t, []snapshot.Node{
		nd("code:package:d:npm:lodash", "package", "lodash"),
		nd("code:package:d:npm:orphan", "package", "orphan"),
	}, []snapshot.Edge{ed("DEPENDS_ON", "service:service:d:orders-api", "code:package:d:npm:lodash")}, "package.json")
	dep := func(eco, name string) truth.Fact {
		return fact(truth.ClassDeps, eco+":"+name, "package.json", map[string]string{"ecosystem": eco, "name": name})
	}
	if s := one(t, snap, dep("python", "lodash")); s != truth.Missing {
		t.Errorf("cross-ecosystem: %s", s)
	}
	if s := one(t, snap, dep("npm", "express")); s != truth.Confirmed {
		t.Errorf("express: %s", s)
	}
	if s := one(t, snap, dep("npm", "orphan")); s != truth.Missing {
		t.Errorf("orphan: %s", s)
	}
	v := Compare(&truth.Report{Facts: []truth.Fact{dep("npm", "orphan")}}, snap)
	found := false
	for _, u := range v.Uncovered {
		if u.NodeKey == "code:package:d:npm:orphan" {
			found = true
		}
	}
	if !found {
		t.Error("orphan package not uncovered")
	}
}
