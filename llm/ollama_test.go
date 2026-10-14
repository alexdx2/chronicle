package llm

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const ollamaOK = `{"model":"qwen3.5:4b","message":{"role":"assistant","content":"{\"status\":\"extracted\",\"facts\":[]}"},"done":true,"done_reason":"stop","prompt_eval_count":3300,"eval_count":50}`

func TestOllama_Complete(t *testing.T) {
	var body map[string]any
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		_ = json.NewDecoder(r.Body).Decode(&body)
		_, _ = w.Write([]byte(ollamaOK))
	}))
	defer srv.Close()

	b := NewOllama("qwen3.5:4b", srv.URL)
	resp, err := b.Complete(context.Background(), Request{System: "SYS", User: "USER", MaxTokens: 100})
	if err != nil {
		t.Fatal(err)
	}
	if gotPath != "/api/chat" {
		t.Errorf("path=%q", gotPath)
	}
	msgs := body["messages"].([]any)
	opts := body["options"].(map[string]any)
	if body["model"] != "qwen3.5:4b" || body["stream"] != false || body["think"] != false || body["format"] != "json" ||
		msgs[0].(map[string]any)["content"] != "SYS" || msgs[1].(map[string]any)["content"] != "USER" ||
		opts["num_predict"] != float64(100) || opts["temperature"] != float64(0) || opts["num_ctx"] != float64(ollamaNumCtx) {
		t.Errorf("body=%v", body)
	}
	if resp.Text != `{"status":"extracted","facts":[]}` || resp.Usage != (Usage{InputTokens: 3300, OutputTokens: 50}) || resp.Model != "qwen3.5:4b" {
		t.Errorf("resp=%+v", resp)
	}
	if b.Name() != "ollama" || b.Model() != "qwen3.5:4b" {
		t.Errorf("name/model: %s %s", b.Name(), b.Model())
	}
}

func TestOllama_ModelMissingAndTruncated(t *testing.T) {
	t.Run("missing", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":"model 'nope:1b' not found"}`))
		}))
		defer srv.Close()
		_, err := NewOllama("nope:1b", srv.URL).Complete(context.Background(), Request{User: "x"})
		if err == nil || !strings.Contains(err.Error(), "ollama pull nope:1b") {
			t.Fatalf("want pull hint, got %v", err)
		}
	})
	t.Run("truncated", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(`{"message":{"content":"{"},"done":true,"done_reason":"length","prompt_eval_count":1,"eval_count":1}`))
		}))
		defer srv.Close()
		_, err := NewOllama("m", srv.URL).Complete(context.Background(), Request{User: "x"})
		if err == nil || !strings.Contains(err.Error(), "truncated") {
			t.Fatalf("want truncated, got %v", err)
		}
	})
}

func TestOllama_HostNormalisation(t *testing.T) {
	cases := map[string]string{
		"":                        "http://localhost:11434",
		"localhost:11434":         "http://localhost:11434",
		"0.0.0.0:11434":           "http://0.0.0.0:11434",
		"http://mac.local:11434/": "http://mac.local:11434",
		"https://ollama.example":  "https://ollama.example",
	}
	for in, want := range cases {
		if got := ollamaBase(in); got != want {
			t.Errorf("ollamaBase(%q)=%q want %q", in, got, want)
		}
	}
}

// A free-text request (an instruction pack in markdown) must not be forced
// into JSON: with format:json the model wraps the pack in {"content":...} and
// often stops after a title (fleet, 14.10).
func TestOllama_TextRequestHasNoFormat(t *testing.T) {
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&body)
		_, _ = w.Write([]byte(ollamaOK))
	}))
	defer srv.Close()
	if _, err := NewOllama("m", srv.URL).Complete(context.Background(), Request{System: "S", User: "U", Text: true}); err != nil {
		t.Fatal(err)
	}
	if _, has := body["format"]; has {
		t.Fatalf("text request carries format: %v", body)
	}
}
