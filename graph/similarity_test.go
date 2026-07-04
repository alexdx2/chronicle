package graph

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alexdx2/chronicle-core/store"
	"github.com/alexdx2/chronicle-core/validate"
)

const cloneA = `
export class OrderService {
  constructor(private repo: OrderRepo, private bus: EventBus) {}
  async create(dto: CreateOrderDto) {
    const order = await this.repo.save(dto);
    await this.bus.publish('order.created', order);
    return order;
  }
  async cancel(id: string) {
    const order = await this.repo.get(id);
    order.status = 'cancelled';
    await this.repo.save(order);
    return order;
  }
}
`

// cloneB is cloneA with renamed class + one literal tweaked — a classic
// copy-paste twin.
const cloneB = `
export class InvoiceService {
  constructor(private repo: OrderRepo, private bus: EventBus) {}
  async create(dto: CreateOrderDto) {
    const order = await this.repo.save(dto);
    await this.bus.publish('invoice.created', order);
    return order;
  }
  async cancel(id: string) {
    const order = await this.repo.get(id);
    order.status = 'cancelled';
    await this.repo.save(order);
    return order;
  }
}
`

const unrelated = `
import { readFileSync } from 'fs';
export function loadConfig(path: string): Config {
  const raw = readFileSync(path, 'utf-8');
  return JSON.parse(raw) as Config;
}
`

func TestMinhashSimilarity(t *testing.T) {
	sigA := minhashSignature([]byte(cloneA))
	sigB := minhashSignature([]byte(cloneB))
	sigU := minhashSignature([]byte(unrelated))

	if self := jaccardEstimate(sigA, sigA); self != 1.0 {
		t.Fatalf("self-similarity = %v, want 1.0", self)
	}
	twin := jaccardEstimate(sigA, sigB)
	if twin < similarityThreshold {
		t.Fatalf("copy-paste twins should clear the threshold, jaccard = %v", twin)
	}
	far := jaccardEstimate(sigA, sigU)
	if far >= similarityThreshold {
		t.Fatalf("unrelated code must stay below threshold, jaccard = %v", far)
	}
	// Deterministic across calls.
	if jaccardEstimate(sigA, minhashSignature([]byte(cloneA))) != 1.0 {
		t.Fatalf("minhash must be deterministic")
	}
}

// TestComputeSimilarityEndToEnd proves near-duplicate units get a SIMILAR_TO
// edge with the jaccard score in metadata + heuristic evidence, unrelated units
// don't, and re-runs are idempotent.
func TestComputeSimilarityEndToEnd(t *testing.T) {
	dir := t.TempDir()
	write := func(rel, content string) string {
		t.Helper()
		p := filepath.Join(dir, rel)
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	pa := write("order.service.ts", cloneA)
	pb := write("invoice.service.ts", cloneB)
	pu := write("config.ts", unrelated)

	g := setupGraphDefaults(t)
	revID := makeRevision(t, g)
	for key, fp := range map[string]string{
		"code:provider:orders:order-svc":   pa,
		"code:provider:orders:invoice-svc": pb,
		"code:provider:orders:config":      pu,
	} {
		name := key[strings.LastIndex(key, ":")+1:]
		if _, err := g.UpsertNode(validate.NodeInput{
			NodeKey: key, Layer: "code", NodeType: "provider", DomainKey: "orders",
			Name: name, FilePath: fp,
		}, revID); err != nil {
			t.Fatalf("UpsertNode %s: %v", key, err)
		}
	}

	if err := g.ComputeSimilarity(revID); err != nil {
		t.Fatalf("ComputeSimilarity: %v", err)
	}

	active := true
	edges, err := g.store.ListEdges(store.EdgeFilter{EdgeType: "SIMILAR_TO", Active: &active})
	if err != nil {
		t.Fatalf("ListEdges: %v", err)
	}
	if len(edges) != 1 {
		t.Fatalf("want exactly 1 SIMILAR_TO edge (the twins), got %d", len(edges))
	}
	if !strings.Contains(edges[0].Metadata, `"jaccard":`) {
		t.Errorf("edge metadata should carry the jaccard score: %q", edges[0].Metadata)
	}
	for _, e := range edges {
		if strings.Contains(e.FromNodeKey, "config") || strings.Contains(e.ToNodeKey, "config") {
			t.Fatalf("unrelated node must not appear in SIMILAR_TO: %s -> %s", e.FromNodeKey, e.ToNodeKey)
		}
	}

	if err := g.ComputeSimilarity(revID); err != nil {
		t.Fatalf("ComputeSimilarity (2nd): %v", err)
	}
	edges2, _ := g.store.ListEdges(store.EdgeFilter{EdgeType: "SIMILAR_TO", Active: &active})
	if len(edges2) != 1 {
		t.Fatalf("re-run duplicated SIMILAR_TO edges: %d", len(edges2))
	}
}
