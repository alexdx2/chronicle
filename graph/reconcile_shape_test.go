package graph

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

// TestReconcilePayloadShape is the Task 9 RED/GREEN test for the
// endpoint_reconcile payload blowup that hit 422KB in the field: the FULL
// domain endpoint list used to be stamped on EVERY unmatched item
// (O(endpoints × unmatched)). The fix narrows each item's own Endpoints to
// the endpoints exposed by ITS target controller (the previously-dead
// controllerEndpoints grouping), and moves the full domain list to ONE
// sibling field on the action payload (KnownEndpoints), set once
// (O(endpoints + unmatched)).
func TestReconcilePayloadShape(t *testing.T) {
	g, _, revID := setupTestGraph(t)

	// 3 controllers x 10 endpoints each = 30 endpoints total, one per host.
	hosts := []string{"svc-a", "svc-b", "svc-c"}
	for _, host := range hosts {
		var facts []Fact
		for i := 0; i < 10; i++ {
			facts = append(facts, Fact{
				Kind: "endpoint", Method: "GET",
				Target: fmt.Sprintf("/%s/item%d", host, i), FromType: "controller",
			})
		}
		factsJSON, err := json.Marshal(facts)
		if err != nil {
			t.Fatal(err)
		}
		g.SaveFileExtraction(revID, "testapp", host+"/src/"+host+".controller.ts", "extracted", "", string(factsJSON), "")

		// One unmatched call per host. The target path carries a digit
		// segment ("item-999") so it (a) doesn't literally match any of the
		// 10 static controller endpoints above and (b) is treated as a
		// dynamic/ID-bearing path by the resolver, so it is NOT
		// auto-materialized into a boundary endpoint (which would silently
		// give the client a CALLS_ENDPOINT edge and drop it from
		// FindUnmatchedHTTPCalls entirely — see materializeExternalEndpoints
		// + pathLooksDynamic).
		callFacts, err := json.Marshal([]Fact{
			{Kind: "http_call", From: host + "Client.unknown", FromType: "provider",
				Target: fmt.Sprintf("http://%s:3000/%s/item-999", host, host), Method: "GET"},
		})
		if err != nil {
			t.Fatal(err)
		}
		g.SaveFileExtraction(revID, "testapp", host+"-client/src/"+host+".client.ts", "extracted", "", string(callFacts), "")
	}

	if _, err := g.ResolveExtractions("testapp", revID); err != nil {
		t.Fatalf("ResolveExtractions: %v", err)
	}

	// Sentinel: an endpoint node exposed by NO controller (no
	// EXPOSES_ENDPOINT edge reaches it) — simulates an endpoint the graph
	// knows about that isn't owned by any single controller. It must appear
	// in the domain-wide list exactly once, and never inside a per-item
	// narrow list.
	sentinelKey, sentinelName := normalizeEndpointKey("testapp", "GET", "/orphan/sentinel")
	g.ensureNodeID("testapp", revID, sentinelKey, sentinelName, "")

	unmatched, allEndpoints := g.FindUnmatchedHTTPCalls("testapp")
	if len(unmatched) != 3 {
		t.Fatalf("expected 3 unmatched calls, got %d: %+v", len(unmatched), unmatched)
	}
	if len(allEndpoints) != 31 {
		t.Fatalf("expected 31 known endpoints (30 controller + 1 sentinel), got %d: %v", len(allEndpoints), allEndpoints)
	}

	// This is the real action payload shape: EndpointReconcile carries the
	// per-item narrow lists, KnownEndpoints carries the full list once.
	action := &ScanAction{
		EndpointReconcile: unmatched,
		KnownEndpoints:    allEndpoints,
	}
	payload, err := json.Marshal(action)
	if err != nil {
		t.Fatalf("marshal action payload: %v", err)
	}
	payloadStr := string(payload)

	sentinelLiteral := fmt.Sprintf("%q", sentinelName)
	if count := strings.Count(payloadStr, sentinelLiteral); count != 1 {
		t.Errorf("expected sentinel endpoint %s exactly once in serialized payload, got %d\npayload=%s",
			sentinelLiteral, count, payloadStr)
	}

	// Each unmatched item's own endpoint set, for the subset check below.
	own := map[string]map[string]bool{}
	for _, host := range hosts {
		set := map[string]bool{}
		for i := 0; i < 10; i++ {
			set[fmt.Sprintf("GET /%s/item%d", host, i)] = true
		}
		own[host] = set
	}

	for _, u := range unmatched {
		ownSet, ok := own[u.TargetHost]
		if !ok {
			t.Fatalf("unmatched call has unexpected target host %q", u.TargetHost)
		}
		if len(u.Endpoints) == 0 {
			t.Errorf("expected narrow endpoints for host %s, got none", u.TargetHost)
		}
		if len(u.Endpoints) > len(ownSet) {
			t.Errorf("per-item endpoints not narrow: host=%s got %d entries, its controller only exposes %d (domain has %d total)",
				u.TargetHost, len(u.Endpoints), len(ownSet), len(allEndpoints))
		}
		for _, ep := range u.Endpoints {
			if !ownSet[ep] {
				t.Errorf("host %s: endpoint %q leaked from a different controller (not one of its own %d)",
					u.TargetHost, ep, len(ownSet))
			}
		}
	}
}

// TestReconcilePayloadShape_DomainScoped is the RED/GREEN test for the Task 9
// review finding: narrowEndpointsForHost/controllerEndpoints had no domain
// scope. EXPOSES_ENDPOINT edges are read via
// ListEdges(EdgeFilter{EdgeType:"EXPOSES_ENDPOINT"}) with no domain filter,
// which spans EVERY domain in the shared store — and multi-domain manifests
// scan domains sequentially into the SAME .depbot/chronicle.db
// (testdata/manifest/multi_domain.yaml). Two domains whose controllers
// happen to live under the same directory name (a realistic coincidence —
// "gateway/", "api/", "svc-shared/" repeat across services) collide on
// controllerHostToken; pre-fix, domain B's narrow endpoint list picked up
// domain A's endpoints too, and calls_endpoint facts against a leaked
// candidate would mint a phantom contract:endpoint node under the wrong
// domain.
func TestReconcilePayloadShape_DomainScoped(t *testing.T) {
	g, s, _ := setupTestGraph(t)

	revA, err := s.CreateRevision("domain-a", "scan_a", "", "manual", "full", "{}")
	if err != nil {
		t.Fatal(err)
	}
	revB, err := s.CreateRevision("domain-b", "scan_b", "", "manual", "full", "{}")
	if err != nil {
		t.Fatal(err)
	}

	// Both domains' controllers live under the SAME directory name
	// ("svc-shared/") — the host-token collision that used to leak across
	// domains. Endpoint names carry a domain-prefix ("a-item"/"b-item") so a
	// leak is unambiguous in assertions.
	seedDomain := func(domainKey string, revID int64, prefix string) {
		var endpointFacts []Fact
		for i := 0; i < 3; i++ {
			endpointFacts = append(endpointFacts, Fact{
				Kind: "endpoint", Method: "GET",
				Target: fmt.Sprintf("/svc-shared/%s-item%d", prefix, i), FromType: "controller",
			})
		}
		factsJSON, err := json.Marshal(endpointFacts)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := g.SaveFileExtraction(revID, domainKey, "svc-shared/src/"+prefix+".controller.ts", "extracted", "", string(factsJSON), ""); err != nil {
			t.Fatal(err)
		}

		// One unmatched call per domain, targeting the shared host token. The
		// digit-bearing path ("item-999") keeps it out of auto-materialization
		// (see the note in TestReconcilePayloadShape above) so it survives to
		// FindUnmatchedHTTPCalls.
		callFacts, err := json.Marshal([]Fact{
			{Kind: "http_call", From: prefix + "Client.unknown", FromType: "provider",
				Target: "http://svc-shared:3000/svc-shared/item-999", Method: "GET"},
		})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := g.SaveFileExtraction(revID, domainKey, prefix+"-client/src/"+prefix+".client.ts", "extracted", "", string(callFacts), ""); err != nil {
			t.Fatal(err)
		}

		if _, err := g.ResolveExtractions(domainKey, revID); err != nil {
			t.Fatalf("ResolveExtractions(%s): %v", domainKey, err)
		}
	}

	seedDomain("domain-a", revA, "a")
	seedDomain("domain-b", revB, "b")

	unmatchedB, allEndpointsB := g.FindUnmatchedHTTPCalls("domain-b")

	// KnownEndpoints (the full domain list) must be domain-b-only.
	for _, ep := range allEndpointsB {
		if strings.Contains(ep, "a-item") {
			t.Errorf("domain-b KnownEndpoints leaked a domain-a endpoint: %q (full list=%v)", ep, allEndpointsB)
		}
	}
	if len(allEndpointsB) != 3 {
		t.Errorf("expected 3 domain-b known endpoints, got %d: %v", len(allEndpointsB), allEndpointsB)
	}

	if len(unmatchedB) != 1 {
		t.Fatalf("expected 1 unmatched call for domain-b, got %d: %+v", len(unmatchedB), unmatchedB)
	}
	// The narrow per-item list must be domain-b-only too.
	for _, ep := range unmatchedB[0].Endpoints {
		if strings.Contains(ep, "a-item") {
			t.Errorf("domain-b narrow Endpoints leaked a domain-a endpoint: %q (list=%v)", ep, unmatchedB[0].Endpoints)
		}
		if !strings.Contains(ep, "b-item") {
			t.Errorf("domain-b narrow Endpoints contains unexpected entry: %q", ep)
		}
	}
}

// TestReconcilePayloadKeysAreDistinct: the endpoint_reconcile payload carries
// two endpoint lists at two different scopes, and they must not share a JSON
// key. Per item, "known_endpoints" is the NARROW candidate set (only the
// endpoints of controllers matching that call's target host) and is
// legitimately empty when nothing narrowed; on the action payload,
// "domain_known_endpoints" is the whole domain, once.
//
// Both spelled "known_endpoints" — the shape this branch shipped with —
// leaves the agent unable to tell which scope it is reading, at exactly the
// moment the narrow one is empty and it needs the wide one.
func TestReconcilePayloadKeysAreDistinct(t *testing.T) {
	action := &ScanAction{
		Phase:  "endpoint_reconcile",
		Action: "reconcile_endpoints",
		EndpointReconcile: []UnmatchedHTTPCall{{
			FromName: "UsersClient", TargetHost: "users-api", Method: "GET", Path: "/v1/users/usr-1",
			Endpoints: nil, // narrow list empty — target didn't narrow to a controller
		}},
		KnownEndpoints: []string{"GET /v1/users", "GET /v1/users/:id"},
	}
	blob, err := json.Marshal(action)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var decoded map[string]any
	if err := json.Unmarshal(blob, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	wide, ok := decoded["domain_known_endpoints"].([]any)
	if !ok || len(wide) != 2 {
		t.Fatalf("action payload must expose the full domain list under \"domain_known_endpoints\"; got %v", decoded["domain_known_endpoints"])
	}
	if _, clash := decoded["known_endpoints"]; clash {
		t.Errorf("action payload still has a top-level \"known_endpoints\" key — it collides with the per-item narrow list: %s", blob)
	}

	items, ok := decoded["endpoint_reconcile"].([]any)
	if !ok || len(items) != 1 {
		t.Fatalf("endpoint_reconcile missing from payload: %s", blob)
	}
	item, _ := items[0].(map[string]any)
	if _, ok := item["known_endpoints"]; !ok {
		t.Errorf("per-item narrow list must stay under \"known_endpoints\": %s", blob)
	}
}
