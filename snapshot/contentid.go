package snapshot

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
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
// line exactly like every other reader does; a file that cannot be read at
// all is an error.
func ContentID(graph, domain, types []byte) (string, error) {
	snap, _, err := Decode(bytes.NewReader(graph), DefaultLimits())
	if err != nil {
		return "", fmt.Errorf("snapshot: ContentID: %w", err)
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
	return hex.EncodeToString(h.Sum(nil)), nil
}
