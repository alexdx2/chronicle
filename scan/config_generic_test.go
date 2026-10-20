package scan

import (
	"os"
	"path/filepath"
	"strings"
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

func TestKeyLiteralUsesAcrossLanguages(t *testing.T) {
	root := t.TempDir()
	write := func(rel, body string) {
		full := filepath.Join(root, rel)
		os.MkdirAll(filepath.Dir(full), 0o755)
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("src/main/resources/application.conf", "billing {\n  url = \"http://billing-api:8080\"\n}\n")
	write("src/Application.kt", "fun main() {\n    val billing = BillingClient(config.property(\"billing.url\").getString())\n    val plain = Logger(config.property(\"billing.url\").getString())\n}\n")
	write("app/catalog.py", "import os\nclient = CatalogClient(os.getenv(\"CATALOG_API_URL\"))\n# other = CatalogClient(os.getenv(\"CATALOG_API_URL\"))\n")
	write("app/settings.py", "CATALOG = os.getenv(\"CATALOG_API_URL\", \"http://catalog-api:3000\")\nx = CatalogClient(os.getenv(\"CATALOG_API_URL\"))\n")
	write("src/lib.rs", "let c = CatalogClient::new(std::env::var(\"CATALOG_API_URL\").unwrap());\n")
	files := []string{"src/main/resources/application.conf", "src/Application.kt", "app/catalog.py", "app/settings.py", "src/lib.rs"}
	tab, _ := ReadConfigHosts(root, files)
	got := map[string]int{}
	for _, u := range tab.Uses {
		got[u.File+":"+u.Callee]++
	}
	want := map[string]int{"src/Application.kt:BillingClient": 1, "app/catalog.py:CatalogClient": 1, "src/lib.rs:CatalogClient": 1}
	for k, n := range want {
		if got[k] != n {
			t.Errorf("uses = %v, want %s", got, k)
		}
	}
	if got["src/Application.kt:Logger"] != 0 {
		t.Errorf("a non-client callee became a use: %v", got)
	}
	if got["app/settings.py:CatalogClient"] != 0 {
		t.Errorf("the file that defines the default is never the use: %v", got)
	}
}

func TestEnvDefaultNamesTheAssignedConstant(t *testing.T) {
	tab := &graph.ConfigHosts{}
	parseEnvDefaults(tab, "dags/etl_config.py", []byte("from airflow.models import Variable\nCATALOG_API_URL = Variable.get(\"catalog_api_url\", default_var=\"http://catalog-api:3000\")\nPAGE_SIZE = int(Variable.get(\"catalog_page_size\", default_var=\"500\"))\n"))
	got := entriesOf(tab)
	if got["CATALOG_API_URL"] != "catalog-api" || got["catalog_api_url"] != "catalog-api" || len(got) != 2 {
		t.Fatalf("%v", got)
	}
	tab = &graph.ConfigHosts{}
	parseEnvDefaults(tab, "config.ts", []byte("export const catalogUrl = process.env.CATALOG_URL || 'http://catalog-api:3000';\nconst timeout = process.env.TIMEOUT_MS || 'http://not-an-address-name:1';\n"))
	got = entriesOf(tab)
	if got["catalogUrl"] != "catalog-api" || got["CATALOG_URL"] != "catalog-api" {
		t.Fatalf("%v", got)
	}
	if _, bad := got["timeout"]; bad {
		t.Fatalf("a name that does not say address became an alias: %v", got)
	}
}

func TestConfigStructFieldFlowsIntoAClientConstructor(t *testing.T) {
	root := t.TempDir()
	write := func(rel, body string) {
		full := filepath.Join(root, rel)
		os.MkdirAll(filepath.Dir(full), 0o755)
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("src/config.rs", "pub fn load() -> Config {\n    Config {\n        catalog_url: std::env::var(\"CATALOG_API_URL\").unwrap_or_else(|_| \"http://catalog-api:3000\".into()),\n    }\n}\n")
	write("src/main.rs", "fn main() {\n    let cfg = config::load();\n    let catalog = CatalogClient::new(cfg.catalog_url.clone());\n    let log = Logger::new(cfg.catalog_url.clone());\n}\n")
	tab, _ := ReadConfigHosts(root, []string{"src/config.rs", "src/main.rs"})
	got := entriesOf(tab)
	if got["catalog_url"] != "catalog-api" || got["CATALOG_API_URL"] != "catalog-api" {
		t.Fatalf("entries %v", got)
	}
	var uses []graph.ConfigUse
	for _, u := range tab.Uses {
		uses = append(uses, u)
	}
	if len(uses) != 1 || uses[0].File != "src/main.rs" || uses[0].Callee != "CatalogClient" || uses[0].Line != 3 {
		t.Fatalf("uses %+v", uses)
	}
	if _, _, e, ok := tab.ResolveSymbol("src/main.rs", uses[0].Symbol); !ok || e.Host != "catalog-api" {
		t.Fatalf("symbol does not resolve: %+v", e)
	}
}

func TestWebConfigAppSettingsAndSQSQueue(t *testing.T) {
	if !IsConfigFile("Web.config") || !IsConfigFile("src/app.config") || IsConfigFile("nuget.config") {
		t.Fatal("web.config / app.config are config files, nuget.config is not")
	}
	tab := &graph.ConfigHosts{}
	parseGenericConfig(tab, "Web.config", []byte("<configuration><appSettings>\n  <add key=\"BillingApiUrl\" value=\"http://billing-api:8080/\" />\n  <add key=\"OrdersQueueUrl\" value=\"https://sqs.eu-central-1.amazonaws.com/123456789012/orders-inbound\" />\n  <!-- <add key=\"Old\" value=\"http://dead:1\" /> -->\n</appSettings></configuration>"))
	got := entriesOf(tab)
	if got["BillingApiUrl"] != "billing-api" || got["OrdersQueueUrl"] != "sqs.eu-central-1.amazonaws.com" || len(got) != 2 {
		t.Fatalf("%v", got)
	}
	var q string
	for _, e := range tab.Entries {
		if e.Key == "OrdersQueueUrl" {
			q = e.Queue
		}
	}
	if q != "orders-inbound" {
		t.Fatalf("queue = %q", q)
	}
	if p := tab.PromptSection("Orders/OrderQueue.cs"); !strings.Contains(p, `OrdersQueueUrl -> SQS queue "orders-inbound"`) || !strings.Contains(p, "BillingApiUrl -> billing-api") {
		t.Fatalf("prompt:\n%s", p)
	}
}
