package llm

import (
	"context"
	"errors"
	"strings"
)

// Open parses "anthropic:<model>", "gemini:<model>" or "fake:<dir>". env is os.Getenv in production.
func Open(spec string, env func(string) string) (Backend, error) {
	provider, arg, ok := strings.Cut(spec, ":")
	if !ok || arg == "" {
		return nil, ErrBadSpec
	}
	switch provider {
	case "fake":
		return NewFake(arg), nil
	case "anthropic":
		key := env("ANTHROPIC_API_KEY")
		if key == "" {
			return nil, ErrNoKey
		}
		return NewAnthropic(arg, key), nil
	case "gemini":
		key := env("GEMINI_API_KEY")
		if key == "" {
			key = env("GOOGLE_API_KEY")
		}
		if key == "" {
			return nil, ErrNoKey
		}
		return NewGemini(arg, key), nil
	}
	return nil, ErrBadSpec
}

// stubBackend is a placeholder until the real provider backends land.
type stubBackend struct{ name, model, msg string }

func (s stubBackend) Name() string  { return s.name }
func (s stubBackend) Model() string { return s.model }
func (s stubBackend) Complete(context.Context, Request) (Response, error) {
	return Response{}, errors.New(s.msg)
}

// NewAnthropic is a stub; the SDK-backed implementation replaces it.
func NewAnthropic(model, apiKey string) Backend {
	return stubBackend{"anthropic", model, "llm: anthropic backend not implemented"}
}

// NewGemini is a stub; the real implementation replaces it.
func NewGemini(model, apiKey string) Backend {
	return stubBackend{"gemini", model, "llm: gemini backend not implemented"}
}
