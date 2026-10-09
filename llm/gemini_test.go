package llm

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

const geminiOK = `{"candidates":[{"content":{"parts":[{"text":"{\"status\":\"extracted\",\"facts\":[]}"}]}}],"usageMetadata":{"promptTokenCount":20,"candidatesTokenCount":5,"cachedContentTokenCount":0}}`

func TestGemini_Complete(t *testing.T) {
	var body map[string]any
	var gotPath, gotKey string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotKey = r.URL.Path, r.URL.Query().Get("key")
		_ = json.NewDecoder(r.Body).Decode(&body)
		_, _ = w.Write([]byte(geminiOK))
	}))
	defer srv.Close()

	b := NewGemini("gemini-2.5-flash", "gk").(*gemini).WithBaseURL(srv.URL)
	resp, err := b.Complete(context.Background(), Request{System: "SYS", User: "USER", MaxTokens: 100})
	if err != nil {
		t.Fatal(err)
	}
	if gotPath != "/v1beta/models/gemini-2.5-flash:generateContent" || gotKey != "gk" {
		t.Errorf("path=%q key=%q", gotPath, gotKey)
	}
	si := body["systemInstruction"].(map[string]any)["parts"].([]any)[0].(map[string]any)["text"]
	ct := body["contents"].([]any)[0].(map[string]any)["parts"].([]any)[0].(map[string]any)["text"]
	gc := body["generationConfig"].(map[string]any)
	if si != "SYS" || ct != "USER" || gc["responseMimeType"] != "application/json" || gc["maxOutputTokens"] != float64(100) {
		t.Errorf("body=%v", body)
	}
	if resp.Text != `{"status":"extracted","facts":[]}` || resp.Usage != (Usage{InputTokens: 20, OutputTokens: 5}) || resp.Model != "gemini-2.5-flash" {
		t.Errorf("resp=%+v", resp)
	}
}

func TestGemini_RetriesOn429(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			http.Error(w, "slow down", http.StatusTooManyRequests)
			return
		}
		_, _ = w.Write([]byte(geminiOK))
	}))
	defer srv.Close()
	var slept []time.Duration
	b := NewGemini("m", "k").(*gemini).WithBaseURL(srv.URL)
	b.sleep = func(d time.Duration) { slept = append(slept, d) }
	if _, err := b.Complete(context.Background(), Request{User: "x"}); err != nil {
		t.Fatal(err)
	}
	if calls != 2 || len(slept) != 1 || slept[0] != time.Second {
		t.Errorf("calls=%d slept=%v", calls, slept)
	}
}

func TestGemini_GivesUpAfterThreeAttempts(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer srv.Close()
	var slept []time.Duration
	b := NewGemini("m", "k").(*gemini).WithBaseURL(srv.URL)
	b.sleep = func(d time.Duration) { slept = append(slept, d) }
	_, err := b.Complete(context.Background(), Request{User: "x"})
	if err == nil || !strings.Contains(err.Error(), "500") {
		t.Fatalf("err=%v", err)
	}
	if calls != 3 || len(slept) != 2 || slept[1] != 3*time.Second {
		t.Errorf("calls=%d slept=%v", calls, slept)
	}
}

func TestGemini_NoRetryOn400(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		http.Error(w, "bad request", http.StatusBadRequest)
	}))
	defer srv.Close()
	b := NewGemini("m", "k").(*gemini).WithBaseURL(srv.URL)
	b.sleep = func(time.Duration) {}
	_, err := b.Complete(context.Background(), Request{User: "x"})
	if err == nil || !strings.Contains(err.Error(), "400") || calls != 1 {
		t.Fatalf("err=%v calls=%d", err, calls)
	}
}

func geminiServe(t *testing.T, resp string) *gemini {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(resp)) }))
	t.Cleanup(srv.Close)
	return NewGemini("m", "k").(*gemini).WithBaseURL(srv.URL)
}

func TestGemini_ResponseProblems(t *testing.T) {
	cases := map[string]struct{ body, want string }{
		"blocked":   {`{"promptFeedback":{"blockReason":"PROHIBITED_CONTENT"}}`, "prompt blocked: PROHIBITED_CONTENT"},
		"safety":    {`{"candidates":[{"finishReason":"SAFETY","content":{"parts":[]}}]}`, "SAFETY"},
		"truncated": {`{"candidates":[{"finishReason":"MAX_TOKENS","content":{"parts":[{"text":"{\"a\""}]}}]}`, "output truncated (MAX_TOKENS)"},
		"empty":     {`{"candidates":[{"finishReason":"STOP","content":{"parts":[]}}]}`, "STOP"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := geminiServe(t, c.body).Complete(context.Background(), Request{User: "x"})
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("err=%v want %q", err, c.want)
			}
		})
	}
}

func TestGemini_TransportErrorDoesNotLeakKey(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	base := srv.URL
	srv.Close() // unreachable
	b := NewGemini("m", "SECRET-KEY").(*gemini).WithBaseURL(base)
	b.sleep = func(time.Duration) {}
	_, err := b.Complete(context.Background(), Request{User: "x"})
	if err == nil {
		t.Fatal("want error")
	}
	if strings.Contains(err.Error(), "SECRET-KEY") || strings.Contains(err.Error(), "key=") {
		t.Fatalf("key leaked: %v", err)
	}
}

func TestGemini_BackoffHonoursCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		http.Error(w, "slow", http.StatusTooManyRequests)
	}))
	defer srv.Close()
	b := NewGemini("m", "k").(*gemini).WithBaseURL(srv.URL)
	b.sleep = func(time.Duration) { cancel() }
	_, err := b.Complete(ctx, Request{User: "x"})
	if !errors.Is(err, context.Canceled) || calls != 1 {
		t.Fatalf("err=%v calls=%d", err, calls)
	}
}
