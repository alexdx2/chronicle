package live

import (
	"context"
	"log"
	"sync"
	"time"

	"github.com/alexdx2/chronicle-core/store"
)

const (
	// pollInterval bounds cross-process latency: another process's inserts
	// wake nobody here, the tick picks them up.
	pollInterval = 500 * time.Millisecond
	tailBatch    = 100
)

// Tailer forwards new diagram events of its sources to send. Screen reports
// are never forwarded: they leave the store only through
// chronicle_dashboard_state.
type Tailer struct {
	sources []Source
	send    func(store.LiveEvent)
	logf    func(format string, args ...any)
	wg      sync.WaitGroup
	// afterDrain runs between the drain and the wait; tests use it to land
	// an insert exactly in the window a lost wake would hide.
	afterDrain func()
}

// NewTailer tails sources into send (which must be safe for concurrent use:
// every source runs in its own goroutine).
func NewTailer(sources []Source, send func(store.LiveEvent)) *Tailer {
	return &Tailer{sources: sources, send: send, logf: log.Printf}
}

// Start takes each source's starting point now — events already stored are
// history the dashboard loads itself — and tails in the background until
// ctx is done.
func (t *Tailer) Start(ctx context.Context) {
	for _, src := range t.sources {
		if src.Store == nil {
			continue
		}
		last, err := src.Store.MaxLiveEventID()
		if err != nil {
			last = -1 // resolved inside the loop
		}
		t.wg.Add(1)
		go t.tail(ctx, src, last)
	}
}

// Wait blocks until every tail goroutine has exited; cancel the Start
// context first.
func (t *Tailer) Wait() {
	t.wg.Wait()
}

func (t *Tailer) tail(ctx context.Context, src Source, last int64) {
	defer t.wg.Done()
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()
	var lastLog time.Time
	logf := func(format string, args ...any) {
		if time.Since(lastLog) >= time.Minute {
			t.logf("live tail %q: "+format, append([]any{src.Repo}, args...)...)
			lastLog = time.Now()
		}
	}
	for last < 0 {
		max, err := src.Store.MaxLiveEventID()
		if err == nil {
			last = max
			break
		}
		logf("%v", err)
		select {
		case <-ticker.C:
		case <-ctx.Done():
			return
		}
	}
	for {
		wake := src.Store.LiveWake() // 1. capture BEFORE reading
		for {                        // 2. drain what is already stored
			evs, err := src.Store.ListLiveEvents(last, KindDiagram, tailBatch)
			if err != nil {
				logf("%v", err)
				break
			}
			for _, ev := range evs {
				ev.Repo = src.Repo
				t.send(ev)
				last = ev.ID
			}
			if len(evs) < tailBatch {
				break
			}
		}
		// The table was reset (ResetDB restarts ids at 1): follow it.
		if max, err := src.Store.MaxLiveEventID(); err == nil && max < last {
			last = 0
			continue
		}
		if t.afterDrain != nil {
			t.afterDrain()
		}
		select { // 3. wait on the channel captured in step 1
		case <-wake:
		case <-ticker.C:
		case <-ctx.Done():
			return
		}
	}
}
