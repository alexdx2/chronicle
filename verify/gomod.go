package verify

import (
	"encoding/json"
	"fmt"
	"strings"
)

// GoModVerifier checks go.mod require directives.
type GoModVerifier struct{}

func (v *GoModVerifier) Kind() string { return "go_module_require" }

// GoModAssertion is the expected shape of assertion JSON.
type GoModAssertion struct {
	Module  string `json:"module"`
	Version string `json:"version,omitempty"`
}

func (v *GoModVerifier) Verify(fileContent []byte, assertion json.RawMessage, oldLocator *Locator) (*Result, error) {
	var a GoModAssertion
	if err := json.Unmarshal(assertion, &a); err != nil {
		return nil, fmt.Errorf("invalid go_module_require assertion: %w", err)
	}
	if a.Module == "" {
		return nil, fmt.Errorf("go_module_require assertion missing 'module' field")
	}

	lines := strings.Split(string(fileContent), "\n")
	inRequire := false

	for i, line := range lines {
		trimmed := strings.TrimSpace(line)

		// Single-line require
		if strings.HasPrefix(trimmed, "require ") && !strings.HasPrefix(trimmed, "require (") {
			if matchGoModRequire(trimmed[8:], a.Module) {
				version := extractGoModVersion(trimmed[8:], a.Module)
				return goModFound(i, version, a, oldLocator)
			}
		}

		// Multi-line require block
		if trimmed == "require (" {
			inRequire = true
			continue
		}
		if inRequire && trimmed == ")" {
			inRequire = false
			continue
		}
		if inRequire && matchGoModRequire(trimmed, a.Module) {
			version := extractGoModVersion(trimmed, a.Module)
			return goModFound(i, version, a, oldLocator)
		}
	}

	return &Result{
		Status:          "missing",
		Confidence:      0.95,
		Reason:          fmt.Sprintf("module %q not found in require directives", a.Module),
		SuggestedAction: "mark_stale",
	}, nil
}

func matchGoModRequire(line, module string) bool {
	// Line looks like: "github.com/foo/bar v1.2.3" or "github.com/foo/bar v1.2.3 // indirect"
	parts := strings.Fields(line)
	return len(parts) >= 1 && parts[0] == module
}

func extractGoModVersion(line, module string) string {
	parts := strings.Fields(line)
	if len(parts) >= 2 && parts[0] == module {
		return parts[1]
	}
	return ""
}

func goModFound(lineIdx int, foundVersion string, a GoModAssertion, _ *Locator) (*Result, error) {
	loc := &Locator{LineStart: lineIdx + 1, LineEnd: lineIdx + 1}

	if a.Version != "" && foundVersion != a.Version {
		return &Result{
			Status:          "valid",
			ChangeType:      "value_changed",
			NewLocator:      loc,
			Confidence:      0.90,
			Reason:          fmt.Sprintf("version changed: %s → %s", a.Version, foundVersion),
			SuggestedAction: "revalidate",
		}, nil
	}

	return &Result{
		Status:          "valid",
		NewLocator:      loc,
		Confidence:      0.95,
		SuggestedAction: "revalidate",
	}, nil
}
