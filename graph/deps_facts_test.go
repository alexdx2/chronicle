package graph

import (
	"strings"
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
	if len(pkgs) != 1 { // only acme.shared (published): consumed packages keep the legacy node shape
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
		if e.EdgeType == "DEPENDS_ON" && e.ToNodeKey != typedNodeKeyFromImport("testapp", "confluent.kafka", "") &&
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

// A dependency resolved by the deps pass and the same dependency resolved from
// an LLM fact must land on ONE node: cross-repo package links are built from
// the consumer nodes dependency facts create.
func TestWriteDepsExtractions_SameNodeAsLegacyDependency(t *testing.T) {
	g, st, rev := setupTestGraph(t)
	rep := deps.Report{Depends: []deps.Dependency{
		{Name: "@acme/ui", Version: "1.0.0", Section: "dependencies", Ecosystem: "npm", File: "web/package.json", Line: 5, Text: `"@acme/ui"`},
	}}
	if _, err := g.WriteDepsExtractions("testapp", rev, rep); err != nil {
		t.Fatal(err)
	}
	rev2, _ := st.CreateRevision("testapp", "scan_2", "", "manual", "full", "{}")
	g.SaveFileExtraction(rev2, "testapp", "web/package.json", "extracted", "manifest",
		`[{"kind":"dependency","to":"@acme/ui","section":"dependencies"}]`, "")
	if _, err := g.ResolveExtractions("testapp", rev2); err != nil {
		t.Fatal(err)
	}

	nodes, _ := st.ListNodes(store.NodeFilter{Domain: "testapp"})
	var uiKeys []string
	for _, n := range nodes {
		if strings.Contains(n.NodeKey, "@acme/ui") {
			uiKeys = append(uiKeys, n.NodeKey)
		}
	}
	if len(uiKeys) != 1 {
		t.Fatalf("want exactly one node for @acme/ui, got %v", uiKeys)
	}
	edges, _ := st.ListEdges(store.EdgeFilter{EdgeType: "DEPENDS_ON"})
	var viaDeps bool
	for _, e := range edges {
		if e.ToNodeKey == uiKeys[0] {
			ev, _ := st.ListEvidenceByEdge(e.EdgeID)
			for _, x := range ev {
				if x.ExtractorID == DepsExtractorID {
					viaDeps = true
				}
			}
		}
	}
	if !viaDeps {
		t.Fatalf("deps-origin DEPENDS_ON edge does not point at %s: %+v", uiKeys[0], edges)
	}
}

func TestWriteDepsExtractions_InternalDependencyIsNeverNoise(t *testing.T) {
	g, st, rev := setupTestGraph(t)
	rep := deps.Report{Depends: []deps.Dependency{
		{Name: "system.core/system.core.csproj", Ecosystem: "nuget", File: "Api/Api.csproj", Line: 4, Internal: true, Text: `ProjectReference Include="System.Core/System.Core.csproj"`},
		{Name: "eslint-config-acme", Ecosystem: "npm", File: "web/package.json", Line: 4, Internal: true, Text: `"eslint-config-acme"`},
	}}
	if _, err := g.WriteDepsExtractions("testapp", rev, rep); err != nil {
		t.Fatal(err)
	}
	edges, _ := st.ListEdges(store.EdgeFilter{EdgeType: "DEPENDS_ON"})
	if len(edges) != 2 {
		t.Fatalf("internal dependencies must not be filtered, got %d edges: %+v", len(edges), edges)
	}
}

func TestResolvePublishesPackage_LLMOriginKeepsLegacyConfidence(t *testing.T) {
	g, st, rev := setupTestGraph(t)
	g.SaveFileExtraction(rev, "testapp", "lib/package.json", "extracted", "manifest",
		`[{"kind":"publishes_package","to":"acme-lib","ecosystem":"npm"}]`, "")
	if _, err := g.ResolveExtractions("testapp", rev); err != nil {
		t.Fatal(err)
	}
	edges, _ := st.ListEdges(store.EdgeFilter{EdgeType: "PUBLISHES"})
	if len(edges) != 1 {
		t.Fatalf("PUBLISHES edges = %+v", edges)
	}
	if edges[0].Confidence > 0.95 {
		t.Errorf("edge confidence = %v, want <= 0.95", edges[0].Confidence)
	}
	ev, _ := st.ListEvidenceByEdge(edges[0].EdgeID)
	if len(ev) == 0 {
		t.Fatal("no evidence")
	}
	for _, e := range ev {
		if e.AssertionKind == "manifest_line" || e.Confidence != 0.95 {
			t.Errorf("evidence = kind %s conf %v, want legacy assertion at 0.95", e.AssertionKind, e.Confidence)
		}
	}
}

func TestWriteDepsExtractions_RerunSupersedesWhatManifestNoLongerSays(t *testing.T) {
	g, st, rev := setupTestGraph(t)
	dep := func(name string, line int) deps.Dependency {
		return deps.Dependency{Name: name, Version: "1.0", Section: "dependencies", Ecosystem: "nuget", File: "Api/Api.csproj", Line: line, Text: `PackageReference Include="` + name + `"`}
	}
	if _, err := g.WriteDepsExtractions("testapp", rev, deps.Report{Depends: []deps.Dependency{dep("confluent.kafka", 6), dep("serilog", 7)}}); err != nil {
		t.Fatal(err)
	}
	rev2, _ := st.CreateRevision("testapp", "scan_2", "", "manual", "full", "{}")
	if _, err := g.WriteDepsExtractions("testapp", rev2, deps.Report{Depends: []deps.Dependency{dep("serilog", 5)}}); err != nil {
		t.Fatal(err)
	}

	active := true
	edges, _ := st.ListEdges(store.EdgeFilter{EdgeType: "DEPENDS_ON", Active: &active})
	if len(edges) != 1 || !strings.HasSuffix(edges[0].ToNodeKey, "serilog") {
		t.Fatalf("want one active DEPENDS_ON (serilog), got %+v", edges)
	}
	all, _ := st.ListEdges(store.EdgeFilter{EdgeType: "DEPENDS_ON"})
	for _, e := range all {
		ev, _ := st.ListEvidenceByEdge(e.EdgeID)
		var live int
		for _, x := range ev {
			if x.EvidenceStatus == "valid" || x.EvidenceStatus == "revalidated" {
				live++
			}
		}
		if strings.HasSuffix(e.ToNodeKey, "kafka") && live != 0 {
			t.Errorf("removed dependency still has %d live evidence rows", live)
		}
		if strings.HasSuffix(e.ToNodeKey, "serilog") && live != 1 {
			t.Errorf("kept dependency has %d live evidence rows, want 1", live)
		}
	}

	// A manifest that left the report asserts nothing and loses its standing row.
	rev3, _ := st.CreateRevision("testapp", "scan_3", "", "manual", "full", "{}")
	if _, err := g.WriteDepsExtractions("testapp", rev3, deps.Report{}); err != nil {
		t.Fatal(err)
	}
	if rows, _ := st.ListStandingExtractionsByRole("testapp", store.DepsExtractionRole); len(rows) != 0 {
		t.Errorf("standing rows left: %+v", rows)
	}
	if edges, _ := st.ListEdges(store.EdgeFilter{EdgeType: "DEPENDS_ON", Active: &active}); len(edges) != 0 {
		t.Errorf("active edges after manifest removal: %+v", edges)
	}
}
