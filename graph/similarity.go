package graph

import (
	"fmt"
	"hash/fnv"
	"sort"

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
		content := readFileContent(n.FilePath)
		if content == nil {
			continue
		}
		seenFile[n.FilePath] = true
		entries = append(entries, entry{key: n.NodeKey, sig: minhashSignature(content)})
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
