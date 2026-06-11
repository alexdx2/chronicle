// Package diagrams holds the in-process registry of diagram sessions.
//
// Sessions are deliberately NOT persisted: a diagram is a working artifact of
// the current conversation (Claude builds it, the user looks at it), not
// knowledge. Restarting the server starts with a clean slate, so the
// dashboard's Live Session tab only ever shows diagrams from this run.
// Both the MCP chronicle_diagram_build tool and the admin HTTP endpoints
// read and write the same registry (they live in one process).
package diagrams

import (
	"sort"
	"sync"
	"time"
)

// Session is one stored diagram: an opaque JSON blob plus list metadata.
type Session struct {
	ID        string
	Title     string
	Data      string // marshaled session JSON, served verbatim
	CreatedAt time.Time
	UpdatedAt time.Time
}

// Registry is a concurrency-safe in-memory session store.
type Registry struct {
	mu sync.RWMutex
	m  map[string]*Session
}

// Default is the process-wide registry shared by MCP tools and the admin server.
var Default = NewRegistry()

func NewRegistry() *Registry {
	return &Registry{m: make(map[string]*Session)}
}

// Save upserts a session, preserving CreatedAt on update.
func (r *Registry) Save(id, title, data string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := time.Now().UTC()
	if existing, ok := r.m[id]; ok {
		existing.Title = title
		existing.Data = data
		existing.UpdatedAt = now
		return
	}
	r.m[id] = &Session{ID: id, Title: title, Data: data, CreatedAt: now, UpdatedAt: now}
}

// Get returns the title and raw JSON for a session.
func (r *Registry) Get(id string) (title, data string, ok bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	s, ok := r.m[id]
	if !ok {
		return "", "", false
	}
	return s.Title, s.Data, true
}

// Delete removes a session.
func (r *Registry) Delete(id string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.m, id)
}

// List returns session metadata, newest first.
func (r *Registry) List() []Session {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Session, 0, len(r.m))
	for _, s := range r.m {
		out = append(out, Session{ID: s.ID, Title: s.Title, CreatedAt: s.CreatedAt, UpdatedAt: s.UpdatedAt})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].UpdatedAt.After(out[j].UpdatedAt) })
	return out
}

// Latest returns the most recently updated session's raw JSON.
func (r *Registry) Latest() (data string, ok bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var latest *Session
	for _, s := range r.m {
		if latest == nil || s.UpdatedAt.After(latest.UpdatedAt) {
			latest = s
		}
	}
	if latest == nil {
		return "", false
	}
	return latest.Data, true
}
