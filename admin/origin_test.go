package admin

import (
	"net/http/httptest"
	"testing"
)

func TestSameOrigin(t *testing.T) {
	cases := []struct {
		host, origin string
		want         bool
	}{
		{"127.0.0.1:4321", "", true},
		{"127.0.0.1:4321", "http://127.0.0.1:4321", true},
		{"localhost:4321", "http://localhost:4321", true},
		{"[::1]:4321", "http://[::1]:4321", true},
		{"192.168.1.5:9090", "http://192.168.1.5:9090", true},
		{"127.0.0.1:4321", "http://127.0.0.1:9999", false},
		{"127.0.0.1:4321", "https://evil.example", false},
		{"127.0.0.1:4321", "null", false},
	}
	for _, c := range cases {
		r := httptest.NewRequest("GET", "/ws", nil)
		r.Host = c.host
		if c.origin != "" {
			r.Header.Set("Origin", c.origin)
		}
		if got := SameOrigin(r); got != c.want {
			t.Errorf("host %s origin %q: got %v want %v", c.host, c.origin, got, c.want)
		}
	}
}
