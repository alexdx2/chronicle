package llm

import "strings"

// Open parses a backend spec. env is os.Getenv in production.
//
//	anthropic:<model>    ANTHROPIC_API_KEY
//	gemini:<model>       GEMINI_API_KEY or GOOGLE_API_KEY
//	openai:<model>       OPENAI_API_KEY, optional OPENAI_BASE_URL (any OpenAI-compatible server)
//	openrouter:<model>   OPENROUTER_API_KEY (model ids like "qwen/qwen3.5-35b-a3b")
//	ollama:<model>       no key; OLLAMA_HOST (default localhost:11434), tags like "qwen3.5:4b"
//	fake:<dir>           fixture replies
//
// The first colon splits provider from model, so Ollama tags keep their own colon.
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
	case "openai":
		key := env("OPENAI_API_KEY")
		if key == "" {
			return nil, ErrNoKey
		}
		c := NewOpenAI(arg, key)
		if base := env("OPENAI_BASE_URL"); base != "" {
			c.WithBaseURL(base)
		}
		return c, nil
	case "openrouter":
		key := env("OPENROUTER_API_KEY")
		if key == "" {
			return nil, ErrNoKey
		}
		return NewOpenRouter(arg, key), nil
	case "ollama":
		return NewOllama(arg, env("OLLAMA_HOST")), nil
	}
	return nil, ErrBadSpec
}
