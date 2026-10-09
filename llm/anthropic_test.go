package llm

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/anthropics/anthropic-sdk-go/option"
)

func TestAnthropic_Complete(t *testing.T) {
	var gotPath, gotKey string
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotKey = r.URL.Path, r.Header.Get("x-api-key")
		_ = json.NewDecoder(r.Body).Decode(&body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"msg","type":"message","role":"assistant","model":"claude-haiku-5-5","content":[{"type":"text","text":"{\"status\":\"extracted\",\"facts\":[]}"}],"stop_reason":"end_turn","usage":{"input_tokens":12,"output_tokens":7,"cache_read_input_tokens":3,"cache_creation_input_tokens":0}}`))
	}))
	defer srv.Close()

	b := NewAnthropic("claude-haiku-5-5", "k-123", option.WithBaseURL(srv.URL))
	if b.Name() != "anthropic" || b.Model() != "claude-haiku-5-5" {
		t.Fatalf("identity: %s %s", b.Name(), b.Model())
	}
	resp, err := b.Complete(context.Background(), Request{System: "SYS", User: "USER"})
	if err != nil {
		t.Fatal(err)
	}
	if gotPath != "/v1/messages" || gotKey != "k-123" {
		t.Errorf("path=%q key=%q", gotPath, gotKey)
	}
	if body["model"] != "claude-haiku-5-5" {
		t.Errorf("model=%v", body["model"])
	}
	if body["max_tokens"] != float64(DefaultMaxTokens) {
		t.Errorf("max_tokens=%v", body["max_tokens"])
	}
	sys := body["system"].([]any)[0].(map[string]any)
	if sys["text"] != "SYS" {
		t.Errorf("system=%v", sys)
	}
	msg := body["messages"].([]any)[0].(map[string]any)
	if msg["content"].([]any)[0].(map[string]any)["text"] != "USER" {
		t.Errorf("messages=%v", msg)
	}
	if resp.Text != `{"status":"extracted","facts":[]}` {
		t.Errorf("text=%q", resp.Text)
	}
	if resp.Usage != (Usage{12, 7, 3, 0}) {
		t.Errorf("usage=%+v", resp.Usage)
	}
	if resp.Model != "claude-haiku-5-5" {
		t.Errorf("model=%q", resp.Model)
	}
}

func TestAnthropic_Refusal(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"msg","type":"message","role":"assistant","model":"m","content":[],"stop_reason":"refusal","stop_details":{"type":"refusal","category":"cyber"},"usage":{"input_tokens":1,"output_tokens":0}}`))
	}))
	defer srv.Close()
	_, err := NewAnthropic("m", "k", option.WithBaseURL(srv.URL)).Complete(context.Background(), Request{User: "x"})
	if err == nil || !strings.Contains(err.Error(), "refus") || !strings.Contains(err.Error(), "cyber") {
		t.Fatalf("want refusal error naming category, got %v", err)
	}
}
