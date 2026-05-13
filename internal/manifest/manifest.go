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

type Manifest struct {
	Domains          []DomainEntry `yaml:"domains"`
	Tech             []string      `yaml:"tech,omitempty"`
	Infrastructure   []InfraEntry  `yaml:"infrastructure,omitempty"`
	InstructionPacks []string      `yaml:"instruction_packs,omitempty"`
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
