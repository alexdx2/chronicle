package validate

import (
	"fmt"
	"strings"
)

// NormalizeName converts any casing convention to lowercase kebab-case.
// PascalCase, camelCase, snake_case, dot.case, UPPER_CASE → kebab-case.
// ArenaController → arena-controller
// arena.controller → arena-controller
// arenaController → arena-controller
// ARENA_CONTROLLER → arena-controller
// my-service → my-service (idempotent)
// IScoreService → i-score-service
func NormalizeName(name string) string {
	if name == "" {
		return ""
	}

	// Split on word boundaries: uppercase transitions, dots, underscores, dashes, spaces
	var words []string
	current := strings.Builder{}

	for i, r := range name {
		switch {
		case r == '.' || r == '_' || r == '-' || r == ' ' || r == '/':
			// Separator — flush current word
			if current.Len() > 0 {
				words = append(words, current.String())
				current.Reset()
			}
		case r >= 'A' && r <= 'Z':
			// Uppercase — check if it's a new word boundary
			if i > 0 && current.Len() > 0 {
				prev := rune(name[i-1])
				// New word if previous was lowercase: "arenaController" → "arena" + "Controller"
				// Or if previous was uppercase and next is lowercase: "IScore" → "I" + "Score"
				if prev >= 'a' && prev <= 'z' {
					words = append(words, current.String())
					current.Reset()
				} else if prev >= 'A' && prev <= 'Z' && i+1 < len(name) && name[i+1] >= 'a' && name[i+1] <= 'z' {
					words = append(words, current.String())
					current.Reset()
				}
			}
			current.WriteRune(r)
		default:
			current.WriteRune(r)
		}
	}
	if current.Len() > 0 {
		words = append(words, current.String())
	}

	// Join with dashes, lowercase
	for i := range words {
		words[i] = strings.ToLower(words[i])
	}
	return strings.Join(words, "-")
}

// NormalizeNodeKey enforces format: layer:type:domain:qualified_name
// Lowercases, trims, strips leading/trailing slashes from qualified_name.
func NormalizeNodeKey(key string) (string, error) {
	key = strings.TrimSpace(key)
	if key == "" {
		return "", fmt.Errorf("node_key is empty")
	}

	parts := strings.SplitN(key, ":", 4)
	if len(parts) < 4 {
		return "", fmt.Errorf("node_key %q must have format layer:type:domain:qualified_name", key)
	}

	layer := strings.ToLower(strings.TrimSpace(parts[0]))
	nodeType := strings.ToLower(strings.TrimSpace(parts[1]))
	domain := strings.ToLower(strings.TrimSpace(parts[2]))
	qualifiedName := strings.TrimSpace(parts[3])

	qualifiedName = strings.Trim(qualifiedName, "/")

	if layer == "" || nodeType == "" || domain == "" || qualifiedName == "" {
		return "", fmt.Errorf("node_key %q has empty component", key)
	}

	// Normalize the qualified_name part — convert PascalCase/camelCase/dots/underscores
	// to kebab-case so ArenaController, arena.controller, arenaController all map to same key.
	// Preserve path separators (/) for file-path-based keys.
	if strings.Contains(qualifiedName, "/") {
		segments := strings.Split(qualifiedName, "/")
		for i, seg := range segments {
			segments[i] = NormalizeName(seg)
		}
		qualifiedName = strings.Join(segments, "/")
	} else {
		qualifiedName = NormalizeName(qualifiedName)
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
