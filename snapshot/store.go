// depbot/snapshot/store.go
package snapshot

import (
	"context"
	"errors"
	"time"
)

// Files are the exported files that make one snapshot (spec §3.1). Types is
// optional; nil means the project has no chronicle.types.yaml. Profile is
// profile.json, part of format version 2 (nil/empty = no profile).
type Files struct{ Graph, Domain, Types, Profile []byte }

// ContentID is the content identity of the files (see ContentID).
func (f Files) ContentID() (string, error) { return ContentIDFiles(f) }

// Labels on a stored snapshot. Every stored snapshot is a head; a snapshot
// that is also part of a long-lived branch's history carries chain:<branch>.
const LabelHead = "head"

// ChainLabel is the label that puts a snapshot on the chain of a branch.
func ChainLabel(branch string) string { return "chain:" + branch }

// Entry is the index record of one stored snapshot.
type Entry struct {
	Repo       string    `json:"repo"`
	CodeSha    string    `json:"code_sha"`
	SnapshotID string    `json:"snapshot_id"`
	Labels     []string  `json:"labels"`
	CreatedAt  time.Time `json:"created_at"`
}

// OnChain says whether the snapshot is on the chain of branch.
func (e Entry) OnChain(branch string) bool {
	for _, l := range e.Labels {
		if l == ChainLabel(branch) {
			return true
		}
	}
	return false
}

// PutOptions describe a Put. Labels are chain labels to add; LabelHead is
// always implied. CodeRef, Base and Producer are carried into the stored
// publication where the backend has a place for them.
type PutOptions struct {
	Labels   []string
	CodeRef  string
	Base     string
	Producer string
}

// PutResult reports what a Put did. Created is true only when the head was
// new: adding a chain label to an existing head reports Created=false (look
// at Entry.Labels). Kept means a different snapshot was already stored under
// the same code sha and was kept (first write wins); Warning then says so.
type PutResult struct {
	Entry   Entry
	Created bool
	Kept    bool
	Warning string
}

// Store keeps snapshots keyed by (repo, code sha). Backends: MemStore here,
// git and directory stores in Pro. Every backend must pass storetest.Run.
type Store interface {
	Kind() string
	Put(ctx context.Context, repo, codeSha string, f Files, o PutOptions) (PutResult, error)
	Get(ctx context.Context, repo, codeSha string) (Files, Entry, error)
	List(ctx context.Context, repo string) ([]Entry, error)
}

var (
	ErrNotFound             = errors.New("snapshot: no snapshot for this repo and code sha")
	ErrBadSha               = errors.New("snapshot: code sha must be 40 or 64 lowercase hex")
	ErrCorrupt              = errors.New("snapshot: stored files do not match the snapshot id")
	ErrNoSnapshot           = errors.New("snapshot: lookup found no snapshot")
	ErrRemoteNotImplemented = errors.New("snapshot: the remote store is an interface only in this release")
)

// ValidCodeSha says whether s is a full lowercase hex git object name
// (SHA-1 or SHA-256). It is the gate before any sha reaches a git argv.
func ValidCodeSha(s string) bool { return shaRe.MatchString(s) }
