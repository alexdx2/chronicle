package admin

import (
	"context"
	"net/http"
	"sync"

	dashboard "github.com/alexdx2/chronicle-core/admin"
	"github.com/alexdx2/chronicle-core/live"
	"github.com/alexdx2/chronicle-core/store"
)

// liveTail owns the running diagram tail (restarted on project switch).
type liveTail struct {
	mu     sync.Mutex
	cancel context.CancelFunc
	tailer *live.Tailer
}

// startLiveTail (re)starts the diagram tail on the current store; a running
// tail is stopped and joined first.
func (s *Server) startLiveTail() {
	s.live.mu.Lock()
	defer s.live.mu.Unlock()
	s.stopLiveTailLocked()
	ctx, cancel := context.WithCancel(context.Background())
	t := live.NewTailer([]live.Source{{Store: s.getStore()}}, func(ev store.LiveEvent) {
		s.hub.Send("live", ev)
	})
	t.Start(ctx)
	s.live.cancel, s.live.tailer = cancel, t
}

func (s *Server) stopLiveTail() {
	s.live.mu.Lock()
	defer s.live.mu.Unlock()
	s.stopLiveTailLocked()
}

func (s *Server) stopLiveTailLocked() {
	if s.live.cancel == nil {
		return
	}
	s.live.cancel()
	s.live.tailer.Wait()
	s.live.cancel, s.live.tailer = nil, nil
}

// restartLiveTail follows a project switch when a tail is running.
func (s *Server) restartLiveTail() {
	s.live.mu.Lock()
	running := s.live.cancel != nil
	s.live.mu.Unlock()
	if running {
		s.startLiveTail()
	}
}

// handleLive serves the dashboard's diagram feed (checks: dashboard.ParseLiveQuery).
func (s *Server) handleLive(w http.ResponseWriter, r *http.Request) {
	q, ok := dashboard.ParseLiveQuery(w, r)
	if !ok {
		return
	}
	var evs []store.LiveEvent
	var err error
	if q.HasAfter {
		evs, err = s.getStore().ListLiveEvents(q.After, live.KindDiagram, q.Limit)
	} else {
		evs, err = s.getStore().RecentLiveEvents(live.KindDiagram, q.Limit)
	}
	if err != nil {
		httpError(w, err, http.StatusInternalServerError)
		return
	}
	dashboard.WritePrivateJSON(w, map[string]any{"events": evs})
}

// handleLiveScreen records what the dashboard shows (checks: dashboard.ScreenReport).
func (s *Server) handleLiveScreen(w http.ResponseWriter, r *http.Request) {
	sc, ok := dashboard.ScreenReport(w, r)
	if !ok {
		return
	}
	if _, err := live.ReportScreen(s.getStore(), sc); err != nil {
		httpError(w, err, http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
