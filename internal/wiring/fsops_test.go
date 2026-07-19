package wiring

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAtomicWriteCreatesParentAndFile(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "a", "b", "f.txt")
	if err := AtomicWrite(p, []byte("hello"), 0644); err != nil {
		t.Fatalf("AtomicWrite: %v", err)
	}
	got, _ := os.ReadFile(p)
	if string(got) != "hello" {
		t.Fatalf("content = %q", got)
	}
	// no temp litter
	entries, _ := os.ReadDir(filepath.Dir(p))
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".chronicle-tmp-") {
			t.Fatalf("temp file left behind: %s", e.Name())
		}
	}
}

func TestAtomicWriteReplacesExisting(t *testing.T) {
	p := filepath.Join(t.TempDir(), "f.txt")
	if err := AtomicWrite(p, []byte("one"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := AtomicWrite(p, []byte("two"), 0644); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(p)
	if string(got) != "two" {
		t.Fatalf("content = %q", got)
	}
}

func TestFileSHA256(t *testing.T) {
	p := filepath.Join(t.TempDir(), "f.txt")
	os.WriteFile(p, []byte("abc"), 0644)
	h, err := FileSHA256(p)
	if err != nil {
		t.Fatal(err)
	}
	// well-known sha256("abc")
	if h != "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad" {
		t.Fatalf("hash = %s", h)
	}
}
