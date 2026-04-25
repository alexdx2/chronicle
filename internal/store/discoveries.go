package store

import "fmt"

type Discovery struct {
	DiscoveryID  int64   `json:"discovery_id"`
	DomainKey    string  `json:"domain_key"`
	Category     string  `json:"category"`     // pattern, correction, insight, unknown_pattern, missing_edge, stale_data
	Title        string  `json:"title"`
	Description  string  `json:"description"`
	Source       string  `json:"source"`        // claude, user, system
	Confidence   float64 `json:"confidence"`
	RelatedNodes string  `json:"related_nodes"` // JSON array of node_keys
	Applied      bool    `json:"applied"`
	CreatedAt    string  `json:"created_at"`
}

func (s *Store) AddDiscovery(d Discovery) (int64, error) {
	if d.RelatedNodes == "" {
		d.RelatedNodes = "[]"
	}
	res, err := s.db.Exec(
		`INSERT INTO graph_discoveries (domain_key, category, title, description, source, confidence, related_nodes, applied)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		d.DomainKey, d.Category, d.Title, d.Description, d.Source, d.Confidence, d.RelatedNodes, d.Applied,
	)
	if err != nil {
		return 0, fmt.Errorf("AddDiscovery: %w", err)
	}
	return res.LastInsertId()
}

func (s *Store) ListDiscoveries(domainKey string, category string) ([]Discovery, error) {
	query := `SELECT discovery_id, domain_key, category, title, description, source, confidence, related_nodes, applied, created_at
		FROM graph_discoveries WHERE 1=1`
	var args []any
	if domainKey != "" {
		query += " AND domain_key = ?"
		args = append(args, domainKey)
	}
	if category != "" {
		query += " AND category = ?"
		args = append(args, category)
	}
	query += " ORDER BY created_at DESC"

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("ListDiscoveries: %w", err)
	}
	defer rows.Close()
	var out []Discovery
	for rows.Next() {
		var d Discovery
		if err := rows.Scan(&d.DiscoveryID, &d.DomainKey, &d.Category, &d.Title, &d.Description,
			&d.Source, &d.Confidence, &d.RelatedNodes, &d.Applied, &d.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

func (s *Store) MarkDiscoveryApplied(id int64) error {
	_, err := s.db.Exec(`UPDATE graph_discoveries SET applied = 1 WHERE discovery_id = ?`, id)
	return err
}
