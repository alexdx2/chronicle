package scan

import (
	"testing"

	"github.com/alexdx2/chronicle-core/graph"
)

func entriesOf(t *graph.ConfigHosts) map[string]string {
	m := map[string]string{}
	for _, e := range t.Entries {
		m[e.Key] = e.Host
	}
	return m
}

func TestGenericConfigFormats(t *testing.T) {
	cases := []struct {
		name, file, body string
		want             map[string]string
	}{
		{"spring yaml", "src/main/resources/application.yml",
			"services:\n  catalog:\n    url: http://catalog-api:3000\n    timeout: 5\nserver:\n  port: 8080\n",
			map[string]string{"services.catalog.url": "catalog-api"}},
		{"spring yaml placeholder", "application.yaml",
			"billing:\n  url: ${BILLING_URL:http://billing-api:8080}\n",
			map[string]string{"billing.url": "billing-api", "BILLING_URL": "billing-api"}},
		{"helm values", "helm/values.yaml",
			"env:\n  CATALOG_URL: http://catalog-api:3000\nimage:\n  repository: registry.example.com/app\n",
			map[string]string{"env.CATALOG_URL": "catalog-api"}},
		{"properties", "application.properties",
			"# comment\nbilling.base-url=http://billing-api:8080\nserver.port=8080\n",
			map[string]string{"billing.base-url": "billing-api"}},
		{"hocon", "src/main/resources/application.conf",
			"billing {\n  url = \"http://billing-api:8080\"\n}\n// url = \"http://commented:1\"\n",
			map[string]string{"billing.url": "billing-api"}},
		{"toml", "config.toml", "[services]\ncatalog = \"http://catalog-api:3000\"\n",
			map[string]string{"services.catalog": "catalog-api"}},
		{"json settings", "settings.json", `{"api":{"base":"http://gateway:8080"}}`,
			map[string]string{"api:base": "gateway"}},
		{"not a url", "config/app.yml", "name: hello\nhomepage: ftp://x\n", map[string]string{}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if !IsConfigFile(c.file) {
				t.Fatalf("%s is not a config file", c.file)
			}
			tab := &graph.ConfigHosts{}
			parseGenericConfig(tab, c.file, []byte(c.body))
			got := entriesOf(tab)
			if len(got) != len(c.want) {
				t.Fatalf("got %v want %v", got, c.want)
			}
			for k, v := range c.want {
				if got[k] != v {
					t.Fatalf("got %v want %v", got, c.want)
				}
			}
		})
	}
}

func TestGenericConfigFileNames(t *testing.T) {
	yes := []string{"application.yml", "src/main/resources/application-prod.properties", "bootstrap.yaml", "helm/values-prod.yaml", "config.toml", "config/endpoints.json", "settings.yaml", "app.properties", "application.conf"}
	no := []string{"package.json", "tsconfig.json", "pom.xml", "docker-compose.yml.bak", "src/app.py", "README.md", "package-lock.json", "tsconfig.build.json"}
	for _, p := range yes {
		if !isGenericConfigFile(p) {
			t.Errorf("%s should be a generic config file", p)
		}
	}
	for _, p := range no {
		if isGenericConfigFile(p) {
			t.Errorf("%s should not be a generic config file", p)
		}
	}
}

func TestEnvDefaultsAcrossLanguages(t *testing.T) {
	cases := []struct{ file, body, key, host string }{
		{"config.py", `CATALOG = os.getenv("CATALOG_API_URL", "http://catalog-api:3000")`, "CATALOG_API_URL", "catalog-api"},
		{"settings.py", "X = os.environ.get(\n  'ES_URL', 'http://elasticsearch:9200')", "ES_URL", "elasticsearch"},
		{"config.go", `u := getenv("INVENTORY_URL", "http://inventory-svc:8080")`, "INVENTORY_URL", "inventory-svc"},
		{"w.rb", `Faraday.new(url: ENV.fetch("CMS_SERVICE_URL", "http://cms-svc:8000"))`, "CMS_SERVICE_URL", "cms-svc"},
		{"c.php", `$u = env('CRM_URL', 'http://crm-svc:3000');`, "CRM_URL", "crm-svc"},
		{"main.rs", `let u = std::env::var("CATALOG_API_URL").unwrap_or_else(|_| "http://catalog-api:3000".into());`, "CATALOG_API_URL", "catalog-api"},
		{"app.config.js", `apiUrl: process.env.API_URL || 'http://gateway:8080',`, "API_URL", "gateway"},
		{"Client.cs", `var u = Environment.GetEnvironmentVariable("PRICING_URL") ?? "http://pricing-api:8080";`, "PRICING_URL", "pricing-api"},
		{"Svc.java", `@Value("${catalog.url:http://catalog-api:3000}") String url;`, "catalog.url", "catalog-api"},
	}
	for _, c := range cases {
		tab := &graph.ConfigHosts{}
		if !isEnvDefaultSource(c.file) {
			t.Errorf("%s is not an env-default source", c.file)
			continue
		}
		parseEnvDefaults(tab, c.file, []byte(c.body))
		got := entriesOf(tab)
		if got[c.key] != c.host {
			t.Errorf("%s: got %v want %s=%s", c.file, got, c.key, c.host)
		}
	}
	// no env read, no entry
	tab := &graph.ConfigHosts{}
	parseEnvDefaults(tab, "x.py", []byte(`DOCS = "http://example.com/docs"`))
	if len(tab.Entries) != 0 {
		t.Errorf("a bare URL literal became an entry: %v", entriesOf(tab))
	}
}

func TestTSConfigEnvFallbackAndJSFiles(t *testing.T) {
	for _, f := range []string{"app.config.js", "environment.ts", "config/config.mjs", "env.config.cjs"} {
		if !IsConfigFile(f) {
			t.Errorf("%s should be a config file", f)
		}
	}
	for _, f := range []string{"src/app.js", "jest.config.spec.js", "types.d.ts"} {
		if IsConfigFile(f) {
			t.Errorf("%s should not be a config file", f)
		}
	}
	tab := &graph.ConfigHosts{}
	parseTSConfig(tab, "app.config.js", []byte("export default {\n  extra: {\n    apiUrl: process.env.API_URL || 'http://gateway:8080',\n    other: process.env.X ?? \"http://beta:1\",\n    name: 'x',\n  },\n};\n"))
	got := entriesOf(tab)
	if got["extra.apiUrl"] != "gateway" || got["extra.other"] != "beta" || len(got) != 2 {
		t.Fatalf("%v", got)
	}
}
