package review

import (
	"archive/zip"
	"bytes"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
)

func sampleBundle() Bundle {
	return Bundle{
		Manifest: BundleManifest{
			FormatVersion: BundleFormatVersion, Repo: "shop",
			BaseRef: "main", BaseCommit: "aaaaaaa1", HeadRef: "HEAD", HeadCommit: "bbbbbbb2",
			Command: "chronicle review main..HEAD", Model: "m", CreatedAt: "2026-10-09T10:00:00Z",
		},
		BaseSnapshot: []byte("base\n"),
		HeadSnapshot: []byte("head\n"),
		ReviewJSON:   []byte(`{"SchemaVersion":1}`),
		Markdown:     "### Chronicle review\n",
		UsageJSON:    []byte(`{"tokens":1}`),
	}
}

func zipNames(t *testing.T, path string) []string {
	t.Helper()
	zr, err := zip.OpenReader(path)
	if err != nil {
		t.Fatal(err)
	}
	defer zr.Close()
	var names []string
	for _, f := range zr.File {
		names = append(names, f.Name)
	}
	sort.Strings(names)
	return names
}

func TestBundle_RoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "b.zip")
	in := sampleBundle()
	if err := WriteBundle(path, in); err != nil {
		t.Fatalf("WriteBundle: %v", err)
	}
	want := []string{"base.graph.jsonl", "head.graph.jsonl", "manifest.json", "review.json", "review.md", "usage.json"}
	if got := zipNames(t, path); !reflect.DeepEqual(got, want) {
		t.Fatalf("entries = %v, want %v", got, want)
	}
	out, err := ReadBundle(path)
	if err != nil {
		t.Fatalf("ReadBundle: %v", err)
	}
	if !reflect.DeepEqual(*out, in) {
		t.Errorf("round trip mismatch:\n got %+v\nwant %+v", *out, in)
	}
}

func TestBundle_OptionalEntriesOmitted(t *testing.T) {
	path := filepath.Join(t.TempDir(), "b.zip")
	in := sampleBundle()
	in.BaseSnapshot, in.UsageJSON = nil, nil
	if err := WriteBundle(path, in); err != nil {
		t.Fatal(err)
	}
	want := []string{"head.graph.jsonl", "manifest.json", "review.json", "review.md"}
	if got := zipNames(t, path); !reflect.DeepEqual(got, want) {
		t.Fatalf("entries = %v, want %v", got, want)
	}
	out, err := ReadBundle(path)
	if err != nil {
		t.Fatal(err)
	}
	if out.BaseSnapshot != nil || out.UsageJSON != nil {
		t.Errorf("absent entries must read back nil, got base=%v usage=%v", out.BaseSnapshot, out.UsageJSON)
	}
}

func TestBundle_RejectsNewerFormat(t *testing.T) {
	path := filepath.Join(t.TempDir(), "b.zip")
	in := sampleBundle()
	in.Manifest.FormatVersion = BundleFormatVersion + 1
	if err := WriteBundle(path, in); err != nil {
		t.Fatal(err)
	}
	_, err := ReadBundle(path)
	if err == nil || !strings.Contains(err.Error(), "newer") {
		t.Fatalf("ReadBundle err = %v, want a newer-format error", err)
	}
}

func TestBundle_RejectsOversizedEntry(t *testing.T) {
	path := filepath.Join(t.TempDir(), "b.zip")
	in := sampleBundle()
	in.Markdown = strings.Repeat("x", 5000)
	if err := WriteBundle(path, in); err != nil {
		t.Fatal(err)
	}
	old := bundleEntryLimit
	bundleEntryLimit = 2000
	defer func() { bundleEntryLimit = old }()
	_, err := ReadBundle(path)
	if err == nil || !strings.Contains(err.Error(), "review.md exceeds") {
		t.Fatalf("ReadBundle err = %v, want an overflow error for review.md", err)
	}
}

func TestBundle_IgnoresUnsafeEntryNames(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "sub", "b.zip")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := WriteBundle(path, sampleBundle()); err != nil {
		t.Fatal(err)
	}
	// Re-pack with an extra traversal entry.
	zr, err := zip.OpenReader(path)
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, f := range zr.File {
		w, _ := zw.Create(f.Name)
		rc, _ := f.Open()
		io.Copy(w, rc)
		rc.Close()
	}
	zr.Close()
	w, _ := zw.Create("../evil")
	w.Write([]byte("evil"))
	zw.Close()
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}

	out, err := ReadBundle(path)
	if err != nil {
		t.Fatalf("ReadBundle: %v", err)
	}
	if !reflect.DeepEqual(*out, sampleBundle()) {
		t.Errorf("bundle changed by the extra entry")
	}
	if _, err := os.Stat(filepath.Join(root, "evil")); err == nil {
		t.Error("../evil was created")
	}
}
