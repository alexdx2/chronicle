package cli

import (
	"bytes"
	"context"
	"encoding/json"
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

func fakeFixtures(t *testing.T, dir, body string) string {
	t.Helper()
	fx := filepath.Join(dir, "fx")
	os.Mkdir(fx, 0o755)
	if body != "" {
		os.WriteFile(filepath.Join(fx, "Review__abc123.json"), []byte(body), 0o644)
	}
	return "fake:" + fx
}

func TestNarrate_EmptyAnswerExit1NoOut(t *testing.T) {
	for _, body := range []string{`""`, `"   \n "`} {
		dir := t.TempDir()
		out := filepath.Join(dir, "n.md")
		code, err := runNarrate(context.Background(), &bytes.Buffer{}, narrateOptions{
			ReviewFile: writeNarrateReview(t, dir), Model: fakeFixtures(t, dir, body), Out: out,
		}, noEnv)
		if code != 1 || err == nil {
			t.Fatalf("body %q: code=%d err=%v", body, code, err)
		}
		if _, err := os.Stat(out); err == nil {
			t.Fatal("out file written for an empty answer")
		}
	}
}

func TestNarrate_ProviderErrorExit1(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "n.md")
	// No fixture and no default.json: the fake backend fails the call.
	code, err := runNarrate(context.Background(), &bytes.Buffer{}, narrateOptions{
		ReviewFile: writeNarrateReview(t, dir), Model: fakeFixtures(t, dir, ""), Out: out,
	}, noEnv)
	if code != 1 || err == nil {
		t.Fatalf("code=%d err=%v", code, err)
	}
	if _, err := os.Stat(out); err == nil {
		t.Fatal("out file written on provider error")
	}
}

func TestNarrate_UsageOut(t *testing.T) {
	dir := t.TempDir()
	usage := filepath.Join(dir, "usage.json")
	code, err := runNarrate(context.Background(), &bytes.Buffer{}, narrateOptions{
		ReviewFile: writeNarrateReview(t, dir), Model: fakeFixtures(t, dir, `"ok then"`), Out: filepath.Join(dir, "n.md"), UsageOut: usage,
	}, noEnv)
	if code != 0 || err != nil {
		t.Fatalf("code=%d err=%v", code, err)
	}
	b, _ := os.ReadFile(usage)
	var u struct {
		Usage map[string]any `json:"usage"`
		Cost  *float64       `json:"cost_usd"`
	}
	if err := json.Unmarshal(b, &u); err != nil {
		t.Fatalf("%v: %s", err, b)
	}
	if _, ok := u.Usage["input_tokens"]; !ok {
		t.Errorf("no input_tokens: %s", b)
	}
	if _, ok := u.Usage["output_tokens"]; !ok {
		t.Errorf("no output_tokens: %s", b)
	}
	if u.Cost == nil {
		t.Errorf("no cost_usd: %s", b)
	}
}

func TestNarrate_ChangedFileParsing(t *testing.T) {
	dir := t.TempDir()
	// The fake backend cannot show the prompt, so unreadable --changed is the
	// error path and a file with blank lines must succeed.
	changed := filepath.Join(dir, "changed.txt")
	os.WriteFile(changed, []byte("\n src/a.ts \n\n\nsrc/b.ts\n  \n"), 0o644)
	code, err := runNarrate(context.Background(), &bytes.Buffer{}, narrateOptions{
		ReviewFile: writeNarrateReview(t, dir), Model: fakeFixtures(t, dir, `"fine"`), Changed: changed, Out: filepath.Join(dir, "n.md"),
	}, noEnv)
	if code != 0 || err != nil {
		t.Fatalf("code=%d err=%v", code, err)
	}
	code, err = runNarrate(context.Background(), &bytes.Buffer{}, narrateOptions{
		ReviewFile: writeNarrateReview(t, dir), Model: fakeFixtures(t, dir, `"fine"`), Changed: filepath.Join(dir, "missing.txt"),
	}, noEnv)
	if code != 1 || err == nil {
		t.Fatalf("missing --changed: code=%d err=%v", code, err)
	}
}

func TestNarrate_UnwrapsObjectAndFencedBodies(t *testing.T) {
	const sentence = "Billing now calls ledger; test the payment path."
	for name, body := range map[string]string{
		"object":        `{"text": "` + sentence + `"}`,
		"fenced object": "```json\n{\"text\": \"" + sentence + "\"}\n```",
		"fenced prose":  "```\n" + sentence + "\n```",
		"json string":   `"` + sentence + `"`,
		"single field":  `{"answer": "` + sentence + `"}`,
	} {
		dir := t.TempDir()
		out := filepath.Join(dir, "n.md")
		code, err := runNarrate(context.Background(), &bytes.Buffer{}, narrateOptions{
			ReviewFile: writeNarrateReview(t, dir), Model: fakeFixtures(t, dir, body), Out: out,
		}, noEnv)
		if code != 0 || err != nil {
			t.Fatalf("%s: code=%d err=%v", name, code, err)
		}
		b, _ := os.ReadFile(out)
		s := string(b)
		if !strings.Contains(s, "\n\n"+sentence+"\n\n") {
			t.Errorf("%s: paragraph not found bare:\n%s", name, s)
		}
		if strings.ContainsAny(strings.SplitN(s, "*Written by", 2)[0], "{}`") {
			t.Errorf("%s: wrapper leaked into the output:\n%s", name, s)
		}
	}
}

func TestNarrate_EmptyObjectExit1NoOut(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "n.md")
	code, err := runNarrate(context.Background(), &bytes.Buffer{}, narrateOptions{
		ReviewFile: writeNarrateReview(t, dir), Model: fakeFixtures(t, dir, `{"text": ""}`), Out: out,
	}, noEnv)
	if code != 1 || err == nil {
		t.Fatalf("code=%d err=%v", code, err)
	}
	if _, err := os.Stat(out); err == nil {
		t.Fatal("out file written for an empty answer")
	}
}
