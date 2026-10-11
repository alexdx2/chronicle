package store

import (
	"errors"
	"os"
	"path/filepath"
)

// SnapshotTo writes a consistent copy of the database to dest (VACUUM INTO).
// The copy carries no WAL and opens with Open. dest's directory is created and
// an existing dest is replaced; concurrent writers on s are fine, the copy is
// one read snapshot.
func (s *Store) SnapshotTo(dest string) error {
	if s.conn == nil {
		return errors.New("store: SnapshotTo needs a connection-backed store")
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return err
	}
	if err := os.Remove(dest); err != nil && !os.IsNotExist(err) {
		return err
	}
	_, err := s.conn.Exec("VACUUM INTO ?", dest)
	return err
}
