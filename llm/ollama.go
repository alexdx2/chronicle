package llm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

// ollamaNumCtx is the context window requested per call. The system prompt
// is ~3k tokens; the rest is the file. Ollama's own default (4096) silently
// truncates the prompt from the front, which drops the fact schema.
const ollamaNumCtx = 16384

// ollamaCtxEnv overrides the window (tokens) for machines with memory to spare.
const ollamaCtxEnv = "CHRONICLE_OLLAMA_NUM_CTX"

func ollamaWindow() int {
	if v, err := strconv.Atoi(strings.TrimSpace(os.Getenv(ollamaCtxEnv))); err == nil && v >= 4096 {
		return v
	}
	return ollamaNumCtx
}

// ollama is a Backend over Ollama's native /api/chat. The native API is used
// instead of the OpenAI-compatible /v1 because only it accepts think:false;
// with thinking on, qwen3.x returns an empty message (measured 09.10.2026).
type ollama struct {
	model, base string
	http        *http.Client
	sleep       func(time.Duration)
}

// NewOllama returns a Backend over an Ollama server. host accepts the forms
// OLLAMA_HOST uses ("", "host:port", "http://host:port").
func NewOllama(model, host string) *ollama {
	// Local models load on first call and large files decode slowly on CPU.
	return &ollama{model: model, base: ollamaBase(host), http: &http.Client{Timeout: 15 * time.Minute}}
}

// ollamaBase normalises an OLLAMA_HOST-style value into a base URL.
func ollamaBase(host string) string {
	host = strings.TrimSpace(host)
	if host == "" {
		return "http://localhost:11434"
	}
	if !strings.Contains(host, "://") {
		host = "http://" + host
	}
	return strings.TrimRight(host, "/")
}

func (o *ollama) Name() string  { return "ollama" }
func (o *ollama) Model() string { return o.model }

// ContextWindow is the hard per-call token window: the prompt and the answer
// share it, and Ollama truncates an overlong prompt from the front.
func (o *ollama) ContextWindow() int { return ollamaWindow() }

func (o *ollama) Complete(ctx context.Context, req Request) (Response, error) {
	maxTokens := req.MaxTokens
	if maxTokens == 0 {
		maxTokens = DefaultMaxTokens
	}
	payload := map[string]any{
		"model":  o.model,
		"stream": false,
		"think":  false,
		"options": map[string]any{
			"temperature": 0,
			"num_predict": maxTokens,
			"num_ctx":     ollamaWindow(),
		},
		"messages": []map[string]string{
			{"role": "system", "content": req.System},
			{"role": "user", "content": req.User},
		},
	}
	if !req.Text {
		payload["format"] = "json"
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return Response{}, err
	}
	raw, status, err := postJSON(ctx, o.http, o.sleep, "ollama", o.base+"/api/chat", nil, body)
	if err != nil {
		return Response{}, err
	}
	if status == http.StatusNotFound {
		return Response{}, fmt.Errorf("llm: ollama: model %q is not available on %s (run `ollama pull %s`): %s", o.model, o.base, o.model, snippet(raw))
	}
	if status < 200 || status > 299 {
		return Response{}, fmt.Errorf("llm: ollama: status %d: %s", status, snippet(raw))
	}
	var out struct {
		Model   string `json:"model"`
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
		DoneReason string `json:"done_reason"`
		PromptEval int    `json:"prompt_eval_count"`
		Eval       int    `json:"eval_count"`
		Error      string `json:"error"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return Response{}, fmt.Errorf("llm: ollama: decode response: %w", err)
	}
	if out.Error != "" {
		return Response{}, fmt.Errorf("llm: ollama: %s", out.Error)
	}
	if out.DoneReason == "length" {
		return Response{}, fmt.Errorf("llm: ollama: %w (num_predict)", ErrTruncated)
	}
	if strings.TrimSpace(out.Message.Content) == "" {
		return Response{}, errors.New("llm: ollama: empty message content")
	}
	model := out.Model
	if model == "" {
		model = o.model
	}
	return Response{
		Text:  out.Message.Content,
		Usage: Usage{InputTokens: out.PromptEval, OutputTokens: out.Eval},
		Model: model,
	}, nil
}
