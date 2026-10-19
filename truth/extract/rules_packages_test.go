package extract

import (
	"sort"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/alexdx2/chronicle-core/truth"
)

func pkgFacts(t *testing.T, files map[string]string) map[string]truth.Fact {
	t.Helper()
	m := fstest.MapFS{}
	for p, c := range files {
		m[p] = &fstest.MapFile{Data: []byte(c)}
	}
	rep, err := Extract(m)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]truth.Fact{}
	for _, f := range rep.Facts {
		if f.Class == truth.ClassPackages {
			out[f.Key] = f
		}
	}
	return out
}

func keys(m map[string]truth.Fact) string {
	var k []string
	for x := range m {
		k = append(k, x)
	}
	sort.Strings(k)
	return strings.Join(k, ",")
}

func TestPackageRules(t *testing.T) {
	cases := []struct {
		name  string
		files map[string]string
		want  string
		src   string
		line  int
	}{
		{"npm private workspace package counts",
			map[string]string{"packages/ui/package.json": "{\n \"private\": true,\n \"name\": \"@acme/ui\"\n}"},
			"npm:@acme/ui", "npm.name", 3},
		{"npm workspace root is not a package",
			map[string]string{"package.json": `{"name":"root","workspaces":["packages/*"]}`}, "", "", 0},
		{"npm workspace root with object workspaces",
			map[string]string{"package.json": `{"name":"root","workspaces":{"packages":["a"]}}`}, "", "", 0},
		{"npm pnpm workspace root",
			map[string]string{"package.json": `{"name":"root"}`, "pnpm-workspace.yaml": "packages:\n - a\n"}, "", "", 0},
		{"npm under node_modules is skipped",
			map[string]string{"node_modules/x/package.json": `{"name":"x"}`, "app/node_modules/y/package.json": `{"name":"y"}`}, "", "", 0},
		{"npm without name",
			map[string]string{"package.json": `{"version":"1.0.0"}`}, "", "", 0},
		{"npm key is case canonical",
			map[string]string{"a/package.json": `{"name":"Foo-Bar"}`}, "npm:foo-bar", "npm.name", 1},
		{"npm under tests dir skipped",
			map[string]string{"tests/app/package.json": `{"name":"t1"}`, "a/fixtures/x/package.json": `{"name":"t2"}`, "examples/e/package.json": `{"name":"t3"}`, "templates/t/package.json": `{"name":"t4"}`}, "", "", 0},
		{"npm lerna root is not a package",
			map[string]string{"package.json": `{"name":"root"}`, "lerna.json": `{}`}, "", "", 0},
		{"poetry package-mode false",
			map[string]string{"pyproject.toml": "[tool.poetry]\nname = \"app\"\npackage-mode = false\n"}, "", "", 0},
		{"csproj conditional PackageId counts",
			map[string]string{"src/Lib.csproj": "<Project><PropertyGroup><PackageId Condition=\"'$(X)'==''\">Acme.Cond</PackageId></PropertyGroup></Project>"},
			"nuget:acme.cond", "csproj.package", 1},
		{"csproj conditional IsPackable true counts",
			map[string]string{"src/Lib.csproj": "<Project><PropertyGroup><IsPackable Condition=\"'$(Configuration)'=='Release'\">true</IsPackable><AssemblyName>Acme.C</AssemblyName></PropertyGroup></Project>"},
			"nuget:acme.c", "csproj.package", 1},
		{"csproj PackageId AssemblyName reference",
			map[string]string{"src/Lib.csproj": "<Project><PropertyGroup><AssemblyName>Acme.Ref</AssemblyName><PackageId>$(AssemblyName)</PackageId></PropertyGroup></Project>"},
			"nuget:acme.ref", "csproj.package", 1},
		{"csproj PackageId AssemblyName reference falls back to stem",
			map[string]string{"src/Acme.Stem.csproj": "<Project><PropertyGroup><PackageId>$(AssemblyName)</PackageId></PropertyGroup></Project>"},
			"nuget:acme.stem", "csproj.package", 1},
		{"csproj with PackageId",
			map[string]string{"src/Lib.csproj": "<Project>\n<PropertyGroup>\n<PackageId>Acme.Lib</PackageId>\n</PropertyGroup>\n</Project>"},
			"nuget:acme.lib", "csproj.package", 3},
		{"csproj IsPackable true uses AssemblyName",
			map[string]string{"src/Lib.csproj": "<Project><PropertyGroup><IsPackable>true</IsPackable><AssemblyName>Acme.Asm</AssemblyName></PropertyGroup></Project>"},
			"nuget:acme.asm", "csproj.package", 1},
		{"csproj GeneratePackageOnBuild uses file stem",
			map[string]string{"src/Acme.Core.csproj": "<Project><PropertyGroup><GeneratePackageOnBuild>true</GeneratePackageOnBuild></PropertyGroup></Project>"},
			"nuget:acme.core", "csproj.package", 1},
		{"csproj without packable flags is not a package",
			map[string]string{"src/Api.csproj": "<Project Sdk=\"Microsoft.NET.Sdk.Web\"><PropertyGroup><AssemblyName>Api</AssemblyName></PropertyGroup></Project>"}, "", "", 0},
		{"csproj IsPackable false wins over PackageId",
			map[string]string{"src/Api.csproj": "<Project><PropertyGroup><PackageId>X</PackageId><IsPackable>false</IsPackable></PropertyGroup></Project>"}, "", "", 0},
		{"csproj flag inside a comment does not count",
			map[string]string{"src/Api.csproj": "<Project><!-- <IsPackable>true</IsPackable> --></Project>"}, "", "", 0},
		{"csproj PackageId from a property expression is refused",
			map[string]string{"src/Api.csproj": "<Project><PackageId>$(Foo)</PackageId></Project>"}, "", "", 0},
		{"pyproject project name is normalised",
			map[string]string{"svc/pyproject.toml": "[build-system]\nrequires = []\n\n[project]\nversion = \"1\"\nname = \"My_Lib.Core\"\n"},
			"python:my-lib-core", "pyproject.name", 6},
		{"pyproject poetry name",
			map[string]string{"pyproject.toml": "[tool.poetry]\nname = \"poet\"\n"}, "python:poet", "pyproject.name", 2},
		{"pyproject without name",
			map[string]string{"pyproject.toml": "[project]\nversion = \"1\"\n[tool.ruff]\nline-length = 100\n"}, "", "", 0},
	}
	for _, c := range cases {
		got := pkgFacts(t, c.files)
		if keys(got) != c.want {
			t.Errorf("%s: keys %q, want %q", c.name, keys(got), c.want)
			continue
		}
		for _, f := range got {
			if len(f.Sources) != 1 || f.Sources[0] != c.src {
				t.Errorf("%s: sources %v want %s", c.name, f.Sources, c.src)
			}
			if f.Primary.Line != c.line {
				t.Errorf("%s: line %d want %d", c.name, f.Primary.Line, c.line)
			}
			if f.Attrs["name"] == "" || f.Attrs["ecosystem"] == "" || f.Attrs["manifest"] != f.Primary.File {
				t.Errorf("%s: attrs %v", c.name, f.Attrs)
			}
		}
	}
}

// The key must equal the one a deps fact for the same package gets.
func TestPackageKeyMatchesDepKey(t *testing.T) {
	got := pkgFacts(t, map[string]string{"p/pyproject.toml": "[project]\nname = \"Foo_Bar\"\n"})
	if _, ok := got[truth.CanonDep("python", "foo-bar")]; !ok {
		t.Fatalf("keys %s", keys(got))
	}
}
