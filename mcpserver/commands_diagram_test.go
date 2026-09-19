package mcpserver

import (
	"encoding/json"
	"regexp"
	"strings"
	"testing"

	"github.com/alexdx2/chronicle-core/graph/viewmodel"
)

var catalogViewSpec = regexp.MustCompile(`view_spec: '(\{.*?\})'`)

// The diagram catalog is instructions an agent follows literally. An example
// that does not parse, or names a mode the tool does not have, costs a round
// trip and teaches the wrong shape — and the old catalog spent every one of
// its examples on modes the renderer could not draw.
func TestDiagramCatalogExamplesAreValidSpecs(t *testing.T) {
	text, ok := CommandInstructions["diagram"]
	if !ok {
		t.Fatal("no diagram command")
	}
	matches := catalogViewSpec.FindAllStringSubmatch(text, -1)
	if len(matches) < 4 {
		t.Fatalf("catalog has %d view_spec examples, want one per graph-derived type", len(matches))
	}
	for _, m := range matches {
		var spec viewmodel.ViewSpec
		if err := json.Unmarshal([]byte(m[1]), &spec); err != nil {
			t.Errorf("example does not parse as a ViewSpec: %v\n  %s", err, m[1])
			continue
		}
		if spec.Layout.Preset == "" {
			t.Errorf("example names no layout preset: %s", m[1])
		}
	}
}

// The catalog used to order the agent into the one path nothing verifies.
// Keeping that instruction out is the point of the rewrite.
func TestDiagramCatalogNoLongerMandatesHandAuthoring(t *testing.T) {
	text := CommandInstructions["diagram"]
	for _, banned := range []string{
		`Use ONLY "nodes"`,
		`NEVER use "node_keys"`,
		"nodes + edges ONLY",
	} {
		if strings.Contains(text, banned) {
			t.Errorf("catalog still instructs the unverified path: %q", banned)
		}
	}
	if !strings.Contains(text, "asserted") {
		t.Error("catalog never warns that hand-written elements render marked asserted")
	}
}
