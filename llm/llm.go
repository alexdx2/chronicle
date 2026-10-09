// Package llm is a provider-agnostic text-completion interface used by the
// headless extractor. It has no dependencies on the rest of the module.
package llm

import (
	"context"
	"errors"
)

// DefaultMaxTokens applies when Request.MaxTokens is 0.
const DefaultMaxTokens = 8192

// Request is one single-turn completion request.
type Request struct {
	System    string
	User      string
	MaxTokens int // 0 -> DefaultMaxTokens
}

// Usage counts tokens for one or more calls.
type Usage struct {
	InputTokens      int `json:"input_tokens"`
	OutputTokens     int `json:"output_tokens"`
	CacheReadTokens  int `json:"cache_read_tokens"`
	CacheWriteTokens int `json:"cache_write_tokens"`
	// CostUSD is the provider-reported cost when the API returns one
	// (OpenRouter does); 0 means "not reported", see Cost.
	CostUSD float64 `json:"cost_usd,omitempty"`
}

// Add returns the field-wise sum of u and o.
func (u Usage) Add(o Usage) Usage {
	return Usage{
		InputTokens:      u.InputTokens + o.InputTokens,
		OutputTokens:     u.OutputTokens + o.OutputTokens,
		CacheReadTokens:  u.CacheReadTokens + o.CacheReadTokens,
		CacheWriteTokens: u.CacheWriteTokens + o.CacheWriteTokens,
		CostUSD:          u.CostUSD + o.CostUSD,
	}
}

// Response is a completed call.
type Response struct {
	Text  string
	Usage Usage
	Model string
}

// Backend is a completion provider.
type Backend interface {
	Name() string // "anthropic", "gemini", "openai", "openrouter", "ollama", "fake"
	Model() string
	Complete(ctx context.Context, req Request) (Response, error)
}

var (
	ErrNoKey   = errors.New("llm: no API key in environment")
	ErrBadSpec = errors.New("llm: bad model spec, want provider:model")
)
