package wiring

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// CanonicalBinaryPath is the Chronicle-managed executable every agent config
// references. Not a symlink, not a wrapper: a real copied binary, atomically
// replaced on upgrade — MCP hosts spawn it without PATH/shell assumptions.
func CanonicalBinaryPath() string {
	name := "chronicle"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	return filepath.Join(Home(), "bin", name)
}

// looksLikeNpxCache guards against persisting an ephemeral npx download path.
func looksLikeNpxCache(p string) bool {
	return strings.Contains(filepath.ToSlash(p), "/_npx/")
}

// pickSourceBinary is the pure decision logic behind ResolveSourceBinary: the
// running executable, unless it lives in an npx cache — then a PATH-resolved
// install is preferred; the npx path is the last resort (better a stale copy
// source than none: the copy itself outlives the cache).
func pickSourceBinary(exe string, exeErr error, lookPath func(string) (string, error)) (string, error) {
	if exeErr == nil && !looksLikeNpxCache(exe) {
		return exe, nil
	}
	if p, err := lookPath("chronicle"); err == nil {
		if abs, aerr := filepath.Abs(p); aerr == nil && !looksLikeNpxCache(abs) {
			return abs, nil
		}
	}
	if exeErr == nil {
		return exe, nil
	}
	return "", exeErr
}

// ResolveSourceBinary picks the binary to copy from. See pickSourceBinary for
// the decision logic.
func ResolveSourceBinary() (string, error) {
	exe, exeErr := os.Executable()
	return pickSourceBinary(exe, exeErr, exec.LookPath)
}

// EnsureCanonicalBinaryFrom copies src to the canonical path via stage +
// atomic rename. Skips when src IS the canonical binary (self-upgrade run)
// or hashes already match.
func EnsureCanonicalBinaryFrom(src string) (string, error) {
	dst := CanonicalBinaryPath()
	absSrc, err := filepath.Abs(src)
	if err != nil {
		return "", err
	}
	if absSrc == dst {
		return dst, nil
	}
	srcHash, err := FileSHA256(absSrc)
	if err != nil {
		return "", err
	}
	if dstHash, herr := FileSHA256(dst); herr == nil && dstHash == srcHash {
		return dst, nil
	}
	data, err := os.ReadFile(absSrc)
	if err != nil {
		return "", err
	}
	if err := AtomicWrite(dst, data, 0755); err != nil {
		return "", err
	}
	return dst, nil
}
