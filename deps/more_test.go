package deps

import (
	"os"
	"path/filepath"
	"testing"
)

func parseOne(t *testing.T, p parser, rel, body string) Report {
	t.Helper()
	if !p.matches(rel) {
		t.Fatalf("%s does not match %s", rel, rel)
	}
	r, err := p.parse("", rel, []byte(body))
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func depNames(r Report) map[string]string {
	m := map[string]string{}
	for _, d := range r.Depends {
		m[d.Name] = d.Section
	}
	return m
}

func TestGemfile(t *testing.T) {
	r := parseOne(t, ruby{}, "Gemfile", "source 'https://rubygems.org'\ngem 'rails', '~> 7.1'\n# gem 'old'\ngroup :test do\n  gem \"rspec-rails\"\nend\ngem \"sidekiq\" # jobs\n")
	got := depNames(r)
	if len(got) != 3 || got["rails"] != "dependencies" || got["rspec-rails"] != "dev" || got["sidekiq"] != "dependencies" {
		t.Fatalf("%v", got)
	}
	if r.Depends[0].Line != 2 || r.Depends[0].Version != "~> 7.1" {
		t.Fatalf("%+v", r.Depends[0])
	}
}

func TestComposerJSON(t *testing.T) {
	r := parseOne(t, php{}, "composer.json", `{"name":"corp/lib","require":{"php":"^8.3","ext-rdkafka":"*","guzzlehttp/guzzle":"^7.8"},"require-dev":{"phpunit/phpunit":"^11"}}`)
	got := depNames(r)
	if len(got) != 2 || got["guzzlehttp/guzzle"] != "dependencies" || got["phpunit/phpunit"] != "dev" {
		t.Fatalf("%v", got)
	}
	if len(r.Publishes) != 1 || r.Publishes[0].Name != "corp/lib" {
		t.Fatalf("publishes %v", r.Publishes)
	}
	app := parseOne(t, php{}, "composer.json", `{"name":"corp/app","type":"project","require":{"laravel/framework":"^11"}}`)
	if len(app.Publishes) != 1 || !app.Publishes[0].App {
		t.Fatalf("a project is an application: %v", app.Publishes)
	}
	if r.Publishes[0].App {
		t.Fatalf("a library is not an application")
	}
}

func TestCargoToml(t *testing.T) {
	r := parseOne(t, rust{}, "Cargo.toml", "[package]\nname = \"geo-rust\"\nversion = \"0.1.0\"\n\n[dependencies]\naxum = \"0.7\"\nreqwest = { version = \"0.12\", features = [\"json\"] }\nlocal = { path = \"../local\" }\n\n[dev-dependencies]\ntower = \"0.4\"\n")
	got := depNames(r)
	if len(got) != 3 || got["axum"] != "dependencies" || got["reqwest"] != "dependencies" || got["tower"] != "dev" {
		t.Fatalf("%v", got)
	}
	if len(r.Publishes) != 1 || r.Publishes[0].Name != "geo-rust" || r.Publishes[0].Line != 2 {
		t.Fatalf("publishes %+v", r.Publishes)
	}
	priv := parseOne(t, rust{}, "Cargo.toml", "[package]\nname = \"x\"\npublish = false\n")
	if len(priv.Publishes) != 0 {
		t.Fatal("publish = false still published")
	}
}

func TestPackagesConfig(t *testing.T) {
	r := parseOne(t, nugetConfig{}, "src/packages.config", "<packages>\n  <package id=\"Newtonsoft.Json\" version=\"13.0.3\" targetFramework=\"net48\" />\n  <package id=\"log4net\" version=\"2.0.15\" />\n</packages>\n")
	got := depNames(r)
	if len(got) != 2 || got["newtonsoft.json"] == "" || got["log4net"] == "" {
		t.Fatalf("%v", got)
	}
	if r.Depends[0].Line != 2 {
		t.Fatalf("%+v", r.Depends[0])
	}
}

func TestAppsByFramework(t *testing.T) {
	mk := func(eco, pub string, deps ...string) Report {
		r := Report{Publishes: []Package{{Name: pub, Ecosystem: eco, File: "m"}}}
		for _, d := range deps {
			r.Depends = append(r.Depends, Dependency{Name: d, Ecosystem: eco, File: "m", Section: "dependencies"})
		}
		markAppsByFramework(&r)
		return r
	}
	yes := []Report{
		mk("python", "svc", "fastapi", "pydantic"),
		mk("maven", "com.corp:inv", "org.springframework.boot:spring-boot-starter-web"),
		mk("go", "github.com/corp/ship", "github.com/go-chi/chi/v5"),
		mk("cargo", "geo", "axum"),
		mk("gem", "crm", "rails"),
	}
	for i, r := range yes {
		if !r.Publishes[0].App {
			t.Errorf("case %d: %s is not an app", i, r.Publishes[0].Name)
		}
	}
	lib := mk("python", "lib", "requests", "pydantic")
	if lib.Publishes[0].App {
		t.Error("a library with ordinary dependencies became an app")
	}
	dev := mk("python", "lib")
	dev.Depends = append(dev.Depends, Dependency{Name: "flask", Ecosystem: "python", File: "m", Section: "dev"})
	markAppsByFramework(&dev)
	if dev.Publishes[0].App {
		t.Error("a dev-only framework made an app")
	}
}

func TestLegacyWebCsprojIsApp(t *testing.T) {
	body := `<Project ToolsVersion="15.0"><PropertyGroup><ProjectTypeGuids>{349C5851-65DF-11DA-9384-00065B846F21};{FAE04EC0-301F-11D3-BF4B-00C04F79EFBC}</ProjectTypeGuids><AssemblyName>LegacyMonolith</AssemblyName></PropertyGroup></Project>`
	r := parseOne(t, nuget{}, "LegacyMonolith.csproj", body)
	if len(r.Publishes) != 1 || !r.Publishes[0].App {
		t.Fatalf("%+v", r.Publishes)
	}
	lib := parseOne(t, nuget{}, "Lib.csproj", `<Project><PropertyGroup><AssemblyName>Lib</AssemblyName></PropertyGroup></Project>`)
	if lib.Publishes[0].App {
		t.Fatal("a plain library is an app")
	}
}

func TestAppsByDockerfile(t *testing.T) {
	root := t.TempDir()
	write := func(rel, body string) {
		full := filepath.Join(root, filepath.FromSlash(rel))
		os.MkdirAll(filepath.Dir(full), 0o755)
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("app/pyproject.toml", "[project]\nname = \"fraud\"\nversion = \"1\"\ndependencies = [\"requests\"]\n")
	write("app/Dockerfile", "FROM python:3.12\nCMD [\"python\", \"-m\", \"fraud\"]\n")
	write("lib/pyproject.toml", "[project]\nname = \"toolkit\"\nversion = \"1\"\n")
	write("lib/Dockerfile", "FROM python:3.12\nRUN pip install .\n")
	rep, err := Scan(root, []string{"app/pyproject.toml", "lib/pyproject.toml"})
	if err != nil {
		t.Fatal(err)
	}
	apps := map[string]bool{}
	for _, p := range rep.Publishes {
		apps[p.Name] = p.App
	}
	if !apps["fraud"] || apps["toolkit"] {
		t.Fatalf("apps = %v", apps)
	}
}
