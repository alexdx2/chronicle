package truth

import (
	"encoding/json"
	"math"
	"strings"
	"testing"
)

func TestCanonEndpointAndPath(t *testing.T) {
	cases := []struct{ got, want string }{
		{CanonEndpoint("get", "api/Orders/{id}/"), "GET /api/orders/{}"},
		{CanonEndpoint("", "/x/:id"), "GET /x/{}"},
		{CanonEndpoint("post", "/"), "POST /"},
		{CanonPath("/u/<int:id>"), "/u/{}"},
		{CanonPath("/u/<id>"), "/u/{}"},
		{CanonPath("/a/[id]/b/${x}"), "/a/{}/b/{}"},
		{CanonPath("/a/b?x=1"), "/a/b"},
		{CanonPath("/api/orders/{id?}"), "/api/orders/{}"},
		{CanonPath("/api/orders/{id:int}"), "/api/orders/{}"},
		{CanonPath("/api/orders/{id?}?x=1#f"), "/api/orders/{}"},
		{EndpointKey("PUT", "/v1/Items/{itemId}"), "PUT /v1/items/{}"},
	}
	for _, c := range cases {
		if c.got != c.want {
			t.Errorf("got %q want %q", c.got, c.want)
		}
	}
}

func TestCanonHost(t *testing.T) {
	cases := map[string]string{
		"https://Api.Example.com:443/": "api.example.com",
		"http://localhost:3000":        "",
		"http://127.0.0.1/x":           "",
		"0.0.0.0":                      "",
		"orders.internal:8080/path":    "orders.internal",
		"https://u:p@Host.io/a":        "host.io",
	}
	for in, want := range cases {
		if got := CanonHost(in); got != want {
			t.Errorf("CanonHost(%q)=%q want %q", in, got, want)
		}
	}
}

func TestHostFromVar(t *testing.T) {
	cases := map[string]string{
		"TOM_API_URL":        "tom-api",
		"PaymentsBaseUrl":    "payments",
		"ORDERS_BASE_URL":    "orders",
		"AUTH_HOST":          "auth",
		"Services__Tom__Url": "services-tom",
		"_TOM_URL":           "tom",
	}
	for in, want := range cases {
		if got := HostFromVar(in); got != want {
			t.Errorf("HostFromVar(%q)=%q want %q", in, got, want)
		}
	}
}

func TestCanonNameDepKeys(t *testing.T) {
	if got := CanonName("Order.Created_v2"); got != "ordercreatedv2" {
		t.Errorf("CanonName=%q", got)
	}
	cases := []struct{ got, want string }{
		{CanonDep("python", "Foo_Bar.baz"), "python:foo-bar-baz"},
		{CanonDep("pypi", "Foo_Bar"), "python:foo-bar"},
		{CanonDep("nuget", "Newtonsoft.Json"), "nuget:newtonsoft.json"},
		{DepKey("npm", "Express"), "npm:express"},
		{ChannelKey("topic", "order-created", "produce"), "topic:order-created#produce"},
		{ServiceKey("own", "orders-api"), "own:orders-api"},
	}
	for _, c := range cases {
		if c.got != c.want {
			t.Errorf("got %q want %q", c.got, c.want)
		}
	}
}

func TestNoise(t *testing.T) {
	cases := []struct {
		eco, name string
		want      bool
	}{
		{"nuget", "Microsoft.Extensions.Logging", true},
		{"nuget", "System.Text.Json", true},
		{"nuget", "xunit.runner.visualstudio", true},
		{"nuget", "Moq", true},
		{"npm", "@types/node", true},
		{"npm", "eslint-plugin-react", true},
		{"python", "pytest-cov", true},
		{"npm", "express", false},
		{"nuget", "Newtonsoft.Json", false},
	}
	for _, c := range cases {
		if got := Noise(c.eco, c.name); got != c.want {
			t.Errorf("Noise(%s,%s)=%v want %v", c.eco, c.name, got, c.want)
		}
	}
}

func near(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

func TestMetricsCompute(t *testing.T) {
	m := Counts{Known: 100, Confirmed: 8, Missing: 2, OutOfScope: 90}.Compute()
	if !m.HasData || !near(m.Coverage, 0.08) || !near(m.Scope, 0.10) || !near(m.Recall, 0.8) {
		t.Errorf("metrics=%+v", m)
	}
	if (Counts{}).Compute().HasData {
		t.Error("empty counts must have no data")
	}
	m = Counts{Known: 5, OutOfScope: 5}.Compute()
	if !m.HasData || m.Recall != 0 {
		t.Errorf("zero recall denominator: %+v", m)
	}
}

func TestWeakest(t *testing.T) {
	mk := func(c Class, known, conf int) ClassSummary {
		cn := Counts{Known: known, Confirmed: conf}
		return ClassSummary{Class: c, Counts: cn, Metrics: cn.Compute()}
	}
	got, ok := Weakest([]ClassSummary{mk(ClassDeps, 0, 0), mk(ClassHTTP, 10, 5), mk(ClassEndpoints, 20, 10), mk(ClassChannels, 10, 8)})
	if !ok || got.Class != ClassEndpoints {
		t.Errorf("weakest=%v ok=%v (tie must go to larger Known)", got.Class, ok)
	}
	if _, ok := Weakest([]ClassSummary{mk(ClassDeps, 0, 0)}); ok {
		t.Error("no data must give ok=false")
	}
}

func TestClassSummaryMarshalSnakeCase(t *testing.T) {
	cn := Counts{Known: 4, Confirmed: 2}
	b, err := json.Marshal(ClassSummary{Class: ClassDeps, Counts: cn, Metrics: cn.Compute()})
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"known", "coverage", "by_source", "out_of_scope", "has_data"} {
		if _, ok := m[k]; !ok {
			t.Errorf("missing key %q in %s", k, b)
		}
	}
	if _, ok := m["Known"]; ok {
		t.Errorf("unexpected key Known in %s", b)
	}
}

func TestLooseTokens(t *testing.T) {
	cases := map[string]string{
		"notifications":        "notification",
		"notification-service": "notification",
		"address":              "address",
		"status":               "status",
		"PaymentsBaseUrl":      "payment",
		"payment-gateway":      "payment",
		"order-events":         "event|order",
		"api-url":              "",
		"service":              "",
		"":                     "",
	}
	for in, want := range cases {
		if got := LooseTokens(in); got != want {
			t.Errorf("LooseTokens(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestReportRulesVersion(t *testing.T) {
	b, err := json.Marshal(Report{RulesVersion: RulesVersion})
	if err != nil || !strings.Contains(string(b), `"rules_version":3`) {
		t.Fatalf("marshal: %s %v", b, err)
	}
	var old Report
	if err := json.Unmarshal([]byte(`{"facts":[],"files":3}`), &old); err != nil || old.RulesVersion != 0 {
		t.Fatalf("old report must decode with 0: %+v %v", old, err)
	}
}
