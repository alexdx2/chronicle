package live

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/alexdx2/chronicle-core/store"
)

func title(t *testing.T, ev store.LiveEvent) string {
	t.Helper()
	var d Diagram
	if err := json.Unmarshal(ev.Payload, &d); err != nil {
		t.Fatalf("payload: %v", err)
	}
	return d.Title
}

func TestTailer_OwnInsertIsImmediate(t *testing.T) {
	st := openStore(t)
	got := make(chan store.LiveEvent, 8)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	NewTailer([]Source{{Repo: "r", Store: st}}, func(ev store.LiveEvent) { got <- ev }).Start(ctx)
	if _, err := PublishDiagram(st, SourceMCP, Diagram{Title: "x"}); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	select {
	case ev := <-got:
		if d := time.Since(start); d > 50*time.Millisecond {
			t.Fatalf("delivered after %v", d)
		}
		if ev.Repo != "r" || ev.Kind != KindDiagram {
			t.Fatalf("event = %+v", ev)
		}
	case <-time.After(time.Second):
		t.Fatal("not delivered")
	}
}

// An insert landing between the drain and the wait must still wake the
// tailer at once: 200 rounds, each insert made exactly in that window.
func TestTailer_NoLostWakeBetweenDrainAndWait(t *testing.T) {
	st := openStore(t)
	got := make(chan store.LiveEvent, 256)
	tl := NewTailer([]Source{{Store: st}}, func(ev store.LiveEvent) { got <- ev })
	var mu sync.Mutex
	inserted := map[int64]time.Time{}
	n := 0
	tl.afterDrain = func() {
		if n >= 200 {
			return
		}
		n++
		ev, err := PublishDiagram(st, SourceMCP, Diagram{Title: fmt.Sprint(n)})
		if err != nil {
			t.Error(err)
			return
		}
		mu.Lock()
		inserted[ev.ID] = time.Now()
		mu.Unlock()
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	tl.Start(ctx)
	for i := 0; i < 200; i++ {
		select {
		case ev := <-got:
			mu.Lock()
			at := inserted[ev.ID]
			mu.Unlock()
			if d := time.Since(at); d > 50*time.Millisecond {
				t.Fatalf("event %d delivered after %v — a wake was lost between drain and wait", ev.ID, d)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("only %d of 200 events delivered", i)
		}
	}
}

func TestTailer_OtherHandleWithinASecond(t *testing.T) {
	path := filepath.Join(t.TempDir(), "x.db")
	a, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	b, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	got := make(chan store.LiveEvent, 8)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	NewTailer([]Source{{Store: a}}, func(ev store.LiveEvent) { got <- ev }).Start(ctx)
	if _, err := PublishDiagram(b, SourceMCP, Diagram{Title: "from another process"}); err != nil {
		t.Fatal(err)
	}
	select {
	case ev := <-got:
		if title(t, ev) != "from another process" {
			t.Fatalf("event = %+v", ev)
		}
	case <-time.After(time.Second):
		t.Fatal("an insert through another handle did not arrive within a second")
	}
}

func TestTailer_ForwardsOnlyNewDiagrams(t *testing.T) {
	st := openStore(t)
	PublishDiagram(st, SourceMCP, Diagram{Title: "history"})
	got := make(chan store.LiveEvent, 8)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	NewTailer([]Source{{Store: st}}, func(ev store.LiveEvent) { got <- ev }).Start(ctx)
	ReportScreen(st, Screen{ClientID: "a"})
	PublishDiagram(st, SourceMCP, Diagram{Title: "new"})
	select {
	case ev := <-got:
		if title(t, ev) != "new" {
			t.Fatalf("first event = %q", title(t, ev))
		}
	case <-time.After(time.Second):
		t.Fatal("the new diagram did not arrive")
	}
	select {
	case ev := <-got:
		t.Fatalf("unexpected event %+v (history or a screen report)", ev)
	case <-time.After(700 * time.Millisecond):
	}
}

func TestTailer_FollowsAResetTable(t *testing.T) {
	st := openStore(t)
	for i := 0; i < 3; i++ {
		PublishDiagram(st, SourceMCP, Diagram{Title: "old"})
	}
	got := make(chan store.LiveEvent, 8)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	NewTailer([]Source{{Store: st}}, func(ev store.LiveEvent) { got <- ev }).Start(ctx)
	if err := st.ResetDB(); err != nil {
		t.Fatal(err)
	}
	PublishDiagram(st, SourceMCP, Diagram{Title: "after reset"})
	select {
	case ev := <-got:
		if title(t, ev) != "after reset" || ev.ID != 1 {
			t.Fatalf("event = %+v", ev)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("a reset table stalled the tail")
	}
}
