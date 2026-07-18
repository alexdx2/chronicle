package store

import (
	"fmt"
	"path/filepath"
	"strings"
)

// NodeKeysByFilePaths maps each given file path to the active node keys backed
// by it — either directly (graph_nodes.file_path) or through evidence rows
// citing the file. Read-only; used by the review report to map a git diff onto
// the graph.
func (s *Store) NodeKeysByFilePaths(paths []string) (map[string][]string, error) {
	result := make(map[string][]string, len(paths))
	if len(paths) == 0 {
		return result, nil
	}

	// Some creation paths store node file_path WITHOUT the extension
	// (path-keyed nodes: api/src/services/payment.service) while git diffs
	// carry the full path — query both variants and map hits back to the
	// caller's original path.
	variantOf := map[string]string{} // variant → original path
	var variants []string
	addVariant := func(v, orig string) {
		if v == "" {
			return
		}
		if _, seen := variantOf[v]; !seen {
			variantOf[v] = orig
			variants = append(variants, v)
		}
	}
	for _, p := range paths {
		addVariant(p, p)
		if ext := filepath.Ext(p); ext != "" {
			addVariant(strings.TrimSuffix(p, ext), p)
		}
	}

	placeholders := strings.TrimRight(strings.Repeat("?,", len(variants)), ",")
	args := make([]any, len(variants))
	for i, p := range variants {
		args[i] = p
	}

	// Direct: nodes anchored at the file.
	directQ := `
		SELECT COALESCE(file_path,''), node_key
		FROM graph_nodes
		WHERE file_path IN (` + placeholders + `)
		  AND (valid_to_revision_id IS NULL OR valid_to_revision_id = 0)
		  AND status = 'active'
	`
	rows, err := s.db.Query(directQ, args...)
	if err != nil {
		return nil, fmt.Errorf("NodeKeysByFilePaths direct: %w", err)
	}
	defer rows.Close()

	seen := map[string]map[string]bool{} // path → node_key set
	add := func(path, key string) {
		// Map the matched variant back to the caller's original path.
		if orig, ok := variantOf[path]; ok {
			path = orig
		}
		if seen[path] == nil {
			seen[path] = map[string]bool{}
		}
		if !seen[path][key] {
			seen[path][key] = true
			result[path] = append(result[path], key)
		}
	}
	for rows.Next() {
		var path, key string
		if err := rows.Scan(&path, &key); err != nil {
			return nil, fmt.Errorf("NodeKeysByFilePaths direct scan: %w", err)
		}
		add(path, key)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("NodeKeysByFilePaths direct rows: %w", err)
	}

	// Via evidence: nodes whose evidence cites the file.
	evQ := `
		SELECT DISTINCT e.file_path, n.node_key
		FROM graph_evidence e
		JOIN graph_nodes n ON n.node_id = e.node_id
		WHERE e.file_path IN (` + placeholders + `)
		  AND e.node_id IS NOT NULL
		  AND (n.valid_to_revision_id IS NULL OR n.valid_to_revision_id = 0)
		  AND n.status = 'active'
	`
	evRows, err := s.db.Query(evQ, args...)
	if err != nil {
		return nil, fmt.Errorf("NodeKeysByFilePaths evidence: %w", err)
	}
	defer evRows.Close()
	for evRows.Next() {
		var path, key string
		if err := evRows.Scan(&path, &key); err != nil {
			return nil, fmt.Errorf("NodeKeysByFilePaths evidence scan: %w", err)
		}
		add(path, key)
	}
	if err := evRows.Err(); err != nil {
		return nil, fmt.Errorf("NodeKeysByFilePaths evidence rows: %w", err)
	}

	return result, nil
}
