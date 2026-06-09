package manifest

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

type ScanConfig struct {
	Include []string `yaml:"include,omitempty"`
	Exclude []string `yaml:"exclude,omitempty"`
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

// InfraNodeKey returns the graph node key for this infrastructure entry.
// Format: infra:{type}:{address} or infra:{type}:{name} if no address.
func (e InfraEntry) InfraNodeKey() string {
	id := e.Address
	if id == "" {
		id = e.Name
	}
	return "infra:" + e.Type + ":" + id
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
		if matchGlob(filePath, pattern) {
			return false
		}
	}
	for _, pattern := range d.Scan.Include {
		if matchGlob(filePath, pattern) {
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

// matchGlob matches a file path against a glob pattern with ** support.
// Copied from graph/discover.go to avoid circular imports.
func matchGlob(filePath, pattern string) bool {
	if strings.Contains(pattern, "**") {
		parts := strings.SplitN(pattern, "**", 2)
		prefix := strings.TrimSuffix(parts[0], "/")
		suffix := ""
		if len(parts) > 1 {
			suffix = strings.TrimPrefix(parts[1], "/")
		}

		if prefix == "" {
			if suffix == "" {
				return true
			}
			matched, _ := filepath.Match(suffix, filepath.Base(filePath))
			return matched
		}

		if !strings.HasPrefix(filePath, prefix+"/") && filePath != prefix {
			return false
		}

		if suffix == "" {
			return true
		}

		matched, _ := filepath.Match(suffix, filepath.Base(filePath))
		return matched
	}

	matched, _ := filepath.Match(pattern, filePath)
	if matched {
		return true
	}
	matched, _ = filepath.Match(pattern, filepath.Base(filePath))
	return matched
}
