package llm

import (
	"errors"
	"testing"
)

func TestOpen_FakeAndErrors(t *testing.T) {
	dir := t.TempDir()
	b, err := Open("fake:"+dir, nil)
	if err != nil || b.Name() != "fake" {
		t.Fatalf("fake: %v %v", b, err)
	}
	noEnv := func(string) string { return "" }
	if _, err := Open("anthropic:x", noEnv); !errors.Is(err, ErrNoKey) {
		t.Fatalf("anthropic no key: %v", err)
	}
	if _, err := Open("gemini:x", noEnv); !errors.Is(err, ErrNoKey) {
		t.Fatalf("gemini no key: %v", err)
	}
	for _, spec := range []string{"nope", "anthropic:", ":x", "fake:", "other:x"} {
		if _, err := Open(spec, noEnv); !errors.Is(err, ErrBadSpec) {
			t.Fatalf("%q: %v", spec, err)
		}
	}
	withKey := func(string) string { return "k" }
	if b, err := Open("anthropic:claude-haiku-5-5", withKey); err != nil || b.Name() != "anthropic" || b.Model() != "claude-haiku-5-5" {
		t.Fatalf("anthropic: %v %v", b, err)
	}
	if b, err := Open("gemini:gemini-2.5-flash", withKey); err != nil || b.Name() != "gemini" {
		t.Fatalf("gemini: %v %v", b, err)
	}
}

func TestOpen_OpenAIOpenRouterOllama(t *testing.T) {
	noEnv := func(string) string { return "" }
	if _, err := Open("openai:gpt-5-mini", noEnv); !errors.Is(err, ErrNoKey) {
		t.Fatalf("openai no key: %v", err)
	}
	if _, err := Open("openrouter:qwen/qwen3.5-35b-a3b", noEnv); !errors.Is(err, ErrNoKey) {
		t.Fatalf("openrouter no key: %v", err)
	}
	// ollama needs no key; the model tag keeps its own colon.
	b, err := Open("ollama:qwen3.5:4b", noEnv)
	if err != nil || b.Name() != "ollama" || b.Model() != "qwen3.5:4b" {
		t.Fatalf("ollama: %v %v", b, err)
	}
	if got := b.(*ollama).base; got != "http://localhost:11434" {
		t.Errorf("default ollama base=%q", got)
	}
	env := map[string]string{
		"OPENAI_API_KEY": "ok", "OPENAI_BASE_URL": "http://proxy:8080/v1/",
		"OPENROUTER_API_KEY": "rk", "OLLAMA_HOST": "mac.local:11434",
	}
	get := func(k string) string { return env[k] }
	if b, err := Open("openai:gpt-5-mini", get); err != nil || b.Name() != "openai" || b.(*chat).base != "http://proxy:8080/v1" {
		t.Fatalf("openai: %v %v", b, err)
	}
	if b, err := Open("openrouter:qwen/qwen3.5-35b-a3b", get); err != nil || b.Name() != "openrouter" || b.Model() != "qwen/qwen3.5-35b-a3b" || b.(*chat).base != openRouterBase {
		t.Fatalf("openrouter: %v %v", b, err)
	}
	if b, err := Open("ollama:qwen3.5:4b", get); err != nil || b.(*ollama).base != "http://mac.local:11434" {
		t.Fatalf("ollama host: %v %v", b, err)
	}
}
