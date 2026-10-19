package graph

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alexdx2/chronicle-core/store"
)

// writeTree puts files under a temp project root and makes it the cwd, so
// evidence paths (repo-relative) resolve the way a scan's do.
func writeTree(t *testing.T, files map[string]string) {
	t.Helper()
	root := t.TempDir()
	for p, body := range files {
		full := filepath.Join(root, p)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	t.Chdir(root)
}

// callServiceEvidence re-verifies the files (the step a scan runs) and returns
// the active CALLS_SERVICE edge to host with its evidence rows.
func callServiceEvidence(t *testing.T, g *Graph, s *store.Store, host string, files ...string) []store.EvidenceRow {
	t.Helper()
	for _, f := range files {
		if _, err := g.VerifyFileEvidence(f, 1, "demo"); err != nil {
			t.Fatalf("VerifyFileEvidence %s: %v", f, err)
		}
	}
	edges, _ := s.ListEdges(store.EdgeFilter{EdgeType: "CALLS_SERVICE"})
	for _, e := range edges {
		if !strings.HasSuffix(e.ToNodeKey, ":"+host) {
			continue
		}
		if !e.Active {
			t.Fatalf("CALLS_SERVICE to %s is inactive", host)
		}
		ev, _ := s.ListEvidenceByEdge(e.EdgeID)
		return ev
	}
	t.Fatalf("no CALLS_SERVICE edge to %s", host)
	return nil
}

func wantTwoValid(t *testing.T, ev []store.EvidenceRow, codeFile, codeSub, cfgFile string) {
	t.Helper()
	if len(ev) != 2 {
		t.Fatalf("evidence rows = %d, want 2: %+v", len(ev), ev)
	}
	var gotCode, gotCfg bool
	for _, e := range ev {
		if e.VerificationStatus == "rejected" || e.EvidenceStatus != "valid" {
			t.Errorf("row %s %s: status %s/%s (%s)", e.FilePath, e.Assertion, e.EvidenceStatus, e.VerificationStatus, e.VerificationReason)
		}
		if e.AssertionKind != "text_contains" {
			t.Errorf("assertion kind %s", e.AssertionKind)
		}
		switch e.FilePath {
		case codeFile:
			gotCode = strings.Contains(e.Assertion, `"substring":"`+codeSub+`"`)
		case cfgFile:
			gotCfg = e.LineStart > 0
		}
	}
	if !gotCode {
		t.Errorf("no code row on %s asserting %q: %+v", codeFile, codeSub, ev)
	}
	if !gotCfg {
		t.Errorf("no config row on %s with a line: %+v", cfgFile, ev)
	}
}

func TestConfigHost_EnvVarFromDotenv(t *testing.T) {
	writeTree(t, map[string]string{
		"portal-bff/.env":                 "PORT=3000\nPRICING_API_URL=http://pricing-api:8080\n",
		"portal-bff/src/routes/prices.ts": "export const prices = () =>\n  fetch(`${process.env.PRICING_API_URL}/prices/all`);\n",
	})
	c := &ConfigHosts{}
	c.Add("PRICING_API_URL", "http://pricing-api:8080", "portal-bff/.env")
	code := "portal-bff/src/routes/prices.ts"
	g, s, _ := resolveFiles(t, c, srcFacts{code, []Fact{
		{Kind: "http_call", From: "prices", FromType: "provider", Target: "`${process.env.PRICING_API_URL}/prices/all`", Method: "GET"},
	}})
	ev := callServiceEvidence(t, g, s, "pricing-api", code, "portal-bff/.env")
	wantTwoValid(t, ev, code, "PRICING_API_URL", "portal-bff/.env")
}

func TestConfigHost_AppsettingsKeyTypedClient(t *testing.T) {
	writeTree(t, map[string]string{
		"sales-api/appsettings.json":             "{\n  \"Services\": {\n    \"PricingApi\": \"http://pricing-api:8080\"\n  }\n}\n",
		"sales-api/Program.cs":                   "builder.Services.AddHttpClient<PricingClient>(c => c.BaseAddress = new Uri(builder.Configuration[\"Services:PricingApi\"]!));\n",
		"sales-api/Services/PricingClient.cs":    "public class PricingClient {\n  public Task<Price> Get() => _http.GetFromJsonAsync<Price>(\"/prices/all\");\n}\n",
		"sales-api/Services/PricingKeyClient.cs": "public class K {\n  var u = _config[\"Services:PricingApi\"] + \"/prices/x\";\n}\n",
	})
	c := &ConfigHosts{}
	c.Add("Services:PricingApi", "http://pricing-api:8080", "sales-api/appsettings.json")
	c.AddClient(ClientBinding{Class: "PricingClient", Key: "Services:PricingApi", File: "sales-api/Program.cs"})
	code := "sales-api/Services/PricingClient.cs"
	g, s, _ := resolveFiles(t, c, srcFacts{code, []Fact{
		{Kind: "http_call", From: "PricingClient", FromType: "provider", Target: "/prices/all", Method: "GET"},
	}})
	ev := callServiceEvidence(t, g, s, "pricing-api", code, "sales-api/appsettings.json")
	wantTwoValid(t, ev, code, "/prices/all", "sales-api/appsettings.json")

	// and the same host through the key written on the line
	code2 := "sales-api/Services/PricingKeyClient.cs"
	g2, s2, _ := resolveFiles(t, c, srcFacts{code2, []Fact{
		{Kind: "http_call", From: "K", FromType: "provider", Target: `_config["Services:PricingApi"] + "/prices/x"`, Method: "GET"},
	}})
	ev2 := callServiceEvidence(t, g2, s2, "pricing-api", code2, "sales-api/appsettings.json")
	wantTwoValid(t, ev2, code2, "Services:PricingApi", "sales-api/appsettings.json")
}

func TestConfigHost_EnvironmentApiUrl(t *testing.T) {
	writeTree(t, map[string]string{
		"portal-web/src/environments/environment.ts": "export const environment = {\n  production: false,\n  apiUrl: 'http://sales-api:8080',\n};\n",
		"portal-web/src/app/orders.service.ts":       "export class OrdersService {\n  list() { return this.http.get(`${environment.apiUrl}/orders`); }\n}\n",
	})
	c := &ConfigHosts{}
	c.Add("apiUrl", "http://sales-api:8080", "portal-web/src/environments/environment.ts")
	code := "portal-web/src/app/orders.service.ts"
	g, s, _ := resolveFiles(t, c, srcFacts{code, []Fact{
		{Kind: "http_call", From: "OrdersService", FromType: "provider", Target: "`${environment.apiUrl}/orders`", Method: "GET"},
	}})
	ev := callServiceEvidence(t, g, s, "sales-api", code, "portal-web/src/environments/environment.ts")
	wantTwoValid(t, ev, code, "environment.apiUrl", "portal-web/src/environments/environment.ts")
}

func TestConfigHost_ConstructorUse(t *testing.T) {
	writeTree(t, map[string]string{
		"portal-bff/.env":                 "NOTIFY_SVC_URL=http://notify-svc:3000\n",
		"portal-bff/src/routes/notify.ts": "import { NotifyClient } from '@acme/notify-client';\nconst client = new NotifyClient(process.env.NOTIFY_SVC_URL ?? '');\n",
	})
	c := &ConfigHosts{}
	c.Add("NOTIFY_SVC_URL", "http://notify-svc:3000", "portal-bff/.env")
	c.AddUse(ConfigUse{File: "portal-bff/src/routes/notify.ts", Line: 2, Text: "process.env.NOTIFY_SVC_URL", Callee: "NotifyClient", Symbol: "process.env.NOTIFY_SVC_URL"})
	code := "portal-bff/src/routes/notify.ts"
	g, s, _ := resolveFiles(t, c, srcFacts{code, []Fact{{Kind: "import", From: "notify", FromType: "provider", Target: "@acme/notify-client"}}})
	ev := callServiceEvidence(t, g, s, "notify-svc", code, "portal-bff/.env")
	wantTwoValid(t, ev, code, "process.env.NOTIFY_SVC_URL", "portal-bff/.env")
}

func TestConfigHost_LiteralHostKeepsItsEvidence(t *testing.T) {
	writeTree(t, map[string]string{
		"svc/a.ts": "fetch('http://jerry-api:3002/jerry/status');\n",
	})
	code := "svc/a.ts"
	g, s, _ := resolveFiles(t, &ConfigHosts{}, srcFacts{code, []Fact{
		{Kind: "http_call", From: "a", FromType: "provider", Target: "http://jerry-api:3002/jerry/status", Method: "GET"},
	}})
	edges, _ := s.ListEdges(store.EdgeFilter{EdgeType: "CALLS_SERVICE"})
	if len(edges) != 1 || !edges[0].Active {
		t.Fatalf("edges = %+v", edges)
	}
	ev, _ := s.ListEvidenceByEdge(edges[0].EdgeID)
	if len(ev) != 1 {
		t.Fatalf("a literal-host call has %d evidence rows, want 1", len(ev))
	}
	if !strings.Contains(ev[0].Assertion, `"substring":"/jerry/status"`) || !strings.Contains(ev[0].Assertion, `"context":"fetch"`) {
		t.Errorf("assertion changed: %s", ev[0].Assertion)
	}
	_ = g
}

// The model writes the configured host itself as the target; the line holds
// only the symbol. The call is treated as a substituted one.
func TestConfigHost_LiteralHostTargetFromSymbolLine(t *testing.T) {
	writeTree(t, map[string]string{
		"notify-svc/.env":                  "PRICING_API_URL=http://pricing-api:8080\n",
		"notify-svc/src/pricing.client.ts": "export const price = (sku: string) =>\n  fetch(`${process.env.PRICING_API_URL}/prices/${sku}`);\n",
	})
	c := &ConfigHosts{}
	c.Add("PRICING_API_URL", "http://pricing-api:8080", "notify-svc/.env")
	code := "notify-svc/src/pricing.client.ts"
	g, s, _ := resolveFiles(t, c, srcFacts{code, []Fact{
		{Kind: "http_call", From: "pricing", FromType: "provider", Target: "pricing-api/prices/${sku}", Method: "GET"},
	}})
	ev := callServiceEvidence(t, g, s, "pricing-api", code, "notify-svc/.env")
	wantTwoValid(t, ev, code, "PRICING_API_URL", "notify-svc/.env")
}

func TestConfigHost_LiteralHostTargetTypedClient(t *testing.T) {
	writeTree(t, map[string]string{
		"sales-api/appsettings.json":          "{\n  \"Services\": {\n    \"PricingApi\": \"http://pricing-api:8080\"\n  }\n}\n",
		"sales-api/Program.cs":                "builder.Services.AddHttpClient<PricingClient>(c => c.BaseAddress = new Uri(builder.Configuration[\"Services:PricingApi\"]!));\n",
		"sales-api/Services/PricingClient.cs": "public class PricingClient {\n  public Task<Price> Get() => _http.GetFromJsonAsync<Price>(\"/prices/all\");\n}\n",
	})
	c := &ConfigHosts{}
	c.Add("Services:PricingApi", "http://pricing-api:8080", "sales-api/appsettings.json")
	c.AddClient(ClientBinding{Class: "PricingClient", Key: "Services:PricingApi", File: "sales-api/Program.cs"})
	code := "sales-api/Services/PricingClient.cs"
	g, s, _ := resolveFiles(t, c, srcFacts{code, []Fact{
		{Kind: "http_call", From: "PricingClient", FromType: "provider", Target: "http://pricing-api:8080/prices/all", Method: "GET"},
	}})
	ev := callServiceEvidence(t, g, s, "pricing-api", code, "sales-api/appsettings.json")
	wantTwoValid(t, ev, code, "/prices/all", "sales-api/appsettings.json")
}

// notify-svc shape: the host also appears as plain text in an error message
// of the same file, but the call itself uses the config symbol. The config
// symbol decides: two valid rows, active edge.
func TestConfigHost_LiteralHostTargetHostAlsoInText(t *testing.T) {
	writeTree(t, map[string]string{
		"notify-svc/.env": "PRICING_API_URL=http://pricing-api:8080\n",
		"notify-svc/src/pricing.client.ts": "export async function price(sku: string) {\n" +
			"  const res = await fetch(`${process.env.PRICING_API_URL}/prices/${sku}`);\n" +
			"  if (!res.ok) throw new Error(`pricing-api answered ${res.status}`);\n" +
			"  return res.json();\n}\n",
	})
	c := &ConfigHosts{}
	c.Add("PRICING_API_URL", "http://pricing-api:8080", "notify-svc/.env")
	code := "notify-svc/src/pricing.client.ts"
	g, s, _ := resolveFiles(t, c, srcFacts{code, []Fact{
		{Kind: "http_call", From: "pricing", FromType: "provider", Target: "pricing-api", Method: "GET"},
	}})
	ev := callServiceEvidence(t, g, s, "pricing-api", code, "notify-svc/.env")
	wantTwoValid(t, ev, code, "PRICING_API_URL", "notify-svc/.env")
}
