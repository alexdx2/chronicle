package structural

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/alexdx2/chronicle-core/extract/ast"
	"github.com/alexdx2/chronicle-core/extract/rules"
)

var nestTech = []string{"nestjs"}

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	return b
}

func extractFixture(t *testing.T, name string) Result {
	t.Helper()
	return ExtractFile(filepath.Join("testdata", name), fixture(t, name), nestTech)
}

func TestExtractFileNestController(t *testing.T) {
	got := extractFixture(t, "nest.ts")

	if got.Outcome != Extracted {
		t.Fatalf("outcome = %q (err=%v), want extracted", got.Outcome, got.Err)
	}
	if got.Err != nil {
		t.Errorf("err = %v, want nil", got.Err)
	}
	if got.FactCount == 0 {
		t.Errorf("fact count = 0, want > 0 (facts: %s)", got.FactsJSON)
	}
	if got.FromType != "controller" {
		t.Errorf("from type = %q, want controller", got.FromType)
	}
	if got.ContentHash == "" {
		t.Error("content hash is empty")
	}
	var facts []map[string]any
	if err := json.Unmarshal([]byte(got.FactsJSON), &facts); err != nil {
		t.Fatalf("facts JSON is not an array: %v (%s)", err, got.FactsJSON)
	}
	if len(facts) != got.FactCount {
		t.Errorf("fact count %d disagrees with %d facts in JSON", got.FactCount, len(facts))
	}
	kinds := map[string]bool{}
	for _, f := range facts {
		kinds[f["kind"].(string)] = true
	}
	if !kinds["import"] {
		t.Errorf("no import fact in %s", got.FactsJSON)
	}
	if !kinds["endpoint"] {
		t.Errorf("no endpoint fact in %s", got.FactsJSON)
	}
}

func TestExtractFileRoutes(t *testing.T) {
	got := extractFixture(t, "routes.ts")
	if got.Outcome != Extracted || got.FromType != "controller" {
		t.Fatalf("outcome=%q from_type=%q, want extracted/controller", got.Outcome, got.FromType)
	}
	var facts []map[string]any
	json.Unmarshal([]byte(got.FactsJSON), &facts)
	endpoints := 0
	for _, f := range facts {
		if f["kind"] == "endpoint" {
			endpoints++
		}
	}
	if endpoints != 3 {
		t.Errorf("endpoints = %d, want 3 (%s)", endpoints, got.FactsJSON)
	}
}

// Zero facts is a valid answer, not a failure and not a reason to ask an LLM.
func TestExtractFileTypesOnly(t *testing.T) {
	got := extractFixture(t, "types-only.ts")
	if got.Outcome != Extracted {
		t.Fatalf("outcome = %q (err=%v), want extracted", got.Outcome, got.Err)
	}
	if got.FactCount != 0 {
		t.Errorf("fact count = %d, want 0 (%s)", got.FactCount, got.FactsJSON)
	}
	if got.FactsJSON != "[]" {
		t.Errorf("facts JSON = %q, want []", got.FactsJSON)
	}
	if got.ContentHash == "" {
		t.Error("content hash is empty — a type-only file was still looked at")
	}
}

// The only unambiguous parser failure is having nothing to parse: content the
// caller could not read. Unbalanced syntax is not one — see below.
func TestExtractFileNilContentFails(t *testing.T) {
	got := ExtractFile("src/unreadable.ts", nil, nestTech)
	if got.Outcome != Failed {
		t.Fatalf("outcome = %q, want failed", got.Outcome)
	}
	if got.Err == nil {
		t.Error("err = nil, want a reason")
	}
	assertFailedShape(t, got)
}

// Documents the real behaviour the replacement contract depends on:
// tree-sitter recovers from unbalanced syntax, so a broken file still returns
// `extracted` and its facts REPLACE the previous ones. Only a file we could not
// read keeps the previous contribution alive.
func TestExtractFileBrokenSyntaxStillExtracts(t *testing.T) {
	got := extractFixture(t, "broken.ts")
	if got.Outcome != Extracted {
		t.Fatalf("outcome = %q (err=%v), want extracted — tree-sitter recovers", got.Outcome, got.Err)
	}
	if got.FactsJSON == "" {
		t.Error("facts JSON is empty, want at least []")
	}
}

func TestExtractFilePrismaSchema(t *testing.T) {
	got := extractFixture(t, "schema.prisma")
	if got.Outcome != Extracted {
		t.Fatalf("outcome = %q (err=%v), want extracted", got.Outcome, got.Err)
	}
	if got.FromType != "schema" {
		t.Errorf("from type = %q, want schema", got.FromType)
	}
	if got.FactCount < 2 {
		t.Errorf("fact count = %d, want >= 2 (one model + one enum): %s", got.FactCount, got.FactsJSON)
	}
	var facts []map[string]any
	if err := json.Unmarshal([]byte(got.FactsJSON), &facts); err != nil {
		t.Fatalf("facts JSON: %v", err)
	}
	kinds := map[string]bool{}
	for _, f := range facts {
		kinds[f["kind"].(string)] = true
	}
	if !kinds["model"] || !kinds["enum"] {
		t.Errorf("want model and enum facts, got %s", got.FactsJSON)
	}
}

func TestExtractFileUnsupported(t *testing.T) {
	got := ExtractFile("README.md", []byte("# hello\n"), nestTech)
	if got.Outcome != Unsupported {
		t.Fatalf("outcome = %q, want unsupported", got.Outcome)
	}
	if got.Err != nil {
		t.Errorf("err = %v, want nil — unsupported is not a failure", got.Err)
	}
	if got.FactCount != 0 || got.FactsJSON != "[]" {
		t.Errorf("unsupported result carries facts: %s", got.FactsJSON)
	}
}

func TestSupported(t *testing.T) {
	for _, p := range []string{"a.ts", "a.tsx", "a.js", "a.jsx", "db/schema.prisma"} {
		if !Supported(p) {
			t.Errorf("Supported(%q) = false, want true", p)
		}
	}
	for _, p := range []string{"README.md", "a.go", "a.cs", "a.json", "package.json", "a.tsx.bak", ""} {
		if Supported(p) {
			t.Errorf("Supported(%q) = true, want false", p)
		}
	}
}

func TestContentHashIsStableAndContentAddressed(t *testing.T) {
	content := fixture(t, "nest.ts")
	a := ExtractFile("testdata/nest.ts", content, nestTech)
	b := ExtractFile("testdata/nest.ts", content, nestTech)
	if a.ContentHash != b.ContentHash {
		t.Errorf("hash not stable: %q vs %q", a.ContentHash, b.ContentHash)
	}
	if len(a.ContentHash) != 64 {
		t.Errorf("hash = %q, want 64 hex chars (sha256)", a.ContentHash)
	}
	other := ExtractFile("testdata/nest.ts", append(append([]byte{}, content...), '\n'), nestTech)
	if other.ContentHash == a.ContentHash {
		t.Error("different content produced the same hash")
	}
	// The hash is of the content, not of the path.
	elsewhere := ExtractFile("src/other/nest.ts", content, nestTech)
	if elsewhere.ContentHash != a.ContentHash {
		t.Error("hash changed with the path")
	}
}

func TestResultCarriesPath(t *testing.T) {
	got := ExtractFile("src/a.ts", []byte("export const x = 1;\n"), nestTech)
	if got.Path != "src/a.ts" {
		t.Errorf("path = %q, want src/a.ts", got.Path)
	}
}

// Every ast row carries the rules-pack version; bumping the pack is what puts
// unchanged files back in the queue, so the constant has to exist and be read
// from one place.
func TestPackVersion(t *testing.T) {
	if rules.PackVersion != "1" {
		t.Errorf("rules.PackVersion = %q, want 1", rules.PackVersion)
	}
}

// The structural phase must not answer to the scan's extractor id. Superseding
// is scoped to (file, extractor_id), and graph.factExtractorID stamps
// "chronicle-ast" on facts the scan's AST merge merely CORROBORATED (origin
// "ast+llm") — rows carrying an LLM's reading that a syntax-only pass will
// never re-assert. Sharing the id would supersede them on every commit.
func TestExtractorID(t *testing.T) {
	if ExtractorID != "chronicle-structural" {
		t.Errorf("ExtractorID = %q, want chronicle-structural", ExtractorID)
	}
	if ExtractorID == "chronicle-ast" {
		t.Error("the structural phase shares the scan's extractor id — it would supersede ast+llm rows")
	}
}

// A parser fault must not look like an answer. tree-sitter is cgo; the guard is
// the only thing between a malformed file and a dead git hook, and an untested
// recover is a recover that silently stops working.
func TestExtractFileSurvivesAParserPanic(t *testing.T) {
	orig := parseTypeScript
	parseTypeScript = func([]byte) *ast.RawResult { panic("grammar exploded") }
	defer func() { parseTypeScript = orig }()

	got := ExtractFile("src/a.ts", []byte("export const x = 1;\n"), nestTech)
	if got.Outcome != Failed {
		t.Fatalf("outcome = %q, want failed", got.Outcome)
	}
	if got.Err == nil {
		t.Error("err = nil, want the panic reason")
	}
	assertFailedShape(t, got)
}

// Every failure has the same shape, whatever caused it — the content hash
// included: a failure means the file was not looked at, and recording a hash
// for it would let the next run conclude "same content, already done" and skip
// the file forever.
func assertFailedShape(t *testing.T, got Result) {
	t.Helper()
	if got.ContentHash != "" {
		t.Errorf("content hash = %q, want empty — the file was not looked at", got.ContentHash)
	}
	if got.FactsJSON != "[]" || got.FactCount != 0 {
		t.Errorf("failed result carries facts: %s (%d)", got.FactsJSON, got.FactCount)
	}
	if got.FromType != "" {
		t.Errorf("from type = %q, want empty", got.FromType)
	}
	if got.Candidates != 0 {
		t.Errorf("candidates = %d, want 0", got.Candidates)
	}
}
