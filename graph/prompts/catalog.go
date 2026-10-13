package prompts

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// PackSource ranks where a pack came from; a higher source replaces a lower one.
type PackSource int

const (
	SourceBuiltin PackSource = iota
	SourceFleet
	SourceProject
)

// CatalogPack is one technology pack with its derived metadata.
type CatalogPack struct {
	ID          string
	Version     string // HashContent(Content)
	Description string // first heading line, without '#'
	Match       string // the "## Match" section
	Content     string
	Source      PackSource
	Exts        []string // file extensions named by Match; empty = any file
	// Contains are content tokens from the match: line. A pack that has some
	// attaches to a file only when one occurs in it, except that a file with
	// the pack's primary extension (Exts[0]) always matches.
	Contains []string
	// Structured: the Match section carries a machine-readable match: line.
	Structured bool
}

// Catalog holds one pack per id; the highest source wins.
type Catalog struct {
	packs map[string]CatalogPack
}

func NewCatalog() *Catalog { return &Catalog{packs: map[string]CatalogPack{}} }

// BuiltinCatalog returns a catalog of all built-in technology packs.
func BuiltinCatalog() *Catalog {
	c := NewCatalog()
	for id, tp := range builtinTechPacks {
		c.Add(CatalogPack{ID: id, Content: tp.Content, Source: SourceBuiltin})
	}
	return c
}

// HashContent is the pack version: sha256 hex, first 12 chars.
func HashContent(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])[:12]
}

var matchExtRe = regexp.MustCompile(`(?:^|[^a-z0-9_])\*?(\.[a-z0-9]{1,8})\b`)

// MatchExts returns the file extensions named in a Match section, unique, in
// order of appearance. Empty means the pack applies to any file.
func MatchExts(match string) []string {
	var out []string
	seen := map[string]bool{}
	for _, m := range matchExtRe.FindAllStringSubmatch(strings.ToLower(match), -1) {
		if !seen[m[1]] {
			seen[m[1]] = true
			out = append(out, m[1])
		}
	}
	return out
}

// ParseMatchLine reads the optional machine-readable line of a Match section:
//
//	match: ext=.cs,.csproj; contains=Confluent.Kafka, IProducer
//
// Key names are case-insensitive, either part may be absent, tokens are
// trimmed, extensions are lower-cased and get a leading dot; no regex. ok is
// false when the section has no such line.
func ParseMatchLine(section string) (exts, contains []string, ok bool) {
	for _, line := range strings.Split(section, "\n") {
		key, rest, found := strings.Cut(strings.TrimSpace(line), ":")
		if !found || strings.ToLower(strings.TrimSpace(key)) != "match" {
			continue
		}
		for _, part := range strings.Split(rest, ";") {
			k, v, found := strings.Cut(part, "=")
			if !found {
				continue
			}
			for _, tok := range strings.Split(v, ",") {
				tok = strings.TrimSpace(tok)
				if tok == "" {
					continue
				}
				switch strings.ToLower(strings.TrimSpace(k)) {
				case "ext":
					tok = strings.ToLower(tok)
					if !strings.HasPrefix(tok, ".") {
						tok = "." + tok
					}
					exts = append(exts, tok)
				case "contains":
					contains = append(contains, tok)
				}
			}
		}
		return exts, contains, true
	}
	return nil, nil, false
}

func firstHeading(content string) string {
	for _, line := range strings.Split(content, "\n") {
		if t := strings.TrimSpace(line); strings.HasPrefix(t, "#") {
			return strings.TrimSpace(strings.TrimLeft(t, "#"))
		}
	}
	return ""
}

// Add inserts a pack unless one of a higher source is already present for the
// id. Version, Match, Exts and Description are computed when empty.
func (c *Catalog) Add(p CatalogPack) {
	if cur, ok := c.packs[p.ID]; ok && cur.Source > p.Source {
		return
	}
	if p.Version == "" {
		p.Version = HashContent(p.Content)
	}
	if p.Match == "" {
		p.Match = ExtractMatchSection(p.Content)
	}
	if exts, contains, ok := ParseMatchLine(p.Match); ok {
		p.Structured = true
		if p.Exts == nil {
			p.Exts = exts
		}
		if p.Contains == nil {
			p.Contains = contains
		}
	} else if p.Exts == nil {
		p.Exts = MatchExts(p.Match)
	}
	if p.Description == "" {
		p.Description = firstHeading(p.Content)
	}
	c.packs[p.ID] = p
}

// AddProjectPacks adds every *.md in dir as a project pack (id = lowercased
// file stem, the same form the profile and the fleet registry use).
// A missing dir is not an error.
func (c *Catalog) AddProjectPacks(dir string) error {
	ents, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	for _, e := range ents {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			return err
		}
		c.Add(CatalogPack{ID: strings.ToLower(strings.TrimSuffix(e.Name(), ".md")), Content: string(data), Source: SourceProject})
	}
	return nil
}

func (c *Catalog) Get(id string) (CatalogPack, bool) {
	p, ok := c.packs[id]
	return p, ok
}

// Metas lists pack metadata sorted by id.
func (c *Catalog) Metas() []PackMeta {
	ids := c.ids()
	out := make([]PackMeta, 0, len(ids))
	for _, id := range ids {
		p := c.packs[id]
		out = append(out, PackMeta{ID: id, Type: "technology", Description: p.Description, AppliesWhen: p.Match})
	}
	return out
}

func (c *Catalog) ids() []string {
	ids := make([]string, 0, len(c.packs))
	for id := range c.packs {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

func (c *Catalog) Known() map[string]bool {
	m := make(map[string]bool, len(c.packs))
	for id := range c.packs {
		m[id] = true
	}
	return m
}

// ForTech returns one pack per tech, in tech order, skipping unknown and duplicate ids.
func (c *Catalog) ForTech(tech []string) []CatalogPack {
	var out []CatalogPack
	seen := map[string]bool{}
	for _, t := range tech {
		t = strings.ToLower(strings.TrimSpace(t))
		if p, ok := c.packs[t]; ok && !seen[t] {
			seen[t] = true
			out = append(out, p)
		}
	}
	return out
}

// ComposeCatalog appends the packs to the core guide.
func ComposeCatalog(core string, packs []CatalogPack) string {
	parts := []string{core}
	for _, p := range packs {
		parts = append(parts, "\n---\n"+p.Content)
	}
	return strings.Join(parts, "\n")
}

// Refs lists the packs as id+version, sorted by id.
func Refs(packs []CatalogPack) []PackRef {
	out := make([]PackRef, 0, len(packs))
	for _, p := range packs {
		out = append(out, PackRef{ID: p.ID, Version: p.Version})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}
