package graph

import (
	"encoding/json"
	"sort"
	"strings"
	"testing"

	"github.com/alexdx2/chronicle-core/store"
)

// fleetConfig is the config table the audit fleet's files produce.
func fleetConfig() *ConfigHosts {
	c := &ConfigHosts{}
	c.Add("Services:PricingApi", "http://pricing-api:8080", "sales-api/appsettings.json")
	c.Add("Services:NotifyUrl", "http://notify-svc:3000", "sales-api/appsettings.json")
	c.AddClient(ClientBinding{Class: "PricingClient", Key: "Services:PricingApi", File: "sales-api/Program.cs"})
	c.AddClient(ClientBinding{Class: "INotifyClient", Key: "Services:NotifyUrl", File: "sales-api/Program.cs"})
	c.Add("apiUrl", "http://sales-api:8080", "portal-web/src/environments/environment.ts")
	c.Add("NOTIFY_SVC_URL", "http://notify-svc:3000", "portal-bff/.env")
	c.Add("SALES_API_URL", "http://sales-api:8080", "portal-bff/.env")
	return c
}

type srcFacts struct {
	path  string
	facts []Fact
}

// resolveFiles saves the facts per file and resolves them in domain "demo".
func resolveFiles(t *testing.T, cfg *ConfigHosts, files ...srcFacts) (*Graph, *store.Store, *ResolveExtractionsResult) {
	t.Helper()
	g, s := newTwinGraph(t)
	if cfg != nil {
		g.SetConfigHosts("demo", cfg)
	}
	revID, _ := s.CreateRevision("demo", "s1", "", "manual", "full", "{}")
	for _, f := range files {
		b, _ := json.Marshal(f.facts)
		if _, err := g.SaveFileExtraction(revID, "demo", f.path, "extracted", "", string(b), ""); err != nil {
			t.Fatalf("SaveFileExtraction: %v", err)
		}
	}
	res, err := g.ResolveExtractions("demo", revID)
	if err != nil {
		t.Fatalf("ResolveExtractions: %v", err)
	}
	return g, s, res
}

func externalNames(t *testing.T, s *store.Store) []string {
	t.Helper()
	var out []string
	for k := range activeKeys(t, s, "external_system") {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func activeEdgesOf(t *testing.T, s *store.Store, typ string) []string {
	t.Helper()
	edges, _ := s.ListEdges(store.EdgeFilter{EdgeType: typ})
	var out []string
	for _, e := range edges {
		if e.Active {
			out = append(out, e.FromNodeKey+" -> "+e.ToNodeKey)
		}
	}
	sort.Strings(out)
	return out
}

func wantOnly(t *testing.T, what string, got []string, want ...string) {
	t.Helper()
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("%s = %v, want %v", what, got, want)
	}
}

func TestHTTPCall_TypedClientPathTakesConfiguredHost(t *testing.T) {
	_, s, res := resolveFiles(t, fleetConfig(), srcFacts{"sales-api/Services/PricingClient.cs", []Fact{
		{Kind: "http_call", From: "PricingClient", FromType: "provider", Target: "/prices/{sku}", Method: "GET"},
	}})
	wantOnly(t, "external systems", externalNames(t, s), "service:external_system:demo:pricing-api")
	if got := activeEdgesOf(t, s, "CALLS_SERVICE"); len(got) != 1 || !strings.HasSuffix(got[0], "service:external_system:demo:pricing-api") {
		t.Errorf("CALLS_SERVICE = %v", got)
	}
	if len(res.Unresolved) != 0 {
		t.Errorf("unresolved: %+v", res.Unresolved)
	}
}

func TestHTTPCall_RefitInterfaceTakesConfiguredHost(t *testing.T) {
	_, s, _ := resolveFiles(t, fleetConfig(), srcFacts{"sales-api/Services/INotifyClient.cs", []Fact{
		{Kind: "http_call", From: "INotifyClient", FromType: "provider", Target: "/notify/order-accepted", Method: "POST"},
	}})
	wantOnly(t, "external systems", externalNames(t, s), "service:external_system:demo:notify-svc")
}

func TestHTTPCall_ConfigurationIndexerIsSubstituted(t *testing.T) {
	_, s, _ := resolveFiles(t, fleetConfig(), srcFacts{"sales-api/Services/Other.cs", []Fact{
		{Kind: "http_call", From: "Other", FromType: "provider", Target: `$"{_config["Services:PricingApi"]}/prices/{sku}"`, Method: "GET"},
		{Kind: "http_call", From: "Other", FromType: "provider", Target: `Configuration["Services:NotifyUrl"] + "/notify"`, Method: "POST"},
	}})
	wantOnly(t, "external systems", externalNames(t, s),
		"service:external_system:demo:notify-svc", "service:external_system:demo:pricing-api")
}

func TestHTTPCall_EnvironmentApiUrlIsTheConfiguredHost(t *testing.T) {
	_, s, _ := resolveFiles(t, fleetConfig(), srcFacts{"portal-web/src/app/orders.service.ts", []Fact{
		{Kind: "http_call", From: "OrdersService", FromType: "provider", Target: "`${environment.apiUrl}/orders`", Method: "GET"},
		{Kind: "http_call", From: "OrdersService", FromType: "provider", Target: "environment.apiUrl + '/orders'", Method: "POST"},
	}})
	wantOnly(t, "external systems", externalNames(t, s), "service:external_system:demo:sales-api")
}

func TestHTTPCall_UnresolvedSymbolMintsNoNode(t *testing.T) {
	for _, target := range []string{
		"`${environment.apiUrl}/orders`", `Configuration["Missing:Key"]`, "config.GetValue<string>(\"Nope\") + \"/x\"",
		"${base}/x", "environment.api.url + '/x'",
	} {
		_, s, res := resolveFiles(t, &ConfigHosts{}, srcFacts{"portal-web/src/app/orders.service.ts", []Fact{
			{Kind: "http_call", From: "OrdersService", FromType: "provider", Target: target, Method: "GET"},
		}})
		if names := externalNames(t, s); len(names) != 0 {
			t.Errorf("%q: external systems minted: %v", target, names)
		}
		if got := activeEdgesOf(t, s, "CALLS_SERVICE"); len(got) != 0 {
			t.Errorf("%q: CALLS_SERVICE %v", target, got)
		}
		if len(res.Unresolved) != 1 || res.Unresolved[0].Kind != "http_call" {
			t.Errorf("%q: unresolved = %+v", target, res.Unresolved)
		}
	}
}

func TestHTTPCall_EnvVarFromDotenvAndWithoutIt(t *testing.T) {
	facts := srcFacts{"portal-bff/src/routes/notify.ts", []Fact{
		{Kind: "http_call", From: "notify", FromType: "provider", Target: "process.env.NOTIFY_SVC_URL ?? ''", Method: "POST"},
	}}
	_, s, _ := resolveFiles(t, fleetConfig(), facts)
	wantOnly(t, "with .env", externalNames(t, s), "service:external_system:demo:notify-svc")
	// no table: the variable's name still names the service (unchanged behaviour)
	facts.facts[0].Target = "process.env.NOTIFY_SVC_URL + '/notify'"
	_, s2, _ := resolveFiles(t, nil, facts)
	wantOnly(t, "without table", externalNames(t, s2), "service:external_system:demo:notify-svc")
}

func TestHTTPCall_PathOnlyMakesNoExternalSystemOrServiceEdge(t *testing.T) {
	_, s, res := resolveFiles(t, fleetConfig(), srcFacts{"sales-api/Services/Mystery.cs", []Fact{
		{Kind: "http_call", From: "Mystery", FromType: "provider", Target: "/notify/order-accepted", Method: "POST"},
		{Kind: "http_call", From: "Mystery", FromType: "provider", Target: `$"/prices/{sku}"`, Method: "GET"},
	}})
	if names := externalNames(t, s); len(names) != 0 {
		t.Errorf("external systems: %v", names)
	}
	if got := activeEdgesOf(t, s, "CALLS_SERVICE"); len(got) != 0 {
		t.Errorf("CALLS_SERVICE: %v", got)
	}
	if got := activeEdgesOf(t, s, "CALLS_ENDPOINT"); len(got) != 0 {
		t.Errorf("CALLS_ENDPOINT without an owning endpoint: %v", got)
	}
	if len(res.Unresolved) != 2 {
		t.Errorf("unresolved = %+v, want both path-only calls", res.Unresolved)
	}
}

func TestHTTPCall_RemotePathIsNotMatchedOnLocalEndpoint(t *testing.T) {
	// portal-bff exposes GET /orders itself and fetches sales-api's /orders.
	_, s, _ := resolveFiles(t, fleetConfig(),
		srcFacts{"portal-bff/src/routes/orders.ts", []Fact{
			{Kind: "endpoint", From: "orders", FromType: "controller", Target: "/orders", Method: "GET"},
			{Kind: "http_call", From: "orders", FromType: "controller", Target: "`${SALES_API_URL}/orders`", Method: "GET"},
		}})
	if got := activeEdgesOf(t, s, "CALLS_ENDPOINT"); len(got) != 0 {
		t.Errorf("a call to another service's /orders was attributed to the local endpoint: %v", got)
	}
	wantOnly(t, "external systems", externalNames(t, s), "service:external_system:demo:sales-api")
}

func TestHTTPCall_BareConfigKeyIsTheConfiguredHostNeverAKeySegment(t *testing.T) {
	_, s, res := resolveFiles(t, fleetConfig(), srcFacts{"sales-api/Services/PricingClient.cs", []Fact{
		{Kind: "http_call", From: "PricingClient", FromType: "provider", Target: "Services:PricingApi/prices/{sku}", Method: "GET"},
	}})
	wantOnly(t, "external systems", externalNames(t, s), "service:external_system:demo:pricing-api")
	if len(res.Unresolved) != 0 {
		t.Errorf("unresolved: %+v", res.Unresolved)
	}
	// a key the table does not hold mints nothing, and is reported
	_, s2, res2 := resolveFiles(t, &ConfigHosts{}, srcFacts{"sales-api/Services/PricingClient.cs", []Fact{
		{Kind: "http_call", From: "PricingClient", FromType: "provider", Target: "Services:PricingApi", Method: "GET"},
	}})
	if names := externalNames(t, s2); len(names) != 0 {
		t.Errorf("key segment minted a node: %v", names)
	}
	if len(res2.Unresolved) != 1 {
		t.Errorf("unresolved = %+v", res2.Unresolved)
	}
	// host:port is still a host
	_, s3, _ := resolveFiles(t, fleetConfig(), srcFacts{"sales-api/Services/X.cs", []Fact{
		{Kind: "http_call", From: "X", FromType: "provider", Target: "pricing-api:8080/prices", Method: "GET"},
	}})
	wantOnly(t, "host:port", externalNames(t, s3), "service:external_system:demo:pricing-api")
}

func TestConfigUse_ConstructorArgumentCallsTheConfiguredService(t *testing.T) {
	cfg := fleetConfig()
	cfg.AddUse(ConfigUse{File: "portal-bff/src/routes/notify.ts", Line: 4, Text: "process.env.NOTIFY_SVC_URL", Callee: "NotifyClient", Symbol: "process.env.NOTIFY_SVC_URL"})
	cfg.AddUse(ConfigUse{File: "portal-bff/src/routes/notify.ts", Line: 9, Text: "process.env.NOTIFY_SVC_URL", Callee: "NotifyClient", Symbol: "process.env.NOTIFY_SVC_URL"})
	cfg.AddUse(ConfigUse{File: "portal-bff/src/routes/ghost.ts", Line: 1, Text: "process.env.NOPE_URL", Callee: "GhostClient", Symbol: "process.env.NOPE_URL"})
	_, s, _ := resolveFiles(t, cfg, srcFacts{"portal-bff/src/routes/notify.ts", []Fact{}}, srcFacts{"portal-bff/src/routes/ghost.ts", []Fact{}})
	got := activeEdgesOf(t, s, "CALLS_SERVICE")
	if len(got) != 1 || !strings.HasSuffix(got[0], "-> service:external_system:demo:notify-svc") || !strings.Contains(got[0], "notify") {
		t.Fatalf("CALLS_SERVICE = %v", got)
	}
	wantOnly(t, "external systems", externalNames(t, s), "service:external_system:demo:notify-svc")
	edges, _ := s.ListEdges(store.EdgeFilter{EdgeType: "CALLS_SERVICE"})
	evs, err := s.ListEvidenceByEdge(edges[0].EdgeID)
	if err != nil || len(evs) == 0 || evs[0].LineStart != 4 {
		t.Errorf("evidence = %+v err=%v, want it anchored at line 4", evs, err)
	}
}

func TestRoutingConfig_ProxyDestinationIsACallWithoutCallSite(t *testing.T) {
	writeTree(t, map[string]string{
		"gateway/appsettings.json": "{\n  \"ReverseProxy\": {\n    \"Clusters\": {\n      \"billing\": { \"Destinations\": { \"d1\": { \"Address\": \"http://billing-api:8080\" } } }\n    }\n  },\n  \"Services\": { \"Docs\": \"http://wiki:80\" }\n}\n",
	})
	c := &ConfigHosts{}
	c.Add("ReverseProxy:Clusters:billing:Destinations:d1:Address", "http://billing-api:8080", "gateway/appsettings.json")
	c.Add("Services:Docs", "http://wiki:80", "gateway/appsettings.json") // not a routing key: no call
	g, s, _ := resolveFiles(t, c, srcFacts{"gateway/Program.cs", []Fact{}})
	got := activeEdgesOf(t, s, "CALLS_SERVICE")
	if len(got) != 1 || !strings.HasSuffix(got[0], "-> service:external_system:demo:billing-api") {
		t.Fatalf("CALLS_SERVICE = %v", got)
	}
	ev := callServiceEvidence(t, g, s, "billing-api", "gateway/appsettings.json")
	if len(ev) == 0 || ev[0].FilePath != "gateway/appsettings.json" || ev[0].LineStart != 4 {
		t.Errorf("evidence = %+v, want the Address line (4)", ev)
	}
}

func TestIsRoutingKey(t *testing.T) {
	for _, k := range []string{"ReverseProxy:Clusters:a:Destinations:d:Address", "upstream.backend.url", "spring.cloud.gateway.routes.0.uri", "nginx.proxy_pass"} {
		if !isRoutingKey(k) {
			t.Errorf("%s should be a routing key", k)
		}
	}
	for _, k := range []string{"Services:PricingApi", "api.url", "NOTIFY_SVC_URL"} {
		if isRoutingKey(k) {
			t.Errorf("%s should not be a routing key", k)
		}
	}
}

func TestPromptSection_NotForTheFileThatDefinesTheValue(t *testing.T) {
	c := &ConfigHosts{}
	c.Add("CATALOG_API_URL", "http://catalog-api:3000", "src/config.rs")
	c.Add("BILLING_URL", "http://billing-api:8080", "src/other.rs")
	own := c.PromptSection("src/config.rs")
	if strings.Contains(own, "CATALOG_API_URL") || !strings.Contains(own, "BILLING_URL") {
		t.Errorf("config.rs must see the other file's values only:\n%s", own)
	}
	if got := (&ConfigHosts{Entries: c.Entries[:1]}).PromptSection("src/config.rs"); got != "" {
		t.Errorf("nothing left to say, yet: %q", got)
	}
}

func TestConfigUse_SymbolDiffersFromTheAnchorText(t *testing.T) {
	writeTree(t, map[string]string{
		"w/appsettings.json":   "{ \"Services\": { \"BillingApi\": \"http://billing-api:8080\" } }\n",
		"w/Clients/Billing.cs": "class B {\n  void F() { var h = _f.CreateClient(\"billing\"); }\n}\n",
	})
	c := &ConfigHosts{}
	c.Add("Services:BillingApi", "http://billing-api:8080", "w/appsettings.json")
	c.AddUse(ConfigUse{File: "w/Clients/Billing.cs", Line: 2, Text: `CreateClient("billing")`, Callee: "IHttpClientFactory.CreateClient", Symbol: `Configuration["Services:BillingApi"]`})
	code := "w/Clients/Billing.cs"
	g, s, _ := resolveFiles(t, c, srcFacts{code, []Fact{}})
	ev := callServiceEvidence(t, g, s, "billing-api", code, "w/appsettings.json")
	wantTwoValid(t, ev, code, `CreateClient(\"billing\")`, "w/appsettings.json")
}

func TestClassifyCallBase_ConfigObjectPaths(t *testing.T) {
	for in, want := range map[string]string{
		"Constants.expoConfig.extra.apiUrl":   "apiUrl",
		"Constants.expoConfig?.extra?.apiUrl": "apiUrl",
		"environment.apiUrl":                  "apiUrl",
		"this.config.api.url":                 "api.url",
		"Config.default.settings.catalog.url": "catalog.url",
	} {
		kind, key := ClassifyCallBase(in)
		if kind != SymConfig || key != want {
			t.Errorf("%s -> %v %q, want config %q", in, kind, key, want)
		}
	}
	for _, in := range []string{"user.name", "response.data.url", "items.length"} {
		if kind, _ := ClassifyCallBase(in); kind == SymConfig {
			t.Errorf("%s must not be a config symbol", in)
		}
	}
}

func TestClassifyCallBase_ViteEnv(t *testing.T) {
	for in, want := range map[string]string{
		"import.meta.env.VITE_API_URL":      "VITE_API_URL",
		"`${import.meta.env.VITE_API_URL}`": "VITE_API_URL",
		"import.meta.env['VITE_API_URL']":   "VITE_API_URL",
	} {
		kind, key := ClassifyCallBase(in)
		if kind != SymEnv || key != want {
			t.Errorf("%s -> %v %q, want env %q", in, kind, key, want)
		}
	}
}

func TestClassifyCallBase_SpringPlaceholderKeys(t *testing.T) {
	for in, want := range map[string]string{
		"${services.catalog.url}":            "services.catalog.url",
		"${services.catalog.url:http://x:1}": "services.catalog.url",
		"${catalog-api.base-url}/stock":      "",
	} {
		kind, key := ClassifyCallBase(in)
		if want == "" {
			continue
		}
		if kind != SymConfig || key != want {
			t.Errorf("%s -> %v %q, want config %q", in, kind, key, want)
		}
	}
	if kind, _ := ClassifyCallBase("${baseUrl}"); kind == SymConfig {
		t.Errorf("a local variable is not a property key")
	}
}

func TestCanonChannelName(t *testing.T) {
	for in, want := range map[string]string{
		"https://sqs.eu-west-1.amazonaws.com/123456789012/orders-inbound":  "orders-inbound",
		"//sqs.eu-west-1.amazonaws.com/123456789012/orders-inbound":        "orders-inbound",
		"https://sqs.eu-west-1.amazonaws.com/123456789012/orders-inbound/": "orders-inbound",
		"http://localhost:4566/000000000000/orders-inbound":                "orders-inbound",
		"arn:aws:sqs:eu-west-1:123456789012:orders-inbound":                "orders-inbound",
		"arn:aws:sns:eu-west-1:123456789012:order-events":                  "order-events",
		"order-placed":                      "order-placed",
		"orders/created":                    "orders/created",
		"https://example.com/api/v1/things": "https://example.com/api/v1/things",
	} {
		if got := canonChannelName(in); got != want {
			t.Errorf("canonChannelName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestProduces_ConfigKeyHoldingAnSQSQueueURLIsTheQueue(t *testing.T) {
	writeTree(t, map[string]string{
		"legacy/Web.config":        "<appSettings><add key=\"OrdersQueueUrl\" value=\"https://sqs.eu-central-1.amazonaws.com/123456789012/orders-inbound\" /></appSettings>\n",
		"legacy/Services/Queue.cs": "class Q { void F() { var url = ConfigurationManager.AppSettings[\"OrdersQueueUrl\"]; sqs.SendMessageAsync(new SendMessageRequest { QueueUrl = url }); } }\n",
	})
	c := &ConfigHosts{}
	c.Add("OrdersQueueUrl", "https://sqs.eu-central-1.amazonaws.com/123456789012/orders-inbound", "legacy/Web.config")
	g, s, _ := resolveFiles(t, c, srcFacts{"legacy/Services/Queue.cs", []Fact{{Kind: "produces", From: "Q", FromType: "service", To: "OrdersQueueUrl"}}})
	if _, err := g.VerifyFileEvidence("legacy/Services/Queue.cs", 1, "demo"); err != nil {
		t.Fatal(err)
	}
	edges, _ := s.ListEdges(store.EdgeFilter{EdgeType: "PUBLISHES_TOPIC"})
	if len(edges) != 1 || !strings.HasSuffix(edges[0].ToNodeKey, ":orders-inbound") || !edges[0].Active {
		t.Fatalf("edges = %+v", edges)
	}
	// a plain topic name is untouched
	_, s2, _ := resolveFiles(t, c, srcFacts{"legacy/Services/Queue.cs", []Fact{{Kind: "produces", From: "Q", FromType: "service", To: "order-placed"}}})
	e2, _ := s2.ListEdges(store.EdgeFilter{EdgeType: "PUBLISHES_TOPIC"})
	if len(e2) != 1 || !strings.HasSuffix(e2[0].ToNodeKey, ":order-placed") {
		t.Fatalf("plain topic: %+v", e2)
	}
}

func TestBareDottedConfigKeyTargetIsTheConfiguredHost(t *testing.T) {
	for _, target := range []string{"services.catalog.url", "services.catalog.url/products/{sku}"} {
		c := &ConfigHosts{}
		c.Add("services.catalog.url", "http://catalog-api:3000", "src/main/resources/application.yml")
		_, s, _ := resolveFiles(t, c, srcFacts{"src/main/java/CatalogClient.java", []Fact{{Kind: "http_call", From: "CatalogClient", FromType: "service", Target: target, Method: "GET"}}})
		wantOnly(t, target, externalNames(t, s), "service:external_system:demo:catalog-api")
	}
	// a host that merely looks dotted is still a host
	c := &ConfigHosts{}
	c.Add("services.catalog.url", "http://catalog-api:3000", "application.yml")
	_, s, _ := resolveFiles(t, c, srcFacts{"A.java", []Fact{{Kind: "http_call", From: "A", FromType: "service", Target: "billing.corp.local/invoices", Method: "GET"}}})
	if got := externalNames(t, s); len(got) != 1 || strings.Contains(got[0], "catalog") {
		t.Fatalf("externals = %v", got)
	}
}
