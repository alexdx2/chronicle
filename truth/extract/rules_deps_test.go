package extract

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/alexdx2/chronicle-core/truth"
)

func depFacts(t *testing.T, files map[string]string) []truth.Fact {
	t.Helper()
	m := fstest.MapFS{}
	for vpath, disk := range files {
		data, err := os.ReadFile(filepath.Join("testdata", "deps", disk))
		if err != nil {
			t.Fatal(err)
		}
		m[vpath] = &fstest.MapFile{Data: data}
	}
	rep, err := Extract(m)
	if err != nil {
		t.Fatal(err)
	}
	var out []truth.Fact
	for _, f := range rep.Facts {
		if f.Class == truth.ClassDeps {
			out = append(out, f)
		}
	}
	return out
}

func TestDepRules(t *testing.T) {
	type w = wantFact
	cases := []struct {
		name  string
		files map[string]string
		want  []wantFact
	}{
		{"nuget lock direct and project, deduped across frameworks",
			map[string]string{"src/Orders/packages.lock.json": "nuget/packages.lock.json"},
			[]w{{"lock.nuget", "nuget:newtonsoft.json", false}, {"lock.nuget", "nuget:serilog", false}, {"lock.nuget", "project:orders.core", false}}},
		{"csproj beside packages.lock.json yields nothing from manifest.csproj",
			map[string]string{"src/Orders/packages.lock.json": "nuget/packages.lock.json", "src/Orders/Orders.csproj": "nuget/Orders.csproj"},
			[]w{{"lock.nuget", "nuget:newtonsoft.json", false}, {"lock.nuget", "nuget:serilog", false}, {"lock.nuget", "project:orders.core", false}}},
		{"lock under tests/ still counts",
			map[string]string{"tests/Orders.Tests/packages.lock.json": "nuget/packages.lock.json"},
			[]w{{"lock.nuget", "nuget:newtonsoft.json", false}, {"lock.nuget", "nuget:serilog", false}, {"lock.nuget", "project:orders.core", false}}},
		{"csproj alone with central versions in an ancestor props",
			map[string]string{"Directory.Packages.props": "manifests/Directory.Packages.props", "src/api/Api.csproj": "manifests/Api.csproj"},
			[]w{{"manifest.csproj", "nuget:stylecop.analyzers", false}, {"manifest.csproj", "nuget:serilog", false},
				{"manifest.csproj", "nuget:dapper", false}, {"manifest.csproj", "nuget:polly", false},
				{"manifest.csproj", "nuget:tuned", false}, {"manifest.csproj", "project:orders.core", false}}},
		{"package-lock v3",
			map[string]string{"package-lock.json": "npm/package-lock-v3.json"},
			[]w{{"lock.npm", "npm:express", false}, {"lock.npm", "npm:lodash", false}, {"lock.npm", "npm:jest", false},
				{"lock.npm", "npm:fsevents", false}, {"lock.npm", "npm:react", false}}},
		{"package-lock v1 selects direct names via package.json",
			map[string]string{"package-lock.json": "npm/package-lock-v1.json", "package.json": "npm/package-v1.json"},
			[]w{{"lock.npm", "npm:express", false}, {"lock.npm", "npm:mocha", false}}},
		{"package-lock v1 without package.json yields nothing",
			map[string]string{"package-lock.json": "npm/package-lock-v1.json"}, nil},
		{"pnpm v9 importers; package.json beside it yields nothing from manifest.npm",
			map[string]string{"pnpm-lock.yaml": "pnpm/pnpm-lock.yaml", "package.json": "manifests/package.json"},
			[]w{{"lock.pnpm", "npm:@nestjs/core", false}, {"lock.pnpm", "npm:rxjs", false},
				{"lock.pnpm", "npm:typescript", false}, {"lock.pnpm", "npm:react", false}}},
		{"pnpm v5 top-level maps",
			map[string]string{"pnpm-lock.yaml": "pnpm/pnpm-lock-v5.yaml"},
			[]w{{"lock.pnpm", "npm:left-pad", false}, {"lock.pnpm", "npm:mocha", false}}},
		{"yarn.lock with package.json",
			map[string]string{"yarn.lock": "yarn/yarn.lock", "package.json": "yarn/package.json"},
			[]w{{"lock.yarn", "npm:express", false}, {"lock.yarn", "npm:@babel/core", false}, {"lock.yarn", "npm:jest", false}}},
		{"yarn.lock without package.json",
			map[string]string{"yarn.lock": "yarn/yarn.lock"}, nil},
		{"poetry.lock; pyproject beside it yields nothing from manifest.pyproject",
			map[string]string{"poetry.lock": "poetry/poetry.lock", "pyproject.toml": "poetry/pyproject.toml"},
			[]w{{"lock.poetry", "python:requests", false}, {"lock.poetry", "python:flask-sqlalchemy", false}, {"lock.poetry", "python:pytest", false}}},
		{"poetry.lock without pyproject",
			map[string]string{"poetry.lock": "poetry/poetry.lock"}, nil},
		{"uv.lock root package dependencies",
			map[string]string{"uv.lock": "uv/uv.lock", "pyproject.toml": "poetry/pyproject.toml"},
			[]w{{"lock.uv", "python:httpx", false}, {"lock.uv", "python:pytest", false}}},
		{"package.json alone",
			map[string]string{"web/package.json": "manifests/package.json"},
			[]w{{"manifest.npm", "npm:express", false}, {"manifest.npm", "npm:jest", false},
				{"manifest.npm", "npm:react", false}, {"manifest.npm", "npm:fsevents", false}}},
		{"pyproject alone",
			map[string]string{"pyproject.toml": "manifests/pyproject.toml"},
			[]w{{"manifest.pyproject", "python:requests", false}, {"manifest.pyproject", "python:flask-sqlalchemy", false},
				{"manifest.pyproject", "python:tool", false}, {"manifest.pyproject", "python:pytest", false},
				{"manifest.pyproject", "python:click", false}, {"manifest.pyproject", "python:ruff", false}}},
		{"requirements.txt",
			map[string]string{"requirements.txt": "manifests/requirements.txt"},
			[]w{{"manifest.requirements", "python:requests", false}, {"manifest.requirements", "python:pkg", false},
				{"manifest.requirements", "python:flask-sqlalchemy", false}, {"manifest.requirements", "python:django", false}}},
		{"requirements/dev.txt",
			map[string]string{"requirements/dev.txt": "manifests/requirements.txt"},
			[]w{{"manifest.requirements", "python:requests", false}, {"manifest.requirements", "python:pkg", false},
				{"manifest.requirements", "python:flask-sqlalchemy", false}, {"manifest.requirements", "python:django", false}}},
		{"requirements beside a lockfile still counts",
			map[string]string{"requirements.txt": "manifests/requirements.txt", "poetry.lock": "poetry/poetry.lock"},
			[]w{{"manifest.requirements", "python:requests", false}, {"manifest.requirements", "python:pkg", false},
				{"manifest.requirements", "python:flask-sqlalchemy", false}, {"manifest.requirements", "python:django", false}}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var g, w []string
			for _, f := range depFacts(t, c.files) {
				g = append(g, strings.Join(f.Sources, ",")+" | "+f.Key)
				if f.Primary.File == "" || f.Primary.Line == 0 || strings.TrimSpace(f.Primary.Text) == "" {
					t.Errorf("%s: bad primary %+v", f.Key, f.Primary)
				}
				if f.Attrs["name"] == "" || f.Attrs["ecosystem"] == "" || f.Attrs["section"] == "" {
					t.Errorf("%s: bad attrs %v", f.Key, f.Attrs)
				}
				if len(f.Derived) != 0 {
					t.Errorf("%s: unexpected derived", f.Key)
				}
			}
			for _, x := range c.want {
				w = append(w, x.src+" | "+x.key)
			}
			sort.Strings(g)
			sort.Strings(w)
			if strings.Join(g, "\n") != strings.Join(w, "\n") {
				t.Fatalf("facts mismatch\n got:\n  %s\nwant:\n  %s", strings.Join(g, "\n  "), strings.Join(w, "\n  "))
			}
		})
	}
}

func TestDepAttrsAndLines(t *testing.T) {
	byKey := func(fs []truth.Fact, key string) truth.Fact {
		for _, f := range fs {
			if f.Key == key {
				return f
			}
		}
		t.Fatalf("no fact %s", key)
		return truth.Fact{}
	}
	check := func(f truth.Fact, name, eco, version, section string, line int) {
		t.Helper()
		a := f.Attrs
		if a["name"] != name || a["ecosystem"] != eco || a["version"] != version || a["section"] != section || f.Primary.Line != line {
			t.Errorf("%s: attrs %v line %d; want %s/%s/%s/%s line %d", f.Key, a, f.Primary.Line, name, eco, version, section, line)
		}
	}
	cs := depFacts(t, map[string]string{"Directory.Packages.props": "manifests/Directory.Packages.props", "src/api/Api.csproj": "manifests/Api.csproj"})
	check(byKey(cs, "nuget:serilog"), "Serilog", "nuget", "3.1.1", "dependencies", 3)
	check(byKey(cs, "nuget:dapper"), "Dapper", "nuget", "2.1.0", "dependencies", 4)
	check(byKey(cs, "nuget:polly"), "Polly", "nuget", "8.2.0", "dependencies", 6)
	check(byKey(cs, "nuget:tuned"), "Tuned", "nuget", "2.0.0", "dependencies", 10)
	check(byKey(cs, "project:orders.core"), "Orders.Core", "project", "", "project", 13)

	nl := depFacts(t, map[string]string{"packages.lock.json": "nuget/packages.lock.json"})
	check(byKey(nl, "nuget:newtonsoft.json"), "Newtonsoft.Json", "nuget", "[13.0.3, )", "direct", 28)

	n3 := depFacts(t, map[string]string{"package-lock.json": "npm/package-lock-v3.json"})
	check(byKey(n3, "npm:express"), "express", "npm", "^4.18.0", "dependencies", 7)
	check(byKey(n3, "npm:jest"), "jest", "npm", "^29.0.0", "devDependencies", 8)
	check(byKey(n3, "npm:fsevents"), "fsevents", "npm", "^2.3.0", "optional", 9)
	check(byKey(n3, "npm:react"), "react", "npm", ">=18", "peer", 10)

	pn := depFacts(t, map[string]string{"pnpm-lock.yaml": "pnpm/pnpm-lock.yaml"})
	check(byKey(pn, "npm:rxjs"), "rxjs", "npm", "^7.8.0", "dependencies", 10)
	if byKey(pn, "npm:react").Attrs["importer"] != "packages/web" || byKey(pn, "npm:rxjs").Attrs["importer"] != "" {
		t.Errorf("importer attrs wrong")
	}

	pt := depFacts(t, map[string]string{"poetry.lock": "poetry/poetry.lock", "pyproject.toml": "poetry/pyproject.toml"})
	check(byKey(pt, "python:requests"), "requests", "python", "2.31.0", "dependencies", 2)
	check(byKey(pt, "python:pytest"), "pytest", "python", "7.4.0", "devDependencies", 18)

	rq := depFacts(t, map[string]string{"requirements.txt": "manifests/requirements.txt"})
	check(byKey(rq, "python:requests"), "requests", "python", "==2.31.0", "requirements.txt", 5)
	check(byKey(rq, "python:pkg"), "pkg", "python", ">=1", "requirements.txt", 6)
	check(byKey(rq, "python:django"), "django", "python", ">=4,<5", "requirements.txt", 13)
}
