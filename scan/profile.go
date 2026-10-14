package scan

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/alexdx2/chronicle-core/deps"
	"github.com/alexdx2/chronicle-core/graph"
	"github.com/alexdx2/chronicle-core/graph/prompts"
	"github.com/alexdx2/chronicle-core/llm"
	"github.com/alexdx2/chronicle-core/manifest"
)

//go:embed profile_prompt.md
var profilePrompt string

// Technology is one technology of the repository, with the lines that prove it.
type Technology struct {
	ID       string   `json:"id"`
	Language string   `json:"language"`
	Role     string   `json:"role"` // http messaging storage infra frontend rpc auth other
	Evidence []string `json:"evidence"`
	Pack     string   `json:"pack"` // a catalog id or "new"
}

// ScopeRule is one scan glob and why it is there.
type ScopeRule struct {
	Glob   string `json:"glob"`
	Reason string `json:"reason"`
}

// Scope is what the extractor reads: include, exclude and test globs.
type Scope struct {
	Include []ScopeRule `json:"include"`
	Exclude []ScopeRule `json:"exclude"`
	Tests   []ScopeRule `json:"tests"`
}

// Profile is the model's reading of one repository.
type Profile struct {
	Technologies []Technology `json:"technologies"`
	Scope        Scope        `json:"scope"`
	Notes        []string     `json:"notes,omitempty"`
}

// LayerCheck is the outcome of one extraction layer's check (see checks.go).
type LayerCheck struct {
	Layer  string `json:"layer"`
	Status string `json:"status"`
	Detail string `json:"detail"`
}

// ProfileFile is profile.json inside a snapshot.
type ProfileFile struct {
	Profile       Profile           `json:"profile"`
	Deps          deps.Report       `json:"deps"`
	Packs         []prompts.PackRef `json:"packs"` // applied packs, sorted
	Checks        []LayerCheck      `json:"checks,omitempty"`
	ScopeSource   string            `json:"scope_source"` // model | seed | repo | base
	ScopeProposed *Scope            `json:"scope_proposed,omitempty"`
	Seeded        bool              `json:"seeded"`
	SeedReason    string            `json:"seed_reason,omitempty"`
	ProfiledAt    string            `json:"profiled_at_sha"` // sha the model profiled
	TopDirs       []string          `json:"top_dirs"`        // sorted top-level directories of the inventory
	// Limitations are facts about how this profile was made that its reader
	// must know (for example a prompt that had to drop dependency lines).
	Limitations []string `json:"limitations,omitempty"`
}

// ParseProfileFile reads profile.json; unknown fields are ignored.
func ParseProfileFile(b []byte) (ProfileFile, error) {
	var pf ProfileFile
	if err := json.Unmarshal(b, &pf); err != nil {
		return ProfileFile{}, fmt.Errorf("profile file: %w", err)
	}
	return pf, nil
}

// SortedTopDirs is the sorted key set of an inventory's TopDirs.
func SortedTopDirs(inv *Inventory) []string {
	out := make([]string, 0, len(inv.TopDirs))
	for d := range inv.TopDirs {
		out = append(out, d)
	}
	sort.Strings(out)
	return out
}

// SeedProfile turns the classifier's tech list into a profile whose packs are
// the built-in ids. Evidence is the dependency lines of the matching
// ecosystem, else one honest line saying the technology came from the seed.
func SeedProfile(seed SeedResult, rep deps.Report) Profile {
	var p Profile
	for _, t := range seed.Tech {
		tech := Technology{ID: t, Pack: t, Role: "other", Language: "other"}
		for _, d := range rep.Depends {
			if !seedTechMatches(t, d) {
				continue
			}
			if tech.Language == "other" {
				tech.Language = ecosystemLanguage(d.Ecosystem)
			}
			if len(tech.Evidence) < 3 {
				tech.Evidence = append(tech.Evidence, fmt.Sprintf("%s:%d %s", d.File, d.Line, d.Text))
			}
		}
		if len(tech.Evidence) == 0 {
			tech.Evidence = []string{"seed: classified from tracked files"}
		}
		p.Technologies = append(p.Technologies, tech)
	}
	p.Scope = seedScope(seed)
	return p
}

func seedTechMatches(tech string, d deps.Dependency) bool {
	if tech == "kafka" {
		return strings.Contains(d.Name, "kafka")
	}
	switch tech {
	case "dotnet":
		return d.Ecosystem == "nuget"
	case "typescript", "nestjs", "prisma", "graphql":
		return d.Ecosystem == "npm"
	case "go":
		return d.Ecosystem == "go"
	}
	return false
}

func ecosystemLanguage(eco string) string {
	switch eco {
	case "nuget":
		return "dotnet"
	case "npm":
		return "typescript"
	case "go":
		return "go"
	case "python":
		return "python"
	case "maven", "gradle":
		return "java"
	}
	return "other"
}

func seedScope(seed SeedResult) Scope {
	var s Scope
	for _, g := range seed.Include {
		s.Include = append(s.Include, ScopeRule{Glob: g, Reason: "seed"})
	}
	for _, g := range seed.Exclude {
		s.Exclude = append(s.Exclude, ScopeRule{Glob: g, Reason: "seed"})
	}
	return s
}

// The same sizing rules the extraction prompts use (headless/budget.go): a
// pessimistic characters-per-token ratio and a share of the window kept for
// the answer.
const (
	profileCharsPerToken = 3
	profileOutputReserve = 2048
	profileMaxTokens     = 4096
	// profileDefaultBudget is the prompt size, in characters, when the backend
	// has no hard window (cloud models).
	profileDefaultBudget = 60000
)

// BuildProfilePrompt assembles the profile request. window is the backend's
// hard per-call token window (llm.WindowedBackend), 0 = none. When the prompt
// does not fit, the inventory summary shrinks first (5 -> 2 -> 0 sample paths),
// then repeated dependencies are collapsed to the first "file:line text" of
// each (ecosystem, name), and only then are dependency lines dropped from the
// end. It returns the number of dependency lines dropped; a caller must say so.
// With window == 0 (no hard limit) only the inventory samples shrink (5 -> 2
// -> 0) and every dependency line is kept: dropped is always 0.
func BuildProfilePrompt(inv *Inventory, rep deps.Report, seed SeedResult, catalog []prompts.PackMeta, window int) (llm.Request, int) {
	budget := profileDefaultBudget
	if window > 0 {
		budget = (window - profileOutputReserve - profileMaxTokens) * profileCharsPerToken
		if budget < 0 {
			budget = 0
		}
	}
	depLine := func(d deps.Dependency) string {
		var b strings.Builder
		fmt.Fprintf(&b, "%s %s %s (%s:%d) %s", d.Ecosystem, d.Name, d.Version, d.File, d.Line, d.Text)
		if d.Internal {
			b.WriteString(" [internal]")
		}
		b.WriteByte('\n')
		return b.String()
	}
	var tail strings.Builder
	if len(rep.Publishes) > 0 {
		tail.WriteString("publishes:\n")
		for _, p := range rep.Publishes {
			fmt.Fprintf(&tail, "%s %s (%s:%d) %s\n", p.Ecosystem, p.Name, p.File, p.Line, p.Text)
		}
	}
	fmt.Fprintf(&tail, "\n## Seed\nseed: include=%s, exclude=%s, tech=%s\n",
		strings.Join(seed.Include, ","), strings.Join(seed.Exclude, ","), strings.Join(seed.Tech, ","))
	tail.WriteString("\n## Known packs\n")
	for _, c := range catalog {
		fmt.Fprintf(&tail, "- %s: %s", c.ID, c.Description)
		if m := strings.Join(strings.Fields(c.AppliesWhen), " "); m != "" {
			if r := []rune(m); len(r) > 300 {
				m = string(r[:300])
			}
			fmt.Fprintf(&tail, " | Match: %s", m)
		}
		tail.WriteByte('\n')
	}

	all := make([]string, 0, len(rep.Depends))
	for _, d := range rep.Depends {
		all = append(all, depLine(d))
	}
	deduped := make([]string, 0, len(rep.Depends))
	seen := map[string]bool{}
	for _, d := range rep.Depends {
		if k := d.Ecosystem + "\x00" + d.Name; !seen[k] {
			seen[k] = true
			deduped = append(deduped, depLine(d))
		}
	}

	render := func(lines []string, samples int) string {
		return "## Inventory\n" + inv.Summary(samples) + "\n## Dependencies\n" + strings.Join(lines, "") + tail.String()
	}
	fits := func(user string) bool { return len(profilePrompt)+len(user) <= budget }
	req := func(user string) llm.Request {
		return llm.Request{System: profilePrompt, User: user, MaxTokens: profileMaxTokens}
	}

	if window <= 0 {
		// No hard window: the 60000-char figure is only a soft target for the
		// inventory samples. A dependency line is never collapsed or dropped.
		for _, n := range []int{5, 2} {
			if u := render(all, n); fits(u) {
				return req(u), 0
			}
		}
		return req(render(all, 0)), 0
	}
	for _, lines := range [][]string{all, deduped} {
		for _, n := range []int{5, 2, 0} {
			if u := render(lines, n); fits(u) {
				return req(u), 0
			}
		}
	}
	// Drop dependency lines from the end until the prompt fits.
	lines := deduped
	for len(lines) > 0 && !fits(render(lines, 0)) {
		lines = lines[:len(lines)-1]
	}
	return req(render(lines, 0)), len(deduped) - len(lines)
}

// ParseProfile decodes the first balanced JSON object of text; unknown fields
// are ignored.
func ParseProfile(text string) (Profile, error) {
	for from := 0; ; {
		off := strings.IndexByte(text[from:], '{')
		if off < 0 {
			return Profile{}, errors.New("no JSON object in the answer")
		}
		start := from + off
		end, ok := balancedEnd(text, start)
		if !ok {
			return Profile{}, errors.New("unbalanced JSON object in the answer")
		}
		if strings.TrimSpace(text[start+1:end]) == "" { // "{}" in prose: look further
			from = end + 1
			continue
		}
		var p Profile
		if err := json.Unmarshal([]byte(text[start:end+1]), &p); err != nil {
			return Profile{}, fmt.Errorf("decode profile: %w", err)
		}
		return p, nil
	}
}

// balancedEnd returns the index of the brace closing the object at start.
func balancedEnd(text string, start int) (int, bool) {
	depth, inStr, esc := 0, false, false
	for i := start; i < len(text); i++ {
		c := text[i]
		switch {
		case inStr:
			if esc {
				esc = false
			} else if c == '\\' {
				esc = true
			} else if c == '"' {
				inStr = false
			}
		case c == '"':
			inStr = true
		case c == '{':
			depth++
		case c == '}':
			depth--
			if depth == 0 {
				return i, true
			}
		}
	}
	return 0, false
}

var (
	idRe       = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)
	globCharRe = regexp.MustCompile(`^[A-Za-z0-9*?/._@\[\]-]+$`)
	roles      = map[string]bool{"http": true, "messaging": true, "storage": true, "infra": true, "frontend": true, "rpc": true, "auth": true, "other": true}
)

// ValidateProfile checks roles, ids, packs (known or "new"; a nil known skips
// the membership check), evidence and globs.
func ValidateProfile(p *Profile, known map[string]bool) error {
	if len(p.Technologies) == 0 {
		return errors.New("technologies must not be empty")
	}
	seen := map[string]bool{}
	for i, t := range p.Technologies {
		if !roles[t.Role] {
			return fmt.Errorf("technology %d (%q): role %q is not one of http messaging storage infra frontend rpc auth other", i, t.ID, t.Role)
		}
		if !idRe.MatchString(t.ID) {
			return fmt.Errorf("technology %d: id %q must match [a-z0-9-]", i, t.ID)
		}
		if !idRe.MatchString(t.Language) {
			return fmt.Errorf("technology %q: language %q must match [a-z0-9-]", t.ID, t.Language)
		}
		if t.Pack != "new" && !(idRe.MatchString(t.Pack) && (known == nil || known[t.Pack])) {
			return fmt.Errorf("technology %q: pack %q is not in the catalog (use a catalog id or \"new\")", t.ID, t.Pack)
		}
		if len(t.Evidence) == 0 {
			return fmt.Errorf("technology %q: evidence is empty", t.ID)
		}
		if k := t.ID + "/" + t.Language; seen[k] {
			return fmt.Errorf("technology %q (%s) is listed twice", t.ID, t.Language)
		} else {
			seen[k] = true
		}
	}
	if len(p.Scope.Include) == 0 {
		return errors.New("scope.include must list at least one glob")
	}
	for _, group := range []struct {
		name  string
		rules []ScopeRule
	}{{"include", p.Scope.Include}, {"exclude", p.Scope.Exclude}, {"tests", p.Scope.Tests}} {
		for _, r := range group.rules {
			if err := validGlob(r.Glob); err != nil {
				return fmt.Errorf("scope.%s: glob %q: %w", group.name, r.Glob, err)
			}
		}
	}
	return nil
}

func validGlob(g string) error {
	switch {
	case g == "":
		return errors.New("empty")
	case strings.HasPrefix(g, "/"):
		return errors.New("must be repository-relative")
	case strings.Contains(g, ".."):
		return errors.New("must not contain ..")
	case !globCharRe.MatchString(g):
		return errors.New("contains characters outside * ? / . - _ [] letters digits")
	}
	return nil
}

// RequestProfile is RequestProfileWith without a catalog: pack ids are not
// checked against it (callers that have one use RequestProfileWith).
func RequestProfile(ctx context.Context, b llm.Backend, req llm.Request) (Profile, error) {
	return RequestProfileWith(ctx, b, req, nil)
}

// RequestProfileWith asks the model once and, when the answer does not parse or
// validate (known == nil skips the catalog membership check), once more with
// the error. A transport error is returned at once.
func RequestProfileWith(ctx context.Context, b llm.Backend, req llm.Request, known map[string]bool) (Profile, error) {
	var lastErr error
	for attempt := 0; attempt < 2; attempt++ {
		r := req
		if attempt == 1 {
			r.User += "\n\n## Your previous answer was rejected\n" + lastErr.Error() + "\nAnswer again with one valid JSON object."
		}
		resp, err := b.Complete(ctx, r)
		if err != nil {
			return Profile{}, err
		}
		p, err := ParseProfile(resp.Text)
		if err == nil {
			err = ValidateProfile(&p, known)
		}
		if err == nil {
			return p, nil
		}
		lastErr = err
	}
	return Profile{}, fmt.Errorf("profile: %w", lastErr)
}

// ScopeToScanConfig drops the reasons: the manifest carries globs only.
func ScopeToScanConfig(s Scope) manifest.ScanConfig {
	globs := func(rs []ScopeRule) []string {
		var out []string
		for _, r := range rs {
			out = append(out, r.Glob)
		}
		return out
	}
	return manifest.ScanConfig{Include: globs(s.Include), Exclude: globs(s.Exclude), Tests: globs(s.Tests)}
}

// GuardScope applies the scope safeguards (spec 7.2) to the model's scope. It
// returns the scope to use, its source ("model" or "seed"), the rejected
// proposal when the seed won, and a warning.
func GuardScope(p Profile, seed SeedResult, inv *Inventory) (Scope, string, *Scope, string) {
	seedCfg := manifest.ScanConfig{Include: seed.Include, Exclude: seed.Exclude}
	var base []string
	for _, f := range inv.Paths() {
		if graph.InScope(f, &seedCfg) {
			base = append(base, f)
		}
	}
	n := len(base)
	if n == 0 {
		return p.Scope, "model", nil, ""
	}
	cfg := ScopeToScanConfig(p.Scope)
	kept := 0
	for _, f := range base {
		if graph.InScope(f, &cfg) {
			kept++
		}
	}
	suspicious := kept*10 < n
	for _, r := range p.Scope.Exclude {
		hit := 0
		for _, f := range base {
			if manifest.MatchGlob(f, r.Glob) {
				hit++
			}
		}
		if hit*10 > n*9 {
			suspicious = true
		}
	}
	if !suspicious {
		return p.Scope, "model", nil, ""
	}
	proposed := p.Scope
	return seedScope(seed), "seed", &proposed,
		fmt.Sprintf("scope taken from the seed: the model's scope kept %d of %d files", kept, n)
}

// WriteProfiledManifest writes the domain manifest (bootstrap format plus
// scan.tests), replacing any file at path.
func WriteProfiledManifest(path, domainKey string, cfg manifest.ScanConfig, tech []string) error {
	buf, err := renderManifest(domainKey, cfg.Include, cfg.Exclude, cfg.Tests, tech)
	if err != nil {
		return err
	}
	return writeFileAtomic(path, buf.Bytes()) // its errors carry the "scan:" prefix already
}

// PackIDFor is the catalog id of the pack a technology would use when the
// profile names none: "<id>-<language>", or the id alone when the language is
// empty or "other". The worker authors packs under this id and the server
// records a repository as waiting for it.
func PackIDFor(t Technology) string {
	id := strings.ToLower(strings.TrimSpace(t.ID))
	lang := strings.ToLower(strings.TrimSpace(t.Language))
	if lang == "" || lang == "other" {
		return id
	}
	return id + "-" + lang
}

// AdoptCatalogPacks gives every technology without a pack ("" or "new") the
// catalog pack named PackIDFor, when the catalog has one, and returns the ids
// adopted. An inherited profile was decided when that pack did not exist
// yet; a later build must apply it or the repository waits for it forever.
func AdoptCatalogPacks(p *Profile, cat *prompts.Catalog) []string {
	if p == nil || cat == nil {
		return nil
	}
	var adopted []string
	for i := range p.Technologies {
		t := &p.Technologies[i]
		if t.Pack != "" && t.Pack != "new" {
			continue
		}
		id := PackIDFor(*t)
		if _, ok := cat.Get(id); ok {
			t.Pack = id
			adopted = append(adopted, id)
		}
	}
	return adopted
}
