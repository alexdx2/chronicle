package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alexdx2/chronicle-core/headless"
)

func noEnv(string) string { return "" }

func TestExtractCmd_PlanOnly(t *testing.T) {
	e := newExtractEnv(t)
	var buf bytes.Buffer
	code, err := runExtract(context.Background(), &buf, e.g, e.root, e.dir, extractCLIOptions{
		PlanOnly: true, Base: "HEAD~1", Head: "HEAD", MaxFiles: 1,
	}, noEnv)
	if err != nil || code != 0 {
		t.Fatalf("code=%d err=%v", code, err)
	}
	var plan headless.Plan
	if err := json.Unmarshal(buf.Bytes(), &plan); err != nil {
		t.Fatalf("output not a plan: %v\n%s", err, buf.String())
	}
	if len(plan.Files) != 1 || plan.Files[0].Path != "src/a.ts" || plan.Domain != "d" {
		t.Fatalf("plan = %+v", plan)
	}
	var budget bool
	for _, s := range plan.Skipped {
		if s.Path == "src/b.ts" && s.Reason == "skipped:budget" {
			budget = true
		}
	}
	if !budget {
		t.Fatalf("no skipped:budget entry for src/b.ts: %+v", plan.Skipped)
	}
}

func TestExtractCmd_NoKeyExit3(t *testing.T) {
	e := newExtractEnv(t)
	var buf bytes.Buffer
	code, err := runExtract(context.Background(), &buf, e.g, e.root, e.dir, extractCLIOptions{
		Base: "HEAD~1", Head: "HEAD", Model: "anthropic:claude-haiku-5-5",
	}, noEnv)
	if code != 3 || err != nil {
		t.Fatalf("code=%d err=%v", code, err)
	}
	if !strings.Contains(buf.String(), `"skipped": true`) && !strings.Contains(buf.String(), `"skipped":true`) {
		t.Fatalf("output = %s", buf.String())
	}
}

func TestExtractCmd_BadSpecExit1(t *testing.T) {
	e := newExtractEnv(t)
	code, err := runExtract(context.Background(), &bytes.Buffer{}, e.g, e.root, e.dir, extractCLIOptions{
		Base: "HEAD~1", Head: "HEAD", Model: "nonsense",
	}, noEnv)
	if code != 1 || err == nil {
		t.Fatalf("code=%d err=%v", code, err)
	}
}

func TestExtractCmd_SeveralDomainsNeedFlag(t *testing.T) {
	e := newExtractEnv(t)
	write(t, e.root, ".depbot/chronicle.domain.yaml", "domains:\n  d:\n    scan:\n      include: [\"src/**\"]\n  e:\n    scan:\n      include: [\"lib/**\"]\ntech: [typescript]\n")
	code, err := runExtract(context.Background(), &bytes.Buffer{}, e.g, e.root, e.dir, extractCLIOptions{Base: "HEAD~1", Head: "HEAD"}, noEnv)
	if code != 1 || err == nil || !strings.Contains(err.Error(), "--domain") {
		t.Fatalf("code=%d err=%v", code, err)
	}
}

func TestExtractCmd_NoManifestExit2(t *testing.T) {
	e := newExtractEnv(t)
	code, err := runExtract(context.Background(), &bytes.Buffer{}, e.g, e.root, filepath.Join(e.root, "nodir"), extractCLIOptions{Base: "HEAD~1"}, noEnv)
	if code != 2 || err == nil {
		t.Fatalf("code=%d err=%v", code, err)
	}
}

func TestExtractCmd_FakeRun(t *testing.T) {
	e := newExtractEnv(t)
	usage := filepath.Join(t.TempDir(), "usage.json")
	var buf bytes.Buffer
	code, err := runExtract(context.Background(), &buf, e.g, e.root, e.dir, extractCLIOptions{
		Base: "HEAD~1", Head: "HEAD", MaxFiles: 1, Concurrency: 2,
		Model: "fake:" + filepath.Join("testdata", "extract-fake"), UsageOut: usage,
	}, noEnv)
	if code != 0 || err != nil {
		t.Fatalf("code=%d err=%v\n%s", code, err, buf.String())
	}
	data, err := os.ReadFile(usage)
	if err != nil {
		t.Fatal(err)
	}
	var res headless.RunResult
	if err := json.Unmarshal(data, &res); err != nil {
		t.Fatal(err)
	}
	if res.Extracted != 1 {
		t.Fatalf("extracted = %d, want 1: %s", res.Extracted, data)
	}
	if !strings.HasPrefix(buf.String(), "chronicle extract: 1 files, 1 extracted, 0 failed, in=") {
		t.Fatalf("summary = %q", buf.String())
	}
}
