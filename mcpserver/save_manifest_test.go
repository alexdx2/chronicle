package mcpserver

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/alexdx2/chronicle-core/graph"
	"github.com/alexdx2/chronicle-core/paths"
	"github.com/alexdx2/chronicle-core/registry"
	"github.com/alexdx2/chronicle-core/store"
)

// Map-format manifests (chronicle.domain.yaml domains: {key: {name: ...}})
// separate the canonical graph domain key (the map key, e.g. "tom-and-jerry")
// from the DISPLAY string (Name, e.g. "Tom and Jerry"). save_manifest's infra
// writer used m.Domains[0].Name for BOTH the revision lookup and the infra
// key's domain segment — the two other correct sites in this codebase
// (graph/discover.go's svcDomain, internal/admin/server.go's
// domainFromManifest) prefer .Key and only fall back to .Name. Using .Name
// here meant: GetLatestRevision("Tom and Jerry") misses the revision that
// actually exists under "tom-and-jerry" (silently keeping revision 0 on every
// re-save), and InfraNodeKey mints "infra:type:tom and jerry:..." — a
// different key than discover.go's "infra:type:tom-and-jerry:..." for the
// exact same manifest entry.
func TestSaveManifestHandler_MapFormatDomain_UsesCanonicalKeyNotDisplayName(t *testing.T) {
	dir := t.TempDir()
	s, err := store.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	reg, err := registry.LoadDefaults()
	if err != nil {
		t.Fatalf("registry.LoadDefaults: %v", err)
	}
	g := graph.New(s, reg)

	// A revision already exists under the CANONICAL domain key, simulating a
	// re-save after a prior scan.
	wantRevID, err := s.CreateRevision("tom-and-jerry", "", "sha1", "manual", "full", "{}")
	if err != nil {
		t.Fatalf("CreateRevision: %v", err)
	}

	manifestPath := filepath.Join(dir, "chronicle.domain.yaml")
	SetManifestPath(manifestPath)
	t.Cleanup(func() { SetManifestPath("") })
	paths.SetProjectRoot(dir)
	t.Cleanup(func() { paths.SetProjectRoot("") })

	content := `
domains:
  tom-and-jerry:
    name: Tom and Jerry
    description: Cartoon battle microservices

tech: [nestjs]

infrastructure:
  - name: events-kafka
    type: broker
    address: kafka-events.internal:9092
`
	out := callToolText(t, saveManifestHandler(g), map[string]any{"content": content})
	if !strings.Contains(out, `"status":"saved"`) {
		t.Fatalf("save_manifest failed: %s", out)
	}

	// The infra node must be findable under the CANONICAL domain key ("tom-
	// and-jerry"), not the display string ("tom and jerry") — the same key
	// discover.go's writer would mint for the identical manifest entry.
	nodes, err := s.ListNodes(store.NodeFilter{Layer: "infra", Domain: "tom-and-jerry"})
	if err != nil {
		t.Fatalf("ListNodes: %v", err)
	}
	if len(nodes) != 1 {
		allNodes, _ := s.ListNodes(store.NodeFilter{Layer: "infra"})
		t.Fatalf("expected 1 infra node under domain_key=tom-and-jerry, got %d (all infra nodes: %+v)", len(nodes), allNodes)
	}
	node := nodes[0]
	if strings.Contains(node.NodeKey, "tom and jerry") {
		t.Errorf("infra node key %q uses the DISPLAY name, not the canonical domain key", node.NodeKey)
	}
	if !strings.Contains(node.NodeKey, ":tom-and-jerry:") {
		t.Errorf("infra node key %q missing canonical domain segment :tom-and-jerry:", node.NodeKey)
	}

	// The revision lookup must hit the EXISTING revision under the canonical
	// key, not silently stay at 0 because it looked up "Tom and Jerry".
	if node.FirstSeenRevisionID != wantRevID {
		t.Errorf("FirstSeenRevisionID = %d, want %d (the pre-existing revision under the canonical domain key)", node.FirstSeenRevisionID, wantRevID)
	}
	if node.LastSeenRevisionID != wantRevID {
		t.Errorf("LastSeenRevisionID = %d, want %d", node.LastSeenRevisionID, wantRevID)
	}
}
