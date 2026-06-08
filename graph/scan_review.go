package graph

import (
	"encoding/json"
	"fmt"
)

// ReviewCandidate is a file that may need a second extraction pass after resolve.
type ReviewCandidate struct {
	FilePath       string `json:"file_path"`
	ObligationID   int64  `json:"obligation_id,omitempty"`
	Reason         string `json:"reason"`
	UnresolvedRefs int    `json:"unresolved_refs,omitempty"`
}

// ScanReviewCandidates lists files worth re-extracting in the post-resolve review pass.
func (g *Graph) ScanReviewCandidates(domainKey string, revisionID int64) ([]ReviewCandidate, error) {
	extractions, err := g.store.ListUnresolvedExtractions(revisionID, domainKey)
	if err != nil {
		return nil, fmt.Errorf("ScanReviewCandidates: %w", err)
	}

	var candidates []ReviewCandidate
	seen := make(map[string]bool)
	for _, ext := range extractions {
		if seen[ext.FilePath] {
			continue
		}
		seen[ext.FilePath] = true
		normalized := normalizeFacts(ext.FactsJSON)
		var facts []Fact
		if err := json.Unmarshal([]byte(normalized), &facts); err != nil {
			candidates = append(candidates, ReviewCandidate{
				FilePath: ext.FilePath,
				Reason:   "parse_error",
			})
			continue
		}
		unresolved := countUnresolvedFacts(facts)
		if unresolved > 0 {
			candidates = append(candidates, ReviewCandidate{
				FilePath:       ext.FilePath,
				Reason:         "unresolved_refs",
				UnresolvedRefs: unresolved,
			})
		}
	}
	return candidates, nil
}

func countUnresolvedFacts(facts []Fact) int {
	n := 0
	for _, f := range facts {
		if f.Confidence > 0 && f.Confidence < 0.5 {
			n++
		}
		if f.Note != "" || f.Reason != "" {
			n++
		}
	}
	return n
}
