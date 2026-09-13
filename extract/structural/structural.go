// Package structural is the deterministic half of Chronicle's knowledge: one
// call per file, no model, no network, no judgement.
//
// A structural pass answers exactly one question about a file — "what does its
// syntax say, under this project's rule packs" — and its answer REPLACES the
// previous answer for the same (file, extractor) pair. That contract is why the
// three outcomes below are a closed set and why the difference between them
// matters more than the facts:
//
//   - extracted   — the file was parsed; N >= 0 facts. Zero facts is an answer
//     (a type-only file asserts nothing) and supersedes whatever the pair used
//     to assert.
//   - failed      — the file could not be looked at. Nothing is superseded and
//     the previous contribution stands; the file goes to the semantic queue.
//   - unsupported — no deterministic extractor for this extension. Not a
//     structural file at all; it is not knowledge that went missing.
//
// The scan pipeline still carries its own copy of this per-file logic
// (graph/scan_workflow.go); this package is the version the refresh phase
// calls, and the two should be collapsed once the scan can be moved.
package structural

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/alexdx2/chronicle-core/extract/ast"
	"github.com/alexdx2/chronicle-core/extract/prisma"
	"github.com/alexdx2/chronicle-core/extract/rules"
)

// ExtractorID is the extractor_id every row this package justifies carries.
// Superseding is scoped to (file_path, extractor_id), so this string is the
// boundary between what a structural pass may replace and what belongs to the
// agent or to an importer.
const ExtractorID = "chronicle-ast"

// Outcome is what a structural pass concluded about one file.
type Outcome string

const (
	Extracted   Outcome = "extracted"   // N >= 0 facts; zero facts is a valid answer
	Failed      Outcome = "failed"      // parser error; caller keeps the previous contribution
	Unsupported Outcome = "unsupported" // no extractor for this file; not a structural file
)

// Result is one file's structural answer.
type Result struct {
	Path        string
	Outcome     Outcome
	FromType    string // rules.ApplyResult.FromType, or "schema" for prisma
	FactsJSON   string // "[]" when none
	FactCount   int
	ContentHash string // sha256 hex of content ("" when there was no content to hash)
	Candidates  int    // raw candidates left for the LLM (not written by this track)
	Err         error  // set when Outcome == Failed
}

// Supported reports whether path has a deterministic extractor: tree-sitter +
// rule packs for TypeScript/JavaScript, the Prisma reader for schemas.
// Extractors that only emit candidates are deliberately not here — a candidate
// is a question for a model, not a structural fact.
func Supported(path string) bool {
	switch {
	case isTypeScript(path):
		return true
	case strings.HasSuffix(path, ".prisma"):
		return true
	}
	return false
}

// isTypeScript covers the four extensions the TypeScript grammar reads. The
// grammar is a superset of JavaScript, so .js/.jsx parse with it.
func isTypeScript(path string) bool {
	return strings.HasSuffix(path, ".ts") || strings.HasSuffix(path, ".tsx") ||
		strings.HasSuffix(path, ".js") || strings.HasSuffix(path, ".jsx")
}

// ExtractFile runs the deterministic extractor for path on content with the
// tech's rule packs.
//
// It never panics. tree-sitter is cgo and a malformed input that trips the
// grammar takes the whole process with it otherwise — and this runs inside a
// git hook, where a crash is indistinguishable from a broken install. A panic
// becomes Outcome Failed, which is the honest answer: the file could not be
// looked at, so nothing it used to assert may be dropped.
func ExtractFile(path string, content []byte, tech []string) (res Result) {
	res = Result{Path: path, Outcome: Unsupported, FactsJSON: "[]"}
	if !Supported(path) {
		return res
	}
	// A caller that could not read the file passes nil. That is a failure, not
	// an empty file: an empty file genuinely asserts nothing, an unreadable one
	// asserts we do not know.
	if content == nil {
		res.Outcome = Failed
		res.Err = fmt.Errorf("structural: no content for %s", path)
		return res
	}
	sum := sha256.Sum256(content)
	res.ContentHash = hex.EncodeToString(sum[:])

	defer func() {
		if r := recover(); r != nil {
			res.Outcome = Failed
			res.FromType = ""
			res.FactsJSON = "[]"
			res.FactCount = 0
			res.Candidates = 0
			res.Err = fmt.Errorf("structural: parser panic on %s: %v", path, r)
		}
	}()

	if isTypeScript(path) {
		raw := ast.ExtractTypeScript(content)
		semantic := rules.NewRegistry(rules.RulesetsForTech(tech)...).Apply(raw)
		res.Outcome = Extracted
		res.FromType = semantic.FromType
		res.FactsJSON = semantic.FactsJSON()
		res.FactCount = len(semantic.Facts)
		res.Candidates = len(raw.Candidates)
		return res
	}

	// Prisma: a schema is structure by definition — models, enums and the
	// relations between them are fixed by construction, with nothing left to
	// infer and no candidates to hand a model.
	p := prisma.Extract(content)
	facts := make([]map[string]any, 0, len(p.Models)+len(p.Enums)+len(p.Relations))
	for _, m := range p.Models {
		facts = append(facts, map[string]any{
			"kind": "model", "name": m.Name, "file_path": path, "line": m.Line,
		})
	}
	for _, e := range p.Enums {
		facts = append(facts, map[string]any{
			"kind": "enum", "name": e.Name, "file_path": path, "line": e.Line,
		})
	}
	for _, r := range p.Relations {
		facts = append(facts, map[string]any{
			"kind": "model_relation", "from": r.From, "to": r.To, "field_name": r.FieldName,
		})
	}
	res.Outcome = Extracted
	res.FromType = "schema"
	res.FactCount = len(facts)
	res.FactsJSON = "[]"
	if len(facts) > 0 {
		if b, err := json.Marshal(facts); err == nil {
			res.FactsJSON = string(b)
		} else {
			// Unserialisable facts are not facts we can hand anyone.
			res.Outcome = Failed
			res.FromType = ""
			res.FactCount = 0
			res.Err = fmt.Errorf("structural: encode prisma facts for %s: %w", path, err)
		}
	}
	return res
}
