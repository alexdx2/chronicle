package graph

import (
	"encoding/json"
	"strings"

	"github.com/alexdx2/chronicle-core/extract/ast"
	"github.com/alexdx2/chronicle-core/extract/deterministic"
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

			// AST classification from decorators is ground truth — it wins over the LLM's
			// from_type when there is a conflict.  LLMs mislabel gateways (@WebSocketGateway
			// → "provider", not "controller") and modules.  Only keep the LLM's value when
			// the AST has no opinion (semantic.FromType == "").
			if semantic.FromType != "" {
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

	// --- C# / .NET — deterministic regex extraction (no tree-sitter grammar yet) ---
	lower := strings.ToLower(filePath)
	if strings.HasSuffix(lower, ".cs") || strings.HasSuffix(lower, ".csproj") {
		content := readFileContent(filePath)
		if content != nil {
			cands := deterministic.ExtractCandidates(filePath, content)
			var detFacts []map[string]any
			detTopics := map[string]bool{}
			detFromType := ""
			for _, c := range cands {
				if c.Kind == "injects" {
					// AddScoped<I,Impl> registrations are DI wiring hints for the
					// agent, not constructor injection — never merge as facts.
					continue
				}
				f := map[string]any{"kind": c.Kind}
				if c.To != "" {
					f["to"] = c.To
				}
				if c.Method != "" {
					f["method"] = c.Method
				}
				if c.Target != "" {
					f["target"] = c.Target
				}
				if c.Kind == "consumes" {
					detTopics[strings.ToLower(c.To)] = true
				}
				if detFromType == "" && c.FromType != "" {
					detFromType = c.FromType
				}
				detFacts = append(detFacts, f)
			}
			// Subscribe("topic") strings are the broker truth. LLMs often emit the
			// deserialized payload class ("BattleResultEvent") as the consumes
			// target — drop LLM consumes facts that don't match a deterministic
			// topic when hard Subscribe evidence exists.
			if len(detTopics) > 0 {
				filtered := llmFacts[:0]
				for _, f := range llmFacts {
					if kind, _ := f["kind"].(string); kind == "consumes" {
						to, _ := f["to"].(string)
						if !detTopics[strings.ToLower(to)] {
							continue
						}
					}
					filtered = append(filtered, f)
				}
				llmFacts = filtered
			}
			if fromType == "" && detFromType != "" {
				fromType = detFromType
			}
			// DbContext subclasses are injectable providers, not entity files —
			// deterministic override regardless of the LLM's from_type guess.
			if strings.Contains(string(content), ": DbContext") {
				fromType = "provider"
			}
			// Entity files: when every substantive fact is a data definition the
			// file IS a model file, whatever the LLM guessed. DbContext classes
			// stay providers (they are injectable), detected by base class.
			if len(llmFacts) > 0 && !strings.Contains(string(content), "DbContext") {
				allData := true
				for _, f := range llmFacts {
					k, _ := f["kind"].(string)
					if k != "model" && k != "enum" && k != "model_relation" && k != "parent" {
						allData = false
						break
					}
				}
				if allData {
					fromType = "model"
				}
			}
			mergedFacts = unionFacts(detFacts, llmFacts)
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
	// method is identity only where it disambiguates (GET vs POST /x, call sites);
	// for topic/eventing kinds the topic name IS the identity — AST and LLM
	// record different method names for the same emit/handler.
	if k != "endpoint" && k != "call" && k != "http_call" && k != "calls_endpoint" {
		method = ""
	}
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
//
// R7d: when an LLM fact wins over a duplicate AST fact, fields that the LLM fact
// lacks but the AST fact has are copied over. This preserves deterministic fields
// like "transport" (tagged by AST rules) that LLMs often omit. Without this, an
// LLM "consumes battle.result" (no transport) would beat an AST "consumes
// battle.result [transport=local]", and the local-skip guard would never fire.
func unionFacts(astFacts, llmFacts []map[string]any) []map[string]any {
	// Index LLM facts by dedup key for fast lookup.
	llmByKey := make(map[string]int, len(llmFacts)) // key → index in llmFacts
	for i, f := range llmFacts {
		llmByKey[factKey(f)] = i
	}

	// For each AST fact that matches an LLM fact, copy AST-only fields to the LLM fact.
	for _, astF := range astFacts {
		idx, dup := llmByKey[factKey(astF)]
		if !dup {
			continue
		}
		llmF := llmFacts[idx]
		// Copy fields present in AST but absent in LLM.
		for _, field := range []string{"transport", "to_type"} {
			if astVal, ok := astF[field]; ok {
				if astVal != nil && astVal != "" {
					if _, hasInLLM := llmF[field]; !hasInLLM {
						llmF[field] = astVal
					} else if s, ok := llmF[field].(string); ok && s == "" {
						llmF[field] = astVal
					}
				}
			}
		}
		llmFacts[idx] = llmF
	}

	var result []map[string]any
	// Add AST facts that don't have a matching LLM fact.
	llmKeys := make(map[string]bool, len(llmFacts))
	for _, f := range llmFacts {
		llmKeys[factKey(f)] = true
	}
	for _, f := range astFacts {
		if !llmKeys[factKey(f)] {
			result = append(result, f)
		}
	}
	// Append all LLM facts (they always win on overlap, now enriched with AST-only fields).
	result = append(result, llmFacts...)
	return result
}
