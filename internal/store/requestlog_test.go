package store

import "testing"

func TestLogRequest(t *testing.T) {
	s := openTestStore(t)
	id, err := s.LogRequest(RequestLogEntry{
		ToolName: "oracle_import_all", ParamsJSON: `{"revision_id": 5}`,
		ResultJSON: `{"nodes_created": 18}`, DurationMs: 142, Summary: "18n 17e 7ev",
	})
	if err != nil {
		t.Fatalf("LogRequest: %v", err)
	}
	if id <= 0 {
		t.Errorf("request_id = %d, want > 0", id)
	}
}

func TestLogRequestError(t *testing.T) {
	s := openTestStore(t)
	id, err := s.LogRequest(RequestLogEntry{
		ToolName: "oracle_query_reverse_deps", ParamsJSON: `{"node_key": "unknown"}`,
		ErrorMessage: `node not found`, DurationMs: 5, Summary: "node not found",
	})
	if err != nil {
		t.Fatalf("LogRequest: %v", err)
	}
	if id <= 0 {
		t.Errorf("request_id = %d, want > 0", id)
	}
}

func TestListRecentRequests(t *testing.T) {
	s := openTestStore(t)
	s.LogRequest(RequestLogEntry{ToolName: "tool_a", ParamsJSON: "{}", DurationMs: 2, Summary: "a"})
	s.LogRequest(RequestLogEntry{ToolName: "tool_b", ParamsJSON: "{}", DurationMs: 1, Summary: "b"})
	s.LogRequest(RequestLogEntry{ToolName: "tool_c", ParamsJSON: "{}", DurationMs: 142, Summary: "c"})

	entries, err := s.ListRecentRequests(10)
	if err != nil {
		t.Fatalf("ListRecentRequests: %v", err)
	}
	if len(entries) != 3 {
		t.Errorf("count = %d, want 3", len(entries))
	}
	if entries[0].ToolName != "tool_c" {
		t.Errorf("first = %q, want tool_c (most recent)", entries[0].ToolName)
	}
}

func TestListRequestsSince(t *testing.T) {
	s := openTestStore(t)
	s.LogRequest(RequestLogEntry{ToolName: "tool_a", ParamsJSON: "{}", DurationMs: 1})
	id2, _ := s.LogRequest(RequestLogEntry{ToolName: "tool_b", ParamsJSON: "{}", DurationMs: 1})
	s.LogRequest(RequestLogEntry{ToolName: "tool_c", ParamsJSON: "{}", DurationMs: 1})

	entries, err := s.ListRequestsSince(id2)
	if err != nil {
		t.Fatalf("ListRequestsSince: %v", err)
	}
	if len(entries) != 1 {
		t.Errorf("count = %d, want 1", len(entries))
	}
	if entries[0].ToolName != "tool_c" {
		t.Errorf("entry = %q, want tool_c", entries[0].ToolName)
	}
}

func TestRequestStats(t *testing.T) {
	s := openTestStore(t)
	s.LogRequest(RequestLogEntry{ToolName: "a", ParamsJSON: "{}", DurationMs: 10})
	s.LogRequest(RequestLogEntry{ToolName: "b", ParamsJSON: "{}", DurationMs: 20, ErrorMessage: "fail"})
	s.LogRequest(RequestLogEntry{ToolName: "c", ParamsJSON: "{}", DurationMs: 30})

	stats, err := s.RequestStats()
	if err != nil {
		t.Fatalf("RequestStats: %v", err)
	}
	if stats.Total != 3 {
		t.Errorf("total = %d, want 3", stats.Total)
	}
	if stats.Errors != 1 {
		t.Errorf("errors = %d, want 1", stats.Errors)
	}
	if stats.AvgDurationMs != 20 {
		t.Errorf("avg = %d, want 20", stats.AvgDurationMs)
	}
}
