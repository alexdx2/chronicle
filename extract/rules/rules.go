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
	ToType     string   `json:"to_type,omitempty"`  // node type of target (controller, provider, etc.)
	From       string   `json:"from,omitempty"`
	Symbols    []string `json:"symbols,omitempty"`
	Method     string   `json:"method,omitempty"`
	Target     string   `json:"target,omitempty"`
	Transport  string   `json:"transport,omitempty"` // "queue", "local", "kafka", etc.
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
	EmitsKind       string // "endpoint", "consumes", "produces", "" (nothing)
	EmitsMethod     string // "GET", "POST", "Query", etc.
	TargetFrom      string // "first_string_arg", "method_name", "method_name_if_no_string_arg"
	TransportTag    string // transport value to attach to emitted fact ("queue", "local", etc.)
	ResolveIdent    bool   // if true, resolve TargetIdent via StringConsts when Target is empty
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

// localEmitterTypes is the set of known in-process event emitter type names.
// Receivers whose injected type matches one of these are tagged transport=local.
var localEmitterTypes = map[string]bool{
	"EventEmitter2": true,
	"EventEmitter":  true,
}

// Apply transforms raw AST facts into semantic facts using active rulesets.
func (r *Registry) Apply(raw *ast.RawResult) *ApplyResult {
	result := &ApplyResult{}
	var controllerPrefix string

	// Pre-pass: build paramName → injectedType map from constructor_param facts.
	// Used to determine the transport type for produces_candidate calls.
	paramTypes := map[string]string{}
	for _, f := range raw.Facts {
		if f.Kind == "constructor_param" && f.To != "" {
			// constructor_param facts carry the type name in To; we need the param name.
			// The param name is NOT in the current RawFact — we use the injected type set
			// to identify known emitter receivers when From is provided on produces_candidate.
			// Store all injected types as potential local emitters (checked via localEmitterTypes).
			paramTypes[f.To] = f.To
		}
	}

	// Build a reverse map: paramName → type — not directly available from constructor_param
	// (which only carries the type). Use a separate scan: for each injects fact, if the
	// injected type is a local emitter, mark any produces_candidate with a From that could
	// be a camelCase variant of that type. This is heuristic but correct for EventEmitter2.
	hasLocalEmitter := false
	for typeName := range paramTypes {
		if localEmitterTypes[typeName] {
			hasLocalEmitter = true
			break
		}
	}

	// Detect @WebSocketServer() decorator presence — this marks the file as a WS gateway.
	// socket.io server.emit() calls are client pushes, not broker publications.
	// When @WebSocketServer is present, all produces_candidate with method=emit are
	// tagged transport=local regardless of receiver name (the receiver IS the WS server).
	hasWebSocketServer := false
	for _, f := range raw.Facts {
		if f.Kind == "decorator" && f.Name == "WebSocketServer" {
			hasWebSocketServer = true
			break
		}
	}

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
				// Resolve identifier arg via StringConsts when no string literal was captured.
				resolvedFact := f
				if rule.ResolveIdent && resolvedFact.Target == "" && resolvedFact.TargetIdent != "" {
					if val, ok := raw.StringConsts[resolvedFact.TargetIdent]; ok {
						resolvedFact.Target = val
					}
				}

				target := resolveTarget(rule, resolvedFact)
				sf := SemanticFact{
					Kind:       rule.EmitsKind,
					Method:     rule.EmitsMethod,
					Target:     target,
					Transport:  rule.TransportTag,
					Confidence: 0.95,
				}
				if rule.EmitsKind == "consumes" || rule.EmitsKind == "produces" {
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
			transport := ""
			// If the file injects a local event emitter and the method is "emit", tag as local.
			if hasLocalEmitter && f.Method == "emit" {
				transport = "local"
			}
			// If the file declares a @WebSocketServer field, all emit() calls are
			// socket.io client pushes — not message broker publications. Tag as local
			// so the resolver does not mint a contract:topic node for them.
			if hasWebSocketServer && f.Method == "emit" {
				transport = "local"
			}
			result.Facts = append(result.Facts, SemanticFact{
				Kind: "produces", To: f.To, Method: f.Method, Transport: transport, Confidence: 0.95,
			})

		case "module_member":
			// Emit provides facts for @Module controllers/providers arrays.
			// The @Module decorator rule fires earlier in the loop (decorators are
			// extracted before module_member facts), so result.FromType == "module"
			// by the time we reach these facts. A post-pass below drops provides
			// facts from non-module files as a safety guard.
			toType := "provider"
			if f.From == "controllers" {
				toType = "controller"
			}
			result.Facts = append(result.Facts, SemanticFact{
				Kind:       "provides",
				To:         f.To,
				ToType:     toType,
				Confidence: 0.95,
			})
		}
	}

	// Post-pass: strip provides facts emitted from non-module files.
	// If AST did not classify this file as a module, drop all provides facts —
	// they come from template noise rather than real @Module declarations.
	if result.FromType != "module" {
		var filtered []SemanticFact
		for _, sf := range result.Facts {
			if sf.Kind != "provides" {
				filtered = append(filtered, sf)
			}
		}
		result.Facts = filtered
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
