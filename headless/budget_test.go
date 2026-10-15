package headless

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/alexdx2/chronicle-core/graph/prompts"
	"github.com/alexdx2/chronicle-core/llm"
)

func TestTechForFile(t *testing.T) {
	all := []string{"nestjs", "prisma", "kafka", "typescript", "dotnet", "mycustom"}
	cases := map[string][]string{
		"svc/main.go":          {"kafka", "mycustom"},
		"src/a.ts":             {"nestjs", "prisma", "kafka", "typescript", "mycustom"},
		"prisma/schema.prisma": {"prisma", "kafka", "mycustom"},
		"Api/Program.cs":       {"kafka", "dotnet", "mycustom"},
		"web/Page.TSX":         {"nestjs", "prisma", "kafka", "typescript", "mycustom"},
		"src/main/Orders.java": {"kafka", "mycustom"},
		"schema/q.graphql":     {"kafka", "mycustom"},
	}
	for path, want := range cases {
		if got := techForFile(path, all, prompts.BuiltinCatalog()); strings.Join(got, ",") != strings.Join(want, ",") {
			t.Errorf("%s: got %v want %v", path, got, want)
		}
	}
}

func TestSplitLines(t *testing.T) {
	var b strings.Builder
	for i := 0; i < 100; i++ {
		fmt.Fprintf(&b, "line %03d\n", i) // 9 bytes
	}
	src := []byte(b.String())
	parts := splitLines(src, 100)
	if len(parts) < 9 {
		t.Fatalf("parts = %d", len(parts))
	}
	var joined []byte
	for _, p := range parts {
		if len(p) > 100 {
			t.Fatalf("part of %d bytes exceeds the budget", len(p))
		}
		if p[len(p)-1] != '\n' {
			t.Fatalf("part does not end at a line boundary: %q", p[len(p)-5:])
		}
		joined = append(joined, p...)
	}
	if string(joined) != string(src) {
		t.Fatal("chunks do not rebuild the file")
	}
	if got := splitLines(src, 0); len(got) != 1 {
		t.Fatal("no budget must keep the file whole")
	}
	long := []byte(strings.Repeat("é", 50)) // one 100-byte line, 2-byte runes
	for _, p := range splitLines(long, 7) {
		if !strings.HasPrefix(string(p), "é") || len(p)%2 != 0 {
			t.Fatalf("cut inside a rune: %q", p)
		}
	}
}

func TestFileBudget(t *testing.T) {
	req := llm.Request{System: strings.Repeat("s", 9000), User: strings.Repeat("u", 300)}
	if fileBudget(0, req) != 0 {
		t.Fatal("no window means no budget")
	}
	// (16384 - 2048 - (9300+reserve)/3) tokens at 3 chars each
	if got, want := fileBudget(16384, req), (16384-2048-(9300+partLabelReserve)/3)*3; got != want {
		t.Fatalf("budget = %d want %d", got, want)
	}
	if got := fileBudget(4096, req); got != minChunkBytes {
		t.Fatalf("an overfull window must floor at %d, got %d", minChunkBytes, got)
	}
}

// windowBackend answers every request with one fact named after the part, and
// records the prompts it saw.
type windowBackend struct {
	window int
	mu     sync.Mutex
	reqs   []llm.Request
}

func (*windowBackend) Name() string         { return "win" }
func (*windowBackend) Model() string        { return "win" }
func (w *windowBackend) ContextWindow() int { return w.window }
func (w *windowBackend) Complete(_ context.Context, r llm.Request) (llm.Response, error) {
	w.mu.Lock()
	w.reqs = append(w.reqs, r)
	n := len(w.reqs)
	w.mu.Unlock()
	return llm.Response{Text: fmt.Sprintf(`{"status":"extracted","facts":[{"kind":"model","name":"M%d"}]}`, n)}, nil
}

func TestRun_LongFileIsSplitToFitTheWindow(t *testing.T) {
	e := newRunEnv(t)
	body := strings.Repeat("export const filler = 'xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx';\n", 400) // ~27 KB
	write(t, e.root, "src/long.ts", body)
	commitAll(t, e.root, "long")
	wb := &windowBackend{window: 8000}
	res, err := Run(context.Background(), e.input(wb, "src/long.ts"))
	if err != nil {
		t.Fatal(err)
	}
	if len(wb.reqs) < 2 {
		t.Fatalf("a 27 KB file in an 8k window must be split, got %d request(s)", len(wb.reqs))
	}
	for i, r := range wb.reqs {
		if est := (len(r.System) + len(r.User)) / charsPerToken; est+outputReserve > wb.window {
			t.Errorf("request %d is ~%d tokens, window %d", i, est, wb.window)
		}
		if !strings.HasPrefix(r.User, "File: src/long.ts\n") {
			t.Errorf("request %d lost the File: first line (the fake backend keys on it)", i)
		}
		if !strings.Contains(r.User, fmt.Sprintf("(part %d of %d", i+1, len(wb.reqs))) {
			t.Errorf("request %d is not labelled as a part: %.120q", i, r.User)
		}
	}
	f := res.Files[0]
	if f.Status != "extracted" || f.Error != "" {
		t.Fatalf("file = %+v", f)
	}
	rows, _ := e.g.Store().ListExtractions(res.RevisionID, "d")
	var facts []map[string]string
	_ = json.Unmarshal([]byte(rows[0].FactsJSON), &facts)
	if len(facts) != len(wb.reqs) {
		t.Fatalf("want one merged fact per part (%d), got %s", len(wb.reqs), rows[0].FactsJSON)
	}
}

func TestRun_ShortFileIsNotSplit(t *testing.T) {
	e := newRunEnv(t)
	wb := &windowBackend{window: 16384}
	if _, err := Run(context.Background(), e.input(wb, "src/a.ts")); err != nil {
		t.Fatal(err)
	}
	if len(wb.reqs) != 1 || strings.Contains(wb.reqs[0].User, "(part ") {
		t.Fatalf("reqs = %d", len(wb.reqs))
	}
}

func TestRun_GuideFollowsFileLanguage(t *testing.T) {
	e := newRunEnv(t)
	e.m.Tech = []string{"typescript", "dotnet"}
	write(t, e.root, "src/svc.cs", "class A {}\n")
	commitAll(t, e.root, "cs")
	wb := &windowBackend{}
	if _, err := Run(context.Background(), e.input(wb, "src/a.ts", "src/svc.cs")); err != nil {
		t.Fatal(err)
	}
	sys := map[string]string{}
	for _, r := range wb.reqs {
		first, _, _ := strings.Cut(r.User, "\n")
		sys[first] = r.System
	}
	ts, cs := sys["File: src/a.ts"], sys["File: src/svc.cs"]
	if ts == "" || cs == "" {
		t.Fatalf("missing prompts: %v", len(sys))
	}
	if strings.Contains(ts, "ASP.NET") == strings.Contains(cs, "ASP.NET") && len(ts) == len(cs) {
		t.Fatal(".ts and .cs got the same guide")
	}
	if !(len(cs) > len(ts)) {
		t.Logf("ts guide %d bytes, cs guide %d bytes", len(ts), len(cs))
	}
}

func TestGuideSet_PacksPerFileAndVersion(t *testing.T) {
	cat := prompts.BuiltinCatalog()
	cat.Add(prompts.CatalogPack{ID: "kafka-dotnet", Content: "# K\n\n## Match\n- *.cs\n\n## Entry points (→ endpoint facts)\nx\n", Source: prompts.SourceFleet})
	gs := newGuideSet(nil, []string{"dotnet", "kafka", "kafka-dotnet"}, cat)
	_, cs := gs.For("src/A.cs")
	_, ts := gs.For("web/a.ts")
	ids := func(r []prompts.PackRef) (s string) {
		for _, p := range r {
			s += p.ID + " "
		}
		return
	}
	if ids(cs) != "dotnet kafka kafka-dotnet " {
		t.Fatalf("cs packs = %q", ids(cs))
	}
	if ids(ts) != "kafka " {
		t.Fatalf("ts packs = %q", ids(ts))
	}
}

func TestGuideSet_ForContent(t *testing.T) {
	gs := NewGuide(prompts.BuiltinCatalog(), []string{"dotnet", "kafka", "prisma", "typescript"})
	ids := func(r []prompts.PackRef) (s string) {
		for _, p := range r {
			s += p.ID + " "
		}
		return
	}
	_, refs := gs.ForContent("src/A.cs", []byte("using Confluent.Kafka;\nclass A {}\n"))
	if ids(refs) != "dotnet kafka " {
		t.Fatalf("cs with Confluent.Kafka: %q", ids(refs))
	}
	_, refs = gs.ForContent("src/B.cs", []byte("class B {}\n"))
	if ids(refs) != "dotnet " {
		t.Fatalf("cs without a token: %q", ids(refs))
	}
	// The own file type of prisma (always=) matches without a token; others need one.
	_, refs = gs.ForContent("prisma/schema.prisma", []byte("model A { id Int }\n"))
	if ids(refs) != "prisma " {
		t.Fatalf("schema.prisma: %q", ids(refs))
	}
	_, refs = gs.ForContent("src/x.ts", []byte("export const a = 1;\n"))
	if ids(refs) != "typescript " {
		t.Fatalf("x.ts without token: %q", ids(refs))
	}
	_, refs = gs.ForContent("src/x.ts", []byte("const p = new PrismaClient();\n"))
	if ids(refs) != "prisma typescript " {
		t.Fatalf("x.ts with PrismaClient: %q", ids(refs))
	}
	// nestjs has no always=: a plain .ts utility does not get it, one with the import does.
	gn := NewGuide(prompts.BuiltinCatalog(), []string{"nestjs", "typescript"})
	_, refs = gn.ForContent("src/util.ts", []byte("export const a = 1;\n"))
	if ids(refs) != "typescript " {
		t.Fatalf("plain .ts with nestjs listed: %q", ids(refs))
	}
	_, refs = gn.ForContent("src/app.controller.ts", []byte("import { Controller } from '@nestjs/common';\n"))
	if ids(refs) != "nestjs typescript " {
		t.Fatalf(".ts with @nestjs/common: %q", ids(refs))
	}
	// Tokens are case-sensitive, as written.
	_, refs = gs.ForContent("src/y.ts", []byte("const k = 'KAFKA';\n"))
	if ids(refs) != "typescript " {
		t.Fatalf("case-sensitive token: %q", ids(refs))
	}
	// For stays content independent: the potential pack set by extension.
	_, refs = gs.For("src/A.cs")
	if ids(refs) != "dotnet kafka " {
		t.Fatalf("For(cs): %q", ids(refs))
	}
	_, refs = gs.For("src/x.ts")
	if ids(refs) != "kafka prisma typescript " {
		t.Fatalf("For(ts): %q", ids(refs))
	}
}

func TestRun_PackIsAttachedOnlyToFilesThatUseIt(t *testing.T) {
	e := newRunEnv(t)
	e.m.Tech = []string{"dotnet", "kafka"}
	write(t, e.root, "src/plain.cs", "class Plain {}\n")
	write(t, e.root, "src/bus.cs", "using Confluent.Kafka;\nclass Bus {}\n")
	commitAll(t, e.root, "cs")
	wb := &windowBackend{}
	if _, err := Run(context.Background(), e.input(wb, "src/plain.cs", "src/bus.cs")); err != nil {
		t.Fatal(err)
	}
	sys := map[string]string{}
	for _, r := range wb.reqs {
		first, _, _ := strings.Cut(r.User, "\n")
		sys[first] = r.System
	}
	const heading = "# KAFKA / EVENT STREAM EXTRACTION RULES"
	if sys["File: src/plain.cs"] == "" || sys["File: src/bus.cs"] == "" {
		t.Fatalf("missing prompts: %d", len(sys))
	}
	if strings.Contains(sys["File: src/plain.cs"], heading) {
		t.Error("kafka-free .cs file got the kafka pack")
	}
	if !strings.Contains(sys["File: src/bus.cs"], heading) {
		t.Error("kafka-using .cs file did not get the kafka pack")
	}
}

// truncatingBackend answers like windowBackend but refuses any request whose
// user prompt is longer than limit bytes with llm.ErrTruncated, the way a
// provider reports an answer cut at its output cap.
type truncatingBackend struct {
	limit int
	mu    sync.Mutex
	reqs  []llm.Request
}

func (*truncatingBackend) Name() string  { return "trunc" }
func (*truncatingBackend) Model() string { return "trunc" }
func (b *truncatingBackend) Complete(_ context.Context, r llm.Request) (llm.Response, error) {
	b.mu.Lock()
	b.reqs = append(b.reqs, r)
	n := len(b.reqs)
	b.mu.Unlock()
	if len(r.User) > b.limit {
		return llm.Response{}, fmt.Errorf("llm: trunc: %w (num_predict)", llm.ErrTruncated)
	}
	return llm.Response{Text: fmt.Sprintf(`{"status":"extracted","facts":[{"kind":"model","name":"M%d"}]}`, n)}, nil
}

func TestRun_TruncatedAnswerSplitsTheFile(t *testing.T) {
	e := newRunEnv(t)
	body := strings.Repeat("export const filler = 'xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx';\n", 400) // ~27 KB, backend without a window
	write(t, e.root, "src/long.ts", body)
	commitAll(t, e.root, "long")
	tb := &truncatingBackend{limit: 12000}
	res, err := Run(context.Background(), e.input(tb, "src/long.ts"))
	if err != nil {
		t.Fatal(err)
	}
	f := res.Files[0]
	if f.Status != "extracted" || f.Error != "" {
		t.Fatalf("file = %+v", f)
	}
	if f.Parts < 3 {
		t.Fatalf("27 KB over a 12 KB answer limit must end in at least 3 parts, got %d", f.Parts)
	}
	var truncated, answered int
	for _, r := range tb.reqs {
		if len(r.User) > tb.limit {
			truncated++
			continue
		}
		answered++
		if !strings.Contains(r.User, "(part ") {
			t.Errorf("an answered piece is not labelled as a part: %.120q", r.User)
		}
	}
	if truncated == 0 || answered != f.Parts {
		t.Fatalf("truncated %d, answered %d, parts %d", truncated, answered, f.Parts)
	}
	rows, _ := e.g.Store().ListExtractions(res.RevisionID, "d")
	var facts []map[string]string
	_ = json.Unmarshal([]byte(rows[0].FactsJSON), &facts)
	if len(facts) != f.Parts {
		t.Fatalf("want one merged fact per part (%d), got %s", f.Parts, rows[0].FactsJSON)
	}
}

func TestRun_TruncationAtTheSmallestPieceFails(t *testing.T) {
	e := newRunEnv(t)
	tb := &truncatingBackend{limit: 0} // every answer is truncated
	res, err := Run(context.Background(), e.input(tb, "src/a.ts"))
	if err != nil {
		t.Fatal(err)
	}
	f := res.Files[0]
	if f.Status != "provider_error" || !strings.Contains(f.Error, "output truncated") {
		t.Fatalf("file = %+v", f)
	}
	if len(tb.reqs) != 1 {
		t.Fatalf("a %d-byte file is under the smallest piece and must not be split: %d requests", f.Bytes, len(tb.reqs))
	}
}
