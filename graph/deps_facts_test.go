package graph

import (
	"testing"

	"github.com/alexdx2/chronicle-core/deps"
	"github.com/alexdx2/chronicle-core/store"
)

func TestWriteDepsExtractions_NuGetPublisherAndConsumer(t *testing.T) {
	g, st, rev := setupTestGraph(t)
	rep := deps.Report{
		Publishes: []deps.Package{{Name: "acme.shared", Ecosystem: "nuget", File: "Shared/Shared.csproj", Line: 3, Text: "<PackageId>Acme.Shared</PackageId>"}},
		Depends: []deps.Dependency{
			{Name: "confluent.kafka", Version: "2.3.0", Section: "dependencies", Ecosystem: "nuget", File: "Api/Api.csproj", Line: 6, Text: `PackageReference Include="Confluent.Kafka"`},
			{Name: "../shared/shared.csproj", Ecosystem: "nuget", File: "Api/Api.csproj", Line: 7, Internal: true, Text: `ProjectReference Include="../Shared/Shared.csproj"`},
			{Name: "system.text.json", Version: "8.0.0", Section: "dependencies", Ecosystem: "nuget", File: "Api/Api.csproj", Line: 8, Text: `PackageReference Include="System.Text.Json"`},
		},
	}
	if _, err := g.WriteDepsExtractions("testapp", rev, rep); err != nil {
		t.Fatal(err)
	}
	rows, err := st.ListStandingExtractionsByRole("testapp", store.DepsExtractionRole)
	if err != nil || len(rows) != 2 {
		t.Fatalf("rows = %d, %v", len(rows), err)
	}
	scanRows, _ := st.ListScanExtractions(rev, "testapp")
	if len(scanRows) != 0 {
		t.Fatalf("deps rows leaked into scan listing: %+v", scanRows)
	}

	pkgs, _ := st.ListNodes(store.NodeFilter{Domain: "testapp", NodeType: "package"})
	if len(pkgs) != 2 { // acme.shared (published) and confluent.kafka (external); system.text.json is infrastructure
		var keys []string
		for _, n := range pkgs {
			keys = append(keys, n.NodeKey)
		}
		t.Fatalf("package nodes = %v", keys)
	}

	edges, _ := st.ListEdges(store.EdgeFilter{})
	var dep, pub int
	for _, e := range edges {
		if e.EdgeType != "DEPENDS_ON" && e.EdgeType != "PUBLISHES" {
			continue
		}
		ev, _ := st.ListEvidenceByEdge(e.EdgeID)
		if len(ev) == 0 || ev[0].AssertionKind != "manifest_line" || ev[0].Confidence != 1.0 || ev[0].ExtractorID != DepsExtractorID {
			t.Fatalf("%s evidence = %+v", e.EdgeKey, ev)
		}
		switch e.EdgeType {
		case "DEPENDS_ON":
			dep++
		case "PUBLISHES":
			pub++
		}
	}
	if dep != 2 || pub != 1 {
		t.Fatalf("edges: depends=%d publishes=%d (%+v)", dep, pub, edges)
	}

	// The project reference lands on the node the sibling publishes from, not on a stub.
	for _, e := range edges {
		if e.EdgeType == "DEPENDS_ON" && e.ToNodeKey != packageNodeKey("testapp", "nuget", "confluent.kafka") &&
			e.ToNodeKey != typedNodeKeyFromFile("testapp", "Shared/Shared.csproj", "file") {
			t.Errorf("internal dependency target = %s", e.ToNodeKey)
		}
	}
}

func TestClassifyDependencyIn_NuGetInfrastructure(t *testing.T) {
	for _, n := range []string{"system.text.json", "microsoft.extensions.logging", "microsoft.net.test.sdk", "xunit", "coverlet.collector", "moq"} {
		if ClassifyDependencyIn("nuget", n) != DepInfrastructure {
			t.Errorf("%s should be infrastructure", n)
		}
	}
	if ClassifyDependencyIn("nuget", "confluent.kafka") == DepInfrastructure {
		t.Error("confluent.kafka is architectural")
	}
	if ClassifyDependencyIn("npm", "lodash") != ClassifyDependency("lodash") {
		t.Error("npm path must equal the legacy classifier")
	}
	for _, n := range []string{"lodash", "@nestjs/core", "left-pad"} {
		if ShouldTrackDependencyIn("npm", n) != ShouldTrackDependency(n) {
			t.Errorf("npm %s: tracking differs from legacy", n)
		}
	}
	if ShouldTrackDependencyIn("maven", "org.junit.jupiter:junit-jupiter") {
		t.Error("junit is infrastructure")
	}
}
