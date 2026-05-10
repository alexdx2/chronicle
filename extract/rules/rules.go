// Package rules maps raw AST syntax facts to semantic architecture facts
// using framework-specific rulesets.
//
// Parser knows syntax. Ruleset knows framework. Resolver knows graph.
package rules

import (
	"encoding/json"

	"github.com/alexdx2/chronicle-core/extract/ast"
)

// SemanticFact is a framework-interpreted fact ready for the graph resolver.
type SemanticFact struct {
	Kind       string   `json:"kind"`
	To         string   `json:"to,omitempty"`
	From       string   `json:"from,omitempty"`
	Symbols    []string `json:"symbols,omitempty"`
	Method     string   `json:"method,omitempty"`
	Target     string   `json:"target,omitempty"`
	Confidence float64  `json:"confidence,omitempty"`
}

// ApplyResult holds the result of applying rulesets to raw AST facts.
type ApplyResult struct {
	Facts    []SemanticFact `json:"facts"`
	FromType string         `json:"from_type"` // controller, module, or "" (provider)
}

// DecoratorRule defines how a decorator maps to semantic meaning.
type DecoratorRule struct {
	SetsFromType    string // "controller", "module", "provider"
	CapturesPrefix  bool   // decorator arg becomes endpoint prefix
	EmitsKind       string // "endpoint", "consumes", "" (nothing)
	EmitsMethod     string // "GET", "POST", "Query", etc.
	TargetFrom      string // "first_string_arg", "method_name", "method_name_if_no_string_arg"
}

// Ruleset is a named collection of decorator rules for a framework.
type Ruleset struct {
	Name       string
	Decorators map[string]DecoratorRule
}

// Registry holds active rulesets.
type Registry struct {
	rulesets []Ruleset
	// merged decorator lookup
	decorators map[string]DecoratorRule
}

// NewRegistry creates a registry with the given rulesets.
func NewRegistry(rulesets ...Ruleset) *Registry {
	r := &Registry{
		rulesets:   rulesets,
		decorators: make(map[string]DecoratorRule),
	}
	// Later rulesets override earlier ones
	for _, rs := range rulesets {
		for name, rule := range rs.Decorators {
			r.decorators[name] = rule
		}
	}
	return r
}

// Apply transforms raw AST facts into semantic facts using active rulesets.
func (r *Registry) Apply(raw *ast.RawResult) *ApplyResult {
	result := &ApplyResult{}
	var controllerPrefix string

	for _, f := range raw.Facts {
		switch f.Kind {
		case "import":
			result.Facts = append(result.Facts, SemanticFact{
				Kind: "import", To: f.To, Symbols: f.Symbols, Confidence: 0.95,
			})

		case "decorator":
			rule, ok := r.decorators[f.Name]
			if !ok {
				continue // unknown decorator — skip
			}

			// Set from_type
			if rule.SetsFromType != "" {
				result.FromType = rule.SetsFromType
			}

			// Capture prefix
			if rule.CapturesPrefix && f.Target != "" {
				controllerPrefix = f.Target
			}

			// Emit semantic fact
			if rule.EmitsKind != "" {
				target := resolveTarget(rule, f)
				sf := SemanticFact{
					Kind:       rule.EmitsKind,
					Method:     rule.EmitsMethod,
					Target:     target,
					Confidence: 0.95,
				}
				if rule.EmitsKind == "consumes" {
					sf.To = target
					sf.Target = ""
				}
				// Apply prefix for endpoint facts
				if rule.EmitsKind == "endpoint" && controllerPrefix != "" {
					sf.From = controllerPrefix
				}
				result.Facts = append(result.Facts, sf)
			}

		case "constructor_param":
			result.Facts = append(result.Facts, SemanticFact{
				Kind: "injects", To: f.To, Confidence: 0.95,
			})

		case "call":
			result.Facts = append(result.Facts, SemanticFact{
				Kind: "call", To: f.To, Method: f.Method, Confidence: 0.95,
			})

		case "member_call":
			result.Facts = append(result.Facts, SemanticFact{
				Kind: "member_call", From: f.From, To: f.To, Method: f.Method, Confidence: 0.95,
			})

		case "produces_candidate":
			result.Facts = append(result.Facts, SemanticFact{
				Kind: "produces", To: f.To, Method: f.Method, Confidence: 0.95,
			})
		}
	}

	return result
}

func resolveTarget(rule DecoratorRule, f ast.RawFact) string {
	switch rule.TargetFrom {
	case "first_string_arg":
		return f.Target
	case "method_name":
		return f.Method
	case "method_name_if_no_string_arg":
		if f.Target != "" {
			return f.Target
		}
		return f.Method
	default:
		return f.Target
	}
}

// FactsJSON returns semantic facts as JSON string.
func (r *ApplyResult) FactsJSON() string {
	if len(r.Facts) == 0 {
		return "[]"
	}
	b, _ := json.Marshal(r.Facts)
	return string(b)
}
