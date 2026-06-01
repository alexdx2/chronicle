package graph

import "testing"

func TestJoinRoutePath(t *testing.T) {
	tests := []struct {
		prefix string
		route  string
		want   string
	}{
		{"arena", "attack", "/arena/attack"},
		{"/arena", "/attack", "/arena/attack"},
		{"arena/", "/attack/", "/arena/attack"},
		{"", "health", "/health"},
		{"arena", "", "/arena"},
		{"", "", "/"},
		{"/", "/", "/"},
		{"api/v1", "users", "/api/v1/users"},
		{"/arena", "attack/special", "/arena/attack/special"},
	}
	for _, tt := range tests {
		got := joinRoutePath(tt.prefix, tt.route)
		if got != tt.want {
			t.Errorf("joinRoutePath(%q, %q) = %q, want %q", tt.prefix, tt.route, got, tt.want)
		}
	}
}
