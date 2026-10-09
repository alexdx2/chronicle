package llm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const geminiDefaultBase = "https://generativelanguage.googleapis.com"

type gemini struct {
	model, apiKey, base string
	http                *http.Client
	sleep               func(time.Duration) // test hook; nil means a context-aware timer
}

// stripURL removes the request URL (which carries the API key) from transport errors.
func stripURL(err error) error {
	var ue *url.Error
	if errors.As(err, &ue) {
		return ue.Err
	}
	return err
}

// NewGemini returns a Backend over the Gemini generateContent REST API.
func NewGemini(model, apiKey string) Backend {
	return &gemini{model: model, apiKey: apiKey, base: geminiDefaultBase,
		http: &http.Client{Timeout: 5 * time.Minute}}
}

// WithBaseURL overrides the API base URL (tests).
func (g *gemini) WithBaseURL(u string) *gemini { g.base = strings.TrimRight(u, "/"); return g }

func (g *gemini) Name() string  { return "gemini" }
func (g *gemini) Model() string { return g.model }

type gemPart struct {
	Text string `json:"text"`
}
type gemContent struct {
	Parts []gemPart `json:"parts"`
}

func (g *gemini) Complete(ctx context.Context, req Request) (Response, error) {
	maxTokens := req.MaxTokens
	if maxTokens == 0 {
		maxTokens = DefaultMaxTokens
	}
	body, err := json.Marshal(map[string]any{
		"systemInstruction": gemContent{Parts: []gemPart{{Text: req.System}}},
		"contents":          []map[string]any{{"role": "user", "parts": []gemPart{{Text: req.User}}}},
		"generationConfig":  map[string]any{"responseMimeType": "application/json", "maxOutputTokens": maxTokens},
	})
	if err != nil {
		return Response{}, err
	}
	endpoint := fmt.Sprintf("%s/v1beta/models/%s:generateContent?key=%s", g.base, g.model, url.QueryEscape(g.apiKey))

	raw, status, err := postJSON(ctx, g.http, g.sleep, "gemini", endpoint, nil, body)
	if err != nil {
		return Response{}, err
	}
	if status < 200 || status > 299 {
		return Response{}, fmt.Errorf("llm: gemini: status %d: %s", status, snippet(raw))
	}
	return g.parse(raw)
}

func (g *gemini) parse(raw []byte) (Response, error) {
	var out struct {
		Candidates []struct {
			Content      gemContent `json:"content"`
			FinishReason string     `json:"finishReason"`
		} `json:"candidates"`
		PromptFeedback struct {
			BlockReason string `json:"blockReason"`
		} `json:"promptFeedback"`
		Usage struct {
			Prompt int `json:"promptTokenCount"`
			Cand   int `json:"candidatesTokenCount"`
			Cached int `json:"cachedContentTokenCount"`
		} `json:"usageMetadata"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return Response{}, fmt.Errorf("llm: gemini: decode response: %w", err)
	}
	if len(out.Candidates) == 0 && out.PromptFeedback.BlockReason != "" {
		return Response{}, fmt.Errorf("llm: gemini: prompt blocked: %s", out.PromptFeedback.BlockReason)
	}
	if len(out.Candidates) == 0 {
		return Response{}, fmt.Errorf("llm: gemini: no candidates in response: %s", snippet(raw))
	}
	var sb strings.Builder
	for _, p := range out.Candidates[0].Content.Parts {
		sb.WriteString(p.Text)
	}
	finish := out.Candidates[0].FinishReason
	if finish == "MAX_TOKENS" {
		return Response{}, errors.New("llm: gemini: output truncated (MAX_TOKENS)")
	}
	if finish == "SAFETY" || finish == "RECITATION" || finish == "OTHER" || sb.Len() == 0 {
		if finish == "" {
			finish = "none"
		}
		return Response{}, fmt.Errorf("llm: gemini: no usable output (finishReason %s)", finish)
	}
	return Response{
		Text:  sb.String(),
		Usage: Usage{InputTokens: out.Usage.Prompt, OutputTokens: out.Usage.Cand, CacheReadTokens: out.Usage.Cached},
		Model: g.model,
	}, nil
}

func snippet(b []byte) string {
	if len(b) > 200 {
		b = b[:200]
	}
	return string(b)
}
