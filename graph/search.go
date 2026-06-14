package graph

import (
	"fmt"
	"sort"
	"strings"

	"github.com/alexdx2/chronicle-core/store"
	"github.com/alexdx2/chronicle-core/validate"
)

// SearchResult is one ranked hit from NodeSearch.
type SearchResult struct {
	NodeKey   string  `json:"node_key"`
	Name      string  `json:"name"`
	Layer     string  `json:"layer"`
	NodeType  string  `json:"node_type"`
	Domain    string  `json:"domain_key"`
	FilePath  string  `json:"file_path,omitempty"`
	Trust     float64 `json:"trust_score"`
	Status    string  `json:"status"`
	MatchKind string  `json:"match_kind"` // exact_name|exact_alias|glossary|prefix|substring|path
	Score     float64 `json:"score"`
	Repo      string  `json:"repo,omitempty"` // set by federation; empty for single-repo
}

// Match tiers per query term. The binding constraint (lowest tier across terms)
// determines the reported match_kind.
const (
	tierExact     = 100.0
	tierGlossary  = 90.0
	tierPrefix    = 80.0
	tierSubstring = 60.0
	tierPath      = 40.0
)

// NodeSearch finds node keys by name fragment. Deterministic lexical matching
// only — no question understanding; the agent picks the terms. All comparisons
// happen on validate.NormalizeName kebab forms (plus a dash-free "flat" form),
// so OrderController ≡ order-controller ≡ order_controller.
//
// Multi-term queries are AND: every whitespace-separated term must match the
// node somewhere (name, key tail, alias, glossary term, or file path).
// Ordering: score desc, trust desc, node_key asc.
func (g *Graph) NodeSearch(q string, f store.NodeFilter, limit int) ([]SearchResult, error) {
	terms := strings.Fields(q)
	if len(terms) == 0 {
		return nil, fmt.Errorf("NodeSearch: query is required — pass a name fragment like 'OrderController'")
	}
	if limit <= 0 {
		limit = 10
	}
	if limit > 50 {
		limit = 50
	}

	nodes, err := g.store.ListNodes(f)
	if err != nil {
		return nil, fmt.Errorf("NodeSearch: %w", err)
	}

	aliasesByNode := map[int64][]string{}
	allAliases, err := g.store.ListAliasesAll()
	if err != nil {
		return nil, fmt.Errorf("NodeSearch aliases: %w", err)
	}
	for _, a := range allAliases {
		aliasesByNode[a.NodeID] = append(aliasesByNode[a.NodeID], norm(a.Alias))
	}

	// Glossary: a query term matching a domain term (or one of its aliases)
	// promotes nodes whose normalized name equals the term itself.
	terms2glossary := map[string]map[string]bool{} // norm(query term) → set of norm(glossary term)
	glossary, err := g.store.GetGlossary("")
	if err != nil {
		return nil, fmt.Errorf("NodeSearch glossary: %w", err)
	}
	for _, dt := range glossary {
		names := append([]string{dt.Term}, dt.Aliases...)
		for _, n := range names {
			key := norm(n)
			if terms2glossary[key] == nil {
				terms2glossary[key] = map[string]bool{}
			}
			terms2glossary[key][norm(dt.Term)] = true
		}
	}

	var results []SearchResult
	for _, n := range nodes {
		if n.Status == "deleted" {
			continue
		}
		cand := nodeCandidates(n, aliasesByNode[n.NodeID])

		total := 0.0
		minTier := tierExact + 1
		minKind := ""
		matched := true
		for _, term := range terms {
			tier, kind := matchTerm(norm(term), cand, terms2glossary)
			if tier == 0 {
				matched = false
				break
			}
			total += tier
			if tier < minTier {
				minTier, minKind = tier, kind
			}
		}
		if !matched {
			continue
		}
		results = append(results, SearchResult{
			NodeKey: n.NodeKey, Name: n.Name, Layer: n.Layer, NodeType: n.NodeType,
			Domain: n.DomainKey, FilePath: n.FilePath, Trust: n.TrustScore,
			Status: n.Status, MatchKind: minKind, Score: total,
		})
	}

	sort.Slice(results, func(i, j int) bool {
		if results[i].Score != results[j].Score {
			return results[i].Score > results[j].Score
		}
		if results[i].Trust != results[j].Trust {
			return results[i].Trust > results[j].Trust
		}
		return results[i].NodeKey < results[j].NodeKey
	})
	if len(results) > limit {
		results = results[:limit]
	}
	return results, nil
}

// candidates holds the normalized strings a node can match against.
type candidates struct {
	exactNames []string // norm name, key tail, qualified name, full key
	aliases    []string // norm aliases
	path       string   // norm file path
}

func nodeCandidates(n store.NodeRow, aliases []string) candidates {
	c := candidates{aliases: aliases, path: norm(n.FilePath)}
	c.exactNames = append(c.exactNames, norm(n.Name))
	if i := strings.LastIndex(n.NodeKey, ":"); i >= 0 {
		c.exactNames = append(c.exactNames, norm(n.NodeKey[i+1:]))
	}
	if n.QualifiedName != "" {
		c.exactNames = append(c.exactNames, norm(n.QualifiedName))
	}
	c.exactNames = append(c.exactNames, strings.ToLower(n.NodeKey))
	return c
}

// matchTerm returns the best tier for one normalized query term, with its kind.
func matchTerm(term string, c candidates, glossary map[string]map[string]bool) (float64, string) {
	for _, en := range c.exactNames {
		if eq(en, term) {
			return tierExact, "exact_name"
		}
	}
	for _, al := range c.aliases {
		if eq(al, term) {
			return tierExact, "exact_alias"
		}
	}
	if gterms, ok := glossary[term]; ok {
		for _, en := range c.exactNames {
			if gterms[en] {
				return tierGlossary, "glossary"
			}
		}
	}
	for _, en := range append(c.exactNames, c.aliases...) {
		if strings.HasPrefix(en, term) || strings.HasPrefix(flat(en), flat(term)) {
			return tierPrefix, "prefix"
		}
	}
	for _, en := range append(c.exactNames, c.aliases...) {
		if strings.Contains(en, term) || strings.Contains(flat(en), flat(term)) {
			return tierSubstring, "substring"
		}
	}
	if c.path != "" && (strings.Contains(c.path, term) || strings.Contains(flat(c.path), flat(term))) {
		return tierPath, "path"
	}
	return 0, ""
}

// norm produces the canonical kebab form shared with key validation.
func norm(s string) string { return validate.NormalizeName(s) }

// flat removes separators so ordercontroller ≡ order-controller.
func flat(s string) string {
	return strings.NewReplacer("-", "", "_", "", ".", "", "/", "").Replace(s)
}

func eq(a, b string) bool { return a == b || flat(a) == flat(b) }
