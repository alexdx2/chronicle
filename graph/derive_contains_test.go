package graph

import (
	"testing"

	"github.com/alexdx2/chronicle-core/store"
	"github.com/alexdx2/chronicle-core/validate"
)

func TestDeriveServiceContains(t *testing.T) {
	g, _, _ := setupTestGraph(t)
	dom := "d1"

	svcID, err := g.UpsertNode(validate.NodeInput{
		NodeKey: "service:service:d1:scoreboardapi", Layer: "service", NodeType: "service",
		DomainKey: dom, Name: "ScoreboardApi", FilePath: "fixtures/sb/ScoreboardApi.csproj",
	}, 1)
	if err != nil || svcID == 0 {
		t.Fatalf("svc upsert: %v", err)
	}
	codeID, err := g.UpsertNode(validate.NodeInput{
		NodeKey: "code:controller:d1:fixtures/sb/controllers/scorecontroller", Layer: "code", NodeType: "controller",
		DomainKey: dom, Name: "ScoreController", FilePath: "fixtures/sb/Controllers/ScoreController.cs",
	}, 1)
	if err != nil || codeID == 0 {
		t.Fatalf("code upsert: %v", err)
	}
	// TS node under a TS service root must NOT get a derived edge.
	if _, err := g.UpsertNode(validate.NodeInput{
		NodeKey: "code:provider:d1:fixtures/ts/src/a.service", Layer: "code", NodeType: "provider",
		DomainKey: dom, Name: "a.service", FilePath: "fixtures/ts/src/a.service.ts",
	}, 1); err != nil {
		t.Fatal(err)
	}

	g.deriveServiceContains(dom, 1)

	edges, _ := g.store.ListEdges(store.EdgeFilter{EdgeType: "CONTAINS"})
	found := false
	for _, e := range edges {
		t.Logf("edge: %s active=%v", e.EdgeKey, e.Active)
		if e.FromNodeKey == "service:service:d1:scoreboardapi" &&
			e.ToNodeKey == "code:controller:d1:fixtures/sb/controllers/scorecontroller" && e.Active {
			found = true
		}
		if e.ToNodeKey == "code:provider:d1:fixtures/ts/src/a.service" {
			t.Errorf("TS node must not get derived service CONTAINS: %s", e.EdgeKey)
		}
	}
	if !found {
		t.Fatalf("derived service CONTAINS edge missing (%d CONTAINS edges total)", len(edges))
	}
}
