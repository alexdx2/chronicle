package store

import (
	"fmt"
	"testing"
)

func makeSnapshot(revID int64, domain, kind string) SnapshotRow {
	return SnapshotRow{
		RevisionID: revID, DomainKey: domain, Kind: kind,
		NodeCount: 10, EdgeCount: 5,
	}
}

func TestCreateSnapshot(t *testing.T) {
	s := openTestStore(t)
	revID, _ := s.CreateRevision("orders", "", "sha1", "manual", "full", "{}")
	id, err := s.CreateSnapshot(makeSnapshot(revID, "orders", "full"))
	if err != nil {
		t.Fatalf("CreateSnapshot: %v", err)
	}
	if id <= 0 {
		t.Fatalf("expected positive id, got %d", id)
	}

	got, err := s.GetSnapshot(id)
	if err != nil {
		t.Fatalf("GetSnapshot: %v", err)
	}
	if got.Summary != "{}" {
		t.Errorf("Summary = %q, want {}", got.Summary)
	}
}

func TestCreateSnapshotDuplicate(t *testing.T) {
	s := openTestStore(t)
	revID, _ := s.CreateRevision("orders", "", "sha1", "manual", "full", "{}")
	_, err := s.CreateSnapshot(makeSnapshot(revID, "orders", "full"))
	if err != nil {
		t.Fatalf("first CreateSnapshot: %v", err)
	}
	_, err = s.CreateSnapshot(makeSnapshot(revID, "orders", "incremental"))
	if err == nil {
		t.Fatal("expected error on duplicate revision+domain, got nil")
	}
}

func TestListSnapshots(t *testing.T) {
	s := openTestStore(t)
	rev1, _ := s.CreateRevision("orders", "", "sha1", "manual", "full", "{}")
	rev2, _ := s.CreateRevision("orders", "sha1", "sha2", "manual", "incremental", "{}")
	s.CreateSnapshot(makeSnapshot(rev1, "orders", "full"))
	s.CreateSnapshot(makeSnapshot(rev2, "orders", "incremental"))

	snaps, err := s.ListSnapshots("orders")
	if err != nil {
		t.Fatalf("ListSnapshots: %v", err)
	}
	if len(snaps) != 2 {
		t.Errorf("expected 2 snapshots, got %d", len(snaps))
	}
}

func TestGetLatestSnapshot(t *testing.T) {
	s := openTestStore(t)
	revID, _ := s.CreateRevision("orders", "", "sha1", "manual", "full", "{}")

	_, err := s.GetLatestSnapshot("orders")
	if err == nil {
		t.Fatal("expected error for no snapshots")
	}

	s.CreateSnapshot(SnapshotRow{RevisionID: revID, DomainKey: "orders", Kind: "full", NodeCount: 10, EdgeCount: 20, Summary: "{}"})

	revID2, _ := s.CreateRevision("orders", "sha1", "sha2", "manual", "incremental", "{}")
	s.CreateSnapshot(SnapshotRow{RevisionID: revID2, DomainKey: "orders", Kind: "incremental", NodeCount: 12, EdgeCount: 22, Summary: "{}"})

	snap, err := s.GetLatestSnapshot("orders")
	if err != nil {
		t.Fatalf("GetLatestSnapshot: %v", err)
	}
	if snap.NodeCount != 12 {
		t.Errorf("node_count = %d, want 12", snap.NodeCount)
	}
	if snap.Kind != "incremental" {
		t.Errorf("kind = %q, want incremental", snap.Kind)
	}
}

func TestTransaction(t *testing.T) {
	s := openTestStore(t)
	err := s.WithTx(func(tx *Store) error {
		tx.CreateRevision("orders", "", "sha1", "manual", "full", "{}")
		return nil
	})
	if err != nil {
		t.Fatalf("WithTx success: %v", err)
	}

	err = s.WithTx(func(tx *Store) error {
		tx.CreateRevision("orders2", "", "sha2", "manual", "full", "{}")
		return fmt.Errorf("intentional failure")
	})
	if err == nil {
		t.Fatal("expected error from failed transaction")
	}
}
