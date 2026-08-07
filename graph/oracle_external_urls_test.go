package graph

import (
	"strings"
	"testing"

	"github.com/alexdx2/chronicle-core/manifest"
	"github.com/alexdx2/chronicle-core/store"
)

// TestOracleExternalURLs_NeverInternalEndpoints is the Task 5 oracle: a
// third-party host (Telegram, Twilio) must never mint a domain-internal
// contract:endpoint: node or show up in known_endpoints — it stays an
// external_system boundary. An unresolved calls_service pseudo-target
// (a string that isn't a URL, e.g. "SupportOutbox") must not leak into HTTP
// reconcile at all. The fact's HTTP method must survive into
// FindUnmatchedHTTPCalls instead of the old hardcoded "GET".
func TestOracleExternalURLs_NeverInternalEndpoints(t *testing.T) {
	g, s, revID := setupTestGraph(t)
	facts := `[
		{"kind":"http_call","method":"POST","target":"https://api.telegram.org/bot<token>/sendMessage"},
		{"kind":"http_call","method":"POST","target":"https://comms.twilio.com/v1/Emails"},
		{"kind":"calls_service","to":"SupportOutbox"}
	]`
	g.SaveFileExtraction(revID, "testapp", "src/notify.ts", "extracted", "provider", facts, "")
	if _, err := g.ResolveExtractions("testapp", revID); err != nil {
		t.Fatal(err)
	}

	nodes, _ := s.ListNodes(store.NodeFilter{})
	for _, n := range nodes {
		if strings.HasPrefix(n.NodeKey, "contract:endpoint:testapp:") &&
			(strings.Contains(n.NodeKey, "sendmessage") || strings.Contains(n.NodeKey, "emails")) {
			t.Errorf("external URL materialized as INTERNAL endpoint: %s", n.NodeKey)
		}
	}
	unmatched, _ := g.FindUnmatchedHTTPCalls("testapp")
	for _, u := range unmatched {
		if u.TargetHost == "SupportOutbox" || u.TargetURL == "SupportOutbox" {
			t.Errorf("calls_service pseudo-target leaked into HTTP reconcile: %+v", u)
		}
		if strings.Contains(u.TargetURL, "twilio") && u.Method != "POST" {
			t.Errorf("method lost: want POST, got %q", u.Method)
		}
	}
}

// TestIsExternalHost_Rule is a direct unit test of the classification helper
// behind the oracle above: relative URLs and hosts matching the domain's own
// infra/services stay internal; a dotted FQDN matching nothing does not.
func TestIsExternalHost_Rule(t *testing.T) {
	own := map[string]bool{"tom-api": true, "api.mycompany.com": true}
	cases := []struct {
		name   string
		target string
		want   bool
	}{
		{"relative path", "/jerry/status", false},
		{"unwrapped env template no scheme", "${TOM_API_URL}/tom/status", false},
		{"declared service host", "http://tom-api:3001/tom/status", false},
		{"declared infra FQDN", "https://api.mycompany.com/webhook/self", false},
		{"bare undeclared host, no dot", "http://battle-svc:9000/battles", false},
		{"third-party dotted host", "https://api.telegram.org/bot<token>/sendMessage", true},
		{"third-party dotted host, no port", "https://comms.twilio.com/v1/Emails", true},
	}
	for _, tc := range cases {
		if got := isExternalHost(own, tc.target); got != tc.want {
			t.Errorf("%s: isExternalHost(%q) = %v, want %v", tc.name, tc.target, got, tc.want)
		}
	}
}

// TestOracleExternalURLs_OwnPublicHostStaysInternal is the Task 5 self-review
// case: a service that calls its OWN public FQDN (not just a bare in-cluster
// service name) must not be misclassified external, as long as that host is
// declared in chronicle.domain.yaml infrastructure — materialized here the
// way discover.go actually creates a manifest infra node (Task 6: 4-segment
// canonical key, raw address preserved on QualifiedName so
// ownHostsForDomain can recover the bare host from it). With the host
// declared, the call still gets its contract:endpoint materialized like any
// other internal call.
func TestOracleExternalURLs_OwnPublicHostStaysInternal(t *testing.T) {
	g, s, revID := setupTestGraph(t)
	entry := manifest.InfraEntry{Name: "public-gateway", Type: "infrastructure", Address: "api.mycompany.com:443"}
	s.UpsertNode(store.NodeRow{
		NodeKey:       canonicalNodeKey(entry.InfraNodeKey("testapp")),
		Layer:         "infra",
		NodeType:      "infrastructure",
		QualifiedName: entry.AddressOrName(),
		DomainKey:     "testapp", Name: entry.Name, Status: "active", LastSeenRevisionID: revID,
	})

	facts := `[{"kind":"http_call","method":"POST","target":"https://api.mycompany.com/webhook/self"}]`
	g.SaveFileExtraction(revID, "testapp", "src/notify.ts", "extracted", "provider", facts, "")
	if _, err := g.ResolveExtractions("testapp", revID); err != nil {
		t.Fatal(err)
	}

	wantKey := "contract:endpoint:testapp:post:/webhook/self"
	if _, err := s.GetNodeByKey(wantKey); err != nil {
		t.Errorf("expected declared own-host call to materialize internal endpoint %q, got err: %v", wantKey, err)
	}
}
