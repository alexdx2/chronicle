package graph

import (
	"path/filepath"
	"strings"

	"github.com/alexdx2/chronicle-core/deps"
)

// Extraction layers: files leave for the model in this order so each layer
// can lean on the one before it.
const (
	LayerContracts    = 1 // manifests, deployment, schemas, migrations
	LayerStructure    = 2 // modules, controllers, services, everything else
	LayerIntegrations = 3 // clients, messaging, SDK wrappers
	LayerTests        = 4
)

var (
	testSegments        = map[string]bool{"test": true, "tests": true, "__tests__": true, "spec": true, "specs": true}
	testNameParts       = []string{".spec.", ".test.", "_test.go", "tests.cs", "test.cs"}
	contractExts        = map[string]bool{".prisma": true, ".graphql": true, ".gql": true, ".proto": true, ".avsc": true, ".avro": true}
	integrationSegments = map[string]bool{"clients": true, "consumers": true, "producers": true, "publishers": true, "subscribers": true,
		"messaging": true, "kafka": true, "sqs": true, "sns": true, "eventbridge": true, "integrations": true, "sdk": true, "adapters": true, "webhooks": true}
	integrationBase = []string{"client", "consumer", "producer", "publisher", "subscriber", "adapter", "gateway", "webhook", "integration"}
)

// LayerOf says which extraction layer a repository path belongs to. Tests are
// detected first (a file under tests/clients/ is a test), then contracts,
// then integrations; everything else is structure.
func LayerOf(path string) int {
	p := filepath.ToSlash(path)
	low := strings.ToLower(p)
	base := strings.ToLower(filepath.Base(p))
	for _, seg := range strings.Split(low, "/") {
		if testSegments[seg] {
			return LayerTests
		}
	}
	for _, part := range testNameParts {
		if strings.Contains(base, part) {
			return LayerTests
		}
	}
	if isContract(p, low, base) {
		return LayerContracts
	}
	for _, seg := range strings.Split(low, "/") {
		if integrationSegments[seg] {
			return LayerIntegrations
		}
	}
	stem := strings.TrimSuffix(base, filepath.Ext(base))
	for _, w := range integrationBase {
		if strings.Contains(stem, w) {
			return LayerIntegrations
		}
	}
	return LayerStructure
}

func isContract(p, low, base string) bool {
	if deps.IsManifest(p) {
		return true
	}
	if base == "dockerfile" || strings.HasPrefix(base, "dockerfile.") || strings.HasPrefix(base, "docker-compose.") || strings.HasPrefix(base, "compose.") {
		return true
	}
	if contractExts[filepath.Ext(base)] {
		return true
	}
	if strings.HasPrefix(base, "openapi.") || strings.HasPrefix(base, "swagger.") || strings.HasSuffix(base, ".schema.json") {
		return true
	}
	return strings.Contains(low, "/migrations/") || strings.HasPrefix(low, "migrations/")
}
