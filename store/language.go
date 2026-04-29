package store

import (
	"encoding/json"
	"fmt"
	"strings"
)

type DomainTerm struct {
	TermID       int64    `json:"term_id"`
	DomainKey    string   `json:"domain_key"`
	Term         string   `json:"term"`
	Aliases      []string `json:"aliases"`
	AntiPatterns []string `json:"anti_patterns"`
	Description  string   `json:"description"`
	Context      string   `json:"context"`
	Examples     []string `json:"examples"`
	CreatedAt    string   `json:"created_at"`
	UpdatedAt    string   `json:"updated_at"`
}

type LanguageViolation struct {
	NodeKey      string `json:"node_key"`
	NodeName     string `json:"node_name"`
	NodeType     string `json:"node_type"`
	MatchedAnti  string `json:"matched_anti_pattern"`
	SuggestedTerm string `json:"suggested_term"`
	TermContext  string `json:"term_context"`
	Severity     string `json:"severity"` // warning, error
}

func (s *Store) UpsertTerm(t DomainTerm) (int64, error) {
	aliasesJSON, _ := json.Marshal(t.Aliases)
	antiJSON, _ := json.Marshal(t.AntiPatterns)
	examplesJSON, _ := json.Marshal(t.Examples)

	// Try update first
	res, err := s.db.Exec(`
		UPDATE domain_language SET aliases=?, anti_patterns=?, description=?, context=?, examples=?,
		updated_at=strftime('%Y-%m-%dT%H:%M:%SZ','now')
		WHERE domain_key=? AND term=?`,
		string(aliasesJSON), string(antiJSON), t.Description, t.Context, string(examplesJSON),
		t.DomainKey, t.Term,
	)
	if err != nil {
		return 0, fmt.Errorf("UpsertTerm update: %w", err)
	}
	rows, _ := res.RowsAffected()
	if rows > 0 {
		var id int64
		s.db.QueryRow("SELECT term_id FROM domain_language WHERE domain_key=? AND term=?", t.DomainKey, t.Term).Scan(&id)
		return id, nil
	}

	// Insert
	res, err = s.db.Exec(`
		INSERT INTO domain_language (domain_key, term, aliases, anti_patterns, description, context, examples)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		t.DomainKey, t.Term, string(aliasesJSON), string(antiJSON), t.Description, t.Context, string(examplesJSON),
	)
	if err != nil {
		return 0, fmt.Errorf("UpsertTerm insert: %w", err)
	}
	return res.LastInsertId()
}

func (s *Store) GetGlossary(domainKey string) ([]DomainTerm, error) {
	query := "SELECT term_id, domain_key, term, aliases, anti_patterns, description, context, examples, created_at, updated_at FROM domain_language"
	var args []any
	if domainKey != "" {
		query += " WHERE domain_key = ?"
		args = append(args, domainKey)
	}
	query += " ORDER BY context, term"

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("GetGlossary: %w", err)
	}
	defer rows.Close()

	var out []DomainTerm
	for rows.Next() {
		var t DomainTerm
		var aliasesJSON, antiJSON, examplesJSON string
		if err := rows.Scan(&t.TermID, &t.DomainKey, &t.Term, &aliasesJSON, &antiJSON,
			&t.Description, &t.Context, &examplesJSON, &t.CreatedAt, &t.UpdatedAt); err != nil {
			return nil, err
		}
		json.Unmarshal([]byte(aliasesJSON), &t.Aliases)
		json.Unmarshal([]byte(antiJSON), &t.AntiPatterns)
		json.Unmarshal([]byte(examplesJSON), &t.Examples)
		out = append(out, t)
	}
	return out, rows.Err()
}

// CheckLanguage scans all active nodes and reports violations against domain language.
func (s *Store) CheckLanguage(domainKey string) ([]LanguageViolation, error) {
	terms, err := s.GetGlossary(domainKey)
	if err != nil {
		return nil, err
	}
	if len(terms) == 0 {
		return nil, nil
	}

	nodes, err := s.ListNodes(NodeFilter{Domain: domainKey, Status: "active"})
	if err != nil {
		return nil, err
	}

	var violations []LanguageViolation
	for _, node := range nodes {
		nameLower := strings.ToLower(node.Name)
		keyLower := strings.ToLower(node.NodeKey)

		for _, term := range terms {
			for _, anti := range term.AntiPatterns {
				antiLower := strings.ToLower(anti)
				if strings.Contains(nameLower, antiLower) || strings.Contains(keyLower, antiLower) {
					violations = append(violations, LanguageViolation{
						NodeKey:       node.NodeKey,
						NodeName:      node.Name,
						NodeType:      node.NodeType,
						MatchedAnti:   anti,
						SuggestedTerm: term.Term,
						TermContext:   term.Context,
						Severity:      "warning",
					})
				}
			}
		}
	}
	return violations, nil
}

func (s *Store) DeleteTerm(domainKey, term string) error {
	_, err := s.db.Exec(`DELETE FROM domain_language WHERE domain_key = ? AND term = ?`, domainKey, term)
	return err
}

func (s *Store) RemoveAntiPattern(domainKey, term, antiPattern string) error {
	t, err := s.getTermByName(domainKey, term)
	if err != nil {
		return err
	}
	newAnti := []string{}
	for _, a := range t.AntiPatterns {
		if !strings.EqualFold(a, antiPattern) {
			newAnti = append(newAnti, a)
		}
	}
	antiJSON, _ := json.Marshal(newAnti)
	_, err = s.db.Exec(`UPDATE domain_language SET anti_patterns = ?, updated_at = strftime('%Y-%m-%dT%H:%M:%SZ','now') WHERE domain_key = ? AND term = ?`,
		string(antiJSON), domainKey, term)
	return err
}

func (s *Store) getTermByName(domainKey, term string) (*DomainTerm, error) {
	var t DomainTerm
	var aliasesJSON, antiJSON, examplesJSON string
	err := s.db.QueryRow(
		"SELECT term_id, domain_key, term, aliases, anti_patterns, description, context, examples, created_at, updated_at FROM domain_language WHERE domain_key = ? AND term = ?",
		domainKey, term,
	).Scan(&t.TermID, &t.DomainKey, &t.Term, &aliasesJSON, &antiJSON, &t.Description, &t.Context, &examplesJSON, &t.CreatedAt, &t.UpdatedAt)
	if err != nil {
		return nil, fmt.Errorf("term %q not found: %w", term, err)
	}
	json.Unmarshal([]byte(aliasesJSON), &t.Aliases)
	json.Unmarshal([]byte(antiJSON), &t.AntiPatterns)
	json.Unmarshal([]byte(examplesJSON), &t.Examples)
	return &t, nil
}
