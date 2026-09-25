package admin

import (
	"net/http"
	"net/url"
	"strings"
)

// SameOrigin reports whether a request may act for the dashboard: requests
// without an Origin header (curl, Go clients) pass; a browser request passes
// only when its Origin names this very host (port and IPv6 brackets
// included). The live channel and its WebSocket use it.
func SameOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	u, err := url.Parse(origin)
	if err != nil || u.Host == "" {
		return false
	}
	return strings.EqualFold(u.Host, r.Host)
}
