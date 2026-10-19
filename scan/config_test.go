package scan

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/alexdx2/chronicle-core/graph"
	"github.com/alexdx2/chronicle-core/llm"
)

const (
	salesAppsettings = `{
  "Logging": { "LogLevel": { "Default": "Information" } },
  "Services": { "PricingApi": "http://pricing-api:8080", "NotifyUrl": "http://notify-svc:3000" },
  "Kafka": { "BootstrapServers": "kafka:9092" }
}`
	salesProgram = `var builder = WebApplication.CreateBuilder(args);
// builder.Services.AddHttpClient<GhostClient>(c => c.BaseAddress = new Uri(builder.Configuration["Services:Ghost"]!));
builder.Services.AddHttpClient<PricingClient>(c =>
{
    c.BaseAddress = new Uri(builder.Configuration["Services:PricingApi"]!);
});
builder.Services.AddRefitClient<INotifyClient>()
    .ConfigureHttpClient(c => c.BaseAddress = new Uri(builder.Configuration["Services:NotifyUrl"]!));
builder.Services.AddHttpClient<IRatesClient, RatesClient>(c => c.BaseAddress = new Uri("https://rates.acme-partners.example/v1"));
`
	webEnvironment = `export const environment = {
  production: false,
  apiUrl: 'http://sales-api:8080',
  auth: { issuer: 'https://user:secret@idp.example/realm?token=abc#frag' },
};
`
)

func TestReadConfigHosts_FleetShapes(t *testing.T) {
	root, _ := newCodeRepoT(t, map[string]string{
		"sales-api/appsettings.json":                 salesAppsettings,
		"sales-api/Program.cs":                       salesProgram,
		"pricing-api/appsettings.Development.json":   `{ "Pricing": { "ExchangeRatesUrl": "https://rates.acme-partners.example/v1", "SelfUrl": "http://localhost:8080" } }`,
		"portal-web/src/environments/environment.ts": webEnvironment,
		"portal-bff/.env":                            "SALES_API_URL=http://sales-api:8080\nNOTIFY_SVC_URL=\"http://notify-svc:3000\"\nPORT=3000\n# OLD=http://old:1\n",
		"deploy/docker-compose.yml":                  "services:\n  bff:\n    environment:\n      - PRICING_API_URL=http://pricing-api:8080\n      - FOO=bar\n  web:\n    environment:\n      API_URL: ${API_URL:-http://sales-api:8080}\n",
	})
	files := []string{"sales-api/appsettings.json", "sales-api/Program.cs", "pricing-api/appsettings.Development.json",
		"portal-web/src/environments/environment.ts", "portal-bff/.env", "deploy/docker-compose.yml"}
	table, parsed := ReadConfigHosts(root, files)

	hosts := map[string]string{}
	for _, e := range table.Entries {
		hosts[e.File+"#"+e.Key] = e.Host
		if strings.ContainsAny(e.URL, "@?#") {
			t.Errorf("%s: URL %q keeps userinfo/query/fragment", e.Key, e.URL)
		}
	}
	for k, want := range map[string]string{
		"sales-api/appsettings.json#Services:PricingApi":                    "pricing-api",
		"sales-api/appsettings.json#Services:NotifyUrl":                     "notify-svc",
		"pricing-api/appsettings.Development.json#Pricing:ExchangeRatesUrl": "rates.acme-partners.example",
		"portal-web/src/environments/environment.ts#apiUrl":                 "sales-api",
		"portal-web/src/environments/environment.ts#auth.issuer":            "idp.example",
		"portal-bff/.env#SALES_API_URL":                                     "sales-api",
		"portal-bff/.env#NOTIFY_SVC_URL":                                    "notify-svc",
		"deploy/docker-compose.yml#PRICING_API_URL":                         "pricing-api",
		"deploy/docker-compose.yml#API_URL":                                 "sales-api",
	} {
		if hosts[k] != want {
			t.Errorf("%s = %q, want %q", k, hosts[k], want)
		}
	}
	if _, ok := hosts["portal-bff/.env#OLD"]; ok {
		t.Error("a commented .env line was read")
	}
	if len(parsed) != 5 {
		t.Errorf("parsed config files = %v, want the 5 config files (not Program.cs)", parsed)
	}

	// client registrations: the commented-out one is not one
	cl := map[string]string{}
	for _, c := range table.Clients {
		cl[c.Class] = c.Key + c.URL
	}
	want := map[string]string{
		"PricingClient": "Services:PricingApi", "INotifyClient": "Services:NotifyUrl",
		"IRatesClient": "https://rates.acme-partners.example/v1", "RatesClient": "https://rates.acme-partners.example/v1",
	}
	for k, v := range want {
		if cl[k] != v {
			t.Errorf("client %s = %q, want %q", k, cl[k], v)
		}
	}
	if _, ok := cl["GhostClient"]; ok {
		t.Error("a commented-out registration was read")
	}

	// the table resolves what the code names
	for _, c := range []struct{ file, base, host string }{
		{"sales-api/Services/X.cs", `{_config["Services:PricingApi"]}`, "pricing-api"},
		{"portal-web/src/app/o.ts", "${environment.apiUrl}", "sales-api"},
		{"portal-bff/src/routes/n.ts", "process.env.NOTIFY_SVC_URL", "notify-svc"},
		{"portal-web/src/app/o.ts", "environment.auth.issuer", "idp.example"},
	} {
		_, _, e, ok := table.ResolveSymbol(c.file, c.base)
		if !ok || e.Host != c.host {
			t.Errorf("%s %s -> %q ok=%v, want %s", c.file, c.base, e.Host, ok, c.host)
		}
	}
	if e, ok := table.ClientBase("sales-api/Services/INotifyClient.cs", "INotifyClient"); !ok || e.Host != "notify-svc" {
		t.Errorf("INotifyClient base = %+v ok=%v", e, ok)
	}

	// the prompt block names keys and hosts, nothing else
	sec := table.PromptSection("portal-web/src/app/o.ts")
	for _, must := range []string{"apiUrl -> sales-api", "Services:PricingApi -> pricing-api", "PricingClient"} {
		if !strings.Contains(sec, must) {
			t.Errorf("prompt section lacks %q:\n%s", must, sec)
		}
	}
	for _, leak := range []string{"secret", "token", "frag", "user:", "/v1", ":8080", "http://"} {
		if strings.Contains(sec, leak) {
			t.Errorf("prompt section leaks %q:\n%s", leak, sec)
		}
	}
}

// capturingBackend answers per file and remembers every prompt.
type capturingBackend struct {
	perFileBackend
	mu      sync.Mutex
	systems map[string]string
}

func (b *capturingBackend) Complete(ctx context.Context, req llm.Request) (llm.Response, error) {
	first, _, _ := strings.Cut(req.User, "\n")
	b.mu.Lock()
	if b.systems == nil {
		b.systems = map[string]string{}
	}
	b.systems[strings.TrimPrefix(first, "File: ")] = req.System
	b.mu.Unlock()
	return b.perFileBackend.Complete(ctx, req)
}

func TestBuildSnapshot_ConfigHostsReachPromptResolverAndCoverage(t *testing.T) {
	repo, head := newCodeRepoT(t, map[string]string{
		"src/environments/environment.ts": webEnvironment,
		"src/app/orders.service.ts":       "const u = `${environment.apiUrl}/orders`;\n",
		"src/app/health.service.ts":       "const h = `${environment.missing}/health`;\n",
		"package.json":                    `{"name":"portal-web","dependencies":{}}`,
	})
	call := func(target string) string {
		return `{"kind":"http_call","method":"GET","target":"` + strings.ReplaceAll(target, `"`, `\"`) + `","from_type":"provider"}`
	}
	b := &capturingBackend{perFileBackend: perFileBackend{facts: map[string]string{
		"src/app/orders.service.ts": call("`${environment.apiUrl}/orders`"),
		"src/app/health.service.ts": call("`${environment.missing}/health`"),
	}}}
	res, err := BuildSnapshot(context.Background(), buildIn(repo, head, t.TempDir(), b))
	if err != nil {
		t.Fatal(err)
	}
	snap := decodeT(t, res.Files)

	var ext []string
	for _, n := range snap.Nodes {
		if strings.Contains(n.ID, "external_system") {
			ext = append(ext, n.ID)
		}
	}
	if len(ext) != 1 || !strings.HasSuffix(ext[0], ":sales-api") {
		t.Errorf("external systems = %v, want only sales-api (no environment-* node)", ext)
	}
	if blobOf(snap, "src/environments/environment.ts") == "" {
		t.Errorf("the config file has no coverage row: %+v", snap.Coverage)
	}
	sys := b.systems["src/app/orders.service.ts"]
	if !strings.Contains(sys, "apiUrl -> sales-api") {
		t.Errorf("the prompt lacks the known config values:\n%s", sys)
	}
	for _, leak := range []string{"secret", "token=", "user:"} {
		if strings.Contains(sys, leak) {
			t.Errorf("the prompt leaks %q", leak)
		}
	}
	_ = graph.ConfigHosts{}
}

func TestReadConfigHosts_ConfigValuePassedToConstructor(t *testing.T) {
	root, _ := newCodeRepoT(t, map[string]string{
		"portal-bff/.env": "NOTIFY_SVC_URL=http://notify-svc:3000\n",
		"portal-bff/src/routes/notify.ts": "import { NotifyClient } from '@acme/notify-client';\n\n" +
			"const client = new NotifyClient(process.env.NOTIFY_SVC_URL ?? '');\n" +
			"const u = new URL(process.env.NOTIFY_SVC_URL!);\n" +
			"// const dead = new DeadClient(process.env.DEAD_URL);\n" +
			"const other = new Thing('x', 5);\n",
	})
	table, _ := ReadConfigHosts(root, []string{"portal-bff/.env", "portal-bff/src/routes/notify.ts"})
	if len(table.Uses) != 1 {
		t.Fatalf("uses = %+v, want exactly the NotifyClient one", table.Uses)
	}
	u := table.Uses[0]
	if u.Callee != "NotifyClient" || u.Line != 3 || u.Symbol != "process.env.NOTIFY_SVC_URL" {
		t.Errorf("use = %+v", u)
	}
}
