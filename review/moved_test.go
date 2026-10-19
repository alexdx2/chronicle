package review

import (
	"strings"
	"testing"

	"github.com/alexdx2/chronicle-core/gitdiff"
	"github.com/alexdx2/chronicle-core/snapshot"
)

func callEdge(from, to, typ, file string) Fact {
	e := &snapshot.Edge{ID: from + "->" + to + ":" + typ, From: from, To: to, Type: typ,
		Evidence: []snapshot.Evidence{{ID: "ev-" + from, File: file, Range: "5-5"}}}
	return Fact{ID: e.ID, Kind: KindEdge, Edge: e}
}

// sameSys puts every caller into one system.
var sameSys = fakeImpactQuerier{collapse: map[string]SystemRef{}, one: &SystemRef{Kind: kindService, Key: "service:service:d:sales", Name: "sales"}}

func TestMovedIntegration_NotPairedAcrossServicesOfOneMonorepo(t *testing.T) {
	removed := callEdge("a", "svc:x", "CALLS_SERVICE", "a/a.cs")
	added := callEdge("b", "svc:x", "CALLS_SERVICE", "b/b.cs")
	changed := []gitdiff.ChangedFile{{Path: "a/a.cs"}, {Path: "b/b.cs"}}
	d := &GraphDelta{Added: []Fact{added}, Removed: []Fact{removed}}
	svcs := fakeImpactQuerier{collapse: map[string]SystemRef{
		"a": {Kind: kindService, Key: "service:service:d:a", Repo: "mono", Domain: "d"},
		"b": {Kind: kindService, Key: "service:service:d:b", Repo: "mono", Domain: "d"},
	}}
	code, moved := splitMovedIntegrations(d, changed, svcs, svcs)
	if len(moved) != 0 || len(code.Added) != 1 || len(code.Removed) != 1 {
		t.Fatalf("paired across services: moved=%+v code=%+v", moved, code)
	}
	// unresolved callers never pair
	if _, moved := splitMovedIntegrations(d, changed, nil, nil); len(moved) != 0 {
		t.Errorf("paired without a resolver: %+v", moved)
	}
	// unowned callers of one domain pair
	dom := fakeImpactQuerier{collapse: map[string]SystemRef{
		"a": {Kind: "domain", Key: "domain:a", Domain: "d"}, "b": {Kind: "domain", Key: "domain:b", Domain: "d"},
	}}
	if _, moved := splitMovedIntegrations(d, changed, dom, dom); len(moved) != 1 {
		t.Errorf("same-domain unowned callers not paired: %+v", moved)
	}
}

func TestMovedIntegration_PairedWhenBothCallersAreInChangedFiles(t *testing.T) {
	removed := callEdge("code:provider:sales:pricing-client", "service:external_system:sales:pricing-api", "CALLS_SERVICE", "Services/PricingClient.cs")
	added := callEdge("code:controller:sales:orders-controller", "service:external_system:sales:pricing-api", "CALLS_SERVICE", "Controllers/OrdersController.cs")
	d := &GraphDelta{Added: []Fact{added}, Removed: []Fact{removed}}
	changed := []gitdiff.ChangedFile{{Status: "M", Path: "Services/PricingClient.cs"}, {Status: "M", Path: "Controllers/OrdersController.cs"}}

	code, moved := splitMovedIntegrations(d, changed, sameSys, sameSys)
	if len(code.Added) != 0 || len(code.Removed) != 0 {
		t.Fatalf("pair left in delta: %+v", code)
	}
	if len(d.Added) != 1 || len(d.Removed) != 1 {
		t.Fatalf("input delta modified")
	}
	if len(moved) != 1 || moved[0].Kind != IntegrationMoved || moved[0].SourceID != added.Edge.From || len(moved[0].Facts) != 2 {
		t.Fatalf("moved = %+v", moved)
	}
	if got := ClassifyDelta(code); len(got) != 0 {
		t.Errorf("ClassifyDelta of the rest = %+v, want none", got)
	}
}

func TestMovedIntegration_NotPairedWhenOneCallerIsOutsideTheDiffOrTargetsDiffer(t *testing.T) {
	removed := callEdge("a", "svc:x", "CALLS_SERVICE", "a.cs")
	added := callEdge("b", "svc:x", "CALLS_SERVICE", "b.cs")
	d := &GraphDelta{Added: []Fact{added}, Removed: []Fact{removed}}

	if _, moved := splitMovedIntegrations(d, []gitdiff.ChangedFile{{Path: "b.cs"}}, sameSys, sameSys); len(moved) != 0 {
		t.Errorf("paired with a caller outside the diff: %+v", moved)
	}
	other := callEdge("b", "svc:y", "CALLS_SERVICE", "b.cs")
	d2 := &GraphDelta{Added: []Fact{other}, Removed: []Fact{removed}}
	both := []gitdiff.ChangedFile{{Path: "a.cs"}, {Path: "b.cs"}}
	if _, moved := splitMovedIntegrations(d2, both, sameSys, sameSys); len(moved) != 0 {
		t.Errorf("paired across different targets: %+v", moved)
	}
	diffType := callEdge("b", "svc:x", "CALLS_ENDPOINT", "b.cs")
	d3 := &GraphDelta{Added: []Fact{diffType}, Removed: []Fact{removed}}
	if _, moved := splitMovedIntegrations(d3, both, sameSys, sameSys); len(moved) != 0 {
		t.Errorf("paired across edge types: %+v", moved)
	}
	// topic edges are never "moved"
	pub := callEdge("b", "topic:t", "PUBLISHES_TOPIC", "b.cs")
	unpub := callEdge("a", "topic:t", "PUBLISHES_TOPIC", "a.cs")
	d4 := &GraphDelta{Added: []Fact{pub}, Removed: []Fact{unpub}}
	if _, moved := splitMovedIntegrations(d4, both, sameSys, sameSys); len(moved) != 0 {
		t.Errorf("topic edges paired: %+v", moved)
	}
}

func TestMovedIntegration_OneToOneLeavesTheExtraEdgeReported(t *testing.T) {
	removed := callEdge("a", "svc:x", "CALLS_SERVICE", "a.cs")
	add1 := callEdge("b", "svc:x", "CALLS_SERVICE", "b.cs")
	add2 := callEdge("c", "svc:x", "CALLS_SERVICE", "c.cs")
	d := &GraphDelta{Added: []Fact{add2, add1}, Removed: []Fact{removed}}
	changed := []gitdiff.ChangedFile{{Path: "a.cs"}, {Path: "b.cs"}, {Path: "c.cs"}}
	code, moved := splitMovedIntegrations(d, changed, sameSys, sameSys)
	if len(moved) != 1 || moved[0].SourceID != "b" {
		t.Fatalf("moved = %+v, want b", moved)
	}
	got := ClassifyDelta(code)
	if len(got) != 1 || got[0].Kind != IntegrationAdded || got[0].SourceID != "c" {
		t.Errorf("rest = %+v, want c's integration_added", got)
	}
}

func TestMovedIntegration_FindingIsInformationalAndRendered(t *testing.T) {
	removed := callEdge("old", "svc:x", "CALLS_SERVICE", "old.cs")
	added := callEdge("new", "svc:x", "CALLS_SERVICE", "new.cs")
	_, moved := splitMovedIntegrations(&GraphDelta{Added: []Fact{added}, Removed: []Fact{removed}},
		[]gitdiff.ChangedFile{{Path: "old.cs"}, {Path: "new.cs"}}, sameSys, sameSys)
	fs := movedFindings(moved, fakeImpactQuerier{collapse: map[string]SystemRef{"new": {Key: "new", Name: "new"}}})
	if len(fs) != 1 || fs[0].Severity != SeverityInformational || fs[0].Reason != ReasonIntegrationMoved || fs[0].File != "old.cs" {
		t.Fatalf("findings = %+v", fs)
	}
	r := &ReviewResult{Findings: fs}
	if v := verdictFrom(fs); v.IntegrationsAdded != 0 || v.DependenciesRemoved != 0 {
		t.Errorf("verdict counts a moved integration: %+v", v)
	}
	if len(r.ChangedRelationships()) != 0 {
		t.Errorf("moved integration listed as a changed relationship")
	}
	sec := movedSection(r, 10)
	if sec == "" || !strings.Contains(sec, "1 integration moved") || !strings.Contains(sec, "was old") {
		t.Errorf("section = %q", sec)
	}
}
