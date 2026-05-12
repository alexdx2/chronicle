package store

import "fmt"

// GetDomains returns distinct domain keys for active, current nodes, ordered alphabetically.
func (s *Store) GetDomains() ([]string, error) {
	const q = `SELECT DISTINCT domain_key FROM graph_nodes
		WHERE domain_key != '' AND status = 'active'
		AND (valid_to_revision_id IS NULL OR valid_to_revision_id = 0)
		ORDER BY domain_key`
	rows, err := s.db.Query(q)
	if err != nil {
		return nil, fmt.Errorf("GetDomains: %w", err)
	}
	defer rows.Close()

	var domains []string
	for rows.Next() {
		var d string
		if err := rows.Scan(&d); err != nil {
			return nil, fmt.Errorf("GetDomains scan: %w", err)
		}
		domains = append(domains, d)
	}
	return domains, rows.Err()
}

// GetCrossDomainEdges returns current edges where the from-node belongs to domainA
// and the to-node belongs to domainB (both nodes must be active and current).
func (s *Store) GetCrossDomainEdges(domainA, domainB string) ([]EdgeRow, error) {
	const q = `
		SELECT e.edge_id, e.from_node_id, e.to_node_id, e.edge_type,
		       e.derivation_kind, e.confidence, e.freshness, e.trust_score
		FROM graph_edges e
		JOIN graph_nodes nf ON e.from_node_id = nf.node_id
		JOIN graph_nodes nt ON e.to_node_id = nt.node_id
		WHERE nf.domain_key = ? AND nt.domain_key = ?
		AND nf.status = 'active' AND nt.status = 'active'
		AND (nf.valid_to_revision_id IS NULL OR nf.valid_to_revision_id = 0)
		AND (nt.valid_to_revision_id IS NULL OR nt.valid_to_revision_id = 0)
	`
	rows, err := s.db.Query(q, domainA, domainB)
	if err != nil {
		return nil, fmt.Errorf("GetCrossDomainEdges: %w", err)
	}
	defer rows.Close()

	var edges []EdgeRow
	for rows.Next() {
		var e EdgeRow
		if err := rows.Scan(&e.EdgeID, &e.FromNodeID, &e.ToNodeID, &e.EdgeType,
			&e.DerivationKind, &e.Confidence, &e.Freshness, &e.TrustScore); err != nil {
			return nil, fmt.Errorf("GetCrossDomainEdges scan: %w", err)
		}
		edges = append(edges, e)
	}
	return edges, rows.Err()
}
