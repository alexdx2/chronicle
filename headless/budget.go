package headless

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"strings"
	"sync"

	"github.com/alexdx2/chronicle-core/graph"
	"github.com/alexdx2/chronicle-core/graph/prompts"
	"github.com/alexdx2/chronicle-core/llm"
)

// techExts lists the file extensions a built-in tech pack is about. A pack that
// is not listed (a project pack, kafka) applies to every file: messaging code
// is written in any language, and a custom pack is the project's own call.
var techExts = map[string][]string{
	"typescript": {".ts", ".tsx", ".mts", ".cts", ".js", ".jsx", ".mjs", ".cjs"},
	"nestjs":     {".ts", ".tsx", ".mts", ".cts", ".js", ".jsx", ".mjs", ".cjs"},
	"graphql":    {".ts", ".tsx", ".js", ".jsx", ".graphql", ".gql"},
	"prisma":     {".prisma", ".ts", ".tsx", ".js", ".jsx", ".mjs", ".cjs"},
	"dotnet":     {".cs", ".csproj", ".fs", ".vb", ".cshtml", ".razor", ".sln", ".props", ".config"},
}

// techForFile keeps the manifest technologies that can matter for one file, so
// a Go file is not sent the NestJS and Prisma rules (~3k tokens per request).
// A tech with a catalog pack matches by the pack's Match extensions (none =
// every file); built-in packs keep the curated techExts table, whose lists are
// wider than the extensions their Match prose happens to name.
func techForFile(path string, tech []string, cat *prompts.Catalog) []string {
	ext := strings.ToLower(filepath.Ext(path))
	out := make([]string, 0, len(tech))
	for _, t := range tech {
		key := strings.ToLower(t)
		var exts []string
		legacy, hasLegacy := techExts[key]
		if p, ok := catGet(cat, key); ok && !(p.Source == prompts.SourceBuiltin && hasLegacy) {
			exts = p.Exts // fleet/project packs: by their own Match section
		} else {
			exts = legacy // curated table for built-ins (their Match prose is not extension-complete)
		}
		if len(exts) == 0 {
			out = append(out, t)
			continue
		}
		for _, e := range exts {
			if e == ext {
				out = append(out, t)
				break
			}
		}
	}
	return out
}

func catGet(cat *prompts.Catalog, id string) (prompts.CatalogPack, bool) {
	if cat == nil {
		return prompts.CatalogPack{}, false
	}
	return cat.Get(id)
}

type guideEntry struct {
	guide string
	packs []prompts.PackRef
}

// guideSet renders the extraction guide per file, memoised by the technology
// set it resolves to (a repo has a handful of distinct sets, not one per file).
type guideSet struct {
	g    *graph.Graph
	tech []string
	cat  *prompts.Catalog
	mu   sync.Mutex
	memo map[string]guideEntry
}

func newGuideSet(g *graph.Graph, tech []string, cat *prompts.Catalog) *guideSet {
	return &guideSet{g: g, tech: tech, cat: cat, memo: map[string]guideEntry{}}
}

// For returns the guide for one file and the pack versions it contains.
func (s *guideSet) For(path string) (string, []prompts.PackRef) {
	t := techForFile(path, s.tech, s.cat)
	key := strings.Join(t, ",")
	s.mu.Lock()
	defer s.mu.Unlock()
	if v, ok := s.memo[key]; ok {
		return v.guide, v.packs
	}
	guide, packs := graph.ExtractionGuideFrom(s.cat, t...)
	if s.g != nil {
		// Agent-path setting override, as today.
		if custom, err := s.g.Store().GetSetting("guide_fact_schema"); err == nil && custom != "" {
			guide, packs = custom, nil
		}
	}
	s.memo[key] = guideEntry{guide, packs}
	return guide, packs
}

const (
	// charsPerToken is deliberately pessimistic: code with long identifiers
	// and the JSON candidate list tokenise at 2.5-3.5 chars per token, and an
	// underestimate silently truncates the prompt from the front.
	charsPerToken = 3
	// outputReserve is the share of the window kept for the model's answer.
	// Observed answers are under 2k tokens; the window also holds them.
	outputReserve = 2048
	// minChunkBytes stops chunking when the system prompt alone nearly fills
	// the window: a model that small cannot do this job.
	minChunkBytes = 2048
	// partLabelReserve covers the "(part i of n ...)" line added to chunked prompts.
	partLabelReserve = 192
)

// fileBudget returns how many bytes of file content fit in one request for a
// backend with a hard token window, after the fixed part of the prompt. Zero
// window means no hard limit (cloud models).
func fileBudget(window int, req llm.Request) int {
	if window <= 0 {
		return 0
	}
	fixed := (len(req.System) + len(req.User) + partLabelReserve) / charsPerToken
	free := window - outputReserve - fixed
	if free*charsPerToken < minChunkBytes {
		return minChunkBytes
	}
	return free * charsPerToken
}

// splitLines cuts content into pieces of at most max bytes at line boundaries.
// A single line longer than max is cut on a UTF-8 boundary.
func splitLines(content []byte, max int) [][]byte {
	if max <= 0 || len(content) <= max {
		return [][]byte{content}
	}
	var out [][]byte
	for len(content) > 0 {
		if len(content) <= max {
			out = append(out, content)
			break
		}
		cut := bytes.LastIndexByte(content[:max], '\n') + 1
		if cut <= 0 {
			cut = max
			for cut > 0 && cut < len(content) && content[cut]&0xC0 == 0x80 {
				cut--
			}
			if cut == 0 {
				cut = max
			}
		}
		out = append(out, content[:cut])
		content = content[cut:]
	}
	return out
}

// mergeAnswers folds the answers for the chunks of one file into one. Facts are
// concatenated without exact duplicates; the file is "extracted" if any chunk
// extracted something, otherwise the first chunk's status stands.
func mergeAnswers(parts []*parsedAnswer) *parsedAnswer {
	merged := &parsedAnswer{Status: parts[0].Status, FromType: parts[0].FromType}
	var facts []json.RawMessage
	seen := map[string]bool{}
	for _, p := range parts {
		if p.Status == "extracted" {
			merged.Status = "extracted"
		}
		if merged.FromType == "" {
			merged.FromType = p.FromType
		}
		var fs []json.RawMessage
		if json.Unmarshal(p.Facts, &fs) != nil {
			continue
		}
		for _, f := range fs {
			k := string(f)
			if !seen[k] {
				seen[k] = true
				facts = append(facts, f)
			}
		}
	}
	if facts == nil {
		facts = []json.RawMessage{}
	}
	merged.Facts, _ = json.Marshal(facts)
	return merged
}
