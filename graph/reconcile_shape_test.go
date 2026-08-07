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
