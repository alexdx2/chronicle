package deps

import "testing"

func TestPublishes_OnlyApplicationsAreMarked(t *testing.T) {
	cases := []struct {
		file, body string
		want       bool
	}{
		{"a/package.json", `{"name":"a","scripts":{"start":"node x"}}`, true},
		{"a/package.json", `{"name":"a","scripts":{"serve":"x"}}`, true},
		{"a/package.json", `{"name":"a","bin":{"a":"cli.js"}}`, true},
		{"a/package.json", `{"name":"a","dependencies":{"@nestjs/core":"^10"}}`, true},
		{"a/package.json", `{"name":"a","dependencies":{"fastify":"^4"}}`, true},
		{"a/package.json", `{"name":"@x/shared","main":"i.js","scripts":{"build":"tsc"},"dependencies":{"zod":"^3"}}`, false},
		{"S/S.csproj", `<Project Sdk="Microsoft.NET.Sdk.Web"><PropertyGroup></PropertyGroup></Project>`, true},
		{"S/S.csproj", "<Project Sdk=\"Microsoft.NET.Sdk\">\n<PropertyGroup><OutputType>Exe</OutputType></PropertyGroup></Project>", true},
		{"S/S.csproj", `<Project Sdk="Microsoft.NET.Sdk"><PropertyGroup><TargetFramework>net8.0</TargetFramework></PropertyGroup></Project>`, false},
		{"p/pyproject.toml", "[project]\nname = \"svc\"\n[project.scripts]\nsvc = \"svc:main\"\n", true},
		{"p/pyproject.toml", "[project]\nname = \"lib\"\n", false},
	}
	for _, c := range cases {
		var r Report
		var err error
		switch {
		case base(c.file) == "package.json":
			r, err = npm{}.parse("", c.file, []byte(c.body))
		case base(c.file) == "pyproject.toml":
			r, err = python{}.parse("", c.file, []byte(c.body))
		default:
			r, err = nuget{}.parse("", c.file, []byte(c.body))
		}
		if err != nil || len(r.Publishes) != 1 {
			t.Fatalf("%s: %v %+v", c.file, err, r.Publishes)
		}
		if r.Publishes[0].App != c.want {
			t.Errorf("%s %q: App = %v, want %v", c.file, c.body, r.Publishes[0].App, c.want)
		}
	}
}
