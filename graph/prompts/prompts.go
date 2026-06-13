// Package prompts provides the instruction pack system for scan agents.
//
// Architecture:
//   - Core packs (always loaded): fact_schema, enrichment, flow_tracing
//   - Technology packs: each has a "## Match" section describing when to load it
//     The agent reads match sections and decides which packs apply.
//     Resolution: .depbot/packs/{name}.md (project) → built-in (bundled default)
//   - Custom packs (agent-created): saved to .depbot/packs/ during scan
//
// The system is framework-agnostic. Built-in packs are bundled defaults.
// Any technology can be supported by creating a pack during scan discovery.
package prompts

import (
	_ "embed"
	"strings"

	"gopkg.in/yaml.v3"
)

// ─── Embedded content ───────────────────────────────────────────────────────

//go:embed fact_schema.md
var FactSchema string

//go:embed enrichment.md
var Enrichment string

//go:embed flow_tracing.md
var FlowTracing string

//go:embed technologies/prisma.md
var TechPrisma string

//go:embed technologies/nestjs.md
var TechNestJS string

//go:embed technologies/graphql.md
var TechGraphQL string

//go:embed technologies/typescript.md
var TechTypeScript string

//go:embed technologies/dotnet.md
var TechDotNet string

//go:embed technologies/kafka.md
var TechKafka string

//go:embed pack_authoring_guide.md
var PackAuthoringGuide string

//go:embed querying.md
var Querying string

//go:embed registry.yaml
var registryYAML string

// ─── Types ──────────────────────────────────────────────────────────────────

// Pack describes a core instruction pack (always loaded).
type Pack struct {
	ID          string `json:"id" yaml:"id"`
	Type        string `json:"type" yaml:"type"` // core
	Description string `json:"description" yaml:"description"`
	Path        string `json:"path" yaml:"path"`
	AlwaysLoad  bool   `json:"always_load" yaml:"always_load"`
	Content     string `json:"content,omitempty" yaml:"-"`
}

// TechPack describes a technology instruction pack with its match section.
type TechPack struct {
	ID      string `json:"id"`
	Match   string `json:"match"`   // extracted from ## Match section
	Content string `json:"-"`       // full content (loaded on demand)
}

// PackSelection is the result of matching packs for a project.
type PackSelection struct {
	Loaded    []PackMeta `json:"loaded_packs"`
	Available []PackMeta `json:"available_packs,omitempty"`
}

// PackMeta is lightweight metadata for listing packs (no content).
type PackMeta struct {
	ID          string `json:"id"`
	Type        string `json:"type"`
	Description string `json:"description"`
	AppliesWhen string `json:"applies_when,omitempty"`
}

// ─── Registry ───────────────────────────────────────────────────────────────

// contentByPath maps pack paths to their embedded content.
var contentByPath map[string]string

// registry is the parsed core pack list (from registry.yaml).
var registry []Pack

// builtinTechPacks stores all built-in technology packs (keyed by ID).
var builtinTechPacks map[string]*TechPack

func init() {
	contentByPath = map[string]string{
		"fact_schema.md":             FactSchema,
		"enrichment.md":              Enrichment,
		"flow_tracing.md":            FlowTracing,
		"technologies/prisma.md":     TechPrisma,
		"technologies/nestjs.md":     TechNestJS,
		"technologies/graphql.md":    TechGraphQL,
		"technologies/typescript.md": TechTypeScript,
		"technologies/dotnet.md":     TechDotNet,
		"technologies/kafka.md":      TechKafka,
	}

	var reg struct {
		Packs []Pack `yaml:"packs"`
	}
	if err := yaml.Unmarshal([]byte(registryYAML), &reg); err == nil {
		registry = reg.Packs
	}

	// Attach content to each core pack
	for i := range registry {
		if content, ok := contentByPath[registry[i].Path]; ok {
			registry[i].Content = content
		}
	}

	// Built-in technology packs — parsed from embedded markdown files.
	rawTechPacks := map[string]string{
		"typescript": TechTypeScript,
		"nestjs":     TechNestJS,
		"prisma":     TechPrisma,
		"graphql":    TechGraphQL,
		"dotnet":     TechDotNet,
		"kafka":      TechKafka,
	}

	builtinTechPacks = make(map[string]*TechPack, len(rawTechPacks))
	for id, content := range rawTechPacks {
		builtinTechPacks[id] = &TechPack{
			ID:      id,
			Match:   ExtractMatchSection(content),
			Content: content,
		}
	}
}

// ExtractMatchSection pulls the "## Match" section from a pack's markdown.
// Returns everything between "## Match" and the next "##" heading (or end of section).
func ExtractMatchSection(content string) string {
	const marker = "## Match"
	idx := strings.Index(content, marker)
	if idx < 0 {
		return ""
	}
	rest := content[idx+len(marker):]

	// Find the next ## heading
	nextH2 := strings.Index(rest, "\n## ")
	if nextH2 >= 0 {
		rest = rest[:nextH2]
	} else {
		// Also stop at # heading (not ##)
		nextH1 := strings.Index(rest, "\n# ")
		if nextH1 >= 0 {
			rest = rest[:nextH1]
		}
	}
	return strings.TrimSpace(rest)
}

// ─── Core packs ─────────────────────────────────────────────────────────────

// AllPacks returns all core packs (always-loaded, with content).
func AllPacks() []Pack {
	return registry
}

// GetPack returns a core pack by ID.
func GetPack(id string) (Pack, bool) {
	for _, p := range registry {
		if p.ID == id {
			return p, true
		}
	}
	return Pack{}, false
}

// CorePacks returns packs with always_load=true.
func CorePacks() []Pack {
	var out []Pack
	for _, p := range registry {
		if p.AlwaysLoad {
			out = append(out, p)
		}
	}
	return out
}

// ─── Technology packs ───────────────────────────────────────────────────────

// ListBuiltinTechPacks returns all built-in technology packs with their match sections.
// Content is NOT included — use GetTechPackContent to fetch it.
func ListBuiltinTechPacks() []TechPack {
	var out []TechPack
	for _, tp := range builtinTechPacks {
		out = append(out, TechPack{ID: tp.ID, Match: tp.Match})
	}
	return out
}

// GetTechPackContent returns the full content of a built-in technology pack.
func GetTechPackContent(id string) (string, bool) {
	tp, ok := builtinTechPacks[strings.ToLower(id)]
	if !ok {
		return "", false
	}
	return tp.Content, true
}

// HasBuiltinTechPack checks if a built-in technology pack exists.
func HasBuiltinTechPack(id string) bool {
	_, ok := builtinTechPacks[strings.ToLower(id)]
	return ok
}

// ─── Gap detection ──────────────────────────────────────────────────────────

// InstructionGap represents a technology declared in the manifest that has no matching pack.
type InstructionGap struct {
	Tech            string `json:"tech"`
	Reason          string `json:"reason"`
	SuggestedAction string `json:"suggested_action"` // "create_pack"
}

// DetectInstructionGaps finds technologies declared in the manifest that have
// no matching built-in pack. Project-level packs (.depbot/packs/) should be
// checked separately by the caller.
func DetectInstructionGaps(manifestTech []string) []InstructionGap {
	var gaps []InstructionGap
	for _, tech := range manifestTech {
		t := strings.ToLower(strings.TrimSpace(tech))
		if t == "" {
			continue
		}
		if !HasBuiltinTechPack(t) {
			gaps = append(gaps, InstructionGap{
				Tech:            tech,
				Reason:          "no built-in instruction pack — agent should create one",
				SuggestedAction: "create_pack",
			})
		}
	}
	return gaps
}

// ─── Custom pack validation ─────────────────────────────────────────────────

// allowedFactKinds is the set of fact kinds defined by the core schema.
var allowedFactKinds = map[string]bool{
	"import": true, "injects": true, "provides": true,
	"endpoint": true, "http_call": true, "calls_service": true,
	"calls_endpoint": true, "uses_model": true,
	"model": true, "enum": true, "model_relation": true,
	"produces": true, "consumes": true,
	"decorator": true, "constructor_param": true,
	"call": true, "member_call": true,
	"flow": true, "delegates": true, "declares_service": true,
	"parent": true,
}

// ValidateCustomPack checks that a custom pack's content doesn't introduce unknown fact kinds.
func ValidateCustomPack(content string) []string {
	var invalid []string
	seen := map[string]bool{}

	for _, line := range strings.Split(content, "\n") {
		idx := strings.Index(line, `"kind":"`)
		if idx < 0 {
			idx = strings.Index(line, `"kind": "`)
		}
		if idx < 0 {
			continue
		}
		rest := line[idx:]
		start := strings.Index(rest, `"kind"`)
		if start < 0 {
			continue
		}
		afterKey := rest[start+len(`"kind"`):]
		afterKey = strings.TrimLeft(afterKey, `: `)
		if len(afterKey) < 2 || afterKey[0] != '"' {
			continue
		}
		end := strings.Index(afterKey[1:], `"`)
		if end < 0 {
			continue
		}
		kind := afterKey[1 : end+1]

		if !allowedFactKinds[kind] && !seen[kind] {
			invalid = append(invalid, kind)
			seen[kind] = true
		}
	}
	return invalid
}

// ValidatePackID checks that a pack ID is safe for file storage.
func ValidatePackID(id string) string {
	if id == "" {
		return "pack ID is required"
	}
	if strings.Contains(id, "..") {
		return "pack ID must not contain '..'"
	}
	slug := id
	if parts := strings.SplitN(id, "/", 2); len(parts) == 2 {
		slug = parts[1]
	}
	if slug == "" {
		return "pack ID must have a name"
	}
	if strings.ContainsAny(slug, "/\\") {
		return "pack slug must not contain path separators"
	}
	for _, p := range registry {
		if p.ID == id {
			return "cannot override core pack '" + id + "'"
		}
	}
	return ""
}

// ─── Legacy compat ──────────────────────────────────────────────────────────

// Defaults returns core guide defaults keyed by setting name.
func Defaults() map[string]string {
	return map[string]string{
		"guide_fact_schema": FactSchema,
		"guide_enrichment":  Enrichment,
		"guide_flow":        FlowTracing,
	}
}

// Compose builds a complete guide by appending relevant technology adapters.
// Deprecated: agents now decide which packs to load based on match sections.
func Compose(coreGuide string, tech []string) string {
	if len(tech) == 0 {
		return coreGuide
	}

	var parts []string
	parts = append(parts, coreGuide)
	seenContent := map[string]bool{}
	for _, t := range tech {
		t = strings.ToLower(strings.TrimSpace(t))
		if tp, ok := builtinTechPacks[t]; ok && !seenContent[tp.Content] {
			parts = append(parts, "\n---\n"+tp.Content)
			seenContent[tp.Content] = true
		}
	}
	return strings.Join(parts, "\n")
}
