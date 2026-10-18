package scan

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"path"
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
//
// Two triggers. A suspicious scope (it keeps under a tenth of the seed's files,
// or an exclude swallows over nine tenths) is replaced by the seed. A scope
// that is otherwise sane but drops a whole directory that holds an application
// or its controllers keeps the model's work: those directories are appended to
// its includes and the warning names them.
func GuardScope(p Profile, seed SeedResult, inv *Inventory, rep deps.Report) (Scope, string, *Scope, string) {
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
	dropped := droppedDirs(base, &cfg, rep)
	if !suspicious && len(dropped) == 0 {
		return p.Scope, "model", nil, ""
	}
	if suspicious {
		proposed := p.Scope
		warn := fmt.Sprintf("scope taken from the seed: the model's scope kept %d of %d files", kept, n)
		if len(dropped) > 0 {
			warn += " and none of " + strings.Join(dropped, ", ")
		}
		return seedScope(seed), "seed", &proposed, warn
	}
	// Keep the model's scope; re-add only the dropped application directories.
	scope := Scope{
		Include: append([]ScopeRule(nil), p.Scope.Include...),
		Exclude: append([]ScopeRule(nil), p.Scope.Exclude...),
		Tests:   append([]ScopeRule(nil), p.Scope.Tests...),
	}
	for _, d := range dropped {
		for _, g := range droppedDirGlobs(d, p.Scope, seed, base) {
			scope.Include = append(scope.Include, ScopeRule{Glob: g, Reason: "application directory the profile scope dropped"})
		}
	}
	// An exclude rooted inside a re-added directory that hides the very
	// application or controller files which made the directory protected would
	// defeat the re-add. Excludes of generated output, vendored code or build
	// products in that directory (svc/generated/**, svc/dist/**) do not.
	var kept2 []ScopeRule
	for _, r := range scope.Exclude {
		if excludeHidesProtected(r.Glob, dropped, base, rep) {
			continue
		}
		kept2 = append(kept2, r)
	}
	scope.Exclude = kept2
	// The seed's exclusions are the floor of the re-added directories (vendored
	// and generated code never carries the application): they are rooted in
	// those directories only, so the model's own includes and excludes elsewhere
	// stay as the model wrote them.
	have := map[string]bool{}
	for _, r := range scope.Exclude {
		have[r.Glob] = true
	}
	for _, d := range dropped {
		for _, g := range bootstrapExclude {
			rooted := d + "/" + g
			if !have[rooted] {
				have[rooted] = true
				scope.Exclude = append(scope.Exclude, ScopeRule{Glob: rooted, Reason: "seed"})
			}
		}
	}
	proposed := p.Scope
	warn := "scope kept from the model; re-added application directories it dropped: " + strings.Join(dropped, ", ")
	return scope, "model", &proposed, warn
}

// droppedDirs lists the top-level directories the model scope keeps no file of
// and that must not be dropped: one holding an application package (per the
// deps report, the same classification that mints service nodes) or a
// controller/handler file. Plain source files, however many, do not protect a
// directory: examples/, docs-site/ and scripts/ are the model's call.
func droppedDirs(base []string, cfg *manifest.ScanConfig, rep deps.Report) []string {
	type tally struct {
		kept      int
		structure bool
	}
	byDir := map[string]*tally{}
	for _, f := range base {
		d := topDirOf(f)
		if d == "." {
			continue
		}
		t := byDir[d]
		if t == nil {
			t = &tally{}
			byDir[d] = t
		}
		if graph.InScope(f, cfg) {
			t.kept++
		}
		if isStructureFile(f) {
			t.structure = true
		}
	}
	app := map[string]bool{}
	for _, pk := range rep.Publishes {
		if pk.App {
			app[topDirOf(pk.File)] = true
		}
	}
	var out []string
	for d, t := range byDir {
		if t.kept == 0 && (t.structure || app[d]) {
			out = append(out, d)
		}
	}
	sort.Strings(out)
	return out
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
	id := normalizeTechToken(t.ID)
	lang := normalizeLanguage(t.Language)
	if lang == "" || lang == "other" || lang == id {
		return id
	}
	return id + "-" + lang
}

// languageAliases maps the names models use for a language to the one the
// built-in packs use, so one technology gets one pack id ("csharp" and
// "dotnet" named three different packs on the first fleet run).
var languageAliases = map[string]string{
	"c#": "dotnet", "csharp": "dotnet", "cs": "dotnet", ".net": "dotnet", "net": "dotnet", "dotnetcore": "dotnet", "net core": "dotnet",
	"js": "javascript", "node": "javascript", "nodejs": "javascript", "node.js": "javascript",
	"ts": "typescript",
	"py": "python", "python3": "python",
	"sh": "shell", "bash": "shell", "shellscript": "shell", "zsh": "shell",
	"golang": "go",
	"kt":     "kotlin",
	"rb":     "ruby",
	"yml":    "yaml",
}

// normalizeLanguage lower-cases a language name and resolves its aliases.
func normalizeLanguage(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	if a, ok := languageAliases[s]; ok {
		return a
	}
	return s
}

// normalizeTechToken lower-cases a technology id and writes it with single
// dashes between words: spaces, underscores and dots become dashes, runs
// collapse, and the alias table applies to ids that are languages
// ("shellscript" → "shell"), so "AWS Secrets Manager", "aws_secrets-manager"
// and "aws-secrets-manager" are one id.
func normalizeTechToken(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	if a, ok := languageAliases[s]; ok {
		return a
	}
	var b strings.Builder
	dash := false
	for _, r := range s {
		switch {
		case r == ' ' || r == '_' || r == '-' || r == '.' || r == '/':
			dash = b.Len() > 0
		default:
			if dash {
				b.WriteByte('-')
				dash = false
			}
			b.WriteRune(r)
		}
	}
	return b.String()
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

// protectedFiles are the files that make a dropped directory protected: its
// controller/handler files and the manifest of an application package.
func protectedFiles(dir string, base []string, rep deps.Report) []string {
	var out []string
	for _, f := range base {
		if topDirOf(f) == dir && isStructureFile(f) {
			out = append(out, f)
		}
	}
	for _, pk := range rep.Publishes {
		if pk.App && topDirOf(pk.File) == dir {
			out = append(out, pk.File)
		}
	}
	return out
}

// excludeHidesProtected reports whether an exclude is rooted inside one of the
// re-added directories and matches a file that made that directory protected.
func excludeHidesProtected(glob string, dropped, base []string, rep deps.Report) bool {
	for _, d := range dropped {
		if !strings.HasPrefix(glob, d+"/") {
			continue
		}
		for _, f := range protectedFiles(d, base, rep) {
			if manifest.MatchGlob(f, glob) {
				return true
			}
		}
	}
	return false
}

// droppedDirGlobs are the include globs that bring a dropped directory back:
// "<dir>/**/<pattern>" for the source extensions of the files actually present
// in that directory (base is the seed-scope inventory), plus the seed's build
// and deployment files. When the directory's files are not known, the union of
// the model's and the seed's source patterns stands in. A bare "<dir>/**"
// would pull json, markdown and images in next to the code.
func droppedDirGlobs(dir string, model Scope, seed SeedResult, base []string) []string {
	isSourceExt := func(pat string) bool {
		if !strings.HasPrefix(pat, "*.") {
			return false
		}
		for _, e := range bootstrapSourceExts {
			if pat == "*"+e {
				return true
			}
		}
		return false
	}
	seen := map[string]bool{}
	var pats []string
	add := func(pat string) {
		if pat != "" && !seen[pat] {
			seen[pat] = true
			pats = append(pats, pat)
		}
	}
	present := false
	for _, f := range base {
		if !strings.HasPrefix(f, dir+"/") {
			continue
		}
		if ext := path.Ext(f); ext != "" && isSourceExt("*"+ext) {
			add("*" + ext)
			present = true
		}
	}
	if !present {
		for _, r := range model.Include {
			if pat := path.Base(r.Glob); isSourceExt(pat) {
				add(pat)
			}
		}
	}
	for _, g := range seed.Include {
		pat := path.Base(g)
		if isSourceExt(pat) {
			if !present {
				add(pat)
			}
			continue
		}
		add(pat) // build and deployment files: package.json, go.mod, *.csproj, Dockerfile
	}
	sort.Strings(pats)
	out := make([]string, 0, len(pats))
	for _, pat := range pats {
		out = append(out, dir+"/**/"+pat)
	}
	if len(out) == 0 {
		out = append(out, dir+"/**")
	}
	return out
}
