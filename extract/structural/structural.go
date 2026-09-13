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
//   - failed      — the parser could not make sense of the file. Nothing is
//     superseded and the previous contribution stands; the file goes to the
//     semantic queue, because a model may still be able to read it.
//   - unreadable  — the bytes never arrived (the caller could not read the
//     file at all). Also supersedes nothing, but it is a DIFFERENT answer: no
//     amount of re-reading by a model helps, and the honest line is "not read"
//     rather than "failed to parse".
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
//
// It is deliberately NOT "chronicle-ast", which the scan already uses.
// graph.factExtractorID stamps "chronicle-ast" on facts the scan's AST merge
// produced OR merely corroborated — origin "ast+llm" — so those rows carry an
// LLM's reading of the file enriched with AST fields. A structural pass sees
// only syntax and would not re-assert them, and sharing the id would make it
// supersede knowledge it cannot produce. Different id, different contribution,
// each replaceable only by its own writer.
const ExtractorID = "chronicle-structural"

// NonCodeImportSuffixes are import targets that are assets, not code. A
// stylesheet, an icon or a JSON blob is a real dependency of the file, but it
// is not a module with behaviour, and minting a `code:provider` node for it
// fills the graph with leaves nothing can be asked about — the lazy-scan spec
// calls them out by name (§10 follow-up 2).
//
// Deterministic resolution skips them silently: an asset import is not an
// unresolved NAME either, so recording it would put noise in the very list
// that exists to say which files still need a reader.
//
// Kept deliberately small and literal. A pattern general enough to catch every
// asset would also catch code, and the cost of missing one is a leaf node, not
// a wrong edge.
var NonCodeImportSuffixes = []string{
	".css", ".scss", ".sass", ".less", ".module.css",
	".json", ".svg", ".png", ".jpg", ".jpeg", ".gif", ".webp",
	".md", ".txt", ".yaml", ".yml",
}

// IsNonCodeImport reports whether an import specifier names an asset rather
// than a module. Any query or fragment a bundler appends ("./x.svg?raw") is
// dropped first — it changes how the asset is loaded, not what it is.
func IsNonCodeImport(specifier string) bool {
	if i := strings.IndexAny(specifier, "?#"); i >= 0 {
		specifier = specifier[:i]
	}
	specifier = strings.ToLower(specifier)
	for _, suffix := range NonCodeImportSuffixes {
		if strings.HasSuffix(specifier, suffix) {
			return true
		}
	}
	return false
}

// Outcome is what a structural pass concluded about one file.
type Outcome string

const (
	Extracted   Outcome = "extracted"   // N >= 0 facts; zero facts is a valid answer
	Failed      Outcome = "failed"      // parser error; caller keeps the previous contribution
	Unreadable  Outcome = "unreadable"  // the bytes never arrived; keeps the previous contribution too
	Unsupported Outcome = "unsupported" // no extractor for this file; not a structural file
)

// Result is one file's structural answer. The caller knows which file it
// asked about, so the path is not repeated here.
type Result struct {
	Outcome   Outcome
	FromType  string // rules.ApplyResult.FromType, or "schema" for prisma
	FactsJSON string // "[]" when none
	FactCount int
	// ContentHash is the sha256 hex of the content, "" when there was
	// nothing to hash or the file produced no answer.
	ContentHash string
	// CandidatesJSON is the raw AST candidates the rule packs did NOT turn
	// into facts, serialised ("[]" when none). The structural phase ignores
	// them — a candidate is a question for a model, not a structural fact —
	// but the scan hands them to one, and both read the same file with the
	// same parser, so it is this call's job to produce them.
	CandidatesJSON string
	Err            error // set when Outcome is failed or unreadable
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

// parseTypeScript is the tree-sitter call as a variable so the panic guard
// below can be exercised: cgo cannot be made to fault on demand, and an
// untested recover is a recover that silently stops working.
var parseTypeScript = ast.ExtractTypeScript

// failedResult is the ONE shape a non-answer has, for either reason. Every
// field a caller might read is cleared, the content hash included: nothing was
// learned about this file, and recording a hash for it would let the next run
// conclude "same content, already done" and skip it forever.
func failedResult(outcome Outcome, err error) Result {
	return Result{Outcome: outcome, FactsJSON: "[]", CandidatesJSON: "[]", Err: err}
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
	res = Result{Outcome: Unsupported, FactsJSON: "[]", CandidatesJSON: "[]"}
	if !Supported(path) {
		return res
	}
	// A caller that could not read the file passes nil. That is not an empty
	// file: an empty file genuinely asserts nothing, an unreadable one says we
	// do not know — and it is not a parse failure either, so it is its own
	// outcome. A model cannot read bytes that never arrived.
	if content == nil {
		return failedResult(Unreadable, fmt.Errorf("structural: no content for %s", path))
	}
	sum := sha256.Sum256(content)
	res.ContentHash = hex.EncodeToString(sum[:])

	defer func() {
		if r := recover(); r != nil {
			res = failedResult(Failed, fmt.Errorf("structural: parser panic on %s: %v", path, r))
		}
	}()

	if isTypeScript(path) {
		raw := parseTypeScript(content)
		semantic := rules.NewRegistry(rules.RulesetsForTech(tech)...).Apply(raw)
		res.Outcome = Extracted
		res.FromType = semantic.FromType
		res.FactsJSON = semantic.FactsJSON()
		res.FactCount = len(semantic.Facts)
		if len(raw.Candidates) > 0 {
			if cb, err := json.Marshal(raw.Candidates); err == nil {
				res.CandidatesJSON = string(cb)
			}
		}
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
			return failedResult(Failed, fmt.Errorf("structural: encode prisma facts for %s: %w", path, err))
		}
	}
	return res
}
