package llm

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestFake_ByPath(t *testing.T) {
	dir := t.TempDir()
	must := func(name, body string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	must("src__a.ts.json", `{"file":"a"}`)
	must("default.json", `{"file":"default"}`)
	b := NewFake(dir)
	if b.Name() != "fake" || b.Model() != "fake" {
		t.Fatalf("identity = %s/%s", b.Name(), b.Model())
	}
	r, err := b.Complete(context.Background(), Request{System: "sys!", User: "File: src/a.ts\nbody"})
	if err != nil {
		t.Fatal(err)
	}
	if r.Text != `{"file":"a"}` {
		t.Fatalf("text = %q", r.Text)
	}
	if r.Usage.InputTokens != (len("sys!")+len("File: src/a.ts\nbody"))/4 || r.Usage.OutputTokens != len(r.Text)/4 {
		t.Fatalf("usage = %+v", r.Usage)
	}
	r, err = b.Complete(context.Background(), Request{User: "File: other/x.ts\n..."})
	if err != nil || r.Text != `{"file":"default"}` {
		t.Fatalf("fallback: %q %v", r.Text, err)
	}
}

func TestFake_NoFixture(t *testing.T) {
	b := NewFake(t.TempDir())
	_, err := b.Complete(context.Background(), Request{User: "File: src/a.ts\n"})
	if err == nil || err.Error() != "fake: no fixture for src/a.ts" {
		t.Fatalf("err = %v", err)
	}
}

func TestFake_MissingFileHeader(t *testing.T) {
	b := NewFake(t.TempDir())
	if _, err := b.Complete(context.Background(), Request{User: "no header"}); err == nil {
		t.Fatal("want error for missing File: header")
	}
}
