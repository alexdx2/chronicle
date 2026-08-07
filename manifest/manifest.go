package manifest

import (
	"fmt"
	"os"
	"path"
	"strings"

	"gopkg.in/yaml.v3"
)

type ScanConfig struct {
	Include []string `yaml:"include,omitempty"`
	Exclude []string `yaml:"exclude,omitempty"`
	// IncludeDevDeps opts this domain into manifest dependency extraction for
	// devDependencies entries (SQ-Contract 2 axis 1 section policy). Default
	// false: dev tooling deps are skipped entirely — most devDependencies are
	// build/test tooling, not architecture, and the default should hide noise
	// rather than show it.
	IncludeDevDeps bool `yaml:"include_dev_deps,omitempty"`
}

type DomainEntry struct {
	Key         string     `yaml:"-"` // YAML map key or name (list format); used as graph domain_key
	Name        string     `yaml:"name"`
	Description string     `yaml:"description,omitempty"`
	Owner       string     `yaml:"owner,omitempty"`
	Scan        ScanConfig `yaml:"scan,omitempty"`
}

type InfraEntry struct {
	Name        string `yaml:"name"`
	Type        string `yaml:"type"`                  // broker, cache, database, queue
	Address     string `yaml:"address,omitempty"`      // host:port or connection string
	Description string `yaml:"description,omitempty"`
}

// AddressOrName returns the connection address when declared, else the
// display name — the identifier InfraNodeKey folds (dashed) into its name
// segment. Exposed so writers can also stash it verbatim on a node's
// QualifiedName column: InfraNodeKey's own segment is canonicalized (port
// colon -> dash, and validate.NormalizeNodeKey further folds dots/case), so
// it can no longer be reverse-parsed back into a bare hostname the way
// graph.ownHostsForDomain needs for isExternalHost matching.
func (e InfraEntry) AddressOrName() string {
	if e.Address != "" {
		return e.Address
	}
	return e.Name
}

// InfraNodeKey returns the graph node key for this infrastructure entry.
// Format: infra:{type}:{domainKey}:{name-or-address} — the 4-segment
// layer:type:domain:qualified_name shape validate.NormalizeNodeKey requires
// (SQ-Contract 3). The previous 3-segment "infra:{type}:{address}" put the
// host in the domain slot; it never parsed, so import_all's edge validation
// could never see these nodes at all. Callers own registry validity for the
// type segment (registryValidInfraType) and must pass the SAME mapped value
// they stamp onto the node_type column, so key and column agree.
//
// A ':' inside the address or name (host:port, e.g. "kafka:9092") is
// replaced with '-' in the name segment so the port colon doesn't fold into
// the wrong segment when the key is later split on ':'.
//
// The raw string returned here is NOT guaranteed to be a fixed point of
// validate.NormalizeNodeKey — only the port-colon fold above is applied. A
// dotted address ("kafka-events.internal:9092" -> raw
// "kafka-events.internal-9092" after the port fold) still has its dot
// folded to a dash by NormalizeNodeKey's qualified-name pass, and an
// uppercase type/name is still lowercased; normalizing the raw result a
// second time changes it. Every caller MUST wrap the return value in
// graph.CanonicalNodeKey (which calls validate.NormalizeNodeKey) before
// storing or looking it up — the WRAPPED form, not this raw one, is the
// actual fixed point SQ-Contract 3 requires. All current writers
// (graph/discover.go, mcpserver's save_manifest handler) already do this.
func (e InfraEntry) InfraNodeKey(domainKey string) string {
	id := strings.ReplaceAll(e.AddressOrName(), ":", "-")
	return "infra:" + e.Type + ":" + domainKey + ":" + id
}

type ServiceEntry struct {
	Key  string `yaml:"key"`
	Path string `yaml:"path"`
	Role string `yaml:"role,omitempty"`
}

type Manifest struct {
	Domains          []DomainEntry  `yaml:"-"` // custom unmarshal: supports both list and map formats
	Tech             []string       `yaml:"tech,omitempty"`
	Infrastructure   []InfraEntry   `yaml:"infrastructure,omitempty"`
	Services         []ServiceEntry `yaml:"services,omitempty"`
	InstructionPacks []string       `yaml:"instruction_packs,omitempty"`
}

// UnmarshalYAML handles two domain formats:
// List: domains: [{name: x, scan: ...}]
// Map:  domains: {x: {name: X, scan: ...}}
func (m *Manifest) UnmarshalYAML(value *yaml.Node) error {
	// Decode into raw struct with domains as raw node
	var raw struct {
		Domains          yaml.Node      `yaml:"domains"`
		Tech             []string       `yaml:"tech,omitempty"`
		Infrastructure   []InfraEntry   `yaml:"infrastructure,omitempty"`
		Services         []ServiceEntry `yaml:"services,omitempty"`
		InstructionPacks []string       `yaml:"instruction_packs,omitempty"`
	}
	if err := value.Decode(&raw); err != nil {
		return err
	}

	m.Tech = raw.Tech
	m.Infrastructure = raw.Infrastructure
	m.Services = raw.Services
	m.InstructionPacks = raw.InstructionPacks

	if raw.Domains.Kind == 0 {
		return nil // no domains field
	}

	switch raw.Domains.Kind {
	case yaml.SequenceNode:
		// List format: [{name: x, ...}]
		if err := raw.Domains.Decode(&m.Domains); err != nil {
			return err
		}
		for i := range m.Domains {
			if m.Domains[i].Key == "" {
				m.Domains[i].Key = m.Domains[i].Name
			}
		}
		return nil

	case yaml.MappingNode:
		// Map format: {key: {name: X, ...}}
		// Each map entry: key is domain key, value is domain config
		type domainValue struct {
			Name             string       `yaml:"name"`
			Description      string       `yaml:"description,omitempty"`
			Owner            string       `yaml:"owner,omitempty"`
			Tech             []string     `yaml:"tech,omitempty"`
			Scan             ScanConfig   `yaml:"scan,omitempty"`
			Infrastructure   []InfraEntry `yaml:"infrastructure,omitempty"`
			InstructionPacks []string     `yaml:"instruction_packs,omitempty"`
		}
		domainMap := map[string]domainValue{}
		if err := raw.Domains.Decode(&domainMap); err != nil {
			return fmt.Errorf("decoding domains map: %w", err)
		}
		for key, dv := range domainMap {
			name := dv.Name
			if name == "" {
				name = key
			}
			entry := DomainEntry{
				Key:         key,
				Name:        name,
				Description: dv.Description,
				Owner:       dv.Owner,
				Scan:        dv.Scan,
			}
			// Promote domain-level tech/infra/packs to manifest level if not set
			if len(dv.Tech) > 0 && len(m.Tech) == 0 {
				m.Tech = dv.Tech
			}
			if len(dv.Infrastructure) > 0 && len(m.Infrastructure) == 0 {
				m.Infrastructure = dv.Infrastructure
			}
			if len(dv.InstructionPacks) > 0 && len(m.InstructionPacks) == 0 {
				m.InstructionPacks = dv.InstructionPacks
			}
			m.Domains = append(m.Domains, entry)
		}
		return nil

	default:
		return fmt.Errorf("domains must be a list or map, got YAML kind %d", raw.Domains.Kind)
	}
}

// InferServices returns explicit services if defined, otherwise derives them
// from scan.include patterns by extracting top-level directory names.
func (m *Manifest) InferServices() []ServiceEntry {
	if len(m.Services) > 0 {
		return m.Services
	}
	seen := map[string]bool{}
	var services []ServiceEntry
	for _, d := range m.Domains {
		for _, pattern := range d.Scan.Include {
			parts := strings.SplitN(pattern, "/", 2)
			if len(parts) > 0 && parts[0] != "**" && parts[0] != "*" {
				dir := parts[0]
				if !seen[dir] {
					seen[dir] = true
					services = append(services, ServiceEntry{Key: dir, Path: dir})
				}
			}
		}
	}
	return services
}

func LoadFile(path string) (*Manifest, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading manifest: %w", err)
	}
	return Load(data)
}

func Load(data []byte) (*Manifest, error) {
	var m Manifest
	if err := yaml.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("parsing manifest: %w", err)
	}
	if len(m.Domains) == 0 {
		return nil, fmt.Errorf("manifest validation: domains is required (must have at least one domain)")
	}
	for i, d := range m.Domains {
		if d.Name == "" {
			return nil, fmt.Errorf("manifest validation: domains[%d].name is required", i)
		}
	}
	return &m, nil
}

// DomainForFile matches a file path against each domain's scan.include/exclude patterns.
// First matching domain wins. Returns "_unassigned" if nothing matches.
func (m *Manifest) DomainForFile(filePath string) string {
	for _, d := range m.Domains {
		if domainMatchesFile(d, filePath) {
			if d.Key != "" {
				return d.Key
			}
			return d.Name
		}
	}
	return "_unassigned"
}

// domainMatchesFile checks if a file matches a domain's scan config.
// If no include patterns, domain does not match any file.
// If include patterns exist, file must match at least one.
// If exclude patterns exist, file must NOT match any.
func domainMatchesFile(d DomainEntry, filePath string) bool {
	if len(d.Scan.Include) == 0 {
		return false
	}
	for _, pattern := range d.Scan.Exclude {
		if MatchGlob(filePath, pattern) {
			return false
		}
	}
	for _, pattern := range d.Scan.Include {
		if MatchGlob(filePath, pattern) {
			return true
		}
	}
	return false
}

// ReplaceDomainsWithClone replaces all domain entries with a single clone of
// baseKey re-keyed as newKey. Used by scan-lab synthetic domains so that file
// discovery assigns every file to the lab domain. Returns false if baseKey is absent.
func (m *Manifest) ReplaceDomainsWithClone(baseKey, newKey string) bool {
	for _, d := range m.Domains {
		if d.Key == baseKey {
			clone := d
			clone.Key = newKey
			m.Domains = []DomainEntry{clone}
			return true
		}
	}
	return false
}

// ScanConfigFor returns the scan config of one named domain, when declared.
// Domain-scoped discovery must not inherit other domains' includes (the
// 2026-07-18 otopoint scan pulled 820 files instead of ~350 because discover
// used the merged view for a single-domain request).
func (m *Manifest) ScanConfigFor(domainKey string) (*ScanConfig, bool) {
	for i := range m.Domains {
		if m.Domains[i].Name == domainKey {
			cfg := m.Domains[i].Scan
			return &cfg, true
		}
	}
	return nil, false
}

// MergedScanConfig builds a single ScanConfig from all domains' scan patterns.
// Useful for callers that need a flat include/exclude list.
func (m *Manifest) MergedScanConfig() ScanConfig {
	var merged ScanConfig
	for _, d := range m.Domains {
		merged.Include = append(merged.Include, d.Scan.Include...)
		merged.Exclude = append(merged.Exclude, d.Scan.Exclude...)
	}
	return merged
}

// MatchGlob matches a file path against a glob pattern with segment-wise
// "**" semantics: the pattern is split on "/"; a "**" segment matches zero
// or more whole path segments — at the start, in the middle, or at the end
// of the pattern; every other segment matches exactly one path segment via
// path.Match. There is no basename fallback: a pattern with no "**" and no
// "/" (e.g. "config.json") matches only a root-level file of that exact
// name, never the same basename at any depth — any-depth intent must be
// spelled "**/config.json".
//
// The single sole exception was fixed here, not carved out: the old
// implementation split on the FIRST "**" only and matched everything after
// it against filepath.Base(filePath) — so a second "**" later in the
// pattern (e.g. "**/__tests__/**") survived as literal, un-evaluated text
// compared against a basename that can never contain "/". That made
// "**/__tests__/**" structurally incapable of ever matching anything.
func MatchGlob(filePath, pattern string) bool {
	return matchSegs(strings.Split(filePath, "/"), strings.Split(pattern, "/"))
}

// matchSegs recursively matches path segments against pattern segments.
// A "**" pattern segment consumes zero or more path segments (tried
// greedily from zero upward, backtracking via recursion); any other
// pattern segment must consume exactly one path segment and match it via
// path.Match (which never crosses a "/" — each segment is already split).
func matchSegs(pathSegs, patSegs []string) bool {
	if len(patSegs) == 0 {
		return len(pathSegs) == 0
	}

	if patSegs[0] == "**" {
		// "**" matches zero segments here...
		if matchSegs(pathSegs, patSegs[1:]) {
			return true
		}
		// ...or one-or-more, by consuming a segment and retrying "**"
		// against the rest of the path.
		if len(pathSegs) == 0 {
			return false
		}
		return matchSegs(pathSegs[1:], patSegs)
	}

	if len(pathSegs) == 0 {
		return false
	}
	matched, _ := path.Match(patSegs[0], pathSegs[0])
	if !matched {
		return false
	}
	return matchSegs(pathSegs[1:], patSegs[1:])
}
