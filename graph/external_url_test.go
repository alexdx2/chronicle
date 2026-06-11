package graph

import "testing"

// Extractors report http_call targets as written in code — env templates,
// concatenations, gRPC URIs. Identity and path must survive all forms.
func TestInferExternalSystemName(t *testing.T) {
	cases := []struct{ url, want string }{
		{"http://notifications:3005/push", "notifications"},
		{"https://hooks.example.com/battles", "hooks.example.com"},
		{"http://tom-api:3001/tom/status", "tom-api"},
		{"${TOM_API_URL}/tom/status", "tom-api"},
		{"${JERRY_API_URL}/jerry/traps", "jerry-api"},
		{"$SCOREBOARD_HOST/api/score", "scoreboard"},
		{"process.env.TOM_API_URL + '/tom/status'", "tom-api"},
		{"${NOTIFICATIONS_BASE_URL}/push", "notifications"},
		{"${PAYMENTS_SERVICE_ENDPOINT}/charge", "payments-service"},
		{"grpc://bookmaker-api:50051/bookmaker.OddsService/GetOdds", "bookmaker-api"},
		// Spring property placeholders (@Value("${tom-api.base-url}"))
		{"${tom-api.base-url}/tom/status", "tom-api"},
		{"${jerry-api.base-url}/jerry/status", "jerry-api"},
		{"${services.payments.endpoint}/charge", "services-payments"},
		// bare SCREAMING hostname without wrapper/underscore stays a hostname
		{"http://LOCALHOST:3000/x", "LOCALHOST"},
		// bare dotted lowercase host is a real DNS name, not a property
		{"http://api.internal.corp/x", "api.internal.corp"},
	}
	for _, c := range cases {
		if got := inferExternalSystemName(c.url); got != c.want {
			t.Errorf("inferExternalSystemName(%q) = %q, want %q", c.url, got, c.want)
		}
	}
}

func TestExtractPathFromURLTemplates(t *testing.T) {
	cases := []struct{ url, want string }{
		{"http://tom-api:3001/tom/status", "/tom/status"},
		{"${TOM_API_URL}/tom/status", "/tom/status"},
		{"process.env.JERRY_API_URL + '/jerry/traps'", "/jerry/traps"},
		{"grpc://bookmaker-api:50051/bookmaker.OddsService/GetOdds", "/bookmaker.OddsService/GetOdds"},
		{"http://api:3000/orders/stats?from=2024", "/orders/stats"},
	}
	for _, c := range cases {
		if got := extractPathFromURL(c.url); got != c.want {
			t.Errorf("extractPathFromURL(%q) = %q, want %q", c.url, got, c.want)
		}
	}
}
