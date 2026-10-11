// depbot/snapshot/store_mem.go
package snapshot

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"
)

type memItem struct {
	entry Entry
	files Files
}

// MemStore is an in-memory Store: the reference implementation of the
// contract and the backend for tests that do not need git.
type MemStore struct {
	mu   sync.Mutex
	now  func() time.Time
	data map[string]map[string]*memItem // repo -> code sha -> item
}

func NewMemStore() *MemStore {
	return &MemStore{now: func() time.Time { return time.Now().UTC().Truncate(time.Second) }, data: map[string]map[string]*memItem{}}
}

func (m *MemStore) Kind() string { return "memory" }

func cloneBytes(b []byte) []byte {
	if b == nil {
		return nil
	}
	return append([]byte{}, b...)
}

func cloneFiles(f Files) Files {
	return Files{Graph: cloneBytes(f.Graph), Domain: cloneBytes(f.Domain), Types: cloneBytes(f.Types)}
}

func cloneEntry(e Entry) Entry {
	e.Labels = append([]string{}, e.Labels...)
	return e
}

// mergeLabels returns LabelHead first, then the distinct other labels sorted.
func mergeLabels(existing, add []string) []string {
	set := map[string]bool{}
	for _, l := range existing {
		set[l] = true
	}
	for _, l := range add {
		set[l] = true
	}
	delete(set, LabelHead)
	rest := make([]string, 0, len(set))
	for l := range set {
		rest = append(rest, l)
	}
	sort.Strings(rest)
	return append([]string{LabelHead}, rest...)
}

func (m *MemStore) Put(ctx context.Context, repo, codeSha string, f Files, o PutOptions) (PutResult, error) {
	if !ValidCodeSha(codeSha) {
		return PutResult{}, ErrBadSha
	}
	if err := ctx.Err(); err != nil {
		return PutResult{}, err
	}
	id, err := f.ContentID()
	if err != nil {
		return PutResult{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	byRepo := m.data[repo]
	if byRepo == nil {
		byRepo = map[string]*memItem{}
		m.data[repo] = byRepo
	}
	if it, ok := byRepo[codeSha]; ok {
		if it.entry.SnapshotID != id {
			return PutResult{
				Entry: cloneEntry(it.entry),
				Kept:  true,
				Warning: fmt.Sprintf("code sha %s already has snapshot %s; offered %s; kept the first",
					codeSha, it.entry.SnapshotID, id),
			}, nil
		}
		it.entry.Labels = mergeLabels(it.entry.Labels, o.Labels)
		return PutResult{Entry: cloneEntry(it.entry)}, nil
	}
	it := &memItem{
		entry: Entry{Repo: repo, CodeSha: codeSha, SnapshotID: id, Labels: mergeLabels(nil, o.Labels), CreatedAt: m.now()},
		files: cloneFiles(f),
	}
	byRepo[codeSha] = it
	return PutResult{Entry: cloneEntry(it.entry), Created: true}, nil
}

func (m *MemStore) Get(ctx context.Context, repo, codeSha string) (Files, Entry, error) {
	if !ValidCodeSha(codeSha) {
		return Files{}, Entry{}, ErrBadSha
	}
	if err := ctx.Err(); err != nil {
		return Files{}, Entry{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	it, ok := m.data[repo][codeSha]
	if !ok {
		return Files{}, Entry{}, ErrNotFound
	}
	return cloneFiles(it.files), cloneEntry(it.entry), nil
}

func (m *MemStore) List(ctx context.Context, repo string) ([]Entry, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Entry, 0, len(m.data[repo]))
	for _, it := range m.data[repo] {
		out = append(out, cloneEntry(it.entry))
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].CreatedAt.Before(out[j].CreatedAt)
		}
		return out[i].CodeSha < out[j].CodeSha
	})
	return out, nil
}
