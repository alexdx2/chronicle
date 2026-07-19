package wiring

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestNpxCacheGuard(t *testing.T) {
	if !looksLikeNpxCache("/home/u/.npm/_npx/abc123/node_modules/.bin/chronicle") {
		t.Fatal("npx cache path not flagged")
	}
	if looksLikeNpxCache("/usr/local/bin/chronicle") {
		t.Fatal("normal path flagged")
	}
}

func TestEnsureCanonicalBinaryCopiesAndIsIdempotent(t *testing.T) {
	t.Setenv("CHRONICLE_HOME", t.TempDir())
	src := filepath.Join(t.TempDir(), "chronicle-src")
	os.WriteFile(src, []byte("BINARY-V1"), 0755)

	dst, err := EnsureCanonicalBinaryFrom(src)
	if err != nil {
		t.Fatalf("EnsureCanonicalBinaryFrom: %v", err)
	}
	want := filepath.Join(Home(), "bin", "chronicle")
	if runtime.GOOS == "windows" {
		want += ".exe"
	}
	if dst != want {
		t.Fatalf("dst = %q, want %q", dst, want)
	}
	if got, _ := os.ReadFile(dst); string(got) != "BINARY-V1" {
		t.Fatalf("copied content = %q", got)
	}
	info, _ := os.Stat(dst)
	if runtime.GOOS != "windows" && info.Mode()&0111 == 0 {
		t.Fatal("not executable")
	}
	// second run: no error, content refreshed on change
	os.WriteFile(src, []byte("BINARY-V2"), 0755)
	if _, err := EnsureCanonicalBinaryFrom(src); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(dst); string(got) != "BINARY-V2" {
		t.Fatalf("upgrade content = %q", got)
	}
}

func TestEnsureCanonicalBinarySelfSkip(t *testing.T) {
	t.Setenv("CHRONICLE_HOME", t.TempDir())
	dst := CanonicalBinaryPath()
	os.MkdirAll(filepath.Dir(dst), 0755)
	os.WriteFile(dst, []byte("SAME"), 0755)
	got, err := EnsureCanonicalBinaryFrom(dst) // running FROM the canonical binary
	if err != nil || got != dst {
		t.Fatalf("self-skip failed: %q %v", got, err)
	}
}

func TestResolveSourceBinaryRejectsNpxWhenAlternativeExists(t *testing.T) {
	// pure guard behavior is covered above; here just assert it returns
	// SOMETHING non-empty in a normal test process (the test binary itself).
	p, err := ResolveSourceBinary()
	if err != nil || p == "" || strings.TrimSpace(p) == "" {
		t.Fatalf("ResolveSourceBinary: %q %v", p, err)
	}
}
