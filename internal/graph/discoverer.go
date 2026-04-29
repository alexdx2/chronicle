package graph

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// SingleRepoDiscoverer is the OSS implementation of GraphDiscoverer.
// It checks the given directory for a .depbot/ subdirectory.
type SingleRepoDiscoverer struct{}

func (d *SingleRepoDiscoverer) Discover(rootDir string) ([]GraphTarget, error) {
	depbotDir := filepath.Join(rootDir, ".depbot")
	dbPath := filepath.Join(depbotDir, "chronicle.db")

	if _, err := os.Stat(dbPath); err != nil {
		return nil, fmt.Errorf("no .depbot/chronicle.db found in %s", rootDir)
	}

	target := GraphTarget{
		RepoName: filepath.Base(rootDir),
		Path:     depbotDir,
	}

	// Try to read domain from manifest.
	manifestPath := filepath.Join(depbotDir, "chronicle.domain.yaml")
	if data, err := os.ReadFile(manifestPath); err == nil {
		var m struct {
			Domain string `yaml:"domain"`
		}
		if yaml.Unmarshal(data, &m) == nil && m.Domain != "" {
			target.Domain = m.Domain
		}
	}

	return []GraphTarget{target}, nil
}

// Compile-time check: SingleRepoDiscoverer implements GraphDiscoverer.
var _ GraphDiscoverer = (*SingleRepoDiscoverer)(nil)
