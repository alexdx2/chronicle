package review

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
)

// BundleFormatVersion is the version of the review bundle zip layout.
const BundleFormatVersion = 1

// Bundle zip entry names.
const (
	bundleManifestEntry = "manifest.json"
	bundleBaseEntry     = "base.graph.jsonl"
	bundleHeadEntry     = "head.graph.jsonl"
	bundleReviewEntry   = "review.json"
	bundleMarkdownEntry = "review.md"
	bundleUsageEntry    = "usage.json"
)

// bundleEntryLimit caps how much one zip entry may inflate to when read.
var bundleEntryLimit int64 = 256 << 20

// BundleManifest describes how and from what a bundle was produced. The
// creation timestamp lives only here, never in the review markdown/JSON.
type BundleManifest struct {
	FormatVersion int    `json:"format_version"`
	Repo          string `json:"repo"` // basename of RepoRoot
	BaseRef       string `json:"base_ref"`
	BaseCommit    string `json:"base_commit"`
	HeadRef       string `json:"head_ref"`
	HeadCommit    string `json:"head_commit"`
	Command       string `json:"command"`
	Model         string `json:"model,omitempty"`
	CreatedAt     string `json:"created_at"` // RFC3339 UTC
}

// Bundle is a review together with both graph versions it compared, ready to
// be shipped as one file (a CI artifact) and imported into a dashboard.
type Bundle struct {
	Manifest     BundleManifest
	BaseSnapshot []byte // graph.jsonl bytes (nil when base had no graph)
	HeadSnapshot []byte
	ReviewJSON   []byte // RenderJSON output
	Markdown     string
	UsageJSON    []byte // optional
}

// WriteBundle writes b as a zip: manifest.json, base.graph.jsonl (omitted
// when nil), head.graph.jsonl, review.json, review.md, usage.json (omitted
// when nil). A zero Manifest.FormatVersion is written as BundleFormatVersion.
func WriteBundle(path string, b Bundle) (err error) {
	if b.Manifest.FormatVersion == 0 {
		b.Manifest.FormatVersion = BundleFormatVersion
	}
	manifest, err := json.MarshalIndent(b.Manifest, "", "  ")
	if err != nil {
		return fmt.Errorf("review: bundle manifest: %w", err)
	}

	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("review: create bundle: %w", err)
	}
	defer func() {
		if cerr := f.Close(); err == nil && cerr != nil {
			err = fmt.Errorf("review: close bundle: %w", cerr)
		}
	}()

	zw := zip.NewWriter(f)
	entries := []struct {
		name string
		data []byte
		skip bool
	}{
		{bundleManifestEntry, manifest, false},
		{bundleBaseEntry, b.BaseSnapshot, b.BaseSnapshot == nil},
		{bundleHeadEntry, b.HeadSnapshot, false},
		{bundleReviewEntry, b.ReviewJSON, false},
		{bundleMarkdownEntry, []byte(b.Markdown), false},
		{bundleUsageEntry, b.UsageJSON, b.UsageJSON == nil},
	}
	for _, e := range entries {
		if e.skip {
			continue
		}
		w, werr := zw.Create(e.name)
		if werr != nil {
			return fmt.Errorf("review: bundle entry %s: %w", e.name, werr)
		}
		if _, werr := w.Write(e.data); werr != nil {
			return fmt.Errorf("review: bundle entry %s: %w", e.name, werr)
		}
	}
	if err := zw.Close(); err != nil {
		return fmt.Errorf("review: finish bundle: %w", err)
	}
	return nil
}

// ReadBundle reads a bundle written by WriteBundle. It rejects a bundle whose
// format version is newer than this build understands.
func ReadBundle(path string) (*Bundle, error) {
	zr, err := zip.OpenReader(path)
	if err != nil {
		return nil, fmt.Errorf("review: open bundle: %w", err)
	}
	defer zr.Close()

	files := make(map[string]*zip.File, len(zr.File))
	for _, zf := range zr.File {
		files[zf.Name] = zf
	}
	read := func(name string) ([]byte, bool, error) {
		zf, ok := files[name]
		if !ok {
			return nil, false, nil
		}
		rc, err := zf.Open()
		if err != nil {
			return nil, true, fmt.Errorf("review: bundle entry %s: %w", name, err)
		}
		defer rc.Close()
		data, err := io.ReadAll(io.LimitReader(rc, bundleEntryLimit+1))
		if err != nil {
			return nil, true, fmt.Errorf("review: bundle entry %s: %w", name, err)
		}
		if int64(len(data)) > bundleEntryLimit {
			return nil, true, fmt.Errorf("review: bundle entry %s exceeds %d bytes", name, bundleEntryLimit)
		}
		return data, true, nil
	}

	var b Bundle
	raw, ok, err := read(bundleManifestEntry)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, fmt.Errorf("review: bundle has no %s", bundleManifestEntry)
	}
	if err := json.NewDecoder(bytes.NewReader(raw)).Decode(&b.Manifest); err != nil {
		return nil, fmt.Errorf("review: bundle manifest: %w", err)
	}
	if b.Manifest.FormatVersion > BundleFormatVersion {
		return nil, fmt.Errorf("review: bundle format version %d is newer than this chronicle supports (%d) — upgrade chronicle",
			b.Manifest.FormatVersion, BundleFormatVersion)
	}

	if b.BaseSnapshot, _, err = read(bundleBaseEntry); err != nil {
		return nil, err
	}
	if b.HeadSnapshot, ok, err = read(bundleHeadEntry); err != nil {
		return nil, err
	} else if !ok {
		return nil, fmt.Errorf("review: bundle has no %s", bundleHeadEntry)
	}
	if b.ReviewJSON, ok, err = read(bundleReviewEntry); err != nil {
		return nil, err
	} else if !ok {
		return nil, fmt.Errorf("review: bundle has no %s", bundleReviewEntry)
	}
	md, _, err := read(bundleMarkdownEntry)
	if err != nil {
		return nil, err
	}
	b.Markdown = string(md)
	if b.UsageJSON, _, err = read(bundleUsageEntry); err != nil {
		return nil, err
	}
	return &b, nil
}
