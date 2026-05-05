package manifest

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

type Repository struct {
	Name        string   `yaml:"name"`
	Path        string   `yaml:"path"`
	Tags        []string `yaml:"tags"`
	Description string   `yaml:"description,omitempty"`
}

type ScanConfig struct {
	Include []string `yaml:"include,omitempty"` // glob patterns for files to scan
	Exclude []string `yaml:"exclude,omitempty"` // glob patterns to skip
}

type Manifest struct {
	Domain       string       `yaml:"domain"`
	Description  string       `yaml:"description"`
	Repositories []Repository `yaml:"repositories"`
	Repos        []Repository `yaml:"repos,omitempty"`    // alias for repositories (legacy)
	Services     []Repository `yaml:"services,omitempty"` // service definitions
	Owner        string       `yaml:"owner"`
	Scan         ScanConfig   `yaml:"scan,omitempty"`
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
	if m.Domain == "" {
		return nil, fmt.Errorf("manifest validation: domain is required")
	}
	// Merge repos → repositories (legacy alias)
	if len(m.Repositories) == 0 && len(m.Repos) > 0 {
		m.Repositories = m.Repos
	}
	// Merge services → repositories if no repos defined
	if len(m.Repositories) == 0 && len(m.Services) > 0 {
		m.Repositories = m.Services
	}
	// Repositories are optional — scan config alone is enough
	return &m, nil
}
