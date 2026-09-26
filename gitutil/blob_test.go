package gitutil

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestBlobSHA_MatchesGit hashes three files with distinct content (including
// an empty one, which exercises the "blob 0\0" header with no body) and
// checks BlobSHA against real `git hash-object` output in a temp repo.
func TestBlobSHA_MatchesGit(t *testing.T) {
	dir, _ := repo(t)

	files := map[string]string{
		"a.txt":        "hello world\n",
		"b/nested.txt": "chronicle knowledge graph\nsecond line\n",
		"empty.txt":    "",
	}
	for rel, content := range files {
		full := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	for rel, content := range files {
		want, err := Run(dir, "hash-object", rel)
		if err != nil {
			t.Fatalf("git hash-object %s: %v", rel, err)
		}
		got, err := BlobSHA(dir, []byte(content))
		if err != nil {
			t.Fatalf("BlobSHA(%s): %v", rel, err)
		}
		if got != want {
			t.Errorf("BlobSHA(%s) = %q, want %q (git hash-object)", rel, got, want)
		}

		// FileBlobSHA must agree, reading the same bytes off disk itself.
		gotFile, err := FileBlobSHA(dir, rel)
		if err != nil {
			t.Fatalf("FileBlobSHA(%s): %v", rel, err)
		}
		if gotFile != want {
			t.Errorf("FileBlobSHA(%s) = %q, want %q", rel, gotFile, want)
		}
	}
}

// TestBlobSHA_SHA256Repo checks the sha256 branch against a real sha256
// repository's own `git hash-object` output — skipped only if this git binary
// genuinely cannot create one (older git), never silently.
func TestBlobSHA_SHA256Repo(t *testing.T) {
	dir := t.TempDir()
	if _, err := Run(dir, "init", "-q", "-b", "main", "--object-format=sha256"); err != nil {
		t.Skipf("git does not support --object-format=sha256: %v", err)
	}
	format, err := Run(dir, "rev-parse", "--show-object-format")
	if err != nil {
		t.Fatalf("rev-parse --show-object-format: %v", err)
	}
	if format != "sha256" {
		t.Fatalf("repo object format = %q, want sha256 (test setup broken, not a skip)", format)
	}

	content := []byte("sha256 object format content\n")
	if err := os.WriteFile(filepath.Join(dir, "f.txt"), content, 0o644); err != nil {
		t.Fatal(err)
	}
	want, err := Run(dir, "hash-object", "f.txt")
	if err != nil {
		t.Fatalf("git hash-object: %v", err)
	}
	if len(want) != 64 {
		t.Fatalf("expected a 64-hex-char sha256 object id from git itself, got %q (len %d)", want, len(want))
	}

	got, err := BlobSHA(dir, content)
	if err != nil {
		t.Fatalf("BlobSHA: %v", err)
	}
	if got != want {
		t.Errorf("BlobSHA (sha256 repo) = %q, want %q (git hash-object)", got, want)
	}

	// A second call must hit the cache, not re-invoke git, and still agree.
	got2, err := BlobSHA(dir, content)
	if err != nil {
		t.Fatalf("BlobSHA (cached): %v", err)
	}
	if got2 != want {
		t.Errorf("cached BlobSHA = %q, want %q", got2, want)
	}
}

// TestBlobSHA_OutsideRepo falls back to sha1 (git's universal default) when
// repoRoot names no repository at all.
func TestBlobSHA_OutsideRepo(t *testing.T) {
	dir := t.TempDir() // deliberately not `git init`-ed
	got, err := BlobSHA(dir, []byte("no repo here\n"))
	if err != nil {
		t.Fatalf("BlobSHA: %v", err)
	}
	if len(got) != 40 {
		t.Errorf("outside a repo, BlobSHA must fall back to sha1 (40 hex chars), got %q (len %d)", got, len(got))
	}
	if strings.TrimSpace(got) != got {
		t.Errorf("BlobSHA must not have surrounding whitespace: %q", got)
	}
}
