package store

import (
	"fmt"
	"path/filepath"
	"sync"
	"testing"
)

func TestStore_SnapshotTo_IsOpenableCopy(t *testing.T) {
	s := openTestStore(t)
	revID, _ := s.CreateRevision("orders", "", "sha1", "manual", "full", "{}")
	if _, err := s.UpsertNode(makeNodeRow("code:controller:orders:oc", "code", "controller", "orders", "OC", revID)); err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertCoverage(CoverageRow{DomainKey: "orders", FilePath: "src/a.ts", ExtractorID: "x", ExtractorVersion: "1", Status: "done", BlobSHA: "abc"}); err != nil {
		t.Fatal(err)
	}

	dest := filepath.Join(t.TempDir(), "sub", "copy.db")
	if err := s.SnapshotTo(dest); err != nil {
		t.Fatalf("SnapshotTo: %v", err)
	}
	c, err := Open(dest)
	if err != nil {
		t.Fatalf("open copy: %v", err)
	}
	if n, err := c.GetNodeByKey("code:controller:orders:oc"); err != nil || n.Name != "OC" {
		t.Fatalf("copy lacks the node: %v %+v", err, n)
	}
	if cov, err := c.ListCoverage("orders"); err != nil || len(cov) != 1 || cov[0].BlobSHA != "abc" {
		t.Fatalf("copy lacks coverage: %v %+v", err, cov)
	}
	c.Close()

	// The source keeps taking writes, and a second call replaces the first copy.
	if _, err := s.UpsertNode(makeNodeRow("code:controller:orders:two", "code", "controller", "orders", "Two", revID)); err != nil {
		t.Fatalf("source write after SnapshotTo: %v", err)
	}
	if err := s.SnapshotTo(dest); err != nil {
		t.Fatalf("second SnapshotTo over an existing file: %v", err)
	}
	c, err = Open(dest)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if _, err := c.GetNodeByKey("code:controller:orders:two"); err != nil {
		t.Fatalf("replaced copy lacks the new node: %v", err)
	}
}

func TestStore_SnapshotTo_ConsistentWhileWriting(t *testing.T) {
	s := openTestStore(t)
	revID, _ := s.CreateRevision("orders", "", "sha1", "manual", "full", "{}")

	var wg sync.WaitGroup
	wg.Add(1)
	werr := make(chan error, 1)
	go func() {
		defer wg.Done()
		for i := 0; i < 200; i++ {
			key := fmt.Sprintf("code:controller:orders:n%d", i)
			if _, err := s.UpsertNode(makeNodeRow(key, "code", "controller", "orders", key, revID)); err != nil {
				werr <- err
				return
			}
		}
	}()

	dir := t.TempDir()
	for i := 0; i < 5; i++ {
		dest := filepath.Join(dir, fmt.Sprintf("copy%d.db", i))
		if err := s.SnapshotTo(dest); err != nil {
			t.Fatalf("SnapshotTo #%d: %v", i, err)
		}
		c, err := Open(dest)
		if err != nil {
			t.Fatalf("open copy #%d: %v", i, err)
		}
		var res string
		if err := c.QueryRowScan("PRAGMA integrity_check", &res); err != nil || res != "ok" {
			t.Fatalf("copy #%d integrity_check = %q, %v", i, res, err)
		}
		c.Close()
	}
	wg.Wait()
	select {
	case err := <-werr:
		t.Fatalf("writer: %v", err)
	default:
	}
}
