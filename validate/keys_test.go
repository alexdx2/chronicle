package validate

import (
	"testing"
)

func TestNormalizeName(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"ArenaController", "arena-controller"},
		{"arena.controller", "arena-controller"},
		{"arenaController", "arena-controller"},
		{"ARENA_CONTROLLER", "arena-controller"},
		{"arena-controller", "arena-controller"},
		{"IScoreService", "i-score-service"},
		{"ScoreboardDbContext", "scoreboard-db-context"},
		{"BattleResultProducer", "battle-result-producer"},
		{"HTTPClient", "http-client"},
		{"myService", "my-service"},
		{"tom-api", "tom-api"},
		{"arena_api", "arena-api"},
		{"arena.api", "arena-api"},
		{"OrderService", "order-service"},
		{"order.service", "order-service"},
		{"order_service", "order-service"},
		{"a", "a"},
		{"", ""},
		{"ABC", "abc"},
		{"ABCDef", "abc-def"},
		{"getHTTPResponse", "get-http-response"},
		// Digit→upper is a word boundary. The resolver's class-name splitter
		// (graph.normalizePascalCase) treats it as one, so this side must too
		// or "S3Client" gets two canonical spellings: the resolver mints
		// code:provider:d:s3-client while a key reference normalizes to
		// s3client and misses. It is also the boundary the file stem carries
		// ("s3.client.ts" → "s3-client").
		{"S3Client", "s3-client"},
		{"V2Service", "v2-service"},
		{"Oauth2Service", "oauth2-service"},
		{"User2FA", "user2-fa"},
		{"s3.client", "s3-client"},
		{"order.created.v2", "order-created-v2"},
	}

	for _, tt := range tests {
		got := NormalizeName(tt.input)
		if got != tt.want {
			t.Errorf("NormalizeName(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestNormalizeNodeKey(t *testing.T) {
	tests := []struct {
		input string
		want  string
		err   bool
	}{
		{"code:controller:orders:OrdersController", "code:controller:orders:orders-controller", false},
		{"  Code:Controller:Orders:OrdersController  ", "code:controller:orders:orders-controller", false},
		{"code:controller:orders:/path/to/thing/", "code:controller:orders:path/to/thing", false},
		{"code:controller:orders:", "", true},
		{"code:controller", "", true},
		{"a:b:c:d:e:f", "a:b:c:d:e:f", false},
		{"", "", true},
	}

	for _, tt := range tests {
		got, err := NormalizeNodeKey(tt.input)
		if tt.err && err == nil {
			t.Errorf("NormalizeNodeKey(%q): expected error", tt.input)
		}
		if !tt.err && err != nil {
			t.Errorf("NormalizeNodeKey(%q): unexpected error: %v", tt.input, err)
		}
		if got != tt.want {
			t.Errorf("NormalizeNodeKey(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

// SQ-Contract 3, rule 1: a route qualified_name keeps its structure. Kebab
// dashes inside "[invoiceId]" would name a route that exists nowhere in the
// source, and the resolve pipeline (which only lowercases) would never agree
// with it.
func TestNormalizeNodeKey_RouteShapeIsLowercasedNotKebabbed(t *testing.T) {
	tests := []struct{ input, want string }{
		{"contract:endpoint:app:GET:/invoices/[invoiceId]/lines", "contract:endpoint:app:get:/invoices/[invoiceid]/lines"},
		{"contract:endpoint:app:get:/invoices/[invoiceid]/lines", "contract:endpoint:app:get:/invoices/[invoiceid]/lines"},
		{"contract:endpoint:app:GET:/orders/{orderId}", "contract:endpoint:app:get:/orders/{orderid}"},
		{"contract:endpoint:app:get:/orders/:orderId", "contract:endpoint:app:get:/orders/:orderid"},
		{"contract:endpoint:app:get:/api/userProfile", "contract:endpoint:app:get:/api/userprofile"},
		{"contract:endpoint:app:get:/orders/", "contract:endpoint:app:get:/orders"},
		{"contract:endpoint:app:POST:/", "contract:endpoint:app:post:/"},
		{"flow:use_case:app:get:/invoices/[invoiceId]/lines", "flow:use_case:app:get:/invoices/[invoiceid]/lines"},
		// NOT a route: no method prefix, so the existing slash-trim + kebab
		// behaviour stands.
		{"code:controller:orders:/path/to/ThingHere/", "code:controller:orders:path/to/thing-here"},
	}
	for _, tt := range tests {
		got, err := NormalizeNodeKey(tt.input)
		if err != nil {
			t.Errorf("NormalizeNodeKey(%q): unexpected error: %v", tt.input, err)
			continue
		}
		if got != tt.want {
			t.Errorf("NormalizeNodeKey(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

// SQ-Contract 3: the canonical form is a fixed point — normalizing a key the
// resolver or the importer already minted must change nothing. Every shape the
// emitters produce is listed here; a shape that is not a fixed point means two
// canonical spellings for one entity.
func TestNormalizeNodeKey_CanonicalFormsAreFixedPoints(t *testing.T) {
	canonical := []string{
		"code:provider:app:orders-service",
		"code:provider:app:src/orders/orders-service",
		"code:module:app:@okeep/ui",
		"service:service:app:@okeep/ui",
		"code:module:app:socket-io",
		"contract:endpoint:app:get:/invoices/[invoiceid]/lines",
		"contract:endpoint:app:post:/",
		"flow:use_case:app:get:/invoices/[invoiceid]/lines",
		"contract:topic:app:battle-results",
		"contract:topic:app:battle-queue:attack",
		"data:field:app:battle/winner-id",
		"data:model:app:battle-event",
		"code:provider:app:session-cookie-store",
		"code:provider:app:s3-client",
		"service:service:app:scoreboard-api",
		"service:service:app:spectators-api",
		"service:external_system:app:hooks-example-com",
	}
	for _, key := range canonical {
		got, err := NormalizeNodeKey(key)
		if err != nil {
			t.Errorf("NormalizeNodeKey(%q): unexpected error: %v", key, err)
			continue
		}
		if got != key {
			t.Errorf("%q is not a fixed point: normalizes to %q", key, got)
		}
	}
}

// Normalizing twice must equal normalizing once, whatever the input spelling.
func TestNormalizeNodeKey_Idempotent(t *testing.T) {
	raw := []string{
		"code:provider:app:OrdersService",
		"code:provider:app:orders.service",
		"code:provider:app:ORDERS_SERVICE",
		"code:provider:app:src/Orders/OrdersService",
		"code:module:app:@okeep/UI",
		"code:module:app:socket.io",
		"contract:endpoint:app:GET:/invoices/[invoiceId]/lines",
		"contract:endpoint:app:get:/orders//",
		"data:field:app:Battle/winnerId",
		"contract:topic:app:order.created",
		"code:provider:app:S3Client",
		"service:service:app:ScoreboardApi",
		"service:external_system:app:hooks.example.com",
		"a:b:c:d:e:f",
	}
	for _, key := range raw {
		once, err := NormalizeNodeKey(key)
		if err != nil {
			t.Errorf("NormalizeNodeKey(%q): unexpected error: %v", key, err)
			continue
		}
		twice, err := NormalizeNodeKey(once)
		if err != nil {
			t.Errorf("NormalizeNodeKey(%q) [second pass on %q]: unexpected error: %v", key, once, err)
			continue
		}
		if twice != once {
			t.Errorf("NormalizeNodeKey not idempotent for %q: %q → %q", key, once, twice)
		}
	}
}

// The point of rule 2: the spellings one identity arrives in must collapse to
// one key, so a class-name reference finds the node minted from the file.
func TestNormalizeNodeKey_SpellingsConverge(t *testing.T) {
	want := "code:provider:app:orders-service"
	for _, spelling := range []string{
		"code:provider:app:OrdersService",
		"code:provider:app:ordersService",
		"code:provider:app:orders.service",
		"code:provider:app:orders_service",
		"code:provider:app:ORDERS_SERVICE",
		"code:provider:app:orders-service",
	} {
		got, err := NormalizeNodeKey(spelling)
		if err != nil {
			t.Fatalf("NormalizeNodeKey(%q): %v", spelling, err)
		}
		if got != want {
			t.Errorf("NormalizeNodeKey(%q) = %q, want %q", spelling, got, want)
		}
	}
}

// The digit boundary must converge the same three spellings rule 2 converges
// for letter-only names: the class name as written, the dot-case stem the
// resolver derives from it, and the file stem it lives in.
func TestNormalizeNodeKey_DigitBoundarySpellingsConverge(t *testing.T) {
	groups := map[string][]string{
		"code:provider:app:s3-client": {
			"code:provider:app:S3Client",
			"code:provider:app:s3.client",
			"code:provider:app:s3-client",
			"code:provider:app:S3_CLIENT",
		},
		"code:provider:app:v2-service": {
			"code:provider:app:V2Service",
			"code:provider:app:v2.service",
		},
		"code:provider:app:oauth2-service": {
			"code:provider:app:Oauth2Service",
			"code:provider:app:oauth2.service",
		},
	}
	for want, spellings := range groups {
		for _, spelling := range spellings {
			got, err := NormalizeNodeKey(spelling)
			if err != nil {
				t.Fatalf("NormalizeNodeKey(%q): %v", spelling, err)
			}
			if got != want {
				t.Errorf("NormalizeNodeKey(%q) = %q, want %q", spelling, got, want)
			}
		}
	}
}

func TestBuildEdgeKey(t *testing.T) {
	from := "code:controller:orders:orderscontroller"
	to := "code:provider:orders:ordersservice"
	edgeType := "INJECTS"
	got := BuildEdgeKey(from, to, edgeType)
	want := "code:controller:orders:orderscontroller->code:provider:orders:ordersservice:INJECTS"
	if got != want {
		t.Errorf("BuildEdgeKey = %q, want %q", got, want)
	}
}

func TestNormalizeEdgeKey(t *testing.T) {
	tests := []struct {
		input string
		want  string
		err   bool
	}{
		{
			"code:controller:orders:OrdersController->code:provider:orders:OrdersService:INJECTS",
			"code:controller:orders:orders-controller->code:provider:orders:orders-service:INJECTS",
			false,
		},
		{"bad-format", "", true},
		{"->:INJECTS", "", true},
	}

	for _, tt := range tests {
		got, err := NormalizeEdgeKey(tt.input)
		if tt.err && err == nil {
			t.Errorf("NormalizeEdgeKey(%q): expected error", tt.input)
		}
		if !tt.err && err != nil {
			t.Errorf("NormalizeEdgeKey(%q): unexpected error: %v", tt.input, err)
		}
		if got != tt.want {
			t.Errorf("NormalizeEdgeKey(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}
