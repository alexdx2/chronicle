package graph

import (
	"os"
	"path/filepath"
	"strings"
)

// DiscoverResult holds files found during project discovery.
type DiscoverResult struct {
	Files         []DiscoveredFile `json:"files"`
	TotalFiles    int              `json:"total_files"`
	ByCategory    map[string]int   `json:"by_category"`
}

// DiscoveredFile is a file found during discovery.
type DiscoveredFile struct {
	Path     string `json:"path"`
	Category string `json:"category"` // manifest, schema, service, controller, resolver, gateway, module, config, source
}

// DiscoverFiles walks the project directory and finds all architecture-relevant files.
// Creates scan obligations for each discovered file.
func (g *Graph) DiscoverFiles(rootDir, domainKey string, revisionID int64) (*DiscoverResult, error) {
	result := &DiscoverResult{
		ByCategory: make(map[string]int),
	}

	skipDirs := map[string]bool{
		"node_modules": true, "dist": true, ".next": true, "coverage": true,
		".git": true, ".depbot": true, "build": true, "out": true,
		"__pycache__": true, ".venv": true, "vendor": true, "target": true,
	}

	filepath.Walk(rootDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if info.IsDir() {
			if skipDirs[info.Name()] {
				return filepath.SkipDir
			}
			return nil
		}

		relPath, _ := filepath.Rel(rootDir, path)
		category := categorizeFile(relPath, info.Name())
		if category == "" {
			return nil // not architecture-relevant
		}

		// Skip test/spec/story files
		name := strings.ToLower(info.Name())
		if strings.Contains(name, ".test.") || strings.Contains(name, ".spec.") ||
			strings.Contains(name, ".stories.") || strings.Contains(name, "__test") {
			return nil
		}

		// Skip generated files
		if strings.Contains(relPath, "generated") || strings.Contains(relPath, "/__generated__/") {
			return nil
		}

		result.Files = append(result.Files, DiscoveredFile{
			Path:     relPath,
			Category: category,
		})
		result.ByCategory[category]++
		result.TotalFiles++

		// Create obligation for this file
		if revisionID > 0 {
			g.store.CreateObligation(revisionID, domainKey, "scan_file", relPath, "discovered during project scan")
		}

		return nil
	})

	return result, nil
}

func categorizeFile(relPath, name string) string {
	lower := strings.ToLower(name)

	// Manifests — always scan
	switch lower {
	case "package.json", "go.mod", "go.sum", "cargo.toml", "pyproject.toml", "requirements.txt":
		return "manifest"
	}

	// Config files
	if strings.HasSuffix(lower, "docker-compose.yml") || strings.HasSuffix(lower, "docker-compose.yaml") ||
		lower == "dockerfile" || lower == ".env" || lower == ".env.example" {
		return "config"
	}

	// Schema/data files
	if strings.HasSuffix(lower, ".prisma") || strings.HasSuffix(lower, ".graphql") ||
		strings.HasSuffix(lower, ".gql") || lower == "schema.gql" {
		return "schema"
	}

	// Source files — categorize by name pattern
	ext := strings.ToLower(filepath.Ext(name))
	sourceExts := map[string]bool{
		".ts": true, ".tsx": true, ".js": true, ".jsx": true,
		".go": true, ".py": true, ".java": true, ".rs": true,
		".rb": true, ".cs": true, ".kt": true, ".swift": true,
	}

	if !sourceExts[ext] {
		// YAML config files
		if ext == ".yml" || ext == ".yaml" {
			if strings.Contains(relPath, "docker") || strings.Contains(relPath, "deploy") ||
				strings.Contains(relPath, "k8s") || strings.Contains(relPath, "config") {
				return "config"
			}
		}
		return ""
	}

	// Categorize source files by name patterns
	lowerPath := strings.ToLower(relPath)
	switch {
	case strings.Contains(lowerPath, ".module."):
		return "module"
	case strings.Contains(lowerPath, ".controller.") || strings.Contains(lowerPath, "/controllers/"):
		return "controller"
	case strings.Contains(lowerPath, ".resolver.") || strings.Contains(lowerPath, "/resolvers/"):
		return "resolver"
	case strings.Contains(lowerPath, ".gateway.") || strings.Contains(lowerPath, "/gateways/"):
		return "gateway"
	case strings.Contains(lowerPath, ".service.") || strings.Contains(lowerPath, "/services/"):
		return "service"
	case strings.Contains(lowerPath, ".guard.") || strings.Contains(lowerPath, ".interceptor.") ||
		strings.Contains(lowerPath, ".pipe.") || strings.Contains(lowerPath, ".middleware.") ||
		strings.Contains(lowerPath, ".decorator.") || strings.Contains(lowerPath, ".filter."):
		return "middleware"
	case strings.Contains(lowerPath, ".consumer.") || strings.Contains(lowerPath, ".producer.") ||
		strings.Contains(lowerPath, ".subscriber.") || strings.Contains(lowerPath, ".listener.") ||
		strings.Contains(lowerPath, ".handler.") || strings.Contains(lowerPath, ".queue.") ||
		strings.Contains(lowerPath, ".task.") || strings.Contains(lowerPath, ".cron.") ||
		strings.Contains(lowerPath, ".job."):
		return "async"
	case strings.Contains(lowerPath, ".client.") || strings.Contains(lowerPath, ".adapter.") ||
		strings.Contains(lowerPath, ".proxy.") || strings.Contains(lowerPath, ".connector."):
		return "client"
	case strings.Contains(lowerPath, "prisma") && strings.Contains(lowerPath, ".service."):
		return "service"
	case strings.Contains(lowerPath, "/src/") || strings.Contains(lowerPath, "/lib/"):
		return "source"
	}

	return ""
}
