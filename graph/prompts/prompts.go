// Package prompts provides the instruction pack registry for scan agents.
//
// Architecture:
//   - Core packs (always loaded): fact_schema, enrichment, flow_tracing
//   - Technology packs (auto-detected or requested): typescript, nestjs, graphql, prisma
//   - Custom packs (user-created via dashboard): stored in settings table
//
// Packs are embedded from .md files. The registry describes triggers for auto-detection.
package prompts

import (
	_ "embed"
	"path/filepath"
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

//go:embed pack_authoring_guide.md
var PackAuthoringGuide string

//go:embed registry.yaml
var registryYAML string

// ─── Registry types ─────────────────────────────────────────────────────────

// Pack describes an instruction pack in the registry.
type Pack struct {
	ID          string       `json:"id" yaml:"id"`
	Type        string       `json:"type" yaml:"type"`                   // core, language, framework, orm, messaging, custom
	Description string       `json:"description" yaml:"description"`
	Path        string       `json:"path" yaml:"path"`                   // relative to prompts dir
	AlwaysLoad  bool         `json:"always_load" yaml:"always_load"`
	Triggers    PackTriggers `json:"triggers,omitempty" yaml:"triggers"`
	Content     string       `json:"content,omitempty" yaml:"-"`         // populated at runtime
}

// PackTriggers defines when a pack should be auto-loaded for a file.
type PackTriggers struct {
	Extensions []string `json:"extensions,omitempty" yaml:"extensions"`
	Imports    []string `json:"imports,omitempty" yaml:"imports"`
	Decorators []string `json:"decorators,omitempty" yaml:"decorators"`
	Tokens     []string `json:"tokens,omitempty" yaml:"tokens"`
	Filenames  []string `json:"filenames,omitempty" yaml:"filenames"`
}

// PackSelection is the result of matching packs for a file.
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

// registry is the parsed pack list.
var registry []Pack

func init() {
	contentByPath = map[string]string{
		"fact_schema.md":            FactSchema,
		"enrichment.md":             Enrichment,
		"flow_tracing.md":           FlowTracing,
		"technologies/prisma.md":    TechPrisma,
		"technologies/nestjs.md":    TechNestJS,
		"technologies/graphql.md":   TechGraphQL,
		"technologies/typescript.md": TechTypeScript,
	}

	var reg struct {
		Packs []Pack `yaml:"packs"`
	}
	if err := yaml.Unmarshal([]byte(registryYAML), &reg); err == nil {
		registry = reg.Packs
	}

	// Attach content to each pack
	for i := range registry {
		if content, ok := contentByPath[registry[i].Path]; ok {
			registry[i].Content = content
		}
	}
}

// AllPacks returns all registered packs (with content).
func AllPacks() []Pack {
	return registry
}

// GetPack returns a pack by ID with its content.
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

// ─── Matching ───────────────────────────────────────────────────────────────

// FileContext provides information about a file for pack matching.
type FileContext struct {
	FilePath   string   // e.g. "src/orders/order.controller.ts"
	Imports    []string // import paths found by AST
	Decorators []string // decorator names found by AST
	Tokens     []string // other tokens detected (e.g. "prisma.")
}

// MatchPacks selects packs for a file. Returns loaded packs + available-but-not-loaded.
// projectTech is the tech list from the manifest (e.g. ["nestjs", "prisma"]).
func MatchPacks(ctx FileContext, projectTech []string) PackSelection {
	var loaded []Pack
	var available []Pack

	ext := strings.ToLower(filepath.Ext(ctx.FilePath))
	baseName := filepath.Base(ctx.FilePath)

	// Build a set of project tech for quick lookup
	techSet := map[string]bool{}
	for _, t := range projectTech {
		techSet[strings.ToLower(t)] = true
	}

	for _, p := range registry {
		if p.AlwaysLoad {
			loaded = append(loaded, p)
			continue
		}

		matched := matchTriggers(p, ext, baseName, ctx)
		// Also match by project tech (e.g. manifest says "nestjs" -> load nestjs pack)
		if !matched {
			matched = matchByTech(p.ID, techSet)
		}

		if matched {
			loaded = append(loaded, p)
		} else if hasAnyTrigger(p) {
			available = append(available, p)
		}
	}

	sel := PackSelection{}
	for _, p := range loaded {
		sel.Loaded = append(sel.Loaded, PackMeta{
			ID: p.ID, Type: p.Type, Description: p.Description,
		})
	}
	for _, p := range available {
		sel.Available = append(sel.Available, PackMeta{
			ID: p.ID, Type: p.Type, Description: p.Description,
			AppliesWhen: triggerSummary(p),
		})
	}
	return sel
}

// ComposeForFile builds the full instruction text for a file based on matched packs.
func ComposeForFile(ctx FileContext, projectTech []string) string {
	ext := strings.ToLower(filepath.Ext(ctx.FilePath))
	baseName := filepath.Base(ctx.FilePath)
	techSet := map[string]bool{}
	for _, t := range projectTech {
		techSet[strings.ToLower(t)] = true
	}

	var parts []string
	for _, p := range registry {
		if p.AlwaysLoad || matchTriggers(p, ext, baseName, ctx) || matchByTech(p.ID, techSet) {
			if p.Content != "" {
				parts = append(parts, p.Content)
			}
		}
	}
	return strings.Join(parts, "\n\n---\n\n")
}

// ─── Helpers ────────────────────────────────────────────────────────────────

func matchTriggers(p Pack, ext, baseName string, ctx FileContext) bool {
	t := p.Triggers

	// Extension match
	for _, e := range t.Extensions {
		if ext == e {
			return true
		}
	}

	// Filename match
	for _, f := range t.Filenames {
		if baseName == f {
			return true
		}
	}

	// Import match
	for _, imp := range ctx.Imports {
		for _, trigger := range t.Imports {
			if strings.HasPrefix(imp, trigger) {
				return true
			}
		}
	}

	// Decorator match
	for _, dec := range ctx.Decorators {
		for _, trigger := range t.Decorators {
			if dec == trigger {
				return true
			}
		}
	}

	// Token match
	for _, tok := range ctx.Tokens {
		for _, trigger := range t.Tokens {
			if strings.Contains(tok, trigger) {
				return true
			}
		}
	}

	return false
}

// matchByTech checks if a pack ID corresponds to project tech.
// e.g. "framework/nestjs" matches tech "nestjs"
func matchByTech(packID string, techSet map[string]bool) bool {
	parts := strings.SplitN(packID, "/", 2)
	if len(parts) != 2 {
		return false
	}
	name := parts[1]
	return techSet[name]
}

func hasAnyTrigger(p Pack) bool {
	t := p.Triggers
	return len(t.Extensions) > 0 || len(t.Imports) > 0 || len(t.Decorators) > 0 ||
		len(t.Tokens) > 0 || len(t.Filenames) > 0
}

func triggerSummary(p Pack) string {
	var parts []string
	if len(p.Triggers.Extensions) > 0 {
		parts = append(parts, strings.Join(p.Triggers.Extensions, ", ")+" files")
	}
	if len(p.Triggers.Imports) > 0 {
		parts = append(parts, "imports: "+strings.Join(p.Triggers.Imports, ", "))
	}
	if len(p.Triggers.Decorators) > 0 {
		parts = append(parts, "decorators: "+strings.Join(p.Triggers.Decorators, ", "))
	}
	return strings.Join(parts, "; ")
}

// ─── Gap detection ──────────────────────────────────────────────────────────

// InstructionGap represents a technology declared in the manifest that has no matching pack.
type InstructionGap struct {
	Tech            string `json:"tech"`
	Reason          string `json:"reason"`
	SuggestedAction string `json:"suggested_action"` // "create_custom_pack"
}

// DetectInstructionGaps finds technologies declared in the manifest that have no matching pack.
func DetectInstructionGaps(manifestTech []string) []InstructionGap {
	var gaps []InstructionGap
	for _, tech := range manifestTech {
		t := strings.ToLower(strings.TrimSpace(tech))
		if t == "" {
			continue
		}
		found := false
		for _, p := range registry {
			if matchByTech(p.ID, map[string]bool{t: true}) {
				found = true
				break
			}
		}
		if !found {
			gaps = append(gaps, InstructionGap{
				Tech:            tech,
				Reason:          "declared in manifest but no matching instruction pack exists",
				SuggestedAction: "create_custom_pack",
			})
		}
	}
	return gaps
}

// ─── Custom pack validation ─────────────────────────────────────────────────

// allowedFactKinds is the set of fact kinds defined by the core schema.
// Custom packs must not introduce new kinds.
var allowedFactKinds = map[string]bool{
	"import": true, "injects": true, "provides": true,
	"endpoint": true, "http_call": true, "calls_service": true,
	"calls_endpoint": true, "uses_model": true,
	"model": true, "enum": true, "model_relation": true,
	"produces": true, "consumes": true,
	"decorator": true, "constructor_param": true,
	"call": true, "member_call": true,
	"flow": true, "delegates": true, "declares_service": true,
}

// ValidateCustomPack checks that a custom pack's content doesn't introduce unknown fact kinds.
// Returns a list of invalid kinds found in the pack text.
func ValidateCustomPack(content string) []string {
	var invalid []string
	seen := map[string]bool{}

	// Look for "kind":"xxx" patterns in the markdown
	for _, line := range strings.Split(content, "\n") {
		// Match {"kind":"xxx" patterns
		idx := strings.Index(line, `"kind":"`)
		if idx < 0 {
			idx = strings.Index(line, `"kind": "`)
		}
		if idx < 0 {
			continue
		}
		// Extract the kind value
		rest := line[idx:]
		start := strings.Index(rest, `"kind"`)
		if start < 0 {
			continue
		}
		// Find the value after "kind":"
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

// ─── Custom pack ID validation ──────────────────────────────────────────────

// reservedPrefixes are pack ID prefixes that cannot be used for custom packs.
var reservedPrefixes = []string{"core/", "language/", "framework/", "orm/", "guide/"}

// ValidateCustomPackID checks that a custom pack ID is safe and doesn't collide with built-ins.
// Returns an error message or "" if valid.
func ValidateCustomPackID(id string) string {
	if id == "" {
		return "pack ID is required"
	}
	if !strings.HasPrefix(id, "custom/") {
		return "custom pack ID must start with 'custom/' (got: " + id + ")"
	}
	slug := strings.TrimPrefix(id, "custom/")
	if slug == "" {
		return "pack ID must have a name after 'custom/'"
	}
	if strings.Contains(id, "..") {
		return "pack ID must not contain '..'"
	}
	if strings.ContainsAny(slug, "/\\") {
		return "pack slug must not contain path separators"
	}
	// Check not overriding built-in
	for _, p := range reservedPrefixes {
		if strings.HasPrefix(id, p) {
			return "cannot use reserved prefix '" + p + "' for custom packs"
		}
	}
	// Check not colliding with existing built-in pack
	if _, exists := GetPack(id); exists {
		return "pack ID '" + id + "' conflicts with a built-in pack"
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
// Deprecated: use ComposeForFile for per-file composition.
func Compose(coreGuide string, tech []string) string {
	if len(tech) == 0 {
		return coreGuide
	}

	techAdapters := map[string]string{
		"prisma": TechPrisma, "nestjs": TechNestJS, "nest": TechNestJS,
		"graphql": TechGraphQL, "typescript": TechTypeScript, "javascript": TechTypeScript,
	}

	var parts []string
	parts = append(parts, coreGuide)
	seenContent := map[string]bool{}
	for _, t := range tech {
		t = strings.ToLower(strings.TrimSpace(t))
		if adapter, ok := techAdapters[t]; ok && !seenContent[adapter] {
			parts = append(parts, "\n---\n"+adapter)
			seenContent[adapter] = true
		}
	}
	return strings.Join(parts, "\n")
}
