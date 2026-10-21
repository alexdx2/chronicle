// Package scancfg is the declarative, per-project scan configuration
// (.depbot/scan.yaml): what to scan, where addresses and channel names live,
// how channels show up in code and in documents. It is data, not code: the
// engine matches templates against comment-free source and never executes
// anything. Both the scan and the independent reference read it; the
// reference executes only builtin and human-reviewed rules (see Layer).
//
// The package imports only std, yaml and the lexer, so truth may use it.
package scancfg

import "sort"

// Version of the file format.
const Version = 1

// Config is one parsed layer or the merged effective configuration.
type Config struct {
	Version int `yaml:"version" json:"version"`
	// Provenance of a project layer: "project" (default, a human wrote it) or
	// "llm:<model>" (a model proposed it; rules stay unreviewed until a human sets reviewed).
	Provenance string         `yaml:"provenance,omitempty" json:"provenance,omitempty"`
	Scope      Scope          `yaml:"scope,omitempty" json:"scope,omitempty"`
	Config     ConfigSection  `yaml:"config,omitempty" json:"config,omitempty"`
	Channels   []ChannelRule  `yaml:"channels,omitempty" json:"channels,omitempty"`
	Documents  []DocumentRule `yaml:"documents,omitempty" json:"documents,omitempty"`
	Clients    []ClientRule   `yaml:"clients,omitempty" json:"clients,omitempty"`
	Routes     []RouteRule    `yaml:"routes,omitempty" json:"routes,omitempty"`
	Aliases    []Alias        `yaml:"aliases,omitempty" json:"aliases,omitempty"`
	Noise      []Noise        `yaml:"noise,omitempty" json:"noise,omitempty"`
}

// Scope says which files the scan reads (glob patterns, ** crosses directories).
type Scope struct {
	Include []string `yaml:"include,omitempty" json:"include,omitempty"`
	Exclude []string `yaml:"exclude,omitempty" json:"exclude,omitempty"`
}

// ConfigSection says where values live and what a key means.
type ConfigSection struct {
	Files []ConfigFile `yaml:"files,omitempty" json:"files,omitempty"`
	// EnvDefaults: read the default next to an environment read in any language
	// (os.getenv("X", "d"), process.env.X || 'd', ENV.fetch, ${k:d}). Default true.
	EnvDefaults *bool        `yaml:"env_defaults,omitempty" json:"env_defaults,omitempty"`
	Keys        []KeyMeaning `yaml:"keys,omitempty" json:"keys,omitempty"`
}

// EnvDefaultsOn reports the effective value of env_defaults (default true).
func (c ConfigSection) EnvDefaultsOn() bool { return c.EnvDefaults == nil || *c.EnvDefaults }

// ConfigFile names a configuration file by glob and its format
// (auto, json, yaml, toml, properties, hocon, dotenv, xml-appsettings).
type ConfigFile struct {
	Glob   string `yaml:"glob" json:"glob"`
	Format string `yaml:"format,omitempty" json:"format,omitempty"`
}

// KeyMeaning classifies a configuration key (glob over the dotted/colon path or
// the variable name): channel, address or ignore.
type KeyMeaning struct {
	Match string `yaml:"match" json:"match"`
	As    string `yaml:"as" json:"as"`
}

// When restricts a rule to files.
type When struct {
	Ext      []string `yaml:"ext,omitempty" json:"ext,omitempty"`
	Glob     []string `yaml:"glob,omitempty" json:"glob,omitempty"`
	Contains []string `yaml:"contains,omitempty" json:"contains,omitempty"` // any token present in the code
	Imports  []string `yaml:"imports,omitempty" json:"imports,omitempty"`   // any token present in the code (import/using/require lines)
}

// ChannelRule recognises channel use or declaration in code.
type ChannelRule struct {
	ID          string    `yaml:"id" json:"id"`
	When        When      `yaml:"when,omitempty" json:"when,omitempty"`
	Patterns    []Pattern `yaml:"patterns" json:"patterns"`
	IgnoreNames []string  `yaml:"ignore_names,omitempty" json:"ignore_names,omitempty"`
	// IgnoreCase makes the call templates case-insensitive (SQL dialects).
	IgnoreCase bool   `yaml:"ignore_case,omitempty" json:"ignore_case,omitempty"`
	Reviewed   bool   `yaml:"reviewed,omitempty" json:"reviewed,omitempty"`
	Source     string `yaml:"-" json:"source,omitempty"` // set by the loader: builtin, fleet, project, llm
}

// Pattern is one call template. {name} captures the channel name (a string
// literal, or an expression resolved through configuration); {any} skips an
// expression; {Ident} captures a named expression.
type Pattern struct {
	Call      string `yaml:"call" json:"call"`
	Side      string `yaml:"side" json:"side"`                     // produce | consume | declare
	Kind      string `yaml:"kind,omitempty" json:"kind,omitempty"` // topic (default) | queue | stream | subject | exchange | subscription | bus | channel
	Transport string `yaml:"transport,omitempty" json:"transport,omitempty"`
}

// DocumentRule reads channel names out of structured files.
type DocumentRule struct {
	ID       string            `yaml:"id" json:"id"`
	Glob     string            `yaml:"glob" json:"glob"`
	WhenKeys map[string]string `yaml:"when_keys,omitempty" json:"when_keys,omitempty"` // dotted path glob -> value glob; all must hold
	Select   string            `yaml:"select,omitempty" json:"select,omitempty"`       // dotted path glob of the values to emit from
	Emit     []Emit            `yaml:"emit" json:"emit"`
	Reviewed bool              `yaml:"reviewed,omitempty" json:"reviewed,omitempty"`
	Source   string            `yaml:"-" json:"source,omitempty"`
}

// Emit is one channel fact of a document rule. Name may use {value} (the
// selected value), {key} (the selected path), {key:N} (path segment N),
// {doc:path} (the value at an absolute dotted path in the same document) and
// {each:doc:path} (every comma-separated item of it).
type Emit struct {
	Name      string            `yaml:"name" json:"name"`
	Side      string            `yaml:"side,omitempty" json:"side,omitempty"`
	SideByKey map[string]string `yaml:"side_by_key,omitempty" json:"side_by_key,omitempty"` // substring of the key -> side
	Kind      string            `yaml:"kind,omitempty" json:"kind,omitempty"`
	Transport string            `yaml:"transport,omitempty" json:"transport,omitempty"`
}

// ClientRule says which call builds an HTTP client from an address.
type ClientRule struct {
	Call   string `yaml:"call" json:"call"` // template with {addr}
	When   When   `yaml:"when,omitempty" json:"when,omitempty"`
	Source string `yaml:"-" json:"source,omitempty"`
}

// RouteRule is a convention hint about route prefixes. In format version 1 it
// reaches the model as a hint ("Conventions of this repository"); it does not
// rewrite paths by itself.
type RouteRule struct {
	Declare   string `yaml:"declare,omitempty" json:"declare,omitempty"`
	AppliesTo string `yaml:"applies_to,omitempty" json:"applies_to,omitempty"`
	File      string `yaml:"file,omitempty" json:"file,omitempty"`
	Hint      string `yaml:"hint,omitempty" json:"hint,omitempty"`
}

// Alias says a host name stands for a service.
type Alias struct {
	Host    string `yaml:"host" json:"host"`
	Service string `yaml:"service" json:"service"`
}

// Noise says what is not an integration: channels by name pattern, files whose
// addresses are not calls.
type Noise struct {
	Name string `yaml:"name,omitempty" json:"name,omitempty"`
	Glob string `yaml:"glob,omitempty" json:"glob,omitempty"`
	HTTP string `yaml:"http,omitempty" json:"http,omitempty"` // "ignore"
}

// Fact is what the engine finds.
type Fact struct {
	Rule      string `json:"rule"`
	Source    string `json:"source"`
	Reviewed  bool   `json:"reviewed"`
	Kind      string `json:"kind"`
	Side      string `json:"side"`
	Transport string `json:"transport,omitempty"`
	Name      string `json:"name"`
	File      string `json:"file"`
	Line      int    `json:"line"`
	Text      string `json:"text"`
	// Via is the configuration key the name was resolved through, if any.
	Via string `json:"via,omitempty"`
}

// Key is "<kind>:<name>#<side>" like the reference's channel facts.
func (f Fact) Key() string { return f.Kind + ":" + f.Name + "#" + f.Side }

// SortFacts orders facts by file, line, key.
func SortFacts(fs []Fact) {
	sort.SliceStable(fs, func(i, j int) bool {
		if fs[i].File != fs[j].File {
			return fs[i].File < fs[j].File
		}
		if fs[i].Line != fs[j].Line {
			return fs[i].Line < fs[j].Line
		}
		return fs[i].Key() < fs[j].Key()
	})
}
