// Package ast provides tree-sitter based extraction for TypeScript files.
// Emits raw syntax facts only — no framework semantics.
// Framework meaning is applied by extract/rules.
package ast

import (
	"crypto/sha1"
	"encoding/json"
	"fmt"
	"strings"

	sitter "github.com/tree-sitter/go-tree-sitter"
	tree_sitter_typescript "github.com/tree-sitter/tree-sitter-typescript/bindings/go"
)

// RawFact is a syntax-level fact from AST analysis. No framework interpretation.
type RawFact struct {
	Kind       string   `json:"kind"`                  // import, decorator, constructor_param, call, member_call, produces_candidate
	Name       string   `json:"name,omitempty"`        // decorator name, class name
	To         string   `json:"to,omitempty"`          // import path, type name
	From       string   `json:"from,omitempty"`        // object in member_call chain
	Symbols    []string `json:"symbols,omitempty"`     // imported symbols
	Method     string   `json:"method,omitempty"`      // called method, decorated method
	Args       string   `json:"args,omitempty"`        // raw decorator arguments text
	Target     string   `json:"target,omitempty"`      // first string arg from decorator
	TargetKind string   `json:"target_kind,omitempty"` // what the decorator is on: class, method
}

// Candidate is an ambiguous code anchor that needs LLM classification.
// AST finds the code location, LLM decides what it means architecturally.
type Candidate struct {
	ID             string `json:"id"`                        // stable: filepath:kind:code_hash
	Kind           string `json:"kind"`                     // call, member_call, fetch_call, emit_call, env_access
	Code           string `json:"code"`                     // source text of the expression
	CodeHash       string `json:"code_hash"`                // sha1 of normalized code
	Line           int    `json:"line"`
	Receiver       string `json:"receiver,omitempty"`       // object being called
	Method         string `json:"method,omitempty"`         // method name
	Context        string `json:"context,omitempty"`        // surrounding class/function name
	ResolvedType   string `json:"resolved_type,omitempty"`  // PascalCase type from constructor params
	ReceiverOrigin string `json:"receiver_origin,omitempty"` // "constructor_param", "import", "local"
	IsArchitectural bool  `json:"is_architectural,omitempty"` // true if receiver is a known DI dependency
}

// RawResult holds raw extraction results for one file.
type RawResult struct {
	Facts      []RawFact   `json:"facts"`
	Candidates []Candidate `json:"candidates,omitempty"`
}

// noise: method calls to skip
var noiseMethods = map[string]bool{
	"push": true, "pop": true, "shift": true, "unshift": true,
	"map": true, "filter": true, "reduce": true, "find": true, "some": true, "every": true,
	"forEach": true, "slice": true, "splice": true, "sort": true, "reverse": true,
	"includes": true, "indexOf": true, "join": true, "flat": true, "flatMap": true,
	"log": true, "error": true, "warn": true, "info": true, "debug": true,
	"then": true, "catch": true, "finally": true,
	"pipe": true, "tap": true,
	"toString": true, "valueOf": true, "keys": true, "values": true, "entries": true,
	"parse": true, "stringify": true, "assign": true,
	"get": true, "set": true, "has": true, "delete": true,
}

// ExtractTypeScript parses a TypeScript file and returns raw syntax facts.
func ExtractTypeScript(fileContent []byte) *RawResult {
	parser := sitter.NewParser()
	defer parser.Close()

	lang := sitter.NewLanguage(tree_sitter_typescript.LanguageTypescript())
	parser.SetLanguage(lang)

	tree := parser.Parse(fileContent, nil)
	defer tree.Close()
	root := tree.RootNode()

	result := &RawResult{}

	// 1. Imports (local only)
	result.Facts = append(result.Facts, extractImportFacts(root, fileContent, lang)...)

	// 2. Decorators (raw — name, args, target method)
	result.Facts = append(result.Facts, extractRawDecorators(root, fileContent, lang)...)

	// 3. Constructor parameters (type annotations)
	result.Facts = append(result.Facts, extractConstructorParams(root, fileContent, lang)...)

	// 4. this.x.method() calls (2-level)
	result.Facts = append(result.Facts, extractCallFacts(root, fileContent, lang)...)

	// 5. this.x.y.method() calls (3-level member chains)
	result.Facts = append(result.Facts, extractMemberChainFacts(root, fileContent, lang)...)

	// 6. .emit/.publish/.send with literal string arg
	result.Facts = append(result.Facts, extractProducerCandidates(root, fileContent, lang)...)

	// 7. Extract enrichment candidates — ambiguous code anchors for LLM classification
	result.Candidates = extractEnrichmentCandidates(root, fileContent, lang)

	return result
}

// FactsJSON returns facts as JSON string.
func (r *RawResult) FactsJSON() string {
	if len(r.Facts) == 0 {
		return "[]"
	}
	b, _ := json.Marshal(r.Facts)
	return string(b)
}

// --- Imports ---

func extractImportFacts(root *sitter.Node, src []byte, lang *sitter.Language) []RawFact {
	q, err := sitter.NewQuery(lang, `(import_statement source: (string (string_fragment) @source)) @import`)
	if err != nil {
		return nil
	}
	defer q.Close()
	cursor := sitter.NewQueryCursor()
	defer cursor.Close()

	var facts []RawFact
	matches := cursor.Matches(q, root, src)
	for m := matches.Next(); m != nil; m = matches.Next() {
		var module string
		var importNode *sitter.Node
		for _, c := range m.Captures {
			switch q.CaptureNames()[c.Index] {
			case "source":
				module = c.Node.Utf8Text(src)
			case "import":
				n := c.Node
				importNode = &n
			}
		}
		if module == "" || importNode == nil {
			continue
		}
		// Only local imports
		if !strings.HasPrefix(module, ".") {
			continue
		}
		symbols := astExtractSymbols(importNode, src)
		facts = append(facts, RawFact{Kind: "import", To: module, Symbols: symbols})
	}
	return facts
}

// --- Decorators (raw) ---

func extractRawDecorators(root *sitter.Node, src []byte, lang *sitter.Language) []RawFact {
	q, err := sitter.NewQuery(lang, `(decorator (call_expression function: (identifier) @name arguments: (arguments) @args)) @decorator`)
	if err != nil {
		return nil
	}
	defer q.Close()
	cursor := sitter.NewQueryCursor()
	defer cursor.Close()

	var facts []RawFact
	matches := cursor.Matches(q, root, src)
	for m := matches.Next(); m != nil; m = matches.Next() {
		var name, args string
		var decoratorNode *sitter.Node
		for _, c := range m.Captures {
			switch q.CaptureNames()[c.Index] {
			case "name":
				name = c.Node.Utf8Text(src)
			case "args":
				args = c.Node.Utf8Text(src)
			case "decorator":
				n := c.Node
				decoratorNode = &n
			}
		}

		// Determine what the decorator is on (class or method)
		targetKind, targetName := findDecoratorTarget(decoratorNode, src)

		facts = append(facts, RawFact{
			Kind:       "decorator",
			Name:       name,
			Args:       args,
			Target:     extractStringArg(args),
			TargetKind: targetKind,
			Method:     targetName,
		})
	}
	return facts
}

// findDecoratorTarget finds what a decorator is applied to.
// Handles stacked decorators by skipping past sibling decorators.
func findDecoratorTarget(decoratorNode *sitter.Node, src []byte) (kind string, name string) {
	if decoratorNode == nil {
		return "", ""
	}
	// Walk past stacked decorator siblings to find the actual target
	next := decoratorNode.NextSibling()
	for next != nil && next.Kind() == "decorator" {
		next = next.NextSibling()
	}
	if next == nil {
		return "", ""
	}
	switch next.Kind() {
	case "method_definition":
		nameNode := next.ChildByFieldName("name")
		if nameNode != nil {
			return "method", nameNode.Utf8Text(src)
		}
		return "method", ""
	case "public_field_definition":
		nameNode := next.ChildByFieldName("name")
		if nameNode != nil {
			return "field", nameNode.Utf8Text(src)
		}
		return "field", ""
	case "class_declaration":
		nameNode := next.ChildByFieldName("name")
		if nameNode != nil {
			return "class", nameNode.Utf8Text(src)
		}
		return "class", ""
	}
	return "", ""
}

// --- Constructor parameters ---

func extractConstructorParams(root *sitter.Node, src []byte, lang *sitter.Language) []RawFact {
	q, err := sitter.NewQuery(lang, `(method_definition name: (property_identifier) @name parameters: (formal_parameters) @params)`)
	if err != nil {
		return nil
	}
	defer q.Close()
	cursor := sitter.NewQueryCursor()
	defer cursor.Close()

	var facts []RawFact
	matches := cursor.Matches(q, root, src)
	for m := matches.Next(); m != nil; m = matches.Next() {
		var methodName string
		var paramsNode *sitter.Node
		for _, c := range m.Captures {
			switch q.CaptureNames()[c.Index] {
			case "name":
				methodName = c.Node.Utf8Text(src)
			case "params":
				n := c.Node
				paramsNode = &n
			}
		}
		if methodName != "constructor" || paramsNode == nil {
			continue
		}
		astWalkNode(paramsNode, func(n *sitter.Node) {
			if n.Kind() == "type_annotation" {
				typeNode := n.Child(1)
				if typeNode != nil {
					typeName := typeNode.Utf8Text(src)
					if typeName != "" && typeName[0] >= 'A' && typeName[0] <= 'Z' {
						facts = append(facts, RawFact{Kind: "constructor_param", To: typeName})
					}
				}
			}
		})
	}
	return facts
}

// --- Method calls (this.x.method) ---

func extractCallFacts(root *sitter.Node, src []byte, lang *sitter.Language) []RawFact {
	q, err := sitter.NewQuery(lang, `
		(call_expression
			function: (member_expression
				object: (member_expression
					object: (this)
					property: (property_identifier) @object
				)
				property: (property_identifier) @method
			)
		)
	`)
	if err != nil {
		return nil
	}
	defer q.Close()
	cursor := sitter.NewQueryCursor()
	defer cursor.Close()

	var facts []RawFact
	seen := map[string]bool{}
	matches := cursor.Matches(q, root, src)
	for m := matches.Next(); m != nil; m = matches.Next() {
		var obj, method string
		for _, c := range m.Captures {
			switch q.CaptureNames()[c.Index] {
			case "object":
				obj = c.Node.Utf8Text(src)
			case "method":
				method = c.Node.Utf8Text(src)
			}
		}
		if noiseMethods[method] {
			continue
		}
		key := obj + "." + method
		if !seen[key] {
			seen[key] = true
			facts = append(facts, RawFact{Kind: "call", To: obj, Method: method})
		}
	}
	return facts
}

// --- 3-level member chains (this.X.Y.method) ---

func extractMemberChainFacts(root *sitter.Node, src []byte, lang *sitter.Language) []RawFact {
	q, err := sitter.NewQuery(lang, `
		(call_expression
			function: (member_expression
				object: (member_expression
					object: (member_expression
						object: (this)
						property: (property_identifier) @object
					)
					property: (property_identifier) @property
				)
				property: (property_identifier) @method
			)
		)
	`)
	if err != nil {
		return nil
	}
	defer q.Close()
	cursor := sitter.NewQueryCursor()
	defer cursor.Close()

	var facts []RawFact
	seen := map[string]bool{}
	matches := cursor.Matches(q, root, src)
	for m := matches.Next(); m != nil; m = matches.Next() {
		var object, property, method string
		for _, c := range m.Captures {
			switch q.CaptureNames()[c.Index] {
			case "object":
				object = c.Node.Utf8Text(src)
			case "property":
				property = c.Node.Utf8Text(src)
			case "method":
				method = c.Node.Utf8Text(src)
			}
		}
		if noiseMethods[method] || noiseMethods[property] {
			continue
		}
		key := object + "." + property + "." + method
		if !seen[key] {
			seen[key] = true
			facts = append(facts, RawFact{Kind: "member_call", From: object, To: property, Method: method})
		}
	}
	return facts
}

// --- Producer candidates (.emit/.publish/.send with string arg) ---

func extractProducerCandidates(root *sitter.Node, src []byte, lang *sitter.Language) []RawFact {
	q, err := sitter.NewQuery(lang, `
		(call_expression
			function: (member_expression
				property: (property_identifier) @method
			)
			arguments: (arguments (string (string_fragment) @topic))
		)
	`)
	if err != nil {
		return nil
	}
	defer q.Close()
	cursor := sitter.NewQueryCursor()
	defer cursor.Close()

	var facts []RawFact
	matches := cursor.Matches(q, root, src)
	for m := matches.Next(); m != nil; m = matches.Next() {
		var method, topic string
		for _, c := range m.Captures {
			switch q.CaptureNames()[c.Index] {
			case "method":
				method = c.Node.Utf8Text(src)
			case "topic":
				topic = c.Node.Utf8Text(src)
			}
		}
		if (method == "emit" || method == "publish" || method == "send") && topic != "" {
			facts = append(facts, RawFact{Kind: "produces_candidate", To: topic, Method: method})
		}
	}
	return facts
}

// --- Enrichment candidates ---

// buildConstructorParamMap scans the tree for constructor methods and returns
// a map of paramName -> typeName (e.g. "orderService" -> "OrderService").
func buildConstructorParamMap(root *sitter.Node, src []byte, lang *sitter.Language) map[string]string {
	paramMap := map[string]string{}
	q, err := sitter.NewQuery(lang, `(method_definition name: (property_identifier) @name parameters: (formal_parameters) @params)`)
	if err != nil {
		return paramMap
	}
	defer q.Close()
	cursor := sitter.NewQueryCursor()
	defer cursor.Close()

	matches := cursor.Matches(q, root, src)
	for m := matches.Next(); m != nil; m = matches.Next() {
		var methodName string
		var paramsNode *sitter.Node
		for _, c := range m.Captures {
			switch q.CaptureNames()[c.Index] {
			case "name":
				methodName = c.Node.Utf8Text(src)
			case "params":
				n := c.Node
				paramsNode = &n
			}
		}
		if methodName != "constructor" || paramsNode == nil {
			continue
		}
		// Walk required_parameter children to extract name and type
		astWalkNode(paramsNode, func(n *sitter.Node) {
			if n.Kind() != "required_parameter" && n.Kind() != "public_field_definition" {
				return
			}
			var paramName, typeName string
			// Find the parameter name (identifier or shorthand_property_identifier)
			for i := uint(0); i < n.ChildCount(); i++ {
				child := n.Child(i)
				if child == nil {
					continue
				}
				switch child.Kind() {
				case "identifier":
					paramName = child.Utf8Text(src)
				case "type_annotation":
					typeNode := child.Child(1)
					if typeNode != nil {
						typeName = typeNode.Utf8Text(src)
					}
				}
			}
			if paramName != "" && typeName != "" && typeName[0] >= 'A' && typeName[0] <= 'Z' {
				paramMap[paramName] = typeName
			}
		})
	}
	return paramMap
}

// findParentClassName walks up from a node to find the enclosing class_declaration name.
func findParentClassName(node *sitter.Node, src []byte) string {
	for p := node.Parent(); p != nil; p = p.Parent() {
		if p.Kind() == "class_declaration" {
			nameNode := p.ChildByFieldName("name")
			if nameNode != nil {
				return nameNode.Utf8Text(src)
			}
			return ""
		}
	}
	return ""
}

// extractEnrichmentCandidates finds ambiguous code anchors for LLM classification.
// These are places where AST sees a syntactic pattern but can't determine architectural meaning.
func extractEnrichmentCandidates(root *sitter.Node, src []byte, lang *sitter.Language, filePath ...string) []Candidate {
	var candidates []Candidate
	fp := ""
	if len(filePath) > 0 {
		fp = filePath[0]
	}

	// Build constructor param map for type resolution
	ctorParams := buildConstructorParamMap(root, src, lang)

	// 1. this.X.method() calls — potential service calls, model access, etc.
	q1, err := sitter.NewQuery(lang, `
		(call_expression
			function: (member_expression
				object: (member_expression
					object: (this)
					property: (property_identifier) @object
				)
				property: (property_identifier) @method
			)
		) @call
	`)
	if err == nil {
		defer q1.Close()
		cursor := sitter.NewQueryCursor()
		defer cursor.Close()
		seen := map[string]bool{}
		matches := cursor.Matches(q1, root, src)
		for m := matches.Next(); m != nil; m = matches.Next() {
			var obj, method, code string
			var line int
			var callNode sitter.Node
			for _, c := range m.Captures {
				switch q1.CaptureNames()[c.Index] {
				case "object":
					obj = c.Node.Utf8Text(src)
				case "method":
					method = c.Node.Utf8Text(src)
				case "call":
					code = strings.TrimSpace(c.Node.Utf8Text(src))
					line = int(c.Node.StartPosition().Row) + 1
					callNode = c.Node
				}
			}
			if noiseMethods[method] || obj == "" {
				continue
			}
			hash := codeHash(code)
			if seen[hash] {
				continue
			}
			seen[hash] = true
			cand := Candidate{
				ID:       candidateID(fp, "call", hash),
				Kind:     "call",
				Code:     truncate(code, 120),
				CodeHash: hash,
				Line:     line,
				Receiver: obj,
				Method:   method,
				Context:  findParentClassName(&callNode, src),
			}
			// Resolve type from constructor params
			if typeName, ok := ctorParams[obj]; ok {
				cand.ResolvedType = typeName
				cand.ReceiverOrigin = "constructor_param"
				cand.IsArchitectural = true
			}
			candidates = append(candidates, cand)
		}
	}

	// 2. this.X.Y.method() — 3-level chains (potential model access)
	q2, err := sitter.NewQuery(lang, `
		(call_expression
			function: (member_expression
				object: (member_expression
					object: (member_expression
						object: (this)
						property: (property_identifier) @object
					)
					property: (property_identifier) @property
				)
				property: (property_identifier) @method
			)
		) @call
	`)
	if err == nil {
		defer q2.Close()
		cursor2 := sitter.NewQueryCursor()
		defer cursor2.Close()
		seen2 := map[string]bool{}
		matches := cursor2.Matches(q2, root, src)
		for m := matches.Next(); m != nil; m = matches.Next() {
			var obj, prop, method, code string
			var line int
			var callNode sitter.Node
			for _, c := range m.Captures {
				switch q2.CaptureNames()[c.Index] {
				case "object":
					obj = c.Node.Utf8Text(src)
				case "property":
					prop = c.Node.Utf8Text(src)
				case "method":
					method = c.Node.Utf8Text(src)
				case "call":
					code = strings.TrimSpace(c.Node.Utf8Text(src))
					line = int(c.Node.StartPosition().Row) + 1
					callNode = c.Node
				}
			}
			if noiseMethods[method] || noiseMethods[prop] {
				continue
			}
			hash := codeHash(code)
			if seen2[hash] {
				continue
			}
			seen2[hash] = true
			cand := Candidate{
				ID:       candidateID(fp, "member_call", hash),
				Kind:     "member_call",
				Code:     truncate(code, 120),
				CodeHash: hash,
				Line:     line,
				Receiver: obj + "." + prop,
				Method:   method,
				Context:  findParentClassName(&callNode, src),
			}
			// Resolve type from constructor params (use the root object)
			if typeName, ok := ctorParams[obj]; ok {
				cand.ResolvedType = typeName
				cand.ReceiverOrigin = "constructor_param"
				cand.IsArchitectural = true
			}
			candidates = append(candidates, cand)
		}
	}

	// 3. fetch/axios calls — potential http_call
	q3, err := sitter.NewQuery(lang, `
		(call_expression
			function: (identifier) @fn
			arguments: (arguments) @args
		) @call
	`)
	if err == nil {
		defer q3.Close()
		cursor3 := sitter.NewQueryCursor()
		defer cursor3.Close()
		matches := cursor3.Matches(q3, root, src)
		for m := matches.Next(); m != nil; m = matches.Next() {
			var fn, code string
			var line int
			var callNode sitter.Node
			for _, c := range m.Captures {
				switch q3.CaptureNames()[c.Index] {
				case "fn":
					fn = c.Node.Utf8Text(src)
				case "call":
					code = strings.TrimSpace(c.Node.Utf8Text(src))
					line = int(c.Node.StartPosition().Row) + 1
					callNode = c.Node
				}
			}
			if fn == "fetch" || fn == "axios" || fn == "got" || fn == "request" {
				hash := codeHash(code)
				candidates = append(candidates, Candidate{
					ID:       candidateID(fp, "fetch_call", hash),
					Kind:     "fetch_call",
					Code:     truncate(code, 200),
					CodeHash: hash,
					Line:     line,
					Method:   fn,
					Context:  findParentClassName(&callNode, src),
				})
			}
		}
	}

	return candidates
}

func codeHash(code string) string {
	normalized := strings.Join(strings.Fields(code), " ")
	h := sha1.Sum([]byte(normalized))
	return fmt.Sprintf("%x", h[:8])
}

func candidateID(filePath, kind, hash string) string {
	if filePath == "" {
		return kind + ":" + hash
	}
	return filePath + ":" + kind + ":" + hash
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
}

// --- Helpers ---

func extractStringArg(argsText string) string {
	for _, q := range []byte{'\'', '"', '`'} {
		start := strings.IndexByte(argsText, q)
		if start >= 0 {
			end := strings.IndexByte(argsText[start+1:], q)
			if end >= 0 {
				return argsText[start+1 : start+1+end]
			}
		}
	}
	return ""
}

func astExtractSymbols(importNode *sitter.Node, source []byte) []string {
	var symbols []string
	astWalkNode(importNode, func(n *sitter.Node) {
		switch n.Kind() {
		case "import_specifier":
			nameNode := n.ChildByFieldName("name")
			if nameNode != nil {
				symbols = append(symbols, nameNode.Utf8Text(source))
			}
		case "identifier":
			parent := n.Parent()
			if parent != nil && parent.Kind() == "import_clause" {
				symbols = append(symbols, n.Utf8Text(source))
			}
		}
	})
	return symbols
}

func astWalkNode(node *sitter.Node, fn func(*sitter.Node)) {
	fn(node)
	for i := uint(0); i < node.ChildCount(); i++ {
		if child := node.Child(i); child != nil {
			astWalkNode(child, fn)
		}
	}
}
