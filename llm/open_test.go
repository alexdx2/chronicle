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
