package review

import (
	"testing"

	"github.com/alexdx2/chronicle-core/snapshot"
)

func topicSetup() (*snapshot.Snapshot, fakeImpactQuerier) {
	base := &snapshot.Snapshot{Edges: []snapshot.Edge{
		{ID: "pub1", From: "code:a", To: "contract:topic:d:price-changed", Type: "PUBLISHES_TOPIC", Active: true},
	}}
	q := fakeImpactQuerier{collapse: map[string]SystemRef{
		"code:a":       {Kind: kindService, Key: "service:service:d:pricing", Name: "pricing"},
		"code:program": {Kind: kindService, Key: "service:service:d:pricing", Name: "pricing"},
		"code:other":   {Kind: kindService, Key: "service:service:d:billing", Name: "billing"},
	}}
	return base, q
}

func TestSameSystemTopic_InformationalNotCounted(t *testing.T) {
	base, q := topicSetup()
	added := callEdge("code:program", "contract:topic:d:price-changed", "PUBLISHES_TOPIC", "Program.cs")
	code, same := splitSameSystemTopic(&GraphDelta{Added: []Fact{added}}, base, q, q)
	if len(code.Added) != 0 || len(same) != 1 {
		t.Fatalf("code=%+v same=%+v", code, same)
	}
	if got := ClassifyDelta(code); len(got) != 0 {
		t.Errorf("classified: %+v", got)
	}
	fs := sameSystemTopicFindings(same, q)
	if len(fs) != 1 || fs[0].Severity != SeverityInformational || fs[0].Reason != ReasonSameSystemTopic || fs[0].File != "Program.cs" {
		t.Fatalf("findings = %+v", fs)
	}
	if v := verdictFrom(fs); v.IntegrationsAdded != 0 {
		t.Errorf("verdict %+v", v)
	}
	if v := verdictFrom(fs); v.IntegrationsAdded != 0 {
		t.Errorf("verdict counts it: %+v", v)
	}
}

func TestSameSystemTopic_NewSystemStillAChange(t *testing.T) {
	base, q := topicSetup()
	added := callEdge("code:other", "contract:topic:d:price-changed", "PUBLISHES_TOPIC", "Other.cs")
	consumer := callEdge("code:program", "contract:topic:d:price-changed", "CONSUMES_TOPIC", "Program.cs") // base has no consumer edge
	code, same := splitSameSystemTopic(&GraphDelta{Added: []Fact{added, consumer}}, base, q, q)
	if len(same) != 0 || len(code.Added) != 2 {
		t.Fatalf("code=%+v same=%+v", code, same)
	}
	if got := ClassifyDelta(code); len(got) != 2 || got[0].Kind != IntegrationAdded {
		t.Errorf("classified: %+v", got)
	}
}
