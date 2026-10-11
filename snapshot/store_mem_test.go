package snapshot_test

import (
	"testing"
	"time"

	"github.com/alexdx2/chronicle-core/snapshot"
	"github.com/alexdx2/chronicle-core/snapshot/storetest"
)

func TestMemStore_Contract(t *testing.T) {
	now := time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC)
	storetest.Run(t, storetest.Harness{
		New: func(t *testing.T) snapshot.Store {
			now = time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC)
			return snapshot.NewMemStoreClock(func() time.Time { return now })
		},
		Advance: func(t *testing.T, st snapshot.Store, d time.Duration) { now = now.Add(d) },
	})
}

func TestMemStore_GetReturnsCopies(t *testing.T) {
	st := snapshot.NewMemStore()
	f := storetest.Sample("a")
	if _, err := st.Put(t.Context(), "r", storetest.Sha(1), f, snapshot.PutOptions{}); err != nil {
		t.Fatal(err)
	}
	got, _, _ := st.Get(t.Context(), "r", storetest.Sha(1))
	got.Graph[0] = 'X'
	again, e, err := st.Get(t.Context(), "r", storetest.Sha(1))
	if err != nil || again.Graph[0] == 'X' {
		t.Fatalf("stored bytes were mutated through a Get result: %v", err)
	}
	e.Labels[0] = "zzz"
	_, e2, _ := st.Get(t.Context(), "r", storetest.Sha(1))
	if e2.Labels[0] != snapshot.LabelHead {
		t.Fatal("stored labels were mutated through a Get result")
	}
}
