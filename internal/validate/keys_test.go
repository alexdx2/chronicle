package validate

import (
	"testing"
)

func TestNormalizeNodeKey(t *testing.T) {
	tests := []struct {
		input string
		want  string
		err   bool
	}{
		{"code:controller:orders:OrdersController", "code:controller:orders:orderscontroller", false},
		{"  Code:Controller:Orders:OrdersController  ", "code:controller:orders:orderscontroller", false},
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
			"code:controller:orders:orderscontroller->code:provider:orders:ordersservice:INJECTS",
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
