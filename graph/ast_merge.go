package graph

import (
	"encoding/json"
	"strings"

	"github.com/alexdx2/chronicle-core/extract/ast"
	"github.com/alexdx2/chronicle-core/extract/prisma"
	"github.com/alexdx2/chronicle-core/extract/rules"
)

// MergeASTFacts computes server-side AST semantic facts for filePath and unions
// them with the LLM-supplied facts from the artifact. It returns the merged
// facts JSON string and the detected from_type (may be "" if AST finds none).
//
// The merge strategy:
//   - Run tree-sitter AST + rules for TypeScript files, prisma extractor for .prisma.
//   - Deduplicate: a fact is a duplicate when kind + lowercased "to"/"target" + method all match.
//   - On duplicate, keep the LLM fact (richer fields like to_type/symbols).
//   - If llmFromType is empty and AST detected one, use the AST from_type.
//
// tech is the manifest tech list; nil/empty falls back to DefaultRulesets (NestJS+GraphQL+WebSocket).
func MergeASTFacts(filePath string, llmFacts []map[string]any, llmFromType string, tech []string) (mergedFacts []map[string]any, fromType string) {
	fromType = llmFromType

	// --- TypeScript ---
	if isTypeScriptFile(filePath) {
		content := readFileContent(filePath)
		if content != nil {
			ruleRegistry := rules.NewRegistry(rules.RulesetsForTech(tech)...)
			rawResult := ast.ExtractTypeScript(content)
			semantic := ruleRegistry.Apply(rawResult)

			astFacts := parseFacts(semantic.FactsJSON())

			if fromType == "" && semantic.FromType != "" {
				fromType = semantic.FromType
			}

			mergedFacts = unionFacts(astFacts, llmFacts)
			return mergedFacts, fromType
		}
	}

	// --- Prisma ---
	if strings.HasSuffix(filePath, ".prisma") {
		content := readFileContent(filePath)
		if content != nil {
			prismaResult := prisma.Extract(content)
			var astFacts []map[string]any
			for _, m := range prismaResult.Models {
				astFacts = append(astFacts, map[string]any{
					"kind": "model", "name": m.Name,
					"file_path": filePath, "line": m.Line,
				})
			}
			for _, e := range prismaResult.Enums {
				astFacts = append(astFacts, map[string]any{
					"kind": "enum", "name": e.Name,
					"file_path": filePath, "line": e.Line,
				})
			}
			for _, r := range prismaResult.Relations {
				astFacts = append(astFacts, map[string]any{
					"kind": "model_relation", "from": r.From,
					"to": r.To, "field_name": r.FieldName,
				})
			}
			if len(astFacts) > 0 && fromType == "" {
				fromType = "schema"
			}
			mergedFacts = unionFacts(astFacts, llmFacts)
			return mergedFacts, fromType
		}
	}

	// Not a file we compute AST for — return LLM facts unchanged.
	return llmFacts, fromType
}

// MergeASTFactsJSON is the string-in/string-out variant used by commit handlers.
// llmFactsJSON is the raw JSON string from the artifact ("[]" or valid JSON array).
// Returns merged facts as a JSON string and the resolved from_type.
func MergeASTFactsJSON(filePath, llmFactsJSON, llmFromType string, tech []string) (string, string) {
	llmFacts := parseFacts(llmFactsJSON)
	merged, fromType := MergeASTFacts(filePath, llmFacts, llmFromType, tech)
	if len(merged) == 0 {
		return llmFactsJSON, fromType
	}
	b, err := json.Marshal(merged)
	if err != nil {
		return llmFactsJSON, fromType
	}
	return string(b), fromType
}

// parseFacts decodes a JSON array of fact objects. Returns nil on failure/empty.
func parseFacts(factsJSON string) []map[string]any {
	if factsJSON == "" || factsJSON == "[]" || factsJSON == "null" {
		return nil
	}
	var facts []map[string]any
	if err := json.Unmarshal([]byte(factsJSON), &facts); err != nil {
		return nil
	}
	return facts
}

// factKey returns a dedup key for a fact: "kind|to|method" (all lowercased).
func factKey(f map[string]any) string {
	kind, _ := f["kind"].(string)
	val, _ := f["to"].(string)
	if val == "" {
		val, _ = f["target"].(string)
	}
	if val == "" {
		val, _ = f["name"].(string)
	}
	method, _ := f["method"].(string)
	k := strings.ToLower(kind)
	v := strings.ToLower(val)
	if k == "endpoint" {
		// AST emits route fragments ("status"), LLMs emit full paths
		// ("/jerry/status") — dedupe on the trailing segment within a file.
		v = strings.Trim(v, "/")
		if i := strings.LastIndex(v, "/"); i >= 0 {
			v = v[i+1:]
		}
	}
	return k + "|" + v + "|" + strings.ToLower(method)
}

// unionFacts merges AST facts with LLM facts, keeping LLM facts on duplicate.
// Result order: non-duplicate AST facts first, then all LLM facts.
func unionFacts(astFacts, llmFacts []map[string]any) []map[string]any {
	// Index LLM facts by dedup key so we can check for duplicates quickly.
	llmKeys := make(map[string]bool, len(llmFacts))
	for _, f := range llmFacts {
		llmKeys[factKey(f)] = true
	}

	var result []map[string]any
	// Add AST facts that don't have a matching LLM fact.
	for _, f := range astFacts {
		if !llmKeys[factKey(f)] {
			result = append(result, f)
		}
	}
	// Append all LLM facts (they always win on overlap).
	result = append(result, llmFacts...)
	return result
}
