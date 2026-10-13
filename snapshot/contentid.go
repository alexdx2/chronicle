package snapshot

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
)

// ContentID is the content identity of a snapshot (spec §3.1): sha256 over
// the canonical graph.jsonl re-encoded with Header.Seq = 0, then the domain
// manifest bytes, then the types bytes (empty when absent), separated by
// 0x1f. Two publications of the same graph share one ContentID whatever
// their Seq, commit or record order; any change to a record changes it.
//
// It is computed from exported FILES, never from a store: Encode normalizes
// record and key order and recomputes Counts, so the bytes hashed here are
// the canonical form regardless of how the input file was assembled.
// Tolerated Decode warnings (bad_json, unknown_kind, ...) drop the offending
// line exactly like every other reader does. Input without a snapshot header
// (empty, binary garbage, node lines only) is an error: it must not yield a
// valid-looking id, since ContentID is compared at a trust boundary.
//
// For a version 2 graph the profile bytes are appended after types, again
// separated by 0x1f (always present, possibly empty); version 1 graphs hash
// exactly the legacy bytes and ignore Profile.
func ContentIDFiles(f Files) (string, error) {
	graph, domain, types := f.Graph, f.Domain, f.Types
	snap, _, err := Decode(bytes.NewReader(graph), DefaultLimits())
	if err != nil {
		return "", fmt.Errorf("snapshot: ContentID: %w", err)
	}
	if snap == nil || snap.Header.Kind != "header" {
		return "", errors.New("snapshot: ContentID: no snapshot header in graph.jsonl")
	}
	if snap.Header.Format != Format {
		return "", fmt.Errorf("snapshot: ContentID: unrecognized format %q (want %q)", snap.Header.Format, Format)
	}
	snap.Header.Seq = 0
	var canon bytes.Buffer
	if err := Encode(&canon, snap); err != nil {
		return "", fmt.Errorf("snapshot: ContentID: %w", err)
	}
	h := sha256.New()
	h.Write(canon.Bytes())
	h.Write([]byte{0x1f})
	h.Write(domain)
	h.Write([]byte{0x1f})
	h.Write(types)
	if snap.Header.Version >= 2 {
		h.Write([]byte{0x1f})
		h.Write(f.Profile)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// ContentID is ContentIDFiles for graph, domain and types only. For a
// version 2 graph it therefore hashes an empty profile.
func ContentID(graph, domain, types []byte) (string, error) {
	return ContentIDFiles(Files{Graph: graph, Domain: domain, Types: types})
}
