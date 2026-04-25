package store

import "fmt"

type ScanMetrics struct {
	TotalMCPCalls     int     `json:"total_mcp_calls"`
	TotalParamsBytes  int64   `json:"total_params_bytes"`
	TotalResultBytes  int64   `json:"total_result_bytes"`
	TotalDurationMs   int64   `json:"total_duration_ms"`
	AvgPayloadBytes   int     `json:"avg_payload_bytes"`
	LargestPayload    int     `json:"largest_payload_bytes"`
	ImportCalls       int     `json:"import_calls"`
	ImportAvgBytes    int     `json:"import_avg_bytes"`
	ImportLargestBytes int    `json:"import_largest_bytes"`
	Errors            int     `json:"errors"`
	ByTool            []ToolMetric `json:"by_tool"`
}

type ToolMetric struct {
	ToolName       string `json:"tool_name"`
	Calls          int    `json:"calls"`
	AvgDurationMs  int    `json:"avg_duration_ms"`
	TotalBytes     int64  `json:"total_bytes"`
	AvgPayloadBytes int   `json:"avg_payload_bytes"`
	Errors         int    `json:"errors"`
}

func (s *Store) GetScanMetrics() (*ScanMetrics, error) {
	m := &ScanMetrics{}

	// Overall stats
	err := s.db.QueryRow(`
		SELECT COUNT(*),
			COALESCE(SUM(LENGTH(params_json)), 0),
			COALESCE(SUM(LENGTH(COALESCE(result_json,''))), 0),
			COALESCE(SUM(duration_ms), 0),
			COALESCE(AVG(LENGTH(params_json)), 0),
			COALESCE(MAX(LENGTH(params_json)), 0),
			COUNT(CASE WHEN error_message != '' AND error_message IS NOT NULL THEN 1 END)
		FROM mcp_request_log
	`).Scan(&m.TotalMCPCalls, &m.TotalParamsBytes, &m.TotalResultBytes,
		&m.TotalDurationMs, &m.AvgPayloadBytes, &m.LargestPayload, &m.Errors)
	if err != nil {
		return nil, fmt.Errorf("GetScanMetrics: %w", err)
	}

	// Import-specific stats
	s.db.QueryRow(`
		SELECT COUNT(*), COALESCE(AVG(LENGTH(params_json)), 0), COALESCE(MAX(LENGTH(params_json)), 0)
		FROM mcp_request_log WHERE tool_name = 'oracle_import_all'
	`).Scan(&m.ImportCalls, &m.ImportAvgBytes, &m.ImportLargestBytes)

	// By tool
	rows, err := s.db.Query(`
		SELECT tool_name, COUNT(*), COALESCE(AVG(duration_ms), 0),
			COALESCE(SUM(LENGTH(params_json)), 0),
			COALESCE(AVG(LENGTH(params_json)), 0),
			COUNT(CASE WHEN error_message != '' AND error_message IS NOT NULL THEN 1 END)
		FROM mcp_request_log
		GROUP BY tool_name
		ORDER BY SUM(LENGTH(params_json)) DESC
	`)
	if err != nil {
		return m, nil // non-fatal
	}
	defer rows.Close()
	for rows.Next() {
		var t ToolMetric
		rows.Scan(&t.ToolName, &t.Calls, &t.AvgDurationMs, &t.TotalBytes, &t.AvgPayloadBytes, &t.Errors)
		m.ByTool = append(m.ByTool, t)
	}

	return m, nil
}
