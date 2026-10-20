package scan

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/alexdx2/chronicle-core/gitutil"
	"github.com/alexdx2/chronicle-core/truth"
	"github.com/alexdx2/chronicle-core/truth/extract"
	"github.com/alexdx2/chronicle-core/truth/scancfg"
)

// The functions here are the tool surface for a model (CLI and MCP): read the
// effective configuration, validate and dry-run a candidate against the
// repository, explain what a file yields, save with a loss guard.

// ScanConfigGetResult describes the configuration in force.
type ScanConfigGetResult struct {
	Path        string   `json:"path"`
	Exists      bool     `json:"exists"`
	ProjectYAML string   `json:"project_yaml,omitempty"`
	ProjectErr  string   `json:"project_error,omitempty"`
	Rules       []string `json:"rules"` // "<id> (<kind>, <source>, reviewed|unreviewed)"
	Version     string   `json:"coverage_version,omitempty"`
	Reference   string   `json:"reference,omitempty"`
}

// ScanConfigGet returns the project layer text and a summary of all rules.
func ScanConfigGet(root string) ScanConfigGetResult {
	cfg, perr := LoadScanConfigErr(root)
	r := ScanConfigGetResult{Path: ScanConfigFile, Version: cfg.CoverageVersion(), Rules: []string{}}
	if b, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(ScanConfigFile))); err == nil {
		r.Exists, r.ProjectYAML = true, string(b)
	}
	if perr != nil {
		r.ProjectErr = perr.Error()
	}
	for _, c := range cfg.Channels {
		r.Rules = append(r.Rules, ruleLabel(c.ID, "channel", c.Source, c.Reviewed))
	}
	for _, d := range cfg.Documents {
		r.Rules = append(r.Rules, ruleLabel(d.ID, "document", d.Source, d.Reviewed))
	}
	r.Reference = "the independent reference runs builtin, fleet and reviewed project rules only"
	return r
}

func ruleLabel(id, kind, source string, reviewed bool) string {
	rv := "unreviewed"
	if reviewed {
		rv = "reviewed"
	}
	return fmt.Sprintf("%s (%s, %s, %s)", id, kind, source, rv)
}

// DryRunFact is one channel fact a configuration yields.
type DryRunFact struct {
	Key    string `json:"key"`
	Rule   string `json:"rule"`
	Source string `json:"source"`
	File   string `json:"file"`
	Line   int    `json:"line"`
	Via    string `json:"via,omitempty"`
}

// DryRunReport compares a candidate with the configuration in force and with
// the repository's golden (truth-expected.json), when there is one.
type DryRunReport struct {
	Valid   bool         `json:"valid"`
	Errors  string       `json:"errors,omitempty"`
	Facts   []DryRunFact `json:"facts"`
	Added   []string     `json:"added_vs_current"`
	Removed []string     `json:"removed_vs_current"`
	Golden  *GoldenDiff  `json:"golden,omitempty"`
	Files   int          `json:"files_read"`
}

// GoldenDiff is the channel-class difference of the reference (reviewed view of
// the candidate) from truth-expected.json.
type GoldenDiff struct {
	File    string   `json:"file"`
	Missing []string `json:"missing"`
	Extra   []string `json:"extra"`
	// The same comparison for the configuration's own facts (what the rules
	// find, without the Go reference): the reference may already know a channel
	// the rules do not, and an author needs to see exactly that.
	ConfigHits    int      `json:"config_hits"`
	ConfigMissing []string `json:"config_missing"`
	ConfigExtra   []string `json:"config_extra"`
}

// isAnswerSheet: golden files of the audit fixtures are never project input.
func isAnswerSheet(p string) bool {
	b := filepath.Base(p)
	return strings.HasPrefix(b, "truth-expected") && strings.HasSuffix(b, ".json")
}

func trackedFiles(root string) []string {
	var out []string
	for _, p := range trackedFilesAll(root) {
		if !isAnswerSheet(p) {
			out = append(out, p)
		}
	}
	return out
}

func trackedFilesAll(root string) []string {
	if out, err := gitutil.RunRaw(root, "-c", "core.quotePath=false", "ls-files", "-z"); err == nil {
		var fs []string
		for _, p := range strings.Split(out, "\x00") {
			if p != "" {
				fs = append(fs, p)
			}
		}
		if len(fs) > 0 {
			return fs
		}
	}
	var fl []string
	_ = filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if n := d.Name(); n == ".git" || n == "node_modules" {
				return filepath.SkipDir
			}
			return nil
		}
		if rel, err := filepath.Rel(root, p); err == nil {
			fl = append(fl, filepath.ToSlash(rel))
		}
		return nil
	})
	return fl
}

func factKeys(cfg *scancfg.Config, root string, files []string) map[string]DryRunFact {
	t, _ := ReadConfigHostsWith(root, files, cfg)
	out := map[string]DryRunFact{}
	for _, rc := range t.RuleChannels {
		key := truth.ChannelKey(rc.Kind, strings.ToLower(rc.Name), rc.Side)
		k := key + "|" + rc.File + fmt.Sprint(rc.Line)
		out[k] = DryRunFact{Key: key, Rule: rc.Rule, Source: rc.Source, File: rc.File, Line: rc.Line}
	}
	return out
}

// candidateConfig layers a candidate project config over builtin and fleet.
func candidateConfig(root, yamlText string) (*scancfg.Config, error) {
	cur, _ := LoadScanConfigErr(root)
	var base []*scancfg.Config
	// keep builtin+fleet: the current merged config minus project-sourced rules
	b := *cur
	b.Channels, b.Documents, b.Clients = nil, nil, nil
	for _, c := range cur.Channels {
		if c.Source == scancfg.SourceBuiltin || c.Source == scancfg.SourceFleet {
			b.Channels = append(b.Channels, c)
		}
	}
	for _, d := range cur.Documents {
		if d.Source == scancfg.SourceBuiltin || d.Source == scancfg.SourceFleet {
			b.Documents = append(b.Documents, d)
		}
	}
	b.Aliases, b.Noise, b.Routes = nil, nil, nil
	b.Config = scancfg.ConfigSection{}
	b.Scope = scancfg.Scope{}
	base = append(base, &b)
	cand, err := scancfg.Parse([]byte(yamlText), scancfg.SourceProject)
	if err != nil {
		return nil, err
	}
	return scancfg.Merge(append(base, cand)...), nil
}

type overlayFS struct {
	fs.FS
	name string
	data []byte
}

func (o overlayFS) Open(n string) (fs.File, error) {
	if n == o.name {
		return nil, fs.ErrNotExist // served through ReadFile below
	}
	return o.FS.Open(n)
}

func (o overlayFS) ReadFile(n string) ([]byte, error) {
	if n == o.name {
		return o.data, nil
	}
	return fs.ReadFile(o.FS, n)
}

// ScanConfigDryRun evaluates yamlText (the candidate project layer) without
// saving it.
func ScanConfigDryRun(root, yamlText string) DryRunReport {
	rep := DryRunReport{Facts: []DryRunFact{}, Added: []string{}, Removed: []string{}}
	cand, err := candidateConfig(root, yamlText)
	if err != nil {
		rep.Errors = err.Error()
		return rep
	}
	rep.Valid = true
	files := trackedFiles(root)
	rep.Files = len(files)
	cur := LoadScanConfig(root)
	before := factKeys(cur, root, files)
	after := factKeys(cand, root, files)
	for _, f := range after {
		rep.Facts = append(rep.Facts, f)
	}
	sort.Slice(rep.Facts, func(i, j int) bool {
		a, b := rep.Facts[i], rep.Facts[j]
		return a.File < b.File || a.File == b.File && (a.Line < b.Line || a.Line == b.Line && a.Key < b.Key)
	})
	set := func(m map[string]DryRunFact) map[string]bool {
		o := map[string]bool{}
		for _, f := range m {
			o[f.Key] = true
		}
		return o
	}
	sb, sa := set(before), set(after)
	for k := range sa {
		if !sb[k] {
			rep.Added = append(rep.Added, k)
		}
	}
	for k := range sb {
		if !sa[k] {
			rep.Removed = append(rep.Removed, k)
		}
	}
	sort.Strings(rep.Added)
	sort.Strings(rep.Removed)
	rep.Golden = goldenDiff(root, yamlText, rep.Facts)
	return rep
}

func goldenDiff(root, yamlText string, facts []DryRunFact) *GoldenDiff {
	var gp string
	for _, c := range []string{"truth-expected.json", ".depbot/truth-expected.json"} {
		if _, err := os.Stat(filepath.Join(root, c)); err == nil {
			gp = c
			break
		}
	}
	if gp == "" {
		return nil
	}
	raw, err := os.ReadFile(filepath.Join(root, gp))
	if err != nil {
		return nil
	}
	var goldRaw map[string]json.RawMessage
	if json.Unmarshal(raw, &goldRaw) != nil {
		return nil
	}
	var goldChannels []struct{ Key string }
	_ = json.Unmarshal(goldRaw[string(truth.ClassChannels)], &goldChannels)
	// the "what if reviewed" view of the candidate: every rule counts
	c, err := scancfg.Parse([]byte(yamlText), scancfg.SourceProject)
	if err != nil {
		return nil
	}
	for i := range c.Channels {
		c.Channels[i].Reviewed = true
	}
	for i := range c.Documents {
		c.Documents[i].Reviewed = true
	}
	c.Provenance = ""
	y, err := yaml.Marshal(c)
	if err != nil {
		return nil
	}
	rep, err := extract.Extract(overlayFS{FS: os.DirFS(root), name: ScanConfigFile, data: y})
	if err != nil {
		return nil
	}
	got, want := map[string]bool{}, map[string]bool{}
	for _, f := range rep.Facts {
		if f.Class == truth.ClassChannels {
			got[f.Key] = true
		}
	}
	for _, e := range goldChannels {
		want[e.Key] = true
	}
	d := &GoldenDiff{File: gp, Missing: []string{}, Extra: []string{}}
	for k := range want {
		if !got[k] {
			d.Missing = append(d.Missing, k)
		}
	}
	for k := range got {
		if !want[k] {
			d.Extra = append(d.Extra, k)
		}
	}
	sort.Strings(d.Missing)
	sort.Strings(d.Extra)
	cfgGot := map[string]bool{}
	for _, f := range facts {
		cfgGot[f.Key] = true
	}
	d.ConfigMissing, d.ConfigExtra = []string{}, []string{}
	for k := range want {
		if cfgGot[k] {
			d.ConfigHits++
		} else {
			d.ConfigMissing = append(d.ConfigMissing, k)
		}
	}
	for k := range cfgGot {
		if !want[k] {
			d.ConfigExtra = append(d.ConfigExtra, k)
		}
	}
	sort.Strings(d.ConfigMissing)
	sort.Strings(d.ConfigExtra)
	return d
}

// ExplainEntry is one thing the configuration found in a file.
type ExplainEntry struct {
	Rule   string `json:"rule"`
	Source string `json:"source"`
	Key    string `json:"key"`
	Line   int    `json:"line"`
	Via    string `json:"via,omitempty"`
}

// ScanConfigExplain lists what the configuration in force yields in one file.
func ScanConfigExplain(root, file string) []ExplainEntry {
	file, ok := insideRepo(root, file)
	if !ok {
		return nil
	}
	cfg := LoadScanConfig(root)
	t, _ := ReadConfigHostsWith(root, []string{file}, cfg)
	var out []ExplainEntry
	for _, rc := range t.RuleChannels {
		out = append(out, ExplainEntry{Rule: rc.Rule, Source: rc.Source, Key: truth.ChannelKey(rc.Kind, strings.ToLower(rc.Name), rc.Side), Line: rc.Line})
	}
	return out
}

// ScanConfigSaveResult reports what a save did or why it refused.
type ScanConfigSaveResult struct {
	Saved   bool     `json:"saved"`
	Refused string   `json:"refused,omitempty"`
	Removed []string `json:"removed,omitempty"`
	Added   []string `json:"added,omitempty"`
	Path    string   `json:"path,omitempty"`
}

// ScanConfigSave writes the project layer. It refuses an invalid candidate, and
// one whose dry run loses facts the current configuration finds, unless the
// caller accepts the removal explicitly.
func ScanConfigSave(root, yamlText string, acceptRemoved bool) (ScanConfigSaveResult, error) {
	dr := ScanConfigDryRun(root, yamlText)
	if !dr.Valid {
		return ScanConfigSaveResult{Refused: dr.Errors}, nil
	}
	res := ScanConfigSaveResult{Removed: dr.Removed, Added: dr.Added}
	if len(dr.Removed) > 0 && !acceptRemoved {
		res.Refused = fmt.Sprintf("the candidate removes %d fact(s) the current configuration finds; pass accept-removed to confirm", len(dr.Removed))
		return res, nil
	}
	p := filepath.Join(root, filepath.FromSlash(ScanConfigFile))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return res, err
	}
	if err := os.WriteFile(p, []byte(yamlText), 0o644); err != nil {
		return res, err
	}
	res.Saved, res.Path = true, ScanConfigFile
	return res, nil
}

// insideRepo cleans a repository-relative path that comes from a model and
// refuses anything that leaves the repository: absolute paths, "..", and
// symlinks that resolve outside of it.
func insideRepo(root, file string) (string, bool) {
	file = filepath.ToSlash(filepath.Clean(filepath.FromSlash(file)))
	if file == "." || file == ".." || strings.HasPrefix(file, "../") || filepath.IsAbs(filepath.FromSlash(file)) {
		return "", false
	}
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", false
	}
	if real, err := filepath.EvalSymlinks(filepath.Join(root, filepath.FromSlash(file))); err == nil {
		if rel, err := filepath.Rel(realRoot, real); err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return "", false
		}
	}
	return file, true
}
