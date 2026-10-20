package scancfg

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

// Layer sources, weakest first. Later layers override earlier rules with the same id.
const (
	SourceBuiltin = "builtin"
	SourceFleet   = "fleet"
	SourceProject = "project"
	SourceLLM     = "llm" // "llm:<model>" is accepted as well
)

// MaxConfigBytes bounds a scan.yaml.
const MaxConfigBytes = 256 << 10

var (
	sides      = map[string]bool{"produce": true, "consume": true, "declare": true}
	kinds      = map[string]bool{"": true, "topic": true, "queue": true, "stream": true, "subject": true, "exchange": true, "subscription": true, "bus": true, "channel": true}
	keyKinds   = map[string]bool{"channel": true, "address": true, "ignore": true}
	idPattern  = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]*$`)
	formatsSet = map[string]bool{"": true, "auto": true, "json": true, "yaml": true, "toml": true, "properties": true, "hocon": true, "dotenv": true, "xml-appsettings": true}
)

// Parse reads one layer. source is stamped on every rule; reviewed is forced
// on for builtin and fleet layers (a human put them there). Unknown fields are errors.
func Parse(data []byte, source string) (*Config, error) {
	if len(data) > MaxConfigBytes {
		return nil, fmt.Errorf("scan config is %d bytes, limit %d", len(data), MaxConfigBytes)
	}
	var c Config
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&c); err != nil {
		if err.Error() == "EOF" {
			return &Config{Version: Version}, nil
		}
		return nil, fmt.Errorf("scan config: %w", err)
	}
	if c.Version == 0 {
		c.Version = Version
	}
	if c.Version != Version {
		return nil, fmt.Errorf("scan config: unsupported version %d (this build reads %d)", c.Version, Version)
	}
	if source == SourceProject && c.Provenance != "" {
		if c.Provenance != "project" && !strings.HasPrefix(c.Provenance, "llm:") {
			return nil, fmt.Errorf("scan config: provenance %q must be project or llm:<model>", c.Provenance)
		}
		source = c.Provenance
	}
	trusted := source == SourceBuiltin || source == SourceFleet
	for i := range c.Channels {
		c.Channels[i].Source = source
		if trusted {
			c.Channels[i].Reviewed = true
		}
	}
	for i := range c.Documents {
		c.Documents[i].Source = source
		if trusted {
			c.Documents[i].Reviewed = true
		}
	}
	for i := range c.Clients {
		c.Clients[i].Source = source
	}
	if err := c.Validate(); err != nil {
		return nil, err
	}
	return &c, nil
}

// Validate reports every problem in one error so an LLM can fix them in one pass.
func (c *Config) Validate() error {
	var errs []string
	add := func(f string, a ...any) { errs = append(errs, fmt.Sprintf(f, a...)) }
	seen := map[string]bool{}
	for i, r := range c.Channels {
		where := fmt.Sprintf("channels[%d]", i)
		if !idPattern.MatchString(r.ID) {
			add("%s: id %q must be lowercase letters, digits, . _ -", where, r.ID)
		} else if seen["c:"+r.ID] {
			add("%s: duplicate id %q", where, r.ID)
		}
		seen["c:"+r.ID] = true
		if len(r.Patterns) == 0 {
			add("%s (%s): no patterns", where, r.ID)
		}
		for j, p := range r.Patterns {
			pw := fmt.Sprintf("%s (%s).patterns[%d]", where, r.ID, j)
			if !sides[p.Side] && p.Side != "define" {
				add("%s: side %q must be produce, consume, declare or define", pw, p.Side)
			}
			if c, err := compileTemplate(p.Call); err == nil {
				switch {
				case p.Side == "define" && !(c.hasID && c.named):
					add("%s: a define pattern needs {id} and {name}", pw)
				case p.Side != "define" && c.hasID:
					add("%s: {id} belongs in a define pattern; use {ref} to look an identifier up", pw)
				case p.Side == "define" && c.hasRef:
					add("%s: a define pattern cannot use {ref}", pw)
				}
			}
			if !kinds[p.Kind] {
				add("%s: kind %q is not a channel kind", pw, p.Kind)
			}
			if _, err := compileTemplate(p.Call); err != nil {
				add("%s: %v", pw, err)
			}
		}
		for _, g := range r.IgnoreNames {
			if _, err := regexp.Compile(globToRegexp(g)); err != nil {
				add("%s (%s): ignore_names %q: %v", where, r.ID, g, err)
			}
		}
	}
	for i, d := range c.Documents {
		where := fmt.Sprintf("documents[%d]", i)
		if !idPattern.MatchString(d.ID) {
			add("%s: id %q must be lowercase letters, digits, . _ -", where, d.ID)
		} else if seen["d:"+d.ID] {
			add("%s: duplicate id %q", where, d.ID)
		}
		seen["d:"+d.ID] = true
		if d.Glob == "" {
			add("%s (%s): glob is empty", where, d.ID)
		}
		if len(d.Emit) == 0 {
			add("%s (%s): no emit", where, d.ID)
		}
		for j, e := range d.Emit {
			if e.Name == "" {
				add("%s (%s).emit[%d]: name is empty", where, d.ID, j)
			}
			if e.Side != "" && !sides[e.Side] {
				add("%s (%s).emit[%d]: side %q", where, d.ID, j, e.Side)
			}
			if e.Side == "" && len(e.SideByKey) == 0 {
				add("%s (%s).emit[%d]: needs side or side_by_key", where, d.ID, j)
			}
			for _, s := range e.SideByKey {
				if !sides[s] {
					add("%s (%s).emit[%d]: side_by_key side %q", where, d.ID, j, s)
				}
			}
			if !kinds[e.Kind] {
				add("%s (%s).emit[%d]: kind %q", where, d.ID, j, e.Kind)
			}
		}
	}
	for i, f := range c.Config.Files {
		if f.Glob == "" {
			add("config.files[%d]: glob is empty", i)
		}
		if !formatsSet[f.Format] {
			add("config.files[%d]: format %q is unknown", i, f.Format)
		}
	}
	for i, k := range c.Config.Keys {
		if k.Match == "" || !keyKinds[k.As] {
			add("config.keys[%d]: needs match and as: channel|address|ignore", i)
		}
	}
	for i, cl := range c.Clients {
		if !strings.Contains(cl.Call, "{addr}") {
			add("clients[%d]: call %q must contain {addr}", i, cl.Call)
		}
	}
	for i, a := range c.Aliases {
		if a.Host == "" || a.Service == "" {
			add("aliases[%d]: host and service are required", i)
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("scan config invalid:\n  - %s", strings.Join(errs, "\n  - "))
	}
	return nil
}

// Merge overlays layers (weakest first). A rule with the same id replaces the
// weaker one; scope, keys, files, clients, routes, aliases and noise accumulate.
func Merge(layers ...*Config) *Config {
	out := &Config{Version: Version}
	chIdx := map[string]int{}
	docIdx := map[string]int{}
	for _, l := range layers {
		if l == nil {
			continue
		}
		out.Scope.Include = append(out.Scope.Include, l.Scope.Include...)
		out.Scope.Exclude = append(out.Scope.Exclude, l.Scope.Exclude...)
		out.Config.Files = append(out.Config.Files, l.Config.Files...)
		out.Config.Keys = append(out.Config.Keys, l.Config.Keys...)
		if l.Config.EnvDefaults != nil {
			out.Config.EnvDefaults = l.Config.EnvDefaults
		}
		for _, r := range l.Channels {
			if i, ok := chIdx[r.ID]; ok {
				out.Channels[i] = r
				continue
			}
			chIdx[r.ID] = len(out.Channels)
			out.Channels = append(out.Channels, r)
		}
		for _, d := range l.Documents {
			if i, ok := docIdx[d.ID]; ok {
				out.Documents[i] = d
				continue
			}
			docIdx[d.ID] = len(out.Documents)
			out.Documents = append(out.Documents, d)
		}
		out.Clients = append(out.Clients, l.Clients...)
		out.Routes = append(out.Routes, l.Routes...)
		out.Aliases = append(out.Aliases, l.Aliases...)
		out.Noise = append(out.Noise, l.Noise...)
	}
	return out
}

// Trusted keeps only rules a human vouches for: builtin, fleet, or marked
// reviewed. The independent reference executes this view.
func (c *Config) Trusted() *Config {
	out := *c
	out.Channels, out.Documents = nil, nil
	for _, r := range c.Channels {
		if r.Reviewed {
			out.Channels = append(out.Channels, r)
		}
	}
	for _, d := range c.Documents {
		if d.Reviewed {
			out.Documents = append(out.Documents, d)
		}
	}
	return &out
}

// Hash is stable over the semantic content; it feeds the coverage version
// "scan-config@<hash>" so a changed configuration re-extracts affected files.
func (c *Config) Hash() string {
	b, _ := json.Marshal(c)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])[:12]
}

// CoverageVersion is the string stored next to extraction coverage.
func (c *Config) CoverageVersion() string {
	if c == nil || c.Empty() {
		return ""
	}
	return "scan-config@" + c.Hash()
}

// Empty reports that the configuration changes nothing.
func (c *Config) Empty() bool {
	return len(c.Channels) == 0 && len(c.Documents) == 0 && len(c.Clients) == 0 &&
		len(c.Aliases) == 0 && len(c.Noise) == 0 && len(c.Config.Files) == 0 &&
		len(c.Config.Keys) == 0 && len(c.Scope.Include) == 0 && len(c.Scope.Exclude) == 0 &&
		c.Config.EnvDefaults == nil && len(c.Routes) == 0
}
