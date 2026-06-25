package ast

import (
	sitter "github.com/tree-sitter/go-tree-sitter"
	tree_sitter_typescript "github.com/tree-sitter/tree-sitter-typescript/bindings/go"
)

// FunctionComplexity is the exact Tier-A complexity of one named function or
// method, computed directly from the TypeScript AST. Because these are exact
// syntactic counts (not heuristics), downstream evidence carries confidence 1.0.
type FunctionComplexity struct {
	Name       string `json:"name"`
	StartLine  int    `json:"start_line"`
	EndLine    int    `json:"end_line"`
	Cyclomatic int    `json:"cyclomatic"`
	LoopCount  int    `json:"loop_count"`
	LoopDepth  int    `json:"loop_depth"`
	// Cognitive is nesting-weighted complexity: each control-flow structure costs
	// 1 + its current nesting depth, so deeply nested logic is penalized more than
	// flat logic with the same cyclomatic count. It is a designed heuristic metric.
	Cognitive int `json:"cognitive"`
	// Smells are heuristic code-smell tags (e.g. unguarded_recursion,
	// linear_scan_in_loop, alloc_in_loop). Lower-confidence signals than the exact
	// counts — interpretation, not a count — but still mechanically reproducible.
	Smells []string `json:"smells,omitempty"`
}

// cogNestingKinds are the structures that both add to cognitive complexity and
// increase the nesting penalty for everything inside them.
var cogNestingKinds = map[string]bool{
	"if_statement":       true,
	"for_statement":      true,
	"for_in_statement":   true,
	"while_statement":    true,
	"do_statement":       true,
	"switch_statement":   true,
	"catch_clause":       true,
	"ternary_expression": true,
}

// scanMethods are array-search methods that turn a surrounding loop into a likely
// O(n²) linear scan.
var scanMethods = map[string]bool{
	"find": true, "findIndex": true, "indexOf": true,
	"lastIndexOf": true, "includes": true, "some": true,
}

// functionBoundaryKinds delimit a nested function scope. A function's own
// metrics never include the bodies of functions nested inside it — those are
// separate scopes with their own complexity.
var functionBoundaryKinds = map[string]bool{
	"function_declaration":           true,
	"function_expression":            true,
	"arrow_function":                 true,
	"method_definition":              true,
	"generator_function":             true,
	"generator_function_declaration": true,
}

// namedFunctionKinds are the function forms that carry a name field and map to
// symbol nodes in the graph.
var namedFunctionKinds = map[string]bool{
	"function_declaration":           true,
	"method_definition":              true,
	"generator_function_declaration": true,
}

// loopKinds are statements that introduce a loop (contribute to loop_count and
// loop nesting depth). for_in_statement covers both `for...of` and `for...in`.
var loopKinds = map[string]bool{
	"for_statement":    true,
	"for_in_statement": true,
	"while_statement":  true,
	"do_statement":     true,
}

// decisionKinds each add one to cyclomatic complexity (the McCabe/ESLint
// convention). switch_default is intentionally excluded; only valued cases add a
// branch. Short-circuit boolean operators are handled separately.
var decisionKinds = map[string]bool{
	"if_statement":       true,
	"for_statement":      true,
	"for_in_statement":   true,
	"while_statement":    true,
	"do_statement":       true,
	"ternary_expression": true,
	"catch_clause":       true,
	"switch_case":        true,
}

// AggregatedComplexity folds a file's per-function metrics to one class/unit
// level value. Chronicle's callable nodes are class-level, so per-method metrics
// are combined with MAX: the result represents "the gnarliest single method in
// this unit". Max also keeps LoopDepth equal to the deepest loop nest anywhere
// in the unit — exactly what graph-level transitive propagation should consume.
// Sharing this fold between the writer and the verifier guarantees they agree.
type AggregatedComplexity struct {
	Cyclomatic int
	LoopCount  int
	LoopDepth  int
	Cognitive  int      // max method's cognitive complexity (heuristic)
	Smells     []string // deduped union of method smells, canonical order (heuristic)
	StartLine  int
	EndLine    int
	Present    bool // false when the file has no measurable functions/methods
}

// canonicalSmells is the fixed output order for aggregated smell tags.
var canonicalSmells = []string{"unguarded_recursion", "linear_scan_in_loop", "alloc_in_loop"}

// AggregateComplexity folds per-function metrics into one class-level value (max
// of each numeric metric; smells = deduped union; line span = min start … max
// end across functions).
func AggregateComplexity(fns []FunctionComplexity) AggregatedComplexity {
	var out AggregatedComplexity
	smellSet := map[string]bool{}
	for _, f := range fns {
		out.Present = true
		if f.Cyclomatic > out.Cyclomatic {
			out.Cyclomatic = f.Cyclomatic
		}
		if f.LoopCount > out.LoopCount {
			out.LoopCount = f.LoopCount
		}
		if f.LoopDepth > out.LoopDepth {
			out.LoopDepth = f.LoopDepth
		}
		if f.Cognitive > out.Cognitive {
			out.Cognitive = f.Cognitive
		}
		for _, s := range f.Smells {
			smellSet[s] = true
		}
		if out.StartLine == 0 || f.StartLine < out.StartLine {
			out.StartLine = f.StartLine
		}
		if f.EndLine > out.EndLine {
			out.EndLine = f.EndLine
		}
	}
	for _, s := range canonicalSmells {
		if smellSet[s] {
			out.Smells = append(out.Smells, s)
		}
	}
	return out
}

// ExtractComplexity computes exact per-function complexity metrics from
// TypeScript source. One entry is returned per named function/method.
func ExtractComplexity(fileContent []byte) []FunctionComplexity {
	parser := sitter.NewParser()
	defer parser.Close()
	lang := sitter.NewLanguage(tree_sitter_typescript.LanguageTypescript())
	parser.SetLanguage(lang)

	tree := parser.Parse(fileContent, nil)
	defer tree.Close()
	root := tree.RootNode()

	var fns []*sitter.Node
	collectFunctions(root, &fns)

	var out []FunctionComplexity
	for _, fn := range fns {
		fc := functionMetrics(fn, fileContent)
		fc.Smells = functionSmells(fn, fileContent, fc.Cyclomatic)
		out = append(out, fc)
	}
	return out
}

// collectFunctions does a full descent collecting every named function/method
// node (including nested ones — each is reported on its own).
func collectFunctions(n *sitter.Node, acc *[]*sitter.Node) {
	if namedFunctionKinds[n.Kind()] {
		*acc = append(*acc, n)
	}
	for i := uint(0); i < n.ChildCount(); i++ {
		if c := n.Child(i); c != nil {
			collectFunctions(c, acc)
		}
	}
}

// functionMetrics computes the metrics for a single function node, stopping at
// nested function boundaries so each scope's complexity is its own.
func functionMetrics(fn *sitter.Node, src []byte) FunctionComplexity {
	fc := FunctionComplexity{
		Name:       functionName(fn, src),
		StartLine:  int(fn.StartPosition().Row) + 1,
		EndLine:    int(fn.EndPosition().Row) + 1,
		Cyclomatic: 1,
	}

	var walk func(n *sitter.Node, loopDepth, cogNesting int)
	walk = func(n *sitter.Node, loopDepth, cogNesting int) {
		for i := uint(0); i < n.ChildCount(); i++ {
			c := n.Child(i)
			if c == nil {
				continue
			}
			kind := c.Kind()
			// Do not descend into nested function scopes.
			if functionBoundaryKinds[kind] {
				continue
			}
			if decisionKinds[kind] {
				fc.Cyclomatic++
			}
			if kind == "binary_expression" && isShortCircuit(c, src) {
				fc.Cyclomatic++
				fc.Cognitive++ // a boolean operator adds a path, no nesting penalty
			}
			childLoop := loopDepth
			if loopKinds[kind] {
				fc.LoopCount++
				childLoop = loopDepth + 1
				if childLoop > fc.LoopDepth {
					fc.LoopDepth = childLoop
				}
			}
			childCog := cogNesting
			if cogNestingKinds[kind] {
				fc.Cognitive += 1 + cogNesting
				childCog = cogNesting + 1
			}
			walk(c, childLoop, childCog)
		}
	}
	walk(fn, 0, 0)
	return fc
}

// functionSmells returns the heuristic smell tags for a function, in a fixed
// order for determinism. cyclomatic is passed in to detect unguarded recursion
// (a self-call with no decision points has no base case).
func functionSmells(fn *sitter.Node, src []byte, cyclomatic int) []string {
	var smells []string
	if isRecursive(fn, src) && cyclomatic == 1 {
		smells = append(smells, "unguarded_recursion")
	}
	scan, alloc := loopBodyHazards(fn, src)
	if scan {
		smells = append(smells, "linear_scan_in_loop")
	}
	if alloc {
		smells = append(smells, "alloc_in_loop")
	}
	return smells
}

// isRecursive reports whether the function calls itself by name (free call
// `name(...)` or method call `this.name(...)` / `x.name(...)`), not descending
// into nested function scopes.
func isRecursive(fn *sitter.Node, src []byte) bool {
	name := functionName(fn, src)
	if name == "" {
		return false
	}
	found := false
	var walk func(n *sitter.Node)
	walk = func(n *sitter.Node) {
		for i := uint(0); i < n.ChildCount(); i++ {
			c := n.Child(i)
			if c == nil {
				continue
			}
			if functionBoundaryKinds[c.Kind()] {
				continue
			}
			if c.Kind() == "call_expression" && callsName(c, src, name) {
				found = true
			}
			walk(c)
		}
	}
	walk(fn)
	return found
}

// callsName reports whether a call_expression invokes the given name, either as
// a free function or as the property of a member expression.
func callsName(call *sitter.Node, src []byte, name string) bool {
	fnNode := call.ChildByFieldName("function")
	if fnNode == nil {
		return false
	}
	switch fnNode.Kind() {
	case "identifier":
		return fnNode.Utf8Text(src) == name
	case "member_expression":
		if prop := fnNode.ChildByFieldName("property"); prop != nil {
			return prop.Utf8Text(src) == name
		}
	}
	return false
}

// loopBodyHazards walks the function and reports whether, inside any loop body, a
// scan-method call (linear scan) or a `new` allocation occurs. Nested function
// scopes are not descended into.
func loopBodyHazards(fn *sitter.Node, src []byte) (scan, alloc bool) {
	var walk func(n *sitter.Node, loopDepth int)
	walk = func(n *sitter.Node, loopDepth int) {
		for i := uint(0); i < n.ChildCount(); i++ {
			c := n.Child(i)
			if c == nil {
				continue
			}
			kind := c.Kind()
			if functionBoundaryKinds[kind] {
				continue
			}
			childDepth := loopDepth
			if loopKinds[kind] {
				childDepth = loopDepth + 1
			}
			if loopDepth > 0 {
				if kind == "new_expression" {
					alloc = true
				}
				if kind == "call_expression" && callsScanMethod(c, src) {
					scan = true
				}
			}
			walk(c, childDepth)
		}
	}
	walk(fn, 0)
	return scan, alloc
}

// callsScanMethod reports whether a call_expression is a member call to an
// array-search method (find/indexOf/includes/...).
func callsScanMethod(call *sitter.Node, src []byte) bool {
	fnNode := call.ChildByFieldName("function")
	if fnNode == nil || fnNode.Kind() != "member_expression" {
		return false
	}
	prop := fnNode.ChildByFieldName("property")
	return prop != nil && scanMethods[prop.Utf8Text(src)]
}

// isShortCircuit reports whether a binary_expression uses a short-circuit
// boolean operator (&&, ||, ??), each of which adds a decision path.
func isShortCircuit(n *sitter.Node, src []byte) bool {
	op := n.ChildByFieldName("operator")
	if op == nil {
		return false
	}
	switch op.Utf8Text(src) {
	case "&&", "||", "??":
		return true
	}
	return false
}

// functionName returns the declared name of a function/method node, or "" for
// anonymous forms.
func functionName(fn *sitter.Node, src []byte) string {
	if name := fn.ChildByFieldName("name"); name != nil {
		return name.Utf8Text(src)
	}
	return ""
}
