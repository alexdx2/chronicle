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
