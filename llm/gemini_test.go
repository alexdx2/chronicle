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
