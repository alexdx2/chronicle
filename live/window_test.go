package live

import (
	"testing"

	"github.com/alexdx2/chronicle-core/store"
)

// The store keeps every report younger than store.LivePruneKeepAge; State
// looks back OpenWindow. The first must outlast the second, or an alive
// client's latest report could be pruned before State reads it.
func TestOpenWindowWithinPruneKeepAge(t *testing.T) {
	if store.LivePruneKeepAge <= OpenWindow {
		t.Fatalf("store.LivePruneKeepAge %v must exceed live.OpenWindow %v", store.LivePruneKeepAge, OpenWindow)
	}
}
