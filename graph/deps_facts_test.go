package graph

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alexdx2/chronicle-core/deps"
	"github.com/alexdx2/chronicle-core/paths"
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
	if len(pkgs) != 2 { // acme.shared (published) and confluent.kafka (consumed); System.* is noise
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

// A dependency resolved by the deps pass and the same dependency resolved from
// a model fact (MCP/API path, no sanitizer) must land on ONE package node and
// ONE active DEPENDS_ON, in either layering order.
func TestWriteDepsExtractions_SameNodeAsLegacyDependency(t *testing.T) {
	for _, depsFirst := range []bool{false, true} {
		name := "model_first"
		if depsFirst {
			name = "deps_first"
		}
		t.Run(name, func(t *testing.T) {
			g, st, rev := setupTestGraph(t)
			rep := deps.Report{
				Publishes: []deps.Package{{Name: "web", Ecosystem: "npm", File: "web/package.json", Line: 2, Text: `"name"`, App: true}},
				Depends: []deps.Dependency{
					{Name: "@acme/ui", Version: "1.0.0", Section: "dependencies", Ecosystem: "npm", File: "web/package.json", Line: 5, Text: `"@acme/ui"`},
				}}
			rev2, _ := st.CreateRevision("testapp", "scan_2", "", "manual", "full", "{}")
			model := func() {
				g.SaveFileExtraction(rev2, "testapp", "web/package.json", "extracted", "manifest",
					`[{"kind":"dependency","to":"@acme/ui","section":"dependencies"}]`, "")
				if _, err := g.ResolveExtractions("testapp", rev2); err != nil {
					t.Fatal(err)
				}
			}
			depsPass := func() {
				if _, err := g.WriteDepsExtractions("testapp", rev, rep); err != nil {
					t.Fatal(err)
				}
			}
			if depsFirst {
				depsPass()
				model()
			} else {
				model()
				depsPass()
			}

			nodes, _ := st.ListNodes(store.NodeFilter{Domain: "testapp"})
			var uiKeys []string
			for _, n := range nodes {
				if strings.Contains(n.NodeKey, "@acme/ui") {
					uiKeys = append(uiKeys, n.NodeKey)
				}
			}
			if len(uiKeys) != 1 || !strings.HasPrefix(uiKeys[0], "code:package:") {
				t.Fatalf("want exactly one code:package node for @acme/ui, got %v", uiKeys)
			}
			edges, _ := st.ListEdges(store.EdgeFilter{EdgeType: "DEPENDS_ON"})
			var active []string
			for _, e := range edges {
				if e.Active && strings.Contains(e.ToNodeKey, "@acme/ui") && strings.HasPrefix(e.FromNodeKey, "service:service:") {
					active = append(active, e.FromNodeKey+"->"+e.ToNodeKey)
				}
			}
			if len(active) != 1 {
				t.Fatalf("want exactly one active DEPENDS_ON from the service to @acme/ui, got %v", active)
			}
		})
	}
}

func TestEcosystemOfManifest(t *testing.T) {
	for path, want := range map[string]string{
		"a/package.json": "npm", "x/Api.csproj": "nuget", "go.mod": "go", "py/pyproject.toml": "python",
		"requirements-dev.txt": "python", "pom.xml": "maven", "svc/build.gradle.kts": "gradle", "README.md": "",
	} {
		if got := deps.EcosystemOfManifest(path); got != want {
			t.Errorf("EcosystemOfManifest(%q) = %q, want %q", path, got, want)
		}
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

// A Go module path is one identity: its host must not collapse several
// modules into one node.
func TestWriteDepsExtractions_GoModulesStayDistinct(t *testing.T) {
	g, st, rev := setupTestGraph(t)
	rep := deps.Report{Depends: []deps.Dependency{
		{Name: "github.com/segmentio/kafka-go", Version: "v0.4.47", Section: "dependencies", Ecosystem: "go", File: "svc/go.mod", Line: 5, Text: "github.com/segmentio/kafka-go v0.4.47"},
		{Name: "github.com/nats-io/nats.go", Version: "v1.0.0", Section: "dependencies", Ecosystem: "go", File: "svc/go.mod", Line: 6, Text: "github.com/nats-io/nats.go v1.0.0"},
		{Name: "github.com/redis/go-redis", Version: "v9.0.0", Section: "dependencies", Ecosystem: "go", File: "svc/go.mod", Line: 7, Text: "github.com/redis/go-redis v9.0.0"},
	}}
	if _, err := g.WriteDepsExtractions("testapp", rev, rep); err != nil {
		t.Fatal(err)
	}
	edges, _ := st.ListEdges(store.EdgeFilter{EdgeType: "DEPENDS_ON"})
	targets := map[string]bool{}
	for _, e := range edges {
		targets[e.ToNodeKey] = true
	}
	// (golang.org/x/* is infrastructure for go since A7, so it would not
	// produce a node at all; three architectural modules stand in.)
	if len(targets) != 3 {
		t.Fatalf("want three distinct to-nodes, got %v", targets)
	}
	for _, name := range []string{"github.com/segmentio/kafka-go", "github.com/nats-io/nats.go", "github.com/redis/go-redis"} {
		if !targets[packageNodeKey("testapp", "go", name)] {
			t.Fatalf("node for %s missing: %v", name, targets)
		}
	}
}

// A fleet build reads a worktree that is not the process cwd: the evidence of
// a manifest fact is checked against the file under the project root.
func TestWriteDepsExtractions_VerifiesAgainstProjectRoot(t *testing.T) {
	g, st, rev := setupTestGraph(t)
	root := t.TempDir()
	rel := "api/Api.csproj"
	if err := os.MkdirAll(filepath.Join(root, "api"), 0o755); err != nil {
		t.Fatal(err)
	}
	body := "<Project Sdk=\"Microsoft.NET.Sdk\">\n  <PropertyGroup>\n    <PackageId>Acme.Api</PackageId>\n  </PropertyGroup>\n  <ItemGroup>\n    <PackageReference Include=\"Confluent.Kafka\" Version=\"2.3.0\" />\n  </ItemGroup>\n</Project>\n"
	if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(rel)), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	rep, err := deps.Scan(root, []string{rel})
	if err != nil {
		t.Fatal(err)
	}
	prev := paths.Root()
	paths.SetProjectRoot(root)
	t.Cleanup(func() { paths.SetProjectRoot(prev) })

	if _, err := g.WriteDepsExtractions("testapp", rev, rep); err != nil {
		t.Fatal(err)
	}
	edges, _ := st.ListEdges(store.EdgeFilter{})
	seen := 0
	for _, e := range edges {
		if e.EdgeType != "DEPENDS_ON" && e.EdgeType != "PUBLISHES" {
			continue
		}
		ev, _ := st.ListEvidenceByEdge(e.EdgeID)
		if len(ev) == 0 {
			t.Fatalf("%s has no evidence", e.EdgeKey)
		}
		for _, x := range ev {
			seen++
			if x.AssertionKind != "manifest_line" || x.VerificationStatus != "verified" {
				t.Errorf("%s evidence: kind=%s status=%s (%s)", e.EdgeKey, x.AssertionKind, x.VerificationStatus, x.VerificationReason)
			}
		}
	}
	if seen < 2 {
		t.Fatalf("expected evidence for a dependency and a publication, saw %d", seen)
	}
}

// The deterministic deps pass is a structural asserter, verified or not.
func TestDepsExtractorIsStructuralTier(t *testing.T) {
	row := store.EvidenceRow{EvidencePolarity: "positive", EvidenceStatus: "valid", SourceKind: "file",
		ExtractorID: DepsExtractorID, Confidence: 1.0, VerificationStatus: "unverified"}
	structural := ConfidenceCap([]store.EvidenceRow{{EvidencePolarity: "positive", EvidenceStatus: "valid",
		SourceKind: "file", ExtractorID: "chronicle-ast", Confidence: 1.0}})
	if got := ConfidenceCap([]store.EvidenceRow{row}); got != structural {
		t.Fatalf("deps cap = %v, want the structural tier %v", got, structural)
	}

	// End to end: the manifest is not readable (so the row stays unverified)
	// and the derived DEPENDS_ON edge still carries the structural trust.
	g, st, rev := setupTestGraph(t)
	rep := deps.Report{Depends: []deps.Dependency{
		{Name: "@acme/ui", Version: "1.0.0", Section: "dependencies", Ecosystem: "npm", File: "gone/package.json", Line: 3, Text: `"@acme/ui"`},
	}}
	if _, err := g.WriteDepsExtractions("testapp", rev, rep); err != nil {
		t.Fatal(err)
	}
	edges, _ := st.ListEdges(store.EdgeFilter{EdgeType: "DEPENDS_ON"})
	if len(edges) != 1 {
		t.Fatalf("edges = %+v", edges)
	}
	if edges[0].Confidence != structural {
		t.Errorf("edge confidence = %v, want %v", edges[0].Confidence, structural)
	}
}

// An npm workspace reference names the sibling by its package name; only
// NuGet project references are known by a file stem.
func TestWriteDepsExtractions_NpmWorkspaceReferenceResolvesToSibling(t *testing.T) {
	g, st, rev := setupTestGraph(t)
	rep := deps.Report{
		Publishes: []deps.Package{{Name: "@acme/ui", Ecosystem: "npm", File: "ui/package.json", Line: 2, Text: `"name"`, App: true}},
		Depends: []deps.Dependency{
			{Name: "@acme/ui", Version: "workspace:*", Section: "dependencies", Ecosystem: "npm", File: "web/package.json", Line: 5, Internal: true, Text: `"@acme/ui": "workspace:*"`},
		},
	}
	if _, err := g.WriteDepsExtractions("testapp", rev, rep); err != nil {
		t.Fatal(err)
	}
	// The publisher is a service node now (deps-declared), the sibling's owner.
	owner := canonicalNodeKey("service:service:testapp:@acme/ui")
	edges, _ := st.ListEdges(store.EdgeFilter{EdgeType: "DEPENDS_ON"})
	if len(edges) != 1 || edges[0].ToNodeKey != owner {
		t.Fatalf("workspace dependency should land on %s, edges = %+v", owner, edges)
	}
	nodes, _ := st.ListNodes(store.NodeFilter{Domain: "testapp"})
	for _, n := range nodes {
		if strings.Contains(n.NodeKey, ":provider:") {
			t.Errorf("twin stub node %s", n.NodeKey)
		}
	}
}

// A ProjectReference to a sibling that publishes nothing (IsPackable=false)
// still lands on that sibling's own node, not on a provider stub.
func TestWriteDepsExtractions_ProjectReferenceToUnpackableSibling(t *testing.T) {
	g, st, rev := setupTestGraph(t)
	rep := deps.Report{
		Depends: []deps.Dependency{
			{Name: "newtonsoft.json", Version: "13.0.1", Section: "dependencies", Ecosystem: "nuget", File: "Shared/Shared.csproj", Line: 4, Text: `PackageReference Include="Newtonsoft.Json"`},
			{Name: "../shared/shared.csproj", Ecosystem: "nuget", File: "Api/Api.csproj", Line: 7, Internal: true, Text: `ProjectReference Include="../Shared/Shared.csproj"`},
		},
	}
	if _, err := g.WriteDepsExtractions("testapp", rev, rep); err != nil {
		t.Fatal(err)
	}
	sibling := typedNodeKeyFromFile("testapp", "Shared/Shared.csproj", "file")
	edges, _ := st.ListEdges(store.EdgeFilter{EdgeType: "DEPENDS_ON"})
	var found bool
	for _, e := range edges {
		if strings.HasPrefix(e.FromNodeKey, typedNodeKeyFromFile("testapp", "Api/Api.csproj", "file")) && e.ToNodeKey == sibling {
			found = true
		}
		if strings.Contains(e.ToNodeKey, "shared-csproj") || strings.Contains(e.ToNodeKey, ":provider:testapp:..") {
			t.Errorf("stub target %s", e.ToNodeKey)
		}
	}
	if !found {
		t.Fatalf("Api -> %s edge missing: %+v", sibling, edges)
	}
}

func TestClassifyDependencyIn_GoAndPythonInfrastructure(t *testing.T) {
	for _, n := range []string{"golang.org/x/sys", "golang.org/x/net", "github.com/stretchr/testify", "github.com/google/go-cmp", "go.uber.org/mock"} {
		if ClassifyDependencyIn("go", n) != DepInfrastructure || ShouldTrackDependencyIn("go", n) {
			t.Errorf("go %s should be infrastructure", n)
		}
	}
	for _, n := range []string{"pytest", "mypy", "black", "ruff", "flake8", "setuptools", "wheel", "pip"} {
		if ClassifyDependencyIn("python", n) != DepInfrastructure || ShouldTrackDependencyIn("python", n) {
			t.Errorf("python %s should be infrastructure", n)
		}
	}
	for _, c := range [][2]string{{"go", "github.com/segmentio/kafka-go"}, {"python", "fastapi"}, {"python", "blackboxlib"}} {
		if !ShouldTrackDependencyIn(c[0], c[1]) {
			t.Errorf("%s %s must be tracked", c[0], c[1])
		}
	}
}

// A Go "// indirect" requirement is transitive: like devDependencies it is
// dropped unless the domain opts in with include_dev_deps.
func TestWriteDepsExtractions_GoIndirectFollowsIncludeDevDeps(t *testing.T) {
	rep := deps.Report{Depends: []deps.Dependency{
		{Name: "github.com/segmentio/kafka-go", Version: "v0.4.47", Section: "dependencies", Ecosystem: "go", File: "svc/go.mod", Line: 5, Text: "github.com/segmentio/kafka-go v0.4.47"},
		{Name: "github.com/nats-io/nats.go", Version: "v1.0.0", Section: "indirect", Ecosystem: "go", File: "svc/go.mod", Line: 6, Text: "github.com/nats-io/nats.go v1.0.0"},
	}}
	count := func(include bool) int {
		g, st, rev := setupTestGraph(t)
		if _, err := g.WriteDepsExtractionsOpts("testapp", rev, rep, DepsOptions{IncludeDevDeps: include}); err != nil {
			t.Fatal(err)
		}
		edges, _ := st.ListEdges(store.EdgeFilter{EdgeType: "DEPENDS_ON"})
		return len(edges)
	}
	if n := count(false); n != 1 {
		t.Errorf("without include_dev_deps: %d edges, want 1 (the direct one)", n)
	}
	if n := count(true); n != 2 {
		t.Errorf("with include_dev_deps: %d edges, want 2", n)
	}
}

func TestWriteDepsExtractions_FrameworksAreFactsInfrastructureIsNoise(t *testing.T) {
	g, st, rev := setupTestGraph(t)
	rep := deps.Report{
		Publishes: []deps.Package{{Name: "acme-api", Ecosystem: "npm", File: "package.json", Line: 2, Text: `"name": "acme-api"`}},
		Depends: []deps.Dependency{
			{Name: "@nestjs/common", Version: "^10", Section: "dependencies", Ecosystem: "npm", File: "package.json", Line: 5, Text: `"@nestjs/common"`},
			{Name: "kafkajs", Version: "^2", Section: "dependencies", Ecosystem: "npm", File: "package.json", Line: 6, Text: `"kafkajs"`},
			{Name: "@types/node", Version: "^20", Section: "dependencies", Ecosystem: "npm", File: "package.json", Line: 7, Text: `"@types/node"`},
			{Name: "confluent.kafka", Version: "2.3.0", Section: "dependencies", Ecosystem: "nuget", File: "Api/Api.csproj", Line: 6, Text: `PackageReference Include="Confluent.Kafka"`},
			{Name: "system.text.json", Version: "8", Section: "dependencies", Ecosystem: "nuget", File: "Api/Api.csproj", Line: 7, Text: `PackageReference Include="System.Text.Json"`},
			{Name: "microsoft.extensions.logging", Version: "8", Section: "dependencies", Ecosystem: "nuget", File: "Api/Api.csproj", Line: 8, Text: `PackageReference Include="Microsoft.Extensions.Logging"`},
		},
	}
	if _, err := g.WriteDepsExtractions("d", rev, rep); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		packageNodeKey("d", "npm", "@nestjs/common"), packageNodeKey("d", "npm", "kafkajs"), packageNodeKey("d", "nuget", "confluent.kafka"),
	} {
		n, err := st.GetNodeByKey(want)
		if err != nil || n == nil {
			t.Fatalf("no node %s: %v", want, err)
		}
		edges, _ := st.ListEdges(store.EdgeFilter{ToNodeID: n.NodeID, EdgeType: "DEPENDS_ON"})
		if len(edges) != 1 {
			t.Errorf("%s: DEPENDS_ON edges = %d", want, len(edges))
		}
	}
	nodes, _ := st.ListNodes(store.NodeFilter{Domain: "d"})
	for _, n := range nodes {
		l := strings.ToLower(n.NodeKey)
		if strings.Contains(l, "system.") || strings.Contains(l, "microsoft.") || strings.Contains(l, "@types") {
			t.Errorf("infrastructure became a node: %s", n.NodeKey)
		}
	}
}

func TestWriteDepsExtractions_PublishedPackageMintsServiceNode(t *testing.T) {
	g, st, rev := setupTestGraph(t)
	// A model-less run met jerry-api as a call target before the manifest.
	extKey := "service:external_system:d:jerry-api"
	if _, err := st.UpsertNode(store.NodeRow{NodeKey: extKey, Layer: "service", NodeType: "external_system", DomainKey: "d", Name: "jerry-api", Status: "active", LastSeenRevisionID: rev}); err != nil {
		t.Fatal(err)
	}
	rep := deps.Report{
		Publishes: []deps.Package{
			{Name: "jerry-api", Ecosystem: "npm", File: "jerry-api/package.json", Line: 2, Text: `"name": "jerry-api"`, App: true},
			{Name: "scoreboardapi", Ecosystem: "nuget", File: "sb/ScoreboardApi.csproj", Line: 3, Text: `<AssemblyName>ScoreboardApi</AssemblyName>`, App: true},
			{Name: "@acme/shared", Ecosystem: "npm", File: "shared/package.json", Line: 2, Text: `"name": "@acme/shared"`},
		},
		Depends: []deps.Dependency{
			{Name: "kafkajs", Version: "^2", Section: "dependencies", Ecosystem: "npm", File: "jerry-api/package.json", Line: 6, Text: `"kafkajs"`},
		},
	}
	if _, err := g.WriteDepsExtractions("d", rev, rep); err != nil {
		t.Fatal(err)
	}
	svc, err := st.GetNodeByKey("service:service:d:jerry-api")
	if err != nil || svc == nil || svc.Status != "active" || svc.FilePath != "jerry-api/package.json" {
		t.Fatalf("service node = %+v, %v", svc, err)
	}
	ev, _ := st.ListEvidenceByNode(svc.NodeID)
	var line bool
	for _, e := range ev {
		if e.ExtractorID == DepsExtractorID && e.AssertionKind == "manifest_line" {
			line = true
		}
	}
	if !line {
		t.Errorf("no manifest_line evidence on the service: %+v", ev)
	}
	if sb, _ := st.GetNodeByKey("service:service:d:scoreboardapi"); sb == nil {
		t.Error("csproj service missing")
	}
	// A library is a package, not a service.
	if lib, _ := st.GetNodeByKey("service:service:d:@acme/shared"); lib != nil {
		t.Errorf("library became a service: %+v", lib)
	}
	if pk, _ := st.GetNodeByKey(packageNodeKey("d", "npm", "@acme/shared")); pk == nil {
		t.Error("library package node missing")
	}
	if ext, _ := st.GetNodeByKey(extKey); ext != nil && ext.Status == "active" {
		t.Errorf("external twin of an own service survived: %+v", ext)
	}
	edges, _ := st.ListEdges(store.EdgeFilter{})
	var pub, dep bool
	for _, e := range edges {
		if e.EdgeType == "PUBLISHES" && e.FromNodeKey == "service:service:d:jerry-api" {
			pub = true
		}
		if e.EdgeType == "DEPENDS_ON" && e.FromNodeKey == "service:service:d:jerry-api" {
			dep = true
		}
	}
	if !pub || !dep {
		t.Errorf("edges must start at the service: publishes=%v depends=%v", pub, dep)
	}
}
