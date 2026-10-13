package deps

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func write(t *testing.T, root, rel, body string) {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestScan_AllEcosystems(t *testing.T) {
	root := t.TempDir()
	write(t, root, "web/package.json", `{"name":"@acme/web","dependencies":{"kafkajs":"^2.2.0"},"devDependencies":{"jest":"29"}}`)
	// BOM + CRLF, as saved by Visual Studio
	write(t, root, "api/Api.csproj", "\ufeff<Project Sdk=\"Microsoft.NET.Sdk\">\r\n  <PropertyGroup>\r\n    <PackageId>Acme.Api</PackageId>\r\n  </PropertyGroup>\r\n  <ItemGroup>\r\n    <PackageReference Include=\"Confluent.Kafka\" Version=\"2.3.0\" />\r\n    <ProjectReference Include=\"..\\Shared\\Shared.csproj\" />\r\n  </ItemGroup>\r\n</Project>\r\n")
	write(t, root, "Shared/Shared.csproj", "<Project Sdk=\"Microsoft.NET.Sdk\"><PropertyGroup><AssemblyName>Acme.Shared</AssemblyName></PropertyGroup></Project>")
	write(t, root, "svc/go.mod", "module github.com/acme/svc\n\ngo 1.25\n\nrequire (\n\tgithub.com/segmentio/kafka-go v0.4.47\n\tgolang.org/x/sys v0.1.0 // indirect\n\tgithub.com/acme/tabbed\tv1.2.3\n)\n")
	write(t, root, "py/pyproject.toml", "[project]\nname = \"acme-py\"\ndependencies = [\n  \"fastapi>=0.100\",\n  \"boto3\",\n]\n\n[project.optional-dependencies]\ndev = [\"pytest>=8\"]\n")
	write(t, root, "py/requirements-dev.txt", "pytest==8.0\n# comment\n")
	write(t, root, "jvm/pom.xml", "<project>\n  <groupId>com.acme</groupId><artifactId>orders</artifactId>\n  <dependencyManagement>\n    <dependencies>\n      <dependency>\n        <groupId>org.apache.kafka</groupId>\n        <artifactId>kafka-clients</artifactId>\n        <version>3.0.0</version>\n      </dependency>\n    </dependencies>\n  </dependencyManagement>\n  <dependencies>\n    <dependency>\n      <groupId>org.apache.kafka</groupId>\n      <artifactId>kafka-clients</artifactId>\n      <version>3.7.0</version>\n      <scope>compile</scope>\n    </dependency>\n  </dependencies>\n</project>")
	write(t, root, "src/main.go", "package main")

	rep, err := Scan(root, []string{"web/package.json", "api/Api.csproj", "Shared/Shared.csproj", "svc/go.mod", "py/pyproject.toml", "py/requirements-dev.txt", "jvm/pom.xml", "src/main.go"})
	if err != nil {
		t.Fatal(err)
	}
	wantPub := map[string]string{"@acme/web": "npm", "acme.api": "nuget", "acme.shared": "nuget", "github.com/acme/svc": "go", "acme-py": "python", "com.acme:orders": "maven"}
	if len(rep.Publishes) != len(wantPub) {
		t.Fatalf("publishes = %+v", rep.Publishes)
	}
	for _, p := range rep.Publishes {
		if wantPub[p.Name] != p.Ecosystem {
			t.Errorf("publish %+v", p)
		}
	}
	findIn := func(name, file string) Dependency {
		for _, d := range rep.Depends {
			if d.Name == name && (file == "" || d.File == file) {
				return d
			}
		}
		t.Fatalf("dependency %q missing in %+v", name, rep.Depends)
		return Dependency{}
	}
	find := func(name string) Dependency { return findIn(name, "") }
	if d := find("confluent.kafka"); d.Version != "2.3.0" || d.Ecosystem != "nuget" || d.File != "api/Api.csproj" || d.Line != 6 || d.Section != "dependencies" || !strings.Contains(d.Text, `Include="Confluent.Kafka"`) {
		t.Errorf("nuget dep = %+v", d)
	}
	if d := find("../shared/shared.csproj"); !d.Internal || d.Ecosystem != "nuget" {
		t.Errorf("project reference = %+v", d)
	}
	if d := find("kafkajs"); d.Version != "^2.2.0" || d.Section != "dependencies" || d.Line == 0 {
		t.Errorf("npm dep = %+v", d)
	}
	if d := find("jest"); d.Section != "devDependencies" {
		t.Errorf("npm dev dep = %+v", d)
	}
	if d := find("github.com/acme/tabbed"); d.Version != "v1.2.3" || !strings.Contains("\tgithub.com/acme/tabbed\tv1.2.3", d.Text) || d.Text != "github.com/acme/tabbed\tv1.2.3" {
		t.Errorf("go tab dep = %+v", d)
	}
	if d := find("golang.org/x/sys"); d.Section != "indirect" || d.Version != "v0.1.0" {
		t.Errorf("go indirect = %+v", d)
	}
	if d := find("fastapi"); d.Version != ">=0.100" || d.Section != "dependencies" {
		t.Errorf("py dep = %+v", d)
	}
	if d := findIn("pytest", "py/pyproject.toml"); d.Section != "dev" || d.Version != ">=8" {
		t.Errorf("optional group dep = %+v", d)
	}
	if d := findIn("pytest", "py/requirements-dev.txt"); d.Section != "requirements-dev" || d.Version != "==8.0" {
		t.Errorf("requirements dep = %+v", d)
	}
	if d := find("org.apache.kafka:kafka-clients"); d.Version != "3.7.0" || d.Section != "compile" || d.Line != 15 {
		t.Errorf("maven dep = %+v", d)
	}
}

func TestScan_SortedAndIdempotent(t *testing.T) {
	root := t.TempDir()
	write(t, root, "b/package.json", `{"name":"b","dependencies":{"z":"1","a":"1"}}`)
	write(t, root, "a/package.json", `{"name":"a"}`)
	r1, _ := Scan(root, []string{"b/package.json", "a/package.json"})
	r2, _ := Scan(root, []string{"a/package.json", "b/package.json"})
	if len(r1.Depends) != 2 || r1.Depends[0].Name != "a" || r1.Publishes[0].Name != "a" {
		t.Fatalf("not sorted: %+v %+v", r1.Publishes, r1.Depends)
	}
	if len(r2.Depends) != len(r1.Depends) || r2.Depends[1] != r1.Depends[1] {
		t.Fatalf("order-dependent result")
	}
}

func TestIsManifest(t *testing.T) {
	for _, p := range []string{"package.json", "x/Api.csproj", "Directory.Packages.props", "go.mod", "pyproject.toml", "requirements.txt", "requirements-dev.txt", "pom.xml", "build.gradle", "build.gradle.kts"} {
		if !IsManifest(p) {
			t.Errorf("%s should be a manifest", p)
		}
	}
	for _, p := range []string{"package-lock.json", "main.go", "a.csproj.user", "README.md"} {
		if IsManifest(p) {
			t.Errorf("%s should not be a manifest", p)
		}
	}
}

func TestScan_SkipsUnparseableManifest(t *testing.T) {
	root := t.TempDir()
	write(t, root, "good/package.json", `{"name":"good","dependencies":{"kafkajs":"1"}}`)
	write(t, root, "bad/package.json", `{not json`)
	rep, err := Scan(root, []string{"bad/package.json", "good/package.json", "missing/package.json"})
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Publishes) != 1 || rep.Publishes[0].Name != "good" || len(rep.Depends) != 1 {
		t.Fatalf("report = %+v", rep)
	}
	if len(rep.Warnings) != 2 || !strings.HasPrefix(rep.Warnings[0], "bad/package.json: ") || !strings.HasPrefix(rep.Warnings[1], "missing/package.json: ") {
		t.Fatalf("warnings = %q", rep.Warnings)
	}
	if _, err := Scan(filepath.Join(root, "nope"), []string{"x/package.json"}); err == nil {
		t.Fatal("unreadable root must be an error")
	}
}

func TestScan_SameNameTwoSectionsOneLine(t *testing.T) {
	root := t.TempDir()
	write(t, root, "package.json", `{"name":"x","devDependencies":{"react":"18"},"peerDependencies":{"react":"^18"},"dependencies":{"react":"18.2"}}`)
	for i := 0; i < 20; i++ {
		rep, err := Scan(root, []string{"package.json"})
		if err != nil {
			t.Fatal(err)
		}
		var got []string
		for _, d := range rep.Depends {
			got = append(got, d.Section)
		}
		if strings.Join(got, ",") != "dependencies,devDependencies,peerDependencies" {
			t.Fatalf("order = %v", got)
		}
	}
}

func findDep(t *testing.T, rep Report, name, file string) Dependency {
	t.Helper()
	for _, d := range rep.Depends {
		if d.Name == name && (file == "" || d.File == file) {
			return d
		}
	}
	t.Fatalf("dependency %q (%s) missing in %+v", name, file, rep.Depends)
	return Dependency{}
}

func TestScan_PackageVersionRowsAreNotDependencies(t *testing.T) {
	root := t.TempDir()
	write(t, root, "Directory.Packages.props", "<Project>\n  <ItemGroup>\n    <PackageVersion Include=\"Confluent.Kafka\" Version=\"2.3.0\" />\n    <PackageVersion Include=\"Unused.Pkg\" Version=\"1.0.0\" />\n  </ItemGroup>\n</Project>\n")
	write(t, root, "Api/Api.csproj", "<Project>\n  <ItemGroup>\n    <PackageReference Include=\"Confluent.Kafka\" />\n  </ItemGroup>\n</Project>\n")
	rep, err := Scan(root, []string{"Directory.Packages.props", "Api/Api.csproj"})
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Depends) != 1 {
		t.Fatalf("want one dependency (the csproj's), got %+v", rep.Depends)
	}
	d := rep.Depends[0]
	if d.File != "Api/Api.csproj" || d.Name != "confluent.kafka" || d.Version != "2.3.0" {
		t.Errorf("dependency = %+v", d)
	}
	if len(rep.Publishes) != 1 || rep.Publishes[0].File != "Api/Api.csproj" {
		t.Errorf("publishes = %+v", rep.Publishes)
	}
}

func TestScan_CentralNuGetVersionsFillOnlyUnversionedReferences(t *testing.T) {
	root := t.TempDir()
	write(t, root, "Directory.Packages.props", "<Project><ItemGroup>\n<PackageVersion Include=\"Serilog\" Version=\"3.1.1\" />\n<PackageVersion Include=\"Polly\" Version=\"8.0.0\" />\n</ItemGroup></Project>")
	write(t, root, "a/A.csproj", "<Project>\n<ItemGroup>\n<PackageReference Include=\"Serilog\" />\n<PackageReference Include=\"Polly\" Version=\"7.2.4\" />\n<PackageReference Include=\"NoCentral\" />\n</ItemGroup>\n</Project>")
	rep, _ := Scan(root, []string{"a/A.csproj", "Directory.Packages.props"})
	if d := findDep(t, rep, "serilog", ""); d.Version != "3.1.1" {
		t.Errorf("serilog = %+v", d)
	}
	if d := findDep(t, rep, "polly", ""); d.Version != "7.2.4" {
		t.Errorf("an explicit version wins: %+v", d)
	}
	if d := findDep(t, rep, "nocentral", ""); d.Version != "" {
		t.Errorf("no central entry, no version: %+v", d)
	}
}

func TestScan_PrettyPrintedPackageJSONLines(t *testing.T) {
	root := t.TempDir()
	write(t, root, "package.json", "{\n  \"name\": \"@acme/web\",\n  \"version\": \"1.0.0\",\n  \"dependencies\": {\n    \"kafkajs\": \"^2.2.0\",\n    \"left-pad\": \"1.3.0\"\n  },\n  \"devDependencies\": {\n    \"jest\": \"29\"\n  }\n}\n")
	rep, err := Scan(root, []string{"package.json"})
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Publishes) != 1 || rep.Publishes[0].Line != 2 {
		t.Fatalf("publishes = %+v", rep.Publishes)
	}
	want := map[string]int{"kafkajs": 5, "left-pad": 6, "jest": 9}
	for name, line := range want {
		d := findDep(t, rep, name, "")
		if d.Line != line {
			t.Errorf("%s on line %d, want %d", name, d.Line, line)
		}
		if !strings.Contains(strings.Split(mustRead(t, root, "package.json"), "\n")[d.Line-1], d.Text) || d.Text == "" {
			t.Errorf("%s text %q is not on line %d", name, d.Text, d.Line)
		}
	}
}

func mustRead(t *testing.T, root, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestScan_GradleEcosystem(t *testing.T) {
	root := t.TempDir()
	write(t, root, "svc/build.gradle", "group = 'com.acme'\ndependencies {\n  implementation 'org.apache.kafka:kafka-clients:3.7.0'\n}\n")
	write(t, root, "svc2/build.gradle.kts", "dependencies {\n  implementation(\"io.ktor:ktor-server-core:2.3.0\")\n}\n")
	rep, _ := Scan(root, []string{"svc/build.gradle", "svc2/build.gradle.kts"})
	for _, d := range rep.Depends {
		if d.Ecosystem != "gradle" {
			t.Errorf("%s: ecosystem %q, want gradle", d.File, d.Ecosystem)
		}
	}
	if len(rep.Publishes) != 1 || rep.Publishes[0].Ecosystem != "gradle" {
		t.Errorf("publishes = %+v", rep.Publishes)
	}
	if len(rep.Depends) != 2 {
		t.Errorf("depends = %+v", rep.Depends)
	}
}

func TestScan_GoIndirectSection(t *testing.T) {
	root := t.TempDir()
	write(t, root, "go.mod", "module x\n\nrequire github.com/a/b v1.0.0 // indirect\n")
	rep, _ := Scan(root, []string{"go.mod"})
	if d := findDep(t, rep, "github.com/a/b", ""); d.Section != "indirect" {
		t.Errorf("%+v", d)
	}
}

func TestScan_PythonSetupCfg(t *testing.T) {
	root := t.TempDir()
	write(t, root, "lib/setup.cfg", "[metadata]\nname = acme-lib\nversion = 1.0\n\n[options]\npackages = find:\ninstall_requires =\n    requests>=2.0\n    boto3\n    # comment\n\n[options.extras_require]\ndev = pytest\n")
	rep, err := Scan(root, []string{"lib/setup.cfg"})
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Publishes) != 1 || rep.Publishes[0].Name != "acme-lib" || rep.Publishes[0].Ecosystem != "python" || rep.Publishes[0].Line != 2 {
		t.Fatalf("publishes = %+v", rep.Publishes)
	}
	if len(rep.Depends) != 2 {
		t.Fatalf("depends = %+v", rep.Depends)
	}
	if d := findDep(t, rep, "requests", ""); d.Version != ">=2.0" || d.Line != 8 || d.Section != "dependencies" || d.Text != "requests>=2.0" {
		t.Errorf("requests = %+v", d)
	}
	if d := findDep(t, rep, "boto3", ""); d.Line != 9 {
		t.Errorf("boto3 = %+v", d)
	}
}

func TestScan_PythonPoetry(t *testing.T) {
	root := t.TempDir()
	write(t, root, "pyproject.toml", "[tool.poetry]\nname = \"acme-poetry\"\nversion = \"0.1.0\"\n\n[tool.poetry.dependencies]\npython = \"^3.11\"\nfastapi = \"^0.100\"\nrequests = { version = \"^2.31\", extras = [\"socks\"] }\n\n[tool.poetry.group.dev.dependencies]\npytest = \"^8\"\n")
	rep, err := Scan(root, []string{"pyproject.toml"})
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Publishes) != 1 || rep.Publishes[0].Name != "acme-poetry" || rep.Publishes[0].Line != 2 {
		t.Fatalf("publishes = %+v", rep.Publishes)
	}
	if len(rep.Depends) != 2 {
		t.Fatalf("depends = %+v (python itself is not a dependency)", rep.Depends)
	}
	if d := findDep(t, rep, "fastapi", ""); d.Version != "^0.100" || d.Line != 7 || d.Section != "dependencies" || d.Text != `fastapi = "^0.100"` {
		t.Errorf("fastapi = %+v", d)
	}
	if d := findDep(t, rep, "requests", ""); d.Version != "^2.31" || d.Line != 8 {
		t.Errorf("requests = %+v", d)
	}
}

func TestScan_PythonSingleQuotedStrings(t *testing.T) {
	root := t.TempDir()
	write(t, root, "pyproject.toml", "[project]\nname = 'acme-sq'\ndependencies = [\n  'fastapi>=0.100',\n  \"boto3; python_version < '3.12'\",\n]\n\n[project.optional-dependencies]\n'dev' = ['pytest>=8']\n")
	rep, err := Scan(root, []string{"pyproject.toml"})
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Publishes) != 1 || rep.Publishes[0].Name != "acme-sq" {
		t.Fatalf("publishes = %+v", rep.Publishes)
	}
	if d := findDep(t, rep, "fastapi", ""); d.Version != ">=0.100" || d.Line != 4 {
		t.Errorf("fastapi = %+v", d)
	}
	if d := findDep(t, rep, "boto3", ""); d.Line != 5 {
		t.Errorf("boto3 = %+v", d)
	}
	if d := findDep(t, rep, "pytest", ""); d.Section != "dev" {
		t.Errorf("pytest = %+v", d)
	}
}
