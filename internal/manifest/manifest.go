package manifest

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

type Repository struct {
	Name string   `yaml:"name"`
	Path string   `yaml:"path"`
	Tags []string `yaml:"tags"`
}

type Manifest struct {
	Domain       string       `yaml:"domain"`
	Description  string       `yaml:"description"`
	Repositories []Repository `yaml:"repositories"`
	Owner        string       `yaml:"owner"`
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
	if len(m.Repositories) == 0 {
		return nil, fmt.Errorf("manifest validation: at least one repository is required")
	}
	for i, r := range m.Repositories {
		if r.Name == "" {
			return nil, fmt.Errorf("manifest validation: repository[%d].name is required", i)
		}
		if r.Path == "" {
			return nil, fmt.Errorf("manifest validation: repository[%d].path is required", i)
		}
	}
	return &m, nil
}
