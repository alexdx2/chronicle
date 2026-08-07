package graph

import (
	"strings"
	"testing"

	"github.com/alexdx2/chronicle-core/store"
)

// SQ-Contract 1: one npm package is one node, regardless of which fact kind
// (import vs injects) observed it, and regardless of import subpath.
// RED against current emission: inferNameFromImport strips the npm scope
// for import facts (bare "ui" node) while the injects last-resort keeps the
// name verbatim ("@okeep/ui" node) — two key shapes for one package.
func TestOraclePackageIdentity_OneNodePerPackage(t *testing.T) {
	g, s, revID := setupTestGraph(t)
	facts := `[
		{"kind":"import","symbols":["Button"],"to":"@okeep/ui/button"},
		{"kind":"import","symbols":["Input"],"to":"@okeep/ui/forms/input"},
		{"kind":"injects","to":"@okeep/ui"}
	]`
	g.SaveFileExtraction(revID, "testapp", "src/app/page.tsx", "extracted", "provider", facts, "")
	if _, err := g.ResolveExtractions("testapp", revID); err != nil {
		t.Fatal(err)
	}
	nodes, _ := s.ListNodes(store.NodeFilter{})
	// Match on the node key's final key segment (after the last ":"), not a
	// substring of the whole key: a naive `strings.Contains(key, "okeep")`
	// filter is a false negative here — the pre-fix bug produces a SECOND,
	// bare "ui" node whose key has already had the "@okeep/" scope
	// stripped, so it contains no "okeep" substring at all and would
	// silently evade a Contains-based filter, defeating the oracle.
	var pkgNodes []string
	for _, n := range nodes {
		i := strings.LastIndex(n.NodeKey, ":")
		if i < 0 {
			continue
		}
		last := n.NodeKey[i+1:]
		if last == "ui" || last == "@okeep/ui" {
			pkgNodes = append(pkgNodes, n.NodeKey)
		}
	}
	if len(pkgNodes) != 1 || !strings.HasSuffix(pkgNodes[0], ":@okeep/ui") {
		t.Fatalf("want exactly one package node ...:@okeep/ui, got %v", pkgNodes)
	}
}

// SQ-Contract 1: the publisher's npm name must be registered as a "name"
// alias at node CREATE time, not only when a fact later merges into an
// existing service node. Today declares_service's new-node branch (:1683)
// skips the alias registration that the merge branch (:1672) performs.
func TestOraclePackageIdentity_PublisherAliasOnCreate(t *testing.T) {
	g, s, revID := setupTestGraph(t)
	g.SaveFileExtraction(revID, "testapp", "packages/ui/package.json", "extracted", "manifest",
		`[{"kind":"declares_service","to":"@okeep/ui"}]`, "")
	g.ResolveExtractions("testapp", revID)
	n, err := s.GetNodeByKey("service:service:testapp:@okeep/ui")
	if err != nil {
		t.Fatalf("service node: %v", err)
	}
	aliases, _ := s.ListAliasesByNode(n.NodeID)
	found := false
	for _, a := range aliases {
		if a.AliasKind == "name" {
			found = true
		}
	}
	if !found {
		t.Fatal("npm name must be registered as alias at CREATE, not only on merge")
	}
}
