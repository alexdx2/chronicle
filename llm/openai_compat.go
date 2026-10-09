package llm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
)

const (
	openAIBase     = "https://api.openai.com/v1"
	openRouterBase = "https://openrouter.ai/api/v1"
)

// chat is a Backend over the OpenAI chat-completions wire format, which
// OpenAI, OpenRouter, Groq, DeepSeek, Mistral, Together, Fireworks, LM Studio,
// llama.cpp and vLLM all speak. Ollama has its own backend (ollama.go) because
// its native API is the only place thinking can be switched off.
type chat struct {
	name, model, base, key string
	extra                  map[string]any    // provider-specific body fields
	headers                map[string]string // provider-specific headers
	http                   *http.Client
	sleep                  func(time.Duration) // test hook
}

// NewOpenAI returns a Backend over an OpenAI-compatible server. The default
// base is api.openai.com; WithBaseURL points it at any compatible endpoint.
func NewOpenAI(model, apiKey string) *chat {
	return &chat{name: "openai", model: model, base: openAIBase, key: apiKey,
		http: &http.Client{Timeout: 5 * time.Minute}}
}

// NewOpenRouter returns a Backend over OpenRouter. Reasoning is disabled on
// every request: thinking models otherwise spend the whole output budget on
// reasoning and return an empty message (measured 09.10.2026 with qwen3.5 and
// gpt-5-nano).
func NewOpenRouter(model, apiKey string) *chat {
	c := NewOpenAI(model, apiKey)
	c.name = "openrouter"
	c.base = openRouterBase
	c.extra = map[string]any{"reasoning": map[string]any{"enabled": false}}
	c.headers = map[string]string{
		"HTTP-Referer": "https://github.com/alexdx2/chronicle-core",
		"X-Title":      "Chronicle",
	}
	return c
}

// WithBaseURL overrides the API base URL (the part before /chat/completions).
func (c *chat) WithBaseURL(u string) *chat { c.base = strings.TrimRight(u, "/"); return c }

func (c *chat) Name() string  { return c.name }
func (c *chat) Model() string { return c.model }

func (c *chat) Complete(ctx context.Context, req Request) (Response, error) {
	maxTokens := req.MaxTokens
	if maxTokens == 0 {
		maxTokens = DefaultMaxTokens
	}
	body := map[string]any{
		"model":       c.model,
		"temperature": 0,
		"max_tokens":  maxTokens,
		"messages": []map[string]string{
			{"role": "system", "content": req.System},
			{"role": "user", "content": req.User},
		},
		"response_format": map[string]string{"type": "json_object"},
	}
	for k, v := range c.extra {
		body[k] = v
	}
	headers := map[string]string{"Authorization": "Bearer " + c.key}
	for k, v := range c.headers {
		headers[k] = v
	}
	endpoint := c.base + "/chat/completions"

	raw, status, err := c.post(ctx, endpoint, headers, body)
	if err != nil {
		return Response{}, err
	}
	// Some compatible servers reject response_format; retry once without it.
	if status == http.StatusBadRequest && strings.Contains(string(raw), "response_format") {
		delete(body, "response_format")
		if raw, status, err = c.post(ctx, endpoint, headers, body); err != nil {
			return Response{}, err
		}
	}
	if status < 200 || status > 299 {
		return Response{}, fmt.Errorf("llm: %s: status %d: %s", c.name, status, snippet(raw))
	}
	return c.parse(raw)
}

func (c *chat) post(ctx context.Context, endpoint string, headers map[string]string, body map[string]any) ([]byte, int, error) {
	b, err := json.Marshal(body)
	if err != nil {
		return nil, 0, err
	}
	return postJSON(ctx, c.http, c.sleep, c.name, endpoint, headers, b)
}

func (c *chat) parse(raw []byte) (Response, error) {
	var out struct {
		Model   string `json:"model"`
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
			FinishReason string `json:"finish_reason"`
		} `json:"choices"`
		Usage struct {
			Prompt     int     `json:"prompt_tokens"`
			Completion int     `json:"completion_tokens"`
			Cost       float64 `json:"cost"` // OpenRouter reports USD per call
			Details    struct {
				Cached int `json:"cached_tokens"`
			} `json:"prompt_tokens_details"`
		} `json:"usage"`
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return Response{}, fmt.Errorf("llm: %s: decode response: %w", c.name, err)
	}
	if len(out.Choices) == 0 {
		msg := out.Error.Message
		if msg == "" {
			msg = snippet(raw)
		}
		return Response{}, fmt.Errorf("llm: %s: no choices in response: %s", c.name, msg)
	}
	ch := out.Choices[0]
	if ch.FinishReason == "length" {
		return Response{}, fmt.Errorf("llm: %s: output truncated (max_tokens)", c.name)
	}
	if strings.TrimSpace(ch.Message.Content) == "" {
		return Response{}, errors.New("llm: " + c.name + ": empty message content (reasoning model spent the budget thinking?)")
	}
	model := out.Model
	if model == "" {
		model = c.model
	}
	return Response{
		Text: ch.Message.Content,
		Usage: Usage{
			InputTokens:     out.Usage.Prompt,
			OutputTokens:    out.Usage.Completion,
			CacheReadTokens: out.Usage.Details.Cached,
			CostUSD:         out.Usage.Cost,
		},
		Model: model,
	}, nil
}
