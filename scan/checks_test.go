package scan

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/alexdx2/chronicle-core/graph"
	"github.com/alexdx2/chronicle-core/store"
)

func checksGraph(t *testing.T) (*graph.Graph, *store.Store) {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "c.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return newGraphT(t, s), s
}

func addNode(t *testing.T, s *store.Store, key, typ string) int64 {
	t.Helper()
	id, err := s.UpsertNode(store.NodeRow{NodeKey: key, Layer: "code", NodeType: typ, DomainKey: "d", Name: key, Status: "active"})
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func addEdge(t *testing.T, s *store.Store, from, to int64, fk, tk, typ string) int64 {
	t.Helper()
	id, err := s.UpsertEdge(store.EdgeRow{EdgeKey: fk + "->" + tk + ":" + typ, FromNodeID: from, ToNodeID: to, FromNodeKey: fk, ToNodeKey: tk, EdgeType: typ, DerivationKind: "hard", Active: true})
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func invOf(paths ...string) *Inventory {
	inv := &Inventory{}
	for _, p := range paths {
		inv.Files = append(inv.Files, InvFile{Path: p})
	}
	return inv
}

func byLayer(cs []LayerCheck) map[string]LayerCheck {
	m := map[string]LayerCheck{}
	for _, c := range cs {
		m[c.Layer] = c
	}
	return m
}

func TestLayerChecks_AllNotApplicable(t *testing.T) {
	g, _ := checksGraph(t)
	cs := LayerChecks(g, "d", Profile{}, &FullReport{}, invOf("src/a.ts"))
	var names []string
	for _, c := range cs {
		names = append(names, c.Layer)
		if c.Status != "n/a" {
			t.Errorf("%s: %s, want n/a", c.Layer, c.Status)
		}
	}
	if strings.Join(names, ",") != "data,structure,messaging,integrations,coverage" {
		t.Fatalf("order %v", names)
	}
	if len(checkLimitations(cs)) != 0 {
		t.Fatal("n/a must not be a limitation")
	}
}

func TestLayerChecks_DataStructure(t *testing.T) {
	g, s := checksGraph(t)
	inv := invOf("prisma/schema.prisma", "src/a.controller.ts")
	m := byLayer(LayerChecks(g, "d", Profile{}, nil, inv))
	if m["data"].Status != "failed" || m["structure"].Status != "failed" {
		t.Fatalf("%+v", m)
	}
	addNode(t, s, "m1", "model")
	addNode(t, s, "e1", "endpoint")
	m = byLayer(LayerChecks(g, "d", Profile{}, nil, inv))
	if m["data"].Status != "ok" || m["structure"].Status != "ok" {
		t.Fatalf("%+v", m)
	}
}

func TestLayerChecks_Messaging(t *testing.T) {
	g, s := checksGraph(t)
	prof := Profile{Technologies: []Technology{{ID: "kafkajs", Role: "messaging"}}}
	addNode(t, s, "t1", "topic")
	m := byLayer(LayerChecks(g, "d", prof, nil, nil))
	if m["messaging"].Status != "failed" {
		t.Fatalf("%+v", m["messaging"])
	}
	svc := addNode(t, s, "svc", "service")
	tp := addNode(t, s, "t2", "topic")
	addEdge(t, s, svc, tp, "svc", "t2", "PUBLISHES_TOPIC")
	m = byLayer(LayerChecks(g, "d", prof, nil, nil))
	if m["messaging"].Status != "ok" {
		t.Fatalf("%+v", m["messaging"])
	}
	lim := checkLimitations([]LayerCheck{{Layer: "messaging", Status: "failed", Detail: "x"}, {Layer: "data", Status: "ok", Detail: "y"}})
	if len(lim) != 1 || lim[0] != "layer messaging: x" {
		t.Fatalf("%v", lim)
	}
}

func TestLayerChecks_IntegrationsUnmatched(t *testing.T) {
	g, s := checksGraph(t)
	ext := addNode(t, s, "service:external:d:ext", "service")
	ep := addNode(t, s, "ep", "endpoint")
	for _, k := range []string{"c1", "c2", "c3"} {
		c := addNode(t, s, k, "method")
		id := addEdge(t, s, c, ext, k, "service:external:d:ext", "CALLS_SERVICE")
		if _, err := s.AddEvidence(store.EvidenceRow{TargetKind: "edge", EdgeID: id, SourceKind: "llm", ExtractorID: "t", ExtractorVersion: "1", ObservedAt: "2026-01-01T00:00:00Z", Assertion: `{"url":"http://ext/orders","method":"GET"}`}); err != nil {
			t.Fatal(err)
		}
		if k == "c3" {
			addEdge(t, s, c, ep, k, "ep", "CALLS_ENDPOINT")
		}
	}
	c := byLayer(LayerChecks(g, "d", Profile{}, nil, nil))["integrations"]
	if c.Status != "failed" || !strings.Contains(c.Detail, "67% unmatched") {
		t.Fatalf("%+v", c)
	}
}

func TestLayerChecks_Coverage(t *testing.T) {
	g, _ := checksGraph(t)
	rep := &FullReport{Domains: []DomainReport{{Domain: "d", Sent: 10}}, Failed: []FailedFile{{Path: "a"}, {Path: "b"}}}
	if c := byLayer(LayerChecks(g, "d", Profile{}, rep, nil))["coverage"]; c.Status != "failed" {
		t.Fatalf("%+v", c)
	}
	rep.Failed = nil
	if c := byLayer(LayerChecks(g, "d", Profile{}, rep, nil))["coverage"]; c.Status != "ok" {
		t.Fatalf("%+v", c)
	}
}
