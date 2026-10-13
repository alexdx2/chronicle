// Package storetest is the contract every snapshot.Store backend must pass.
// A backend's test file calls Run with a Harness; the subtest names are fixed
// and other packages rely on them.
package storetest

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/alexdx2/chronicle-core/snapshot"
)

// Harness builds the backend under test.
type Harness struct {
	// New returns a fresh, empty store.
	New func(t *testing.T) snapshot.Store
	// Tamper corrupts the stored files of (repo, codeSha) behind the Store's
	// back. nil skips the TamperedFilesRejected check.
	Tamper func(t *testing.T, st snapshot.Store, repo, codeSha string)
	// Advance moves the backend's clock forward by at least d, so that the
	// next Put gets a later CreatedAt second. nil means a real sleep of d.
	Advance func(t *testing.T, st snapshot.Store, d time.Duration)
}

const sampleDomain = "domains:\n  d: {name: D}\n"

// Sample returns valid snapshot files. Different names give different
// ContentIDs.
func Sample(name string) snapshot.Files {
	snap := &snapshot.Snapshot{
		Header: snapshot.Header{Kind: "header", Format: snapshot.Format, Version: snapshot.Version, Seq: 1, Domains: []string{"d"}, FactSchema: snapshot.FactSchema},
		Nodes: []snapshot.Node{
			{Kind: "node", ID: "service:service:d:" + name + "-api", Layer: "service", Type: "service", Domain: "d", Name: name + "-api", QualifiedName: name + "-api"},
			{Kind: "node", ID: "service:service:d:" + name + "-db", Layer: "service", Type: "service", Domain: "d", Name: name + "-db", QualifiedName: name + "-db"},
		},
	}
	var buf bytes.Buffer
	if err := snapshot.Encode(&buf, snap); err != nil {
		panic(fmt.Sprintf("storetest.Sample: %v", err))
	}
	return snapshot.Files{Graph: buf.Bytes(), Domain: []byte(sampleDomain)}
}

// Sha returns a deterministic 40-hex code sha.
func Sha(n int) string { return fmt.Sprintf("%040x", n) }

func mustID(t *testing.T, f snapshot.Files) string {
	t.Helper()
	id, err := f.ContentID()
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func mustPut(t *testing.T, st snapshot.Store, repo, sha string, f snapshot.Files, o snapshot.PutOptions) snapshot.PutResult {
	t.Helper()
	r, err := st.Put(context.Background(), repo, sha, f, o)
	if err != nil {
		t.Fatalf("Put(%s, %s): %v", repo, sha, err)
	}
	return r
}

func sameFiles(a, b snapshot.Files) bool {
	return bytes.Equal(a.Graph, b.Graph) && bytes.Equal(a.Domain, b.Domain) && bytes.Equal(a.Types, b.Types) && bytes.Equal(a.Profile, b.Profile)
}

func within(a, b time.Time) bool {
	d := a.Sub(b)
	return d < time.Second && d > -time.Second
}

// Run executes the contract against h.
func Run(t *testing.T, h Harness) {
	ctx := context.Background()

	t.Run("PutGetRoundTrip", func(t *testing.T) {
		st := h.New(t)
		f := Sample("a")
		r := mustPut(t, st, "r1", Sha(1), f, snapshot.PutOptions{Labels: []string{snapshot.ChainLabel("main")}})
		if !r.Created || r.Kept || r.Warning != "" {
			t.Fatalf("first Put: %+v", r)
		}
		if r.Entry.Repo != "r1" || r.Entry.CodeSha != Sha(1) {
			t.Fatalf("Put entry Repo/CodeSha = %q/%q, want r1/%s", r.Entry.Repo, r.Entry.CodeSha, Sha(1))
		}
		if r.Entry.SnapshotID != mustID(t, f) {
			t.Fatalf("SnapshotID %s, want %s", r.Entry.SnapshotID, mustID(t, f))
		}
		if want := []string{snapshot.LabelHead, snapshot.ChainLabel("main")}; !reflect.DeepEqual(r.Entry.Labels, want) {
			t.Fatalf("labels %v, want %v", r.Entry.Labels, want)
		}
		got, e, err := st.Get(ctx, "r1", Sha(1))
		if err != nil {
			t.Fatal(err)
		}
		if !sameFiles(got, f) {
			t.Fatal("Get returned different bytes than Put stored")
		}
		if e.Repo != "r1" || e.CodeSha != Sha(1) || e.SnapshotID != r.Entry.SnapshotID ||
			!reflect.DeepEqual(e.Labels, r.Entry.Labels) || !within(e.CreatedAt, r.Entry.CreatedAt) {
			t.Fatalf("Get entry %+v differs from Put entry %+v", e, r.Entry)
		}
	})

	t.Run("PutGetRoundTripProfile", func(t *testing.T) {
		st := h.New(t)
		f := Sample("p")
		f.Types = []byte("types: {}\n")
		f.Profile = []byte("{}")
		mustPut(t, st, "r1", Sha(1), f, snapshot.PutOptions{})
		got, _, err := st.Get(ctx, "r1", Sha(1))
		if err != nil {
			t.Fatal(err)
		}
		if !sameFiles(got, f) || string(got.Profile) != "{}" {
			t.Fatalf("Get differs: %+v", got)
		}
	})

	t.Run("PutIdempotent", func(t *testing.T) {
		st := h.New(t)
		mustPut(t, st, "r1", Sha(1), Sample("a"), snapshot.PutOptions{})
		r := mustPut(t, st, "r1", Sha(1), Sample("a"), snapshot.PutOptions{})
		if r.Created || r.Kept {
			t.Fatalf("repeat Put: %+v", r)
		}
		l, err := st.List(ctx, "r1")
		if err != nil || len(l) != 1 {
			t.Fatalf("List: %v %v", l, err)
		}
	})

	t.Run("PutConflictKeepsFirst", func(t *testing.T) {
		st := h.New(t)
		first := mustPut(t, st, "r1", Sha(1), Sample("a"), snapshot.PutOptions{})
		r := mustPut(t, st, "r1", Sha(1), Sample("b"), snapshot.PutOptions{})
		if r.Created || !r.Kept || r.Warning == "" {
			t.Fatalf("conflicting Put: %+v", r)
		}
		if r.Entry.SnapshotID != first.Entry.SnapshotID {
			t.Fatalf("conflicting Put returned entry %s, want the first %s", r.Entry.SnapshotID, first.Entry.SnapshotID)
		}
		got, e, err := st.Get(ctx, "r1", Sha(1))
		if err != nil {
			t.Fatal(err)
		}
		if !sameFiles(got, Sample("a")) || e.SnapshotID != first.Entry.SnapshotID {
			t.Fatal("Get must still return the first snapshot")
		}
	})

	t.Run("LabelAddedLater", func(t *testing.T) {
		st := h.New(t)
		mustPut(t, st, "r1", Sha(1), Sample("a"), snapshot.PutOptions{})
		r := mustPut(t, st, "r1", Sha(1), Sample("a"), snapshot.PutOptions{Labels: []string{snapshot.ChainLabel("release-1")}})
		if r.Created || r.Kept {
			t.Fatalf("adding a label to an existing head: %+v", r)
		}
		l, err := st.List(ctx, "r1")
		if err != nil || len(l) != 1 {
			t.Fatalf("List: %v %v", l, err)
		}
		if !l[0].OnChain("release-1") {
			t.Fatalf("label not added: %+v", l[0])
		}
	})

	t.Run("GetUnknown", func(t *testing.T) {
		st := h.New(t)
		if _, _, err := st.Get(ctx, "r1", Sha(1)); !errors.Is(err, snapshot.ErrNotFound) {
			t.Fatalf("err = %v, want ErrNotFound", err)
		}
	})

	t.Run("ListUnknownRepo", func(t *testing.T) {
		st := h.New(t)
		l, err := st.List(ctx, "nobody")
		if err != nil || len(l) != 0 {
			t.Fatalf("List unknown repo: %v %v", l, err)
		}
	})

	t.Run("ReposAreIsolated", func(t *testing.T) {
		st := h.New(t)
		mustPut(t, st, "r1", Sha(1), Sample("a"), snapshot.PutOptions{})
		mustPut(t, st, "r2", Sha(1), Sample("b"), snapshot.PutOptions{})
		for repo, name := range map[string]string{"r1": "a", "r2": "b"} {
			got, e, err := st.Get(ctx, repo, Sha(1))
			if err != nil {
				t.Fatalf("Get %s: %v", repo, err)
			}
			if !sameFiles(got, Sample(name)) || e.Repo != repo {
				t.Fatalf("%s: wrong snapshot %+v", repo, e)
			}
		}
	})

	t.Run("BadShaRejected", func(t *testing.T) {
		st := h.New(t)
		for _, sha := range []string{"--upload-pack=x", "ABC", ""} {
			if _, err := st.Put(ctx, "r1", sha, Sample("a"), snapshot.PutOptions{}); !errors.Is(err, snapshot.ErrBadSha) {
				t.Fatalf("Put(%q): err = %v, want ErrBadSha", sha, err)
			}
			if _, _, err := st.Get(ctx, "r1", sha); !errors.Is(err, snapshot.ErrBadSha) {
				t.Fatalf("Get(%q): err = %v, want ErrBadSha", sha, err)
			}
		}
	})

	t.Run("InvalidFilesRejected", func(t *testing.T) {
		st := h.New(t)
		if _, err := st.Put(ctx, "r1", Sha(1), snapshot.Files{Graph: []byte("garbage")}, snapshot.PutOptions{}); err == nil {
			t.Fatal("Put of invalid files must fail")
		}
		l, err := st.List(ctx, "r1")
		if err != nil || len(l) != 0 {
			t.Fatalf("nothing may be stored after a rejected Put: %v %v", l, err)
		}
	})

	t.Run("ListOrder", func(t *testing.T) {
		st := h.New(t)
		advance := func() {
			if h.Advance != nil {
				h.Advance(t, st, 1100*time.Millisecond)
			} else {
				time.Sleep(1100 * time.Millisecond)
			}
		}
		// Sha(3) goes in first, then the clock moves past a second boundary;
		// Sha(1) and Sha(2) share a later second. The order is CreatedAt
		// first and CodeSha within a tie: [3, 1, 2]. A backend sorting by
		// CodeSha alone would answer [1, 2, 3].
		mustPut(t, st, "r1", Sha(3), Sample("s3"), snapshot.PutOptions{})
		advance()
		mustPut(t, st, "r1", Sha(1), Sample("s1"), snapshot.PutOptions{})
		mustPut(t, st, "r1", Sha(2), Sample("s2"), snapshot.PutOptions{})
		l, err := st.List(ctx, "r1")
		if err != nil || len(l) != 3 {
			t.Fatalf("List: %v %v", l, err)
		}
		for i, want := range []string{Sha(3), Sha(1), Sha(2)} {
			if l[i].CodeSha != want {
				t.Fatalf("entry %d is %s, want %s; list: %+v", i, l[i].CodeSha, want, l)
			}
		}
	})

	t.Run("ContextCancelled", func(t *testing.T) {
		st := h.New(t)
		mustPut(t, st, "r1", Sha(1), Sample("a"), snapshot.PutOptions{})
		cctx, cancel := context.WithCancel(ctx)
		cancel()
		if _, err := st.Put(cctx, "r1", Sha(2), Sample("b"), snapshot.PutOptions{}); !errors.Is(err, context.Canceled) {
			t.Fatalf("Put: err = %v, want context.Canceled", err)
		}
		if _, _, err := st.Get(cctx, "r1", Sha(1)); !errors.Is(err, context.Canceled) {
			t.Fatalf("Get: err = %v, want context.Canceled", err)
		}
		if _, err := st.List(cctx, "r1"); !errors.Is(err, context.Canceled) {
			t.Fatalf("List: err = %v, want context.Canceled", err)
		}
		if l, err := st.List(ctx, "r1"); err != nil || len(l) != 1 {
			t.Fatalf("a cancelled Put must store nothing: %v %v", l, err)
		}
	})

	t.Run("TamperedFilesRejected", func(t *testing.T) {
		if h.Tamper == nil {
			t.Skip("backend has no way to tamper with stored files")
		}
		st := h.New(t)
		mustPut(t, st, "r1", Sha(1), Sample("a"), snapshot.PutOptions{})
		h.Tamper(t, st, "r1", Sha(1))
		if _, _, err := st.Get(ctx, "r1", Sha(1)); !errors.Is(err, snapshot.ErrCorrupt) {
			t.Fatalf("Get after tamper: err = %v, want ErrCorrupt", err)
		}
	})
}
