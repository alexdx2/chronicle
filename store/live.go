package store

import (
	"encoding/json"
	"fmt"
	"sync"
	"time"
)

// liveKeepPerKind bounds live_events: every insert keeps the newest rows of
// its kind. Live events are ephemeral — not journaled, not carried by
// journal rebuild, cleared by ResetDB — the dashboard and the agent only
// ever need "recent".
const liveKeepPerKind = 200

// livePruneKeepAge protects a row from pruning while it is younger than
// this, no matter how many rows of its kind accumulate: a tab reporting
// rapidly (e.g. a busy screen every 300ms) must never push another tab's
// still-alive heartbeat out of the table. Must stay longer than
// live.OpenWindow (90s; see live/live.go) so every alive client's latest
// report is always still here when live.State reads it.
const livePruneKeepAge = 120 * time.Second

// LiveEvent is one row of the agent ↔ dashboard channel; package live gives
// it meaning. Payload is the kind's JSON document.
type LiveEvent struct {
	ID      int64           `json:"id"`
	At      string          `json:"at"`
	Kind    string          `json:"kind"`
	Source  string          `json:"source"`
	Payload json.RawMessage `json:"payload"`
	// Repo names the repository whose store the event came from; readers
	// of several stores set it, a single-repo store leaves it empty.
	Repo string `json:"repo,omitempty"`
}

// liveWaker is a broadcast channel: each insert through this handle closes
// the current channel (waking every waiter) and arms a new one.
type liveWaker struct {
	mu sync.Mutex
	ch chan struct{}
}

func newLiveWaker() *liveWaker { return &liveWaker{ch: make(chan struct{})} }

func (w *liveWaker) wait() <-chan struct{} {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.ch
}

func (w *liveWaker) notify() {
	w.mu.Lock()
	close(w.ch)
	w.ch = make(chan struct{})
	w.mu.Unlock()
}

// LiveWake returns a channel closed by the next InsertLiveEvent through this
// handle, after its write. Capture it BEFORE reading the table: an insert
// landing between the read and the wait then still wakes the reader. Nil
// for transaction-scoped stores (their rows are visible only after commit;
// readers fall back to polling).
func (s *Store) LiveWake() <-chan struct{} {
	if s.live == nil {
		return nil
	}
	return s.live.wait()
}

// InsertLiveEvent appends an event, keeps the newest liveKeepPerKind rows of
// its kind, and wakes in-process readers.
func (s *Store) InsertLiveEvent(kind, source string, payload []byte) (LiveEvent, error) {
	if kind == "" {
		return LiveEvent{}, fmt.Errorf("InsertLiveEvent: kind is required")
	}
	if len(payload) == 0 {
		payload = []byte("{}")
	}
	res, err := s.db.Exec(`INSERT INTO live_events (kind, source, payload) VALUES (?, ?, ?)`, kind, source, string(payload))
	if err != nil {
		return LiveEvent{}, fmt.Errorf("InsertLiveEvent: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return LiveEvent{}, fmt.Errorf("InsertLiveEvent: %w", err)
	}
	// Keep the newest liveKeepPerKind rows of the kind, UNION every row
	// younger than livePruneKeepAge: only a row that is BOTH beyond the
	// count boundary AND older than the age boundary is deleted.
	ageCutoff := fmt.Sprintf("-%d seconds", int64(livePruneKeepAge/time.Second))
	if _, err := s.db.Exec(`DELETE FROM live_events WHERE kind = ? AND id <= (
		SELECT id FROM live_events WHERE kind = ? ORDER BY id DESC LIMIT 1 OFFSET ?)
		AND at < strftime('%Y-%m-%dT%H:%M:%fZ', 'now', ?)`,
		kind, kind, liveKeepPerKind, ageCutoff); err != nil {
		return LiveEvent{}, fmt.Errorf("InsertLiveEvent prune: %w", err)
	}
	ev := LiveEvent{ID: id, Kind: kind, Source: source}
	var p string
	if err := s.db.QueryRow(`SELECT at, payload FROM live_events WHERE id = ?`, id).Scan(&ev.At, &p); err != nil {
		return LiveEvent{}, fmt.Errorf("InsertLiveEvent read-back: %w", err)
	}
	ev.Payload = json.RawMessage(p)
	if s.live != nil {
		s.live.notify()
	}
	return ev, nil
}

// ListLiveEvents returns events with id > afterID (of one kind when kind is
// set), ascending, at most limit (default 100).
func (s *Store) ListLiveEvents(afterID int64, kind string, limit int) ([]LiveEvent, error) {
	if limit <= 0 {
		limit = 100
	}
	q := `SELECT id, at, kind, source, payload FROM live_events WHERE id > ?`
	args := []any{afterID}
	if kind != "" {
		q += ` AND kind = ?`
		args = append(args, kind)
	}
	q += ` ORDER BY id ASC LIMIT ?`
	args = append(args, limit)
	return s.queryLiveEvents(q, args...)
}

// RecentLiveEvents returns the newest limit events (of one kind when kind is
// set, default 20), in ascending id order.
func (s *Store) RecentLiveEvents(kind string, limit int) ([]LiveEvent, error) {
	if limit <= 0 {
		limit = 20
	}
	return s.queryLiveEvents(`SELECT id, at, kind, source, payload FROM (
		SELECT id, at, kind, source, payload FROM live_events
		WHERE (? = '' OR kind = ?) ORDER BY id DESC LIMIT ?
	) ORDER BY id ASC`, kind, kind, limit)
}

// LiveEventsSince returns events of kind with at >= since, ascending by id.
// since is formatted to match the sortable ISO-8601 form the at column is
// written in (millisecond precision, UTC, trailing literal "Z"): used by
// live.State to gather every alive-candidate report within OpenWindow
// without the liveKeepPerKind cap dropping a quiet client (F1).
func (s *Store) LiveEventsSince(kind string, since time.Time) ([]LiveEvent, error) {
	return s.queryLiveEvents(`SELECT id, at, kind, source, payload FROM live_events
		WHERE kind = ? AND at >= ? ORDER BY id ASC`, kind, since.UTC().Format("2006-01-02T15:04:05.000Z"))
}

// MaxLiveEventID is the newest event id (0 for an empty table).
func (s *Store) MaxLiveEventID() (int64, error) {
	var id int64
	if err := s.db.QueryRow(`SELECT COALESCE(MAX(id), 0) FROM live_events`).Scan(&id); err != nil {
		return 0, fmt.Errorf("MaxLiveEventID: %w", err)
	}
	return id, nil
}

func (s *Store) queryLiveEvents(q string, args ...any) ([]LiveEvent, error) {
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, fmt.Errorf("live events: %w", err)
	}
	defer rows.Close()
	out := []LiveEvent{}
	for rows.Next() {
		var e LiveEvent
		var p string
		if err := rows.Scan(&e.ID, &e.At, &e.Kind, &e.Source, &p); err != nil {
			return nil, fmt.Errorf("live events: %w", err)
		}
		e.Payload = json.RawMessage(p)
		out = append(out, e)
	}
	return out, rows.Err()
}
