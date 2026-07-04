package graph

import (
	"encoding/json"
	"fmt"
	"hash/fnv"
	"sort"
	"strconv"
	"strings"

	"github.com/alexdx2/chronicle-core/store"
	"github.com/alexdx2/chronicle-core/validate"
)

// MinHash near-clone detection (adapted from codebase-memory-mcp's
// SIMILAR_TO pass): token 3-shingles hashed through minhashPerms permutations;
// matching signature slots estimate Jaccard similarity. Pairs at or above
// similarityThreshold are copy-paste twins worth a refactor look.
const (
	minhashPerms        = 64
	shingleSize         = 3
	similarityThreshold = 0.8
	similarToEdgeType   = "SIMILAR_TO"
)

type minhashSig [minhashPerms]uint64

// minhashSignature computes a MinHash signature over token 3-shingles of the
// content. Tokenization is language-agnostic: identifier/number runs are
// tokens, other non-space characters are single-char tokens. Deterministic.
func minhashSignature(content []byte) minhashSig {
	tokens := tokenize(content)
	var sig minhashSig
	for i := range sig {
		sig[i] = ^uint64(0)
	}
	if len(tokens) < shingleSize {
		return sig
	}
	for i := 0; i+shingleSize <= len(tokens); i++ {
		h := fnv.New64a()
		for j := 0; j < shingleSize; j++ {
			h.Write([]byte(tokens[i+j]))
			h.Write([]byte{0})
		}
		base := h.Sum64()
		// Cheap universal-hash family: mix the base hash with per-permutation
		// odd multipliers (splitmix-style finalization keeps bits spread).
		for p := 0; p < minhashPerms; p++ {
			v := base ^ (uint64(p)*0x9E3779B97F4A7C15 + 0xBF58476D1CE4E5B9)
			v ^= v >> 30
			v *= 0xBF58476D1CE4E5B9
			v ^= v >> 27
			if v < sig[p] {
				sig[p] = v
			}
		}
	}
	return sig
}

// tokenize splits content into identifier/number tokens and single-char
// punctuation tokens; whitespace separates.
func tokenize(content []byte) []string {
	var tokens []string
	i := 0
	isWord := func(c byte) bool {
		return c == '_' || (c >= '0' && c <= '9') ||
			(c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
	}
	for i < len(content) {
		c := content[i]
		switch {
		case c == ' ' || c == '\t' || c == '\n' || c == '\r':
			i++
		case isWord(c):
			j := i + 1
			for j < len(content) && isWord(content[j]) {
				j++
			}
			tokens = append(tokens, string(content[i:j]))
			i = j
		default:
			tokens = append(tokens, string(c))
			i++
		}
	}
	return tokens
}

// fingerprintFromMetadata decodes a cached minhash signature from node
// Metadata ("fp" hex). ok=false when absent or malformed.
func fingerprintFromMetadata(metadata string) (minhashSig, bool) {
	var sig minhashSig
	if metadata == "" {
		return sig, false
	}
	var wrap struct {
		FP string `json:"fp"`
	}
	if err := json.Unmarshal([]byte(metadata), &wrap); err != nil || len(wrap.FP) != minhashPerms*16 {
		return sig, false
	}
	for i := 0; i < minhashPerms; i++ {
		v, err := strconv.ParseUint(wrap.FP[i*16:(i+1)*16], 16, 64)
		if err != nil {
			return sig, false
		}
		sig[i] = v
	}
	return sig, true
}

// mergeFingerprint stores the signature as hex under the "fp" Metadata key.
func mergeFingerprint(metadata string, sig minhashSig) (string, error) {
	root, err := metadataMap(metadata)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	b.Grow(minhashPerms * 16)
	for _, v := range sig {
		fmt.Fprintf(&b, "%016x", v)
	}
	root["fp"] = b.String()
	return marshalMetadata(root)
}

// jaccardEstimate estimates Jaccard similarity as the fraction of matching
// signature slots.
func jaccardEstimate(a, b minhashSig) float64 {
	match := 0
	for i := range a {
		if a[i] == b[i] {
			match++
		}
	}
	return float64(match) / float64(minhashPerms)
}

// ComputeSimilarity detects near-duplicate code units: it fingerprints every
// file-backed code node and emits a SIMILAR_TO edge (with the jaccard score in
// metadata + a heuristic minhash evidence row) for each pair at or above the
// threshold. Pairwise comparison is fine at Chronicle's node counts (units,
// not functions); deterministic order. Unreadable files are skipped.
func (g *Graph) ComputeSimilarity(revisionID int64) error {
	return g.computeSimilarity(revisionID, nil)
}

// computeSimilarity is the scoped worker. Fingerprints are cached in node
// Metadata ("fp" hex): on an incremental finalize only in-scope (changed)
// files are re-read and re-fingerprinted; everything else reuses its cached
// signature, so the pairwise comparison still spans the whole repo without
// touching unchanged files.
func (g *Graph) computeSimilarity(revisionID int64, scope fileScope) error {
	nodes, err := g.store.ListNodes(store.NodeFilter{Layer: "code"})
	if err != nil {
		return fmt.Errorf("ComputeSimilarity nodes: %w", err)
	}
	type entry struct {
		key string
		sig minhashSig
	}
	var entries []entry
	seenFile := map[string]bool{}
	for _, n := range nodes {
		if n.FilePath == "" || seenFile[n.FilePath] {
			continue
		}
		var sig minhashSig
		if cached, ok := fingerprintFromMetadata(n.Metadata); ok && !scope.matches(n.FilePath) {
			sig = cached
		} else {
			content := readFileContent(n.FilePath)
			if content == nil {
				if !ok {
					continue // no file, no cache — nothing to compare
				}
				sig = cached // unreadable now, cached fingerprint still stands
			} else {
				sig = minhashSignature(content)
				merged, err := mergeFingerprint(n.Metadata, sig)
				if err != nil {
					return fmt.Errorf("ComputeSimilarity fp merge %s: %w", n.NodeKey, err)
				}
				if err := g.store.UpdateNodeMetadata(n.NodeID, merged); err != nil {
					return fmt.Errorf("ComputeSimilarity fp cache %s: %w", n.NodeKey, err)
				}
			}
		}
		seenFile[n.FilePath] = true
		entries = append(entries, entry{key: n.NodeKey, sig: sig})
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].key < entries[j].key })

	for i := 0; i < len(entries); i++ {
		for j := i + 1; j < len(entries); j++ {
			jac := jaccardEstimate(entries[i].sig, entries[j].sig)
			if jac < similarityThreshold {
				continue
			}
			from, to := entries[i].key, entries[j].key
			meta := fmt.Sprintf(`{"jaccard":%.4f,"method":"minhash/%d"}`, jac, minhashPerms)
			if _, err := g.UpsertEdge(validate.EdgeInput{
				FromNodeKey: from, ToNodeKey: to,
				EdgeType: similarToEdgeType, DerivationKind: "inferred",
				FromLayer: "code", ToLayer: "code",
				Confidence: jac, Metadata: meta,
			}, revisionID); err != nil {
				return fmt.Errorf("ComputeSimilarity edge %s~%s: %w", from, to, err)
			}
			edgeKey := from + "->" + to + ":" + similarToEdgeType
			if _, err := g.AddEdgeEvidence(edgeKey, validate.EvidenceInput{
				SourceKind:       "ast",
				ExtractorID:      "similarity-minhash",
				ExtractorVersion: "1",
				ASTRule:          "similarity/v1",
				Confidence:       jac,
				RevisionID:       revisionID,
				Assertion:        meta,
				AssertionKind:    "similarity",
				Metadata:         `{"metric_type":"heuristic"}`,
			}); err != nil {
				return fmt.Errorf("ComputeSimilarity evidence: %w", err)
			}
		}
	}
	return nil
}
