package scan

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/alexdx2/chronicle-core/snapshot"
	"github.com/alexdx2/chronicle-core/store"
)

// The build state of BuildInput.StateDir: a consistent copy of the working
// database, the manifest when the bootstrap generated it, and state.json
// naming the build (sha and base) the files belong to. A restart that finds
// the same build picks the work up where the last wave left it.

const (
	stateFile     = "state.json"
	stateDB       = "chronicle.db"
	stateManifest = "chronicle.domain.yaml"
)

type buildState struct {
	Version int    `json:"version"`
	Sha     string `json:"sha"`
	BaseSha string `json:"base_sha"`
}

// renameFile is a variable so a test can fail the rename between the copy and
// its publication.
var renameFile = os.Rename

// stateHasContent reports whether stateDir exists and holds anything.
func stateHasContent(stateDir string) bool {
	ents, err := os.ReadDir(stateDir)
	return err == nil && len(ents) > 0
}

// clearState empties stateDir, keeping the directory itself.
func clearState(stateDir string) {
	ents, err := os.ReadDir(stateDir)
	if err != nil {
		return
	}
	for _, e := range ents {
		_ = os.RemoveAll(filepath.Join(stateDir, e.Name()))
	}
}

// removeDB deletes a database file with its WAL and shared-memory companions.
func removeDB(path string) {
	for _, suffix := range []string{"", "-wal", "-shm"} {
		_ = os.Remove(path + suffix)
	}
}

// openProbe opens the database at path the way the build will and runs a
// quick integrity check, so a truncated or garbled file is found before any
// work is built on it.
func openProbe(path string) error {
	s, err := store.OpenWithoutJournalSync(path)
	if err != nil {
		return err
	}
	defer s.Close()
	var res string
	if err := s.QueryRowScan("PRAGMA quick_check", &res); err != nil {
		return err
	}
	if res != "ok" {
		return fmt.Errorf("quick_check: %s", res)
	}
	return nil
}

// restoreState copies the saved database (and generated manifest) of stateDir
// into the working copy wt when state.json names exactly this sha and base and
// the database is usable. State that does not match or does not open is
// removed (the directory is emptied) and restored is false; an error is an I/O
// problem with the working copy, which leaves the saved state alone.
func restoreState(stateDir, wt, sha, baseSha string) (restored bool, err error) {
	raw, rerr := os.ReadFile(filepath.Join(stateDir, stateFile))
	if errors.Is(rerr, fs.ErrNotExist) {
		if stateHasContent(stateDir) {
			clearState(stateDir) // files without a state.json belong to no build
		}
		return false, nil
	}
	var st buildState
	if rerr != nil || json.Unmarshal(raw, &st) != nil || st.Version != 1 || st.Sha != sha || st.BaseSha != baseSha {
		clearState(stateDir)
		return false, nil
	}

	depDir := filepath.Join(wt, filepath.Dir(snapshot.ManifestPath))
	if err := os.MkdirAll(depDir, 0o755); err != nil {
		return false, err
	}
	dbPath := filepath.Join(depDir, stateDB)
	removeDB(dbPath) // a database committed in the tree is not this build's state
	if err := copyFile(filepath.Join(stateDir, stateDB), dbPath); err != nil {
		removeDB(dbPath)
		if errors.Is(err, fs.ErrNotExist) {
			clearState(stateDir)
			return false, nil
		}
		return false, err
	}
	if perr := openProbe(dbPath); perr != nil {
		removeDB(dbPath)
		clearState(stateDir)
		return false, nil
	}

	manPath := filepath.Join(wt, filepath.FromSlash(snapshot.ManifestPath))
	if _, merr := os.Lstat(manPath); errors.Is(merr, fs.ErrNotExist) {
		if err := copyFile(filepath.Join(stateDir, stateManifest), manPath); err != nil && !errors.Is(err, fs.ErrNotExist) {
			removeDB(dbPath)
			return false, err
		}
	}
	return true, nil
}

// checkpointState saves the working database (and, when the bootstrap
// generated it, the manifest) into stateDir and then state.json. The database
// goes through a temporary file and a rename, so a crash leaves the previous
// checkpoint whole.
func checkpointState(stateDir, wt, sha, baseSha string, st *store.Store, manifestGenerated bool) error {
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		return err
	}
	tmp := filepath.Join(stateDir, stateDB+".tmp")
	if err := st.SnapshotTo(tmp); err != nil {
		return fmt.Errorf("copy database: %w", err)
	}
	if err := renameFile(tmp, filepath.Join(stateDir, stateDB)); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if manifestGenerated {
		manTmp := filepath.Join(stateDir, stateManifest+".tmp")
		if err := copyFile(filepath.Join(wt, filepath.FromSlash(snapshot.ManifestPath)), manTmp); err != nil {
			return err
		}
		if err := renameFile(manTmp, filepath.Join(stateDir, stateManifest)); err != nil {
			_ = os.Remove(manTmp)
			return err
		}
	}
	raw, err := json.Marshal(buildState{Version: 1, Sha: sha, BaseSha: baseSha})
	if err != nil {
		return err
	}
	stTmp := filepath.Join(stateDir, stateFile+".tmp")
	if err := os.WriteFile(stTmp, raw, 0o644); err != nil {
		return err
	}
	if err := renameFile(stTmp, filepath.Join(stateDir, stateFile)); err != nil {
		_ = os.Remove(stTmp)
		return err
	}
	return nil
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}
