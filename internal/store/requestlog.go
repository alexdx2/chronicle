package store

import "fmt"

type RequestLogEntry struct {
	RequestID    int64  `json:"request_id"`
	Timestamp    string `json:"timestamp"`
	ToolName     string `json:"tool_name"`
	ParamsJSON   string `json:"params_json"`
	ResultJSON   string `json:"result_json,omitempty"`
	ErrorMessage string `json:"error_message,omitempty"`
	DurationMs   int    `json:"duration_ms"`
	Summary      string `json:"summary,omitempty"`
}

type RequestLogStats struct {
	Total         int `json:"total"`
	Errors        int `json:"errors"`
	AvgDurationMs int `json:"avg_duration_ms"`
}

func (s *Store) LogRequest(e RequestLogEntry) (int64, error) {
	res, err := s.db.Exec(
		`INSERT INTO mcp_request_log (tool_name, params_json, result_json, error_message, duration_ms, summary)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		e.ToolName, e.ParamsJSON, e.ResultJSON, e.ErrorMessage, e.DurationMs, e.Summary,
	)
	if err != nil {
		return 0, fmt.Errorf("LogRequest: %w", err)
	}
	return res.LastInsertId()
}

func (s *Store) ListRecentRequests(limit int) ([]RequestLogEntry, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.db.Query(
		`SELECT request_id, timestamp, tool_name, params_json,
		 COALESCE(result_json,''), COALESCE(error_message,''), duration_ms, COALESCE(summary,'')
		 FROM mcp_request_log ORDER BY request_id DESC LIMIT ?`, limit,
	)
	if err != nil {
		return nil, fmt.Errorf("ListRecentRequests: %w", err)
	}
	defer rows.Close()
	var out []RequestLogEntry
	for rows.Next() {
		var e RequestLogEntry
		if err := rows.Scan(&e.RequestID, &e.Timestamp, &e.ToolName, &e.ParamsJSON,
			&e.ResultJSON, &e.ErrorMessage, &e.DurationMs, &e.Summary); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func (s *Store) ListRequestsSince(afterID int64) ([]RequestLogEntry, error) {
	rows, err := s.db.Query(
		`SELECT request_id, timestamp, tool_name, params_json,
		 COALESCE(result_json,''), COALESCE(error_message,''), duration_ms, COALESCE(summary,'')
		 FROM mcp_request_log WHERE request_id > ? ORDER BY request_id ASC`, afterID,
	)
	if err != nil {
		return nil, fmt.Errorf("ListRequestsSince: %w", err)
	}
	defer rows.Close()
	var out []RequestLogEntry
	for rows.Next() {
		var e RequestLogEntry
		if err := rows.Scan(&e.RequestID, &e.Timestamp, &e.ToolName, &e.ParamsJSON,
			&e.ResultJSON, &e.ErrorMessage, &e.DurationMs, &e.Summary); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func (s *Store) RequestStats() (*RequestLogStats, error) {
	var stats RequestLogStats
	err := s.db.QueryRow(
		`SELECT COUNT(*), COUNT(CASE WHEN error_message != '' AND error_message IS NOT NULL THEN 1 END),
		 COALESCE(AVG(duration_ms), 0) FROM mcp_request_log`,
	).Scan(&stats.Total, &stats.Errors, &stats.AvgDurationMs)
	if err != nil {
		return nil, fmt.Errorf("RequestStats: %w", err)
	}
	return &stats, nil
}
