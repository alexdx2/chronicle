package graph

import (
	"strings"
	"testing"

	"github.com/alexdx2/chronicle-core/gitdiff"
	"github.com/alexdx2/chronicle-core/validate"
)

const reviewOldSchema = `
model Battle {
  id       String @id
  winnerId String
  score    Int
}
`

const reviewNewSchema = `
model Battle {
  id    String @id
  score Float
}
`

// seedReviewGraph: Battle model (+fields) anchored at schema.prisma, a
// same-domain writer of winnerId, and a foreign-domain consumer of the model.
func seedReviewGraph(t *testing.T) *Graph {
	t.Helper()
	g := setupGraphDefaults(t)
	revID, _ := g.Store().CreateRevision("orders", "", "sha1", "manual", "full", "{}")

	nodes := []validate.NodeInput{
		{NodeKey: "data:model:orders:battle", Layer: "data", NodeType: "model", DomainKey: "orders", Name: "Battle", FilePath: "prisma/schema.prisma"},
		{NodeKey: "data:field:orders:battle/winner-id", Layer: "data", NodeType: "field", DomainKey: "orders", Name: "Battle.winnerId"},
		{NodeKey: "data:field:orders:battle/score", Layer: "data", NodeType: "field", DomainKey: "orders", Name: "Battle.score"},
		{NodeKey: "code:provider:orders:writer", Layer: "code", NodeType: "provider", DomainKey: "orders", Name: "Writer"},
		{NodeKey: "code:provider:billing:consumer", Layer: "code", NodeType: "provider", DomainKey: "billing", Name: "BillingConsumer"},
	}
	for _, n := range nodes {
		if _, err := g.UpsertNode(n, revID); err != nil {
			t.Fatalf("UpsertNode %s: %v", n.NodeKey, err)
		}
	}
	edges := []validate.EdgeInput{
		{FromNodeKey: "data:model:orders:battle", ToNodeKey: "data:field:orders:battle/winner-id", EdgeType: "HAS_FIELD", DerivationKind: "hard", FromLayer: "data", ToLayer: "data"},
		{FromNodeKey: "data:model:orders:battle", ToNodeKey: "data:field:orders:battle/score", EdgeType: "HAS_FIELD", DerivationKind: "hard", FromLayer: "data", ToLayer: "data"},
		{FromNodeKey: "code:provider:orders:writer", ToNodeKey: "data:field:orders:battle/winner-id", EdgeType: "WRITES_FIELD", DerivationKind: "hard", FromLayer: "code", ToLayer: "data"},
		{FromNodeKey: "code:provider:billing:consumer", ToNodeKey: "data:model:orders:battle", EdgeType: "USES_MODEL", DerivationKind: "hard", FromLayer: "code", ToLayer: "data"},
	}
	for _, e := range edges {
		if _, err := g.UpsertEdge(e, revID); err != nil {
			t.Fatalf("UpsertEdge %s: %v", e.EdgeType, err)
		}
	}
	return g
}

func TestBuildReviewReport(t *testing.T) {
	g := seedReviewGraph(t)

	report, err := g.BuildReviewReport(nil, "orders", ReviewReportOptions{
		Base: "main",
		Changed: []gitdiff.ChangedFile{
			{Path: "prisma/schema.prisma", Status: "M"},
			{Path: "docs/notes.txt", Status: "A"},
		},
		OldPrisma: map[string][]byte{"prisma/schema.prisma": []byte(reviewOldSchema)},
		NewPrisma: map[string][]byte{"prisma/schema.prisma": []byte(reviewNewSchema)},
	})
	if err != nil {
		t.Fatalf("BuildReviewReport: %v", err)
	}

	// Battle entity mapped with two field changes.
	var battle *ReviewEntity
	for i := range report.Entities {
		if report.Entities[i].NodeKey == "data:model:orders:battle" {
			battle = &report.Entities[i]
		}
	}
	if battle == nil {
		t.Fatalf("Battle entity missing; entities: %+v", report.Entities)
	}
	changes := map[string]string{}
	for _, fc := range battle.FieldChanges {
		changes[fc.Model+"."+fc.Field] = fc.Change
	}
	if changes["Battle.winnerId"] != "removed" {
		t.Errorf("winnerId change: got %q want removed (all: %v)", changes["Battle.winnerId"], changes)
	}
	if changes["Battle.score"] != "type_changed" {
		t.Errorf("score change: got %q want type_changed", changes["Battle.score"])
	}

	// Impact: field-precision seed reaches the writer; billing consumer is external.
	if battle.Impact == nil {
		t.Fatal("Battle impact missing")
	}
	impacted := map[string]string{}
	for _, imp := range battle.Impact.Impacts {
		impacted[imp.NodeKey] = imp.Precision
	}
	if prec, ok := impacted["code:provider:orders:writer"]; !ok || prec != "field" {
		t.Errorf("writer: ok=%v precision=%q want field", ok, prec)
	}
	if _, ok := impacted["code:provider:billing:consumer"]; !ok {
		t.Error("billing consumer missing from impact")
	}
	foundExternal := false
	for _, s := range report.ExternalServices {
		if s == "code:provider:billing:consumer" {
			foundExternal = true
		}
	}
	if !foundExternal {
		t.Errorf("billing consumer not in ExternalServices: %v", report.ExternalServices)
	}

	// Unmapped file surfaced explicitly.
	if len(report.Unmapped) != 1 || report.Unmapped[0] != "docs/notes.txt" {
		t.Errorf("unmapped: %v want [docs/notes.txt]", report.Unmapped)
	}

	// Markdown has the honest sections.
	md := report.Markdown()
	for _, want := range []string{"## Changed entities", "## External services affected", "## Unmapped changes", "Battle.winnerId", "impact is UNKNOWN"} {
		if !strings.Contains(md, want) {
			t.Errorf("markdown missing %q", want)
		}
	}
}
