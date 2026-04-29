package validate

import (
	"fmt"
	"strings"
)

// NormalizeNodeKey enforces format: layer:type:domain:qualified_name
// Lowercases, trims, strips leading/trailing slashes from qualified_name.
func NormalizeNodeKey(key string) (string, error) {
	key = strings.TrimSpace(key)
	if key == "" {
		return "", fmt.Errorf("node_key is empty")
	}
	key = strings.ToLower(key)

	parts := strings.SplitN(key, ":", 4)
	if len(parts) < 4 {
		return "", fmt.Errorf("node_key %q must have format layer:type:domain:qualified_name", key)
	}

	layer := strings.TrimSpace(parts[0])
	nodeType := strings.TrimSpace(parts[1])
	domain := strings.TrimSpace(parts[2])
	qualifiedName := strings.TrimSpace(parts[3])

	qualifiedName = strings.Trim(qualifiedName, "/")

	if layer == "" || nodeType == "" || domain == "" || qualifiedName == "" {
		return "", fmt.Errorf("node_key %q has empty component", key)
	}

	return layer + ":" + nodeType + ":" + domain + ":" + qualifiedName, nil
}

// BuildEdgeKey constructs edge_key from from_node_key, to_node_key, edge_type.
func BuildEdgeKey(fromNodeKey, toNodeKey, edgeType string) string {
	return fromNodeKey + "->" + toNodeKey + ":" + edgeType
}

// NormalizeEdgeKey normalizes an edge_key.
// Format: {from_node_key}->{to_node_key}:{EDGE_TYPE}
func NormalizeEdgeKey(key string) (string, error) {
	key = strings.TrimSpace(key)
	if key == "" {
		return "", fmt.Errorf("edge_key is empty")
	}

	arrowIdx := strings.Index(key, "->")
	if arrowIdx < 0 {
		return "", fmt.Errorf("edge_key %q missing '->' separator", key)
	}

	fromRaw := key[:arrowIdx]
	rest := key[arrowIdx+2:]

	lastColon := strings.LastIndex(rest, ":")
	if lastColon < 0 {
		return "", fmt.Errorf("edge_key %q missing edge_type after to_node_key", key)
	}

	toRaw := rest[:lastColon]
	edgeType := rest[lastColon+1:]

	fromNorm, err := NormalizeNodeKey(fromRaw)
	if err != nil {
		return "", fmt.Errorf("edge_key from part: %w", err)
	}
	toNorm, err := NormalizeNodeKey(toRaw)
	if err != nil {
		return "", fmt.Errorf("edge_key to part: %w", err)
	}

	if edgeType == "" {
		return "", fmt.Errorf("edge_key %q has empty edge_type", key)
	}

	return BuildEdgeKey(fromNorm, toNorm, edgeType), nil
}
