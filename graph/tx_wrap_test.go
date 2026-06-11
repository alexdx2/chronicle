package graph

import (
	"testing"
)

func TestResolveExtractionsRunsInTx(t *testing.T) {
	g := setupGraphDefaults(t)
	revID, err := g.store.CreateRevision("dom", "", "abc", "manual", "full", "{}")
	if err != nil {
		t.Fatal(err)
	}
	if g.store.InTx() {
		t.Fatal("outer store must not be in tx")
	}
	var sawTx bool
	prev := testHookResolveInTx
	testHookResolveInTx = func(inTx bool) { sawTx = inTx }
	defer func() { testHookResolveInTx = prev }()

	if _, err := g.ResolveExtractions("dom", revID); err != nil {
		t.Fatal(err)
	}
	if !sawTx {
		t.Fatal("ResolveExtractions body did not run inside WithTx")
	}
}
