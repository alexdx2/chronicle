package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alexdx2/chronicle-core/review"
)

func writeNarrateReview(t *testing.T, dir string) string {
	t.Helper()
	b, err := review.RenderJSON(&review.ReviewResult{
		Head:    review.Provenance{Commit: "abc123"},
		Verdict: review.Verdict{Learned: 2},
		Findings: []review.ReviewFinding{{
			Severity: review.SeverityChanged, Kind: review.IntegrationAdded,
			System: review.SystemRef{Name: "billing"}, Reason: review.ReasonIntegrationAdded,
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, "review.json")
	if err := os.WriteFile(p, b, 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestNarrate_FakeBackend(t *testing.T) {
	dir := t.TempDir()
	fx := filepath.Join(dir, "fx")
	os.Mkdir(fx, 0o755)
	os.WriteFile(filepath.Join(fx, "Review__abc123.json"), []byte(`"Billing now calls ledger; test the payment path."`), 0o644)
	out := filepath.Join(dir, "narrate.md")
	var buf bytes.Buffer
	code, err := runNarrate(context.Background(), &buf, narrateOptions{
		ReviewFile: writeNarrateReview(t, dir), Model: "fake:" + fx, Out: out, MaxWords: 50,
	}, noEnv)
	if code != 0 || err != nil {
		t.Fatalf("code=%d err=%v", code, err)
	}
	b, _ := os.ReadFile(out)
	s := string(b)
	for _, want := range []string{"### What this means (model's reading)", "Billing now calls ledger; test the payment path.", "*Written by fake:fake from the deterministic review above;"} {
		if !strings.Contains(s, want) {
			t.Errorf("output lacks %q:\n%s", want, s)
		}
	}
}

func TestNarrate_NoKeyExit3(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "n.md")
	var buf bytes.Buffer
	code, err := runNarrate(context.Background(), &buf, narrateOptions{
		ReviewFile: writeNarrateReview(t, dir), Model: "anthropic:claude-haiku-5-5", Out: out,
	}, noEnv)
	if code != 3 || err != nil {
		t.Fatalf("code=%d err=%v", code, err)
	}
	if !strings.Contains(buf.String(), `"skipped": true`) {
		t.Fatalf("output = %s", buf.String())
	}
	if _, err := os.Stat(out); err == nil {
		t.Fatal("file written on skip")
	}
}

func TestNarrate_BadReviewJSONExit1(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "bad.json")
	os.WriteFile(p, []byte("not json"), 0o644)
	code, err := runNarrate(context.Background(), &bytes.Buffer{}, narrateOptions{ReviewFile: p, Model: "fake:" + dir}, noEnv)
	if code != 1 || err == nil {
		t.Fatalf("code=%d err=%v", code, err)
	}
}
