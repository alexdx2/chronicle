package llm

import "strings"

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
