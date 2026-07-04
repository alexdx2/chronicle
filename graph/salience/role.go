package salience

import "sort"

// RoleClaim is one extractor's classification of a node's semantic role, with
// the extractor's own confidence (NOT system trust) and a short reason. Claims
// are recorded as node evidence (source_kind "role_classification") so they are
// auditable and can accumulate across scans/extractors.
type RoleClaim struct {
	Role       string
	Confidence float64
	Reason     string
}

// ResolveRole picks the winning role among claims for one node: highest
// confidence wins; ties break to the lexically smallest role for determinism.
// "unknown"/"" claims are ignored unless they are the only claims present.
// Returns ok=false when there are no claims at all.
func ResolveRole(claims []RoleClaim) (RoleClaim, bool) {
	if len(claims) == 0 {
		return RoleClaim{}, false
	}
	// Prefer concrete roles; fall back to unknown-only.
	concrete := make([]RoleClaim, 0, len(claims))
	for _, c := range claims {
		if c.Role != "" && c.Role != "unknown" {
			concrete = append(concrete, c)
		}
	}
	pool := concrete
	if len(pool) == 0 {
		pool = claims
	}
	sort.Slice(pool, func(i, j int) bool {
		if pool[i].Confidence != pool[j].Confidence {
			return pool[i].Confidence > pool[j].Confidence // higher confidence first
		}
		return pool[i].Role < pool[j].Role // deterministic tie-break
	})
	return pool[0], true
}
