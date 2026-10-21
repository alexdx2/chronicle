package llm

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

const chatOK = `{"id":"x","model":"qwen/qwen3.5-35b-a3b","choices":[{"message":{"role":"assistant","content":"{\"status\":\"extracted\",\"facts\":[]}"},"finish_reason":"stop"}],"usage":{"prompt_tokens":30,"completion_tokens":7,"prompt_tokens_details":{"cached_tokens":4},"cost":0.00012}}`

func TestOpenAI_Complete(t *testing.T) {
	var body map[string]any
	var gotPath, gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotAuth = r.URL.Path, r.Header.Get("Authorization")
		_ = json.NewDecoder(r.Body).Decode(&body)
		_, _ = w.Write([]byte(chatOK))
	}))
	defer srv.Close()

	b := NewOpenAI("m1", "sk").WithBaseURL(srv.URL + "/v1")
	resp, err := b.Complete(context.Background(), Request{System: "SYS", User: "USER", MaxTokens: 100})
	if err != nil {
		t.Fatal(err)
	}
	if gotPath != "/v1/chat/completions" || gotAuth != "Bearer sk" {
		t.Errorf("path=%q auth=%q", gotPath, gotAuth)
	}
	msgs := body["messages"].([]any)
	sys := msgs[0].(map[string]any)
	usr := msgs[1].(map[string]any)
	rf := body["response_format"].(map[string]any)
	if body["model"] != "m1" || sys["role"] != "system" || sys["content"] != "SYS" || usr["content"] != "USER" ||
		body["max_tokens"] != float64(100) || body["temperature"] != float64(0) || rf["type"] != "json_object" {
		t.Errorf("body=%v", body)
	}
	if _, has := body["reasoning"]; has {
		t.Errorf("plain openai must not send reasoning: %v", body)
	}
	want := Usage{InputTokens: 30, OutputTokens: 7, CacheReadTokens: 4, CostUSD: 0.00012}
	if resp.Text != `{"status":"extracted","facts":[]}` || resp.Usage != want || resp.Model != "qwen/qwen3.5-35b-a3b" {
		t.Errorf("resp=%+v", resp)
	}
	if b.Name() != "openai" || b.Model() != "m1" {
		t.Errorf("name/model: %s %s", b.Name(), b.Model())
	}
}

func TestOpenRouter_ReasoningOffAndHeaders(t *testing.T) {
	var body map[string]any
	var referer, title string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		referer, title = r.Header.Get("HTTP-Referer"), r.Header.Get("X-Title")
		_ = json.NewDecoder(r.Body).Decode(&body)
		_, _ = w.Write([]byte(chatOK))
	}))
	defer srv.Close()

	b := NewOpenRouter("qwen/qwen3.5-35b-a3b", "sk").WithBaseURL(srv.URL)
	if _, err := b.Complete(context.Background(), Request{User: "x"}); err != nil {
		t.Fatal(err)
	}
	rs, _ := body["reasoning"].(map[string]any)
	if rs == nil || rs["enabled"] != false {
		t.Errorf("openrouter must disable reasoning: %v", body)
	}
	if referer == "" || title == "" {
		t.Errorf("openrouter attribution headers missing: %q %q", referer, title)
	}
	if b.Name() != "openrouter" {
		t.Errorf("name=%s", b.Name())
	}
}

func TestOpenAI_FallsBackWithoutResponseFormat(t *testing.T) {
	var bodies []map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		bodies = append(bodies, body)
		if _, has := body["response_format"]; has {
			http.Error(w, `{"error":{"message":"response_format is not supported by this model"}}`, http.StatusBadRequest)
			return
		}
		_, _ = w.Write([]byte(chatOK))
	}))
	defer srv.Close()
	b := NewOpenAI("m", "k").WithBaseURL(srv.URL)
	if _, err := b.Complete(context.Background(), Request{User: "x"}); err != nil {
		t.Fatal(err)
	}
	if len(bodies) != 2 {
		t.Fatalf("want 2 requests, got %d", len(bodies))
	}
	if _, has := bodies[1]["response_format"]; has {
		t.Errorf("second request still carries response_format")
	}
}

func TestOpenAI_TruncatedEmptyAndBadStatus(t *testing.T) {
	cases := map[string]struct {
		body   string
		status int
		want   string
	}{
		"truncated": {`{"choices":[{"message":{"content":"{"},"finish_reason":"length"}],"usage":{}}`, 200, "truncated"},
		"empty":     {`{"choices":[{"message":{"content":""},"finish_reason":"stop"}],"usage":{}}`, 200, "empty"},
		"nochoices": {`{"error":{"message":"no endpoints found"}}`, 200, "no choices"},
		"401":       {`{"error":{"message":"bad key"}}`, 401, "status 401"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(c.status)
				_, _ = w.Write([]byte(c.body))
			}))
			defer srv.Close()
			b := NewOpenAI("m", "k").WithBaseURL(srv.URL)
			_, err := b.Complete(context.Background(), Request{User: "x"})
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("want error containing %q, got %v", c.want, err)
			}
		})
	}
}

func TestOpenAI_RetriesOn429(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			http.Error(w, "slow down", http.StatusTooManyRequests)
			return
		}
		_, _ = w.Write([]byte(chatOK))
	}))
	defer srv.Close()
	var slept []time.Duration
	b := NewOpenAI("m", "k").WithBaseURL(srv.URL)
	b.sleep = func(d time.Duration) { slept = append(slept, d) }
	if _, err := b.Complete(context.Background(), Request{User: "x"}); err != nil {
		t.Fatal(err)
	}
	if calls != 2 || len(slept) != 1 || slept[0] <= 0 || slept[0] > 5*time.Second {
		t.Errorf("calls=%d slept=%v", calls, slept)
	}
}

func TestOpenAI_TextRequestHasNoResponseFormat(t *testing.T) {
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&body)
		_, _ = w.Write([]byte(chatOK))
	}))
	defer srv.Close()
	b := NewOpenAI("m1", "sk").WithBaseURL(srv.URL + "/v1")
	if _, err := b.Complete(context.Background(), Request{System: "S", User: "U", Text: true}); err != nil {
		t.Fatal(err)
	}
	if _, has := body["response_format"]; has {
		t.Fatalf("text request forces JSON: %v", body)
	}
}

func TestOpenRouter_MandatoryReasoningRetriesWithLowEffort(t *testing.T) {
	var bodies []map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		bodies = append(bodies, body)
		if rs, _ := body["reasoning"].(map[string]any); rs["enabled"] == false {
			http.Error(w, `{"error":{"message":"Reasoning is mandatory for this endpoint and cannot be disabled.","code":400}}`, http.StatusBadRequest)
			return
		}
		_, _ = w.Write([]byte(chatOK))
	}))
	defer srv.Close()
	b := NewOpenRouter("anthropic/claude-sonnet-5.5", "k").WithBaseURL(srv.URL)
	if _, err := b.Complete(context.Background(), Request{User: "x", MaxTokens: 4000}); err != nil {
		t.Fatal(err)
	}
	if len(bodies) != 2 {
		t.Fatalf("want a retry, got %d requests", len(bodies))
	}
	rs, _ := bodies[1]["reasoning"].(map[string]any)
	if rs["effort"] != "low" || rs["enabled"] != nil || bodies[1]["max_tokens"] != float64(4000+reasoningRoom) {
		t.Errorf("retry body: %v", bodies[1])
	}
	// the next call goes straight to low effort
	if _, err := b.Complete(context.Background(), Request{User: "y", MaxTokens: 4000}); err != nil {
		t.Fatal(err)
	}
	if len(bodies) != 3 {
		t.Fatalf("remembered setting still retried: %d requests", len(bodies))
	}
	// an error that merely mentions another thing is not retried
	srv2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":{"message":"bad key"}}`, http.StatusBadRequest)
	}))
	defer srv2.Close()
	if _, err := NewOpenRouter("m", "k").WithBaseURL(srv2.URL).Complete(context.Background(), Request{User: "x"}); err == nil {
		t.Fatal("other 400s must still fail")
	}
}
