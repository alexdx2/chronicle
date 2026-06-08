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
