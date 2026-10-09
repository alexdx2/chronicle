package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const geminiDefaultBase = "https://generativelanguage.googleapis.com"

type gemini struct {
	model, apiKey, base string
	http                *http.Client
	sleep               func(time.Duration)
}

// NewGemini returns a Backend over the Gemini generateContent REST API.
func NewGemini(model, apiKey string) Backend {
	return &gemini{model: model, apiKey: apiKey, base: geminiDefaultBase,
		http: &http.Client{Timeout: 5 * time.Minute}, sleep: time.Sleep}
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

	backoff := []time.Duration{time.Second, 3 * time.Second}
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		if attempt > 0 {
			g.sleep(backoff[attempt-1])
		}
		raw, status, err := g.post(ctx, endpoint, body)
		if err != nil {
			if ctx.Err() != nil {
				return Response{}, ctx.Err()
			}
			lastErr = fmt.Errorf("llm: gemini: %w", err)
			continue
		}
		if status == http.StatusTooManyRequests || status >= 500 {
			lastErr = fmt.Errorf("llm: gemini: status %d: %s", status, snippet(raw))
			continue
		}
		if status < 200 || status > 299 {
			return Response{}, fmt.Errorf("llm: gemini: status %d: %s", status, snippet(raw))
		}
		return g.parse(raw)
	}
	return Response{}, lastErr
}

func (g *gemini) post(ctx context.Context, endpoint string, body []byte) ([]byte, int, error) {
	r, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, 0, err
	}
	r.Header.Set("Content-Type", "application/json")
	resp, err := g.http.Do(r)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	return raw, resp.StatusCode, err
}

func (g *gemini) parse(raw []byte) (Response, error) {
	var out struct {
		Candidates []struct {
			Content gemContent `json:"content"`
		} `json:"candidates"`
		Usage struct {
			Prompt int `json:"promptTokenCount"`
			Cand   int `json:"candidatesTokenCount"`
			Cached int `json:"cachedContentTokenCount"`
		} `json:"usageMetadata"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return Response{}, fmt.Errorf("llm: gemini: decode response: %w", err)
	}
	if len(out.Candidates) == 0 {
		return Response{}, fmt.Errorf("llm: gemini: no candidates in response: %s", snippet(raw))
	}
	var sb strings.Builder
	for _, p := range out.Candidates[0].Content.Parts {
		sb.WriteString(p.Text)
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
