package verify

import (
	"encoding/json"
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

// YAMLVerifier checks existence of keys/values in YAML files.
type YAMLVerifier struct{}

func (v *YAMLVerifier) Kind() string { return "yaml_key_exists" }

// YAMLAssertion describes an expected YAML key path.
type YAMLAssertion struct {
	// Path is a dot-separated key path, e.g. "services.order-api.image"
	Path string `json:"path"`
	// ExpectedValue is optional — if set, checks the value matches.
	ExpectedValue *string `json:"expected_value,omitempty"`
}

func (v *YAMLVerifier) Verify(fileContent []byte, assertion json.RawMessage, oldLocator *Locator) (*Result, error) {
	var a YAMLAssertion
	if err := json.Unmarshal(assertion, &a); err != nil {
		return nil, fmt.Errorf("invalid yaml_key_exists assertion: %w", err)
	}
	if a.Path == "" {
		return nil, fmt.Errorf("yaml_key_exists assertion missing 'path' field")
	}

	// Parse YAML
	var doc yaml.Node
	if err := yaml.Unmarshal(fileContent, &doc); err != nil {
		return &Result{
			Status:          "missing",
			Confidence:      0.85,
			Reason:          "file is not valid YAML: " + err.Error(),
			SuggestedAction: "needs_claude",
		}, nil
	}

	// Navigate the path
	parts := strings.Split(a.Path, ".")
	node, line := navigateYAMLPath(&doc, parts)

	if node == nil {
		return &Result{
			Status:          "missing",
			Confidence:      0.90,
			Reason:          fmt.Sprintf("key path %q not found", a.Path),
			SuggestedAction: "mark_stale",
		}, nil
	}

	// Check value if expected
	if a.ExpectedValue != nil {
		actualValue := node.Value
		if actualValue != *a.ExpectedValue {
			return &Result{
				Status:          "valid",
				ChangeType:      "value_changed",
				NewLocator:      &Locator{LineStart: line, LineEnd: line},
				Confidence:      0.85,
				Reason:          fmt.Sprintf("key exists but value changed: %q → %q", *a.ExpectedValue, actualValue),
				SuggestedAction: "revalidate",
			}, nil
		}
	}

	return &Result{
		Status:          "valid",
		NewLocator:      &Locator{LineStart: line, LineEnd: line},
		Confidence:      0.90,
		SuggestedAction: "revalidate",
	}, nil
}

// navigateYAMLPath walks a yaml.Node tree following the dot-separated path.
func navigateYAMLPath(node *yaml.Node, parts []string) (*yaml.Node, int) {
	if node == nil || len(parts) == 0 {
		return node, node.Line
	}

	// yaml.Unmarshal wraps in a document node
	if node.Kind == yaml.DocumentNode && len(node.Content) > 0 {
		return navigateYAMLPath(node.Content[0], parts)
	}

	if node.Kind != yaml.MappingNode {
		return nil, 0
	}

	target := parts[0]
	remaining := parts[1:]

	// MappingNode content is [key, value, key, value, ...]
	for i := 0; i+1 < len(node.Content); i += 2 {
		keyNode := node.Content[i]
		valueNode := node.Content[i+1]

		if keyNode.Value == target {
			if len(remaining) == 0 {
				return valueNode, valueNode.Line
			}
			return navigateYAMLPath(valueNode, remaining)
		}
	}

	return nil, 0
}
