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
// S3Client → s3-client (digit→upper is a word boundary)
//
// Word boundaries: a separator (. _ - space /), lower→upper, digit→upper, and
// the last uppercase of an acronym run before a lowercase. The digit→upper
// boundary is not cosmetic — graph.normalizePascalCase splits there when it
// derives the resolver's key segment from a class name, and the file stem the
// same class lives in carries it too ("s3.client.ts"). Without it "S3Client"
// gets two canonical spellings, "s3-client" from the resolver and "s3client"
// from a key reference, which is exactly the defect SQ-Contract 3 forbids.
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
				// Or a digit: "S3Client" → "s3" + "Client", "V2Service" → "v2" + "Service"
				// Or if previous was uppercase and next is lowercase: "IScore" → "I" + "Score"
				if (prev >= 'a' && prev <= 'z') || (prev >= '0' && prev <= '9') {
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

// routeQualifiedName splits a method-prefixed route qualified_name into its
// prefix and rooted path: "get:/orders/[orderId]" → ("get:", "/orders/[orderId]").
// ok is false for anything else, including bare paths ("/a/b") and plain
// identifiers — only the "<word>:/..." shape the endpoint/flow emitters mint
// is treated as a route.
func routeQualifiedName(qn string) (prefix, path string, ok bool) {
	i := strings.Index(qn, ":")
	if i <= 0 {
		return "", "", false
	}
	for _, r := range qn[:i] {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_', r == '-':
		default:
			return "", "", false
		}
	}
	if !strings.HasPrefix(qn[i+1:], "/") {
		return "", "", false
	}
	return qn[:i+1], qn[i+1:], true
}

// NormalizeQualifiedName applies rule 2 of the canonical key rule (see
// NormalizeNodeKey) to a bare qualified name: split on "/", each segment
// through NormalizeName, rejoined. "/" is a real separator (file paths, scoped
// packages) and survives; "@" passes through, so "@okeep/ui" is a fixed point.
//
// Exported because emitters need to canonicalize a NAME before it becomes a key
// segment (graph.normalizePackageName). A second, private rule there is what
// produced "scoreboardapi" for a service the key rule spells "scoreboard-api".
func NormalizeQualifiedName(qualifiedName string) string {
	qualifiedName = strings.Trim(strings.TrimSpace(qualifiedName), "/")
	if !strings.Contains(qualifiedName, "/") {
		return NormalizeName(qualifiedName)
	}
	segments := strings.Split(qualifiedName, "/")
	for i, seg := range segments {
		segments[i] = NormalizeName(seg)
	}
	return strings.Join(segments, "/")
}

// NormalizeNodeKey enforces format: layer:type:domain:qualified_name.
//
// # The canonical key rule (SQ-Contract 3)
//
// There is ONE spelling per key, and both writers agree on it: the import_all
// path (graph.UpsertNode/UpsertEdge) and the resolve pipeline
// (graph.ensureNodeID/ensureNode) both send every key through this function,
// and every key either of them mints is a FIXED POINT of it — normalizing a
// second time changes nothing. Anything else means two canonical forms for one
// entity, and honest references get rejected.
//
// layer, type and domain are trimmed and lowercased. qualified_name is
// normalized by shape:
//
//  1. Route shape — "<word>:/..." (a method prefix followed by a rooted path,
//     e.g. "get:/invoices/[invoiceId]/lines"): lowercased VERBATIM, structure
//     preserved. Punctuation is data in a route: "[invoiceId]", "{orderId}"
//     and ":orderId" are each ONE path segment, and kebab-inserting a dash
//     ("[invoice-id]") invents a route that exists nowhere in the source.
//     Trailing slashes are trimmed; a root path stays "/".
//  2. Everything else: split on "/", each segment through NormalizeName (any
//     casing convention → lowercase kebab), rejoined. This is what collapses
//     the several spellings of one identity onto one key — "OrdersService",
//     "orders.service" and "orders_service" all key as "orders-service", which
//     is exactly how a class-name reference finds the node minted from
//     orders.service.ts. "@" passes through, so scoped package keys
//     ("@okeep/ui") are fixed points; a leading/trailing "/" is stripped.
//     A digit followed by an uppercase is a word boundary like any other, so
//     "S3Client" keys as "s3-client" — the same split graph.normalizePascalCase
//     makes and the same one the file stem "s3.client.ts" carries. Two sides
//     splitting differently is precisely the two-forms defect.
//
// Consequence worth naming: inside a bare identifier a dot is a word
// separator, not part of the name — the npm package "socket.io" keys as
// "socket-io". At this layer a bare "socket.io" is indistinguishable from the
// file stem "arena.service", and preserving dots for one would break the
// PascalCase↔dot-case convergence that rule 2 exists for. The true specifier
// survives on the node's name and aliases, so only the key spelling is lossy.
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

	// Rule 1: route-shaped qualified names keep their structure.
	if prefix, path, ok := routeQualifiedName(qualifiedName); ok && layer != "" && nodeType != "" && domain != "" {
		path = strings.TrimRight(path, "/")
		if path == "" {
			path = "/"
		}
		return layer + ":" + nodeType + ":" + domain + ":" + strings.ToLower(prefix+path), nil
	}

	qualifiedName = strings.Trim(qualifiedName, "/")

	if layer == "" || nodeType == "" || domain == "" || qualifiedName == "" {
		return "", fmt.Errorf("node_key %q has empty component", key)
	}

	// Rule 2: convert PascalCase/camelCase/dots/underscores to kebab-case so
	// ArenaController, arena.controller, arenaController all map to the same key.
	// Preserve path separators (/) for file-path-based and scoped-package keys.
	qualifiedName = NormalizeQualifiedName(qualifiedName)

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
