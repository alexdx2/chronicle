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
	StartLine  int
	EndLine    int
	Present    bool // false when the file has no measurable functions/methods
}

// AggregateComplexity folds per-function metrics into one class-level value (max
// of each metric; line span = min start … max end across functions).
func AggregateComplexity(fns []FunctionComplexity) AggregatedComplexity {
	var out AggregatedComplexity
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
		if out.StartLine == 0 || f.StartLine < out.StartLine {
			out.StartLine = f.StartLine
		}
		if f.EndLine > out.EndLine {
			out.EndLine = f.EndLine
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
		out = append(out, functionMetrics(fn, fileContent))
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

	var walk func(n *sitter.Node, loopDepth int)
	walk = func(n *sitter.Node, loopDepth int) {
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
			}
			childDepth := loopDepth
			if loopKinds[kind] {
				fc.LoopCount++
				childDepth = loopDepth + 1
				if childDepth > fc.LoopDepth {
					fc.LoopDepth = childDepth
				}
			}
			walk(c, childDepth)
		}
	}
	walk(fn, 0)
	return fc
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
