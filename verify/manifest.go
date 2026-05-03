package verify

import (
	"encoding/json"
	"fmt"
	"strings"
)

// ManifestVerifier checks package.json manifest dependencies.
type ManifestVerifier struct{}

func (v *ManifestVerifier) Kind() string { return "manifest_dependency" }

// ManifestAssertion is the expected shape of assertion JSON for manifest_dependency.
type ManifestAssertion struct {
	Package         string   `json:"package"`
	Sections        []string `json:"sections"` // e.g. ["dependencies", "devDependencies"]
	ExpectedVersion string   `json:"expected_version,omitempty"`
}

func (v *ManifestVerifier) Verify(fileContent []byte, assertion json.RawMessage, oldLocator *Locator) (*Result, error) {
	var a ManifestAssertion
	if err := json.Unmarshal(assertion, &a); err != nil {
		return nil, fmt.Errorf("invalid manifest_dependency assertion: %w", err)
	}
	if a.Package == "" {
		return nil, fmt.Errorf("manifest_dependency assertion missing 'package' field")
	}

	// Parse the package.json
	var pkg map[string]json.RawMessage
	if err := json.Unmarshal(fileContent, &pkg); err != nil {
		return &Result{
			Status:          "missing",
			Confidence:      0.90,
			Reason:          "file is not valid JSON",
			SuggestedAction: "needs_claude",
		}, nil
	}

	// Default sections to check
	sections := a.Sections
	if len(sections) == 0 {
		sections = []string{"dependencies", "devDependencies", "peerDependencies", "optionalDependencies"}
	}

	// Search each section for the package
	for _, section := range sections {
		raw, ok := pkg[section]
		if !ok {
			continue
		}
		var deps map[string]string
		if err := json.Unmarshal(raw, &deps); err != nil {
			continue
		}
		if version, found := deps[a.Package]; found {
			// Found it — compute locator
			loc := findJSONKeyLocation(fileContent, section, a.Package)

			// Check if version matches expectation
			if a.ExpectedVersion != "" && version != a.ExpectedVersion {
				return &Result{
					Status:          "valid",
					ChangeType:      "value_changed",
					NewLocator:      loc,
					Confidence:      0.90,
					Reason:          fmt.Sprintf("found in %s but version changed: %s → %s", section, a.ExpectedVersion, version),
					SuggestedAction: "revalidate",
				}, nil
			}

			// Check if section changed from what was originally observed
			if oldLocator != nil && oldLocator.JSONPath != "" {
				oldSection := extractSectionFromPath(oldLocator.JSONPath)
				if oldSection != "" && oldSection != section {
					return &Result{
						Status:          "valid",
						ChangeType:      "scope_changed",
						NewLocator:      loc,
						Confidence:      0.90,
						Reason:          fmt.Sprintf("moved from %s to %s", oldSection, section),
						SuggestedAction: "revalidate",
					}, nil
				}
			}

			return &Result{
				Status:          "valid",
				NewLocator:      loc,
				Confidence:      0.95,
				SuggestedAction: "revalidate",
			}, nil
		}
	}

	return &Result{
		Status:          "missing",
		Confidence:      0.95,
		Reason:          fmt.Sprintf("package %q not found in sections: %s", a.Package, strings.Join(sections, ", ")),
		SuggestedAction: "mark_stale",
	}, nil
}

// findJSONKeyLocation does a simple line scan to find where a key appears.
func findJSONKeyLocation(content []byte, section, key string) *Locator {
	lines := strings.Split(string(content), "\n")
	inSection := false
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.Contains(trimmed, fmt.Sprintf("%q", section)) && strings.Contains(trimmed, ":") {
			inSection = true
			continue
		}
		if inSection {
			if trimmed == "}" || trimmed == "}," {
				inSection = false
				continue
			}
			if strings.Contains(trimmed, fmt.Sprintf("%q", key)) {
				return &Locator{
					LineStart: i + 1,
					LineEnd:   i + 1,
					JSONPath:  fmt.Sprintf("/%s/%s", section, strings.ReplaceAll(key, "/", "~1")),
				}
			}
		}
	}
	return nil
}

func extractSectionFromPath(jsonPath string) string {
	// e.g. "/dependencies/@scope~1pkg" → "dependencies"
	parts := strings.SplitN(strings.TrimPrefix(jsonPath, "/"), "/", 2)
	if len(parts) > 0 {
		return parts[0]
	}
	return ""
}
