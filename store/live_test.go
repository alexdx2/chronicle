package store

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"testing"
	"time"
)

func TestLiveEvents_InsertListMax(t *testing.T) {
	s := openTestStore(t)
	a, err := s.InsertLiveEvent("diagram", "mcp", []byte(`{"title":"A"}`))
	if err != nil {
		t.Fatalf("insert diagram: %v", err)
	}
	b, err := s.InsertLiveEvent("screen", "dashboard", nil)
	if err != nil {
		t.Fatalf("insert screen: %v", err)
	}
	if a.ID <= 0 || b.ID <= a.ID {
		t.Fatalf("ids must grow: %d then %d", a.ID, b.ID)
	}
	if a.Kind != "diagram" || a.Source != "mcp" || a.At == "" || string(a.Payload) != `{"title":"A"}` {
		t.Fatalf("diagram round-trip: %+v", a)
	}
	if string(b.Payload) != "{}" {
		t.Fatalf("empty payload must be stored as {}: %q", b.Payload)
	}
	all, err := s.ListLiveEvents(0, "", 10)
	if err != nil || len(all) != 2 || all[0].ID != a.ID || all[1].ID != b.ID {
		t.Fatalf("list all = %+v, %v", all, err)
	}
	diagrams, _ := s.ListLiveEvents(0, "diagram", 10)
	if len(diagrams) != 1 || diagrams[0].ID != a.ID {
		t.Fatalf("list diagrams = %+v", diagrams)
	}
	after, _ := s.ListLiveEvents(a.ID, "", 10)
	if len(after) != 1 || after[0].ID != b.ID {
		t.Fatalf("list after %d = %+v", a.ID, after)
	}
	if max, err := s.MaxLiveEventID(); err != nil || max != b.ID {
		t.Fatalf("max = %d, %v; want %d", max, err, b.ID)
	}
	if _, err := s.InsertLiveEvent("", "mcp", nil); err == nil {
		t.Fatal("an event without a kind must be refused")
	}
}

func TestLiveEvents_KeepNewestPerKind(t *testing.T) {
	s := openTestStore(t)
	for i := 0; i < liveKeepPerKind+5; i++ {
		if _, err := s.InsertLiveEvent("screen", "dashboard", []byte(fmt.Sprintf(`{"n":%d}`, i))); err != nil {
			t.Fatal(err)
		}
	}
	d, err := s.InsertLiveEvent("diagram", "mcp", nil)
	if err != nil {
		t.Fatal(err)
	}
	screens, _ := s.ListLiveEvents(0, "screen", 1000)
	if len(screens) != liveKeepPerKind {
		t.Fatalf("screens kept = %d, want %d", len(screens), liveKeepPerKind)
	}
	var first, last struct {
		N int `json:"n"`
	}
	json.Unmarshal(screens[0].Payload, &first)
	json.Unmarshal(screens[len(screens)-1].Payload, &last)
	if first.N != 5 || last.N != liveKeepPerKind+4 {
		t.Fatalf("kept n=%d..%d, want the newest 5..%d", first.N, last.N, liveKeepPerKind+4)
	}
	diagrams, _ := s.ListLiveEvents(0, "diagram", 10)
	if len(diagrams) != 1 || diagrams[0].ID != d.ID {
		t.Fatalf("pruning screens touched diagrams: %+v", diagrams)
	}
}

func TestLiveEvents_RecentIsNewestAscending(t *testing.T) {
	s := openTestStore(t)
	var ids []int64
	for i := 0; i < 5; i++ {
		ev, err := s.InsertLiveEvent("diagram", "mcp", nil)
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, ev.ID)
	}
	if _, err := s.InsertLiveEvent("screen", "dashboard", nil); err != nil {
		t.Fatal(err)
	}
	got, err := s.RecentLiveEvents("diagram", 3)
	if err != nil || len(got) != 3 {
		t.Fatalf("recent = %+v, %v", got, err)
	}
	for i, ev := range got {
		if ev.ID != ids[2+i] {
			t.Fatalf("recent[%d] = %d, want %d (newest three, ascending)", i, ev.ID, ids[2+i])
		}
	}
}

func TestLiveEvents_WakeBroadcast(t *testing.T) {
	s := openTestStore(t)
	w1, w2 := s.LiveWake(), s.LiveWake()
	if _, err := s.InsertLiveEvent("diagram", "mcp", nil); err != nil {
		t.Fatal(err)
	}
	for i, w := range []<-chan struct{}{w1, w2} {
		select {
		case <-w:
		case <-time.After(time.Second):
			t.Fatalf("waiter %d was not woken by the insert", i)
		}
	}
	select {
	case <-s.LiveWake():
		t.Fatal("the next wake channel must stay open until the next insert")
	default:
	}
}

func TestLiveEvents_OtherHandleReadsThem(t *testing.T) {
	path := filepath.Join(t.TempDir(), "shared.db")
	a, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	b, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	ev, err := a.InsertLiveEvent("diagram", "mcp", []byte(`{"title":"x"}`))
	if err != nil {
		t.Fatal(err)
	}
	got, err := b.ListLiveEvents(0, "diagram", 10)
	if err != nil || len(got) != 1 || got[0].ID != ev.ID {
		t.Fatalf("second handle sees %+v, %v", got, err)
	}
}

func TestLiveEvents_ResetClearsAndRestartsIDs(t *testing.T) {
	s := openTestStore(t)
	for i := 0; i < 2; i++ {
		if _, err := s.InsertLiveEvent("diagram", "mcp", nil); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.ResetDB(); err != nil {
		t.Fatalf("reset: %v", err)
	}
	got, err := s.ListLiveEvents(0, "", 10)
	if err != nil || len(got) != 0 {
		t.Fatalf("after reset: %+v, %v", got, err)
	}
	ev, err := s.InsertLiveEvent("diagram", "mcp", nil)
	if err != nil || ev.ID != 1 {
		t.Fatalf("ids restart after reset (readers must cope): id=%d err=%v", ev.ID, err)
	}
}

func TestLiveEvents_NotCarriedByJournalRebuild(t *testing.T) {
	src := openJournalTestStore(t)
	if _, err := src.InsertLiveEvent("diagram", "mcp", nil); err != nil {
		t.Fatal(err)
	}
	dst := openJournalTestStore(t)
	if _, err := dst.CopyLocalState(src); err != nil {
		t.Fatal(err)
	}
	var n int
	dst.QueryRowScan(`SELECT COUNT(*) FROM live_events`, &n)
	if n != 0 {
		t.Fatalf("journal rebuild carried %d live events; they are ephemeral", n)
	}
}
