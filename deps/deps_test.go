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
	// BOM + CRLF, как из Visual Studio
	write(t, root, "api/Api.csproj", "\ufeff<Project Sdk=\"Microsoft.NET.Sdk\">\r\n  <PropertyGroup>\r\n    <PackageId>Acme.Api</PackageId>\r\n  </PropertyGroup>\r\n  <ItemGroup>\r\n    <PackageReference Include=\"Confluent.Kafka\" Version=\"2.3.0\" />\r\n    <ProjectReference Include=\"..\\Shared\\Shared.csproj\" />\r\n  </ItemGroup>\r\n</Project>\r\n")
	write(t, root, "Shared/Shared.csproj", "<Project Sdk=\"Microsoft.NET.Sdk\"><PropertyGroup><AssemblyName>Acme.Shared</AssemblyName></PropertyGroup></Project>")
	write(t, root, "svc/go.mod", "module github.com/acme/svc\n\ngo 1.25\n\nrequire (\n\tgithub.com/segmentio/kafka-go v0.4.47\n\tgolang.org/x/sys v0.1.0 // indirect\n)\n")
	write(t, root, "py/pyproject.toml", "[project]\nname = \"acme-py\"\ndependencies = [\n  \"fastapi>=0.100\",\n  \"boto3\",\n]\n\n[project.optional-dependencies]\ndev = [\"pytest>=8\"]\n")
	write(t, root, "py/requirements-dev.txt", "pytest==8.0\n# comment\n")
	write(t, root, "jvm/pom.xml", `<project><groupId>com.acme</groupId><artifactId>orders</artifactId><dependencies><dependency><groupId>org.apache.kafka</groupId><artifactId>kafka-clients</artifactId><version>3.7.0</version><scope>compile</scope></dependency></dependencies></project>`)
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
	if d := find("org.apache.kafka:kafka-clients"); d.Version != "3.7.0" || d.Section != "compile" {
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
