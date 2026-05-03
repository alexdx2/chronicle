package verify

import (
	"encoding/json"
	"fmt"

	sitter "github.com/tree-sitter/go-tree-sitter"
	tree_sitter_typescript "github.com/tree-sitter/tree-sitter-typescript/bindings/go"
)

// TSCallVerifier uses tree-sitter to verify method/function call expressions.
type TSCallVerifier struct{}

func (v *TSCallVerifier) Kind() string { return "call_expression" }

// TSCallAssertion describes an expected method call.
type TSCallAssertion struct {
	// CallerContext narrows where the call should appear (class/function name). Optional.
	CallerContext string `json:"caller_context,omitempty"`
	// CalleeObject is the object the method is called on (e.g. "pricingEngine").
	CalleeObject string `json:"callee_object,omitempty"`
	// CalleeMethod is the method name (e.g. "calculate").
	CalleeMethod string `json:"callee_method"`
	// FreeFunction — if true, look for a standalone function call, not method call.
	FreeFunction bool `json:"free_function,omitempty"`
}

func (v *TSCallVerifier) Verify(fileContent []byte, assertion json.RawMessage, oldLocator *Locator) (*Result, error) {
	var a TSCallAssertion
	if err := json.Unmarshal(assertion, &a); err != nil {
		return nil, fmt.Errorf("invalid call_expression assertion: %w", err)
	}
	if a.CalleeMethod == "" {
		return nil, fmt.Errorf("call_expression assertion missing 'callee_method' field")
	}

	parser := sitter.NewParser()
	defer parser.Close()

	lang := sitter.NewLanguage(tree_sitter_typescript.LanguageTypescript())
	parser.SetLanguage(lang)

	tree := parser.Parse(fileContent, nil)
	defer tree.Close()

	root := tree.RootNode()

	var querySource string
	if a.FreeFunction || a.CalleeObject == "" {
		// Free function call: calculate(...) or calleeMethod(...)
		querySource = fmt.Sprintf(`(call_expression
			function: (identifier) @fn_name
			(#eq? @fn_name "%s")
		) @call`, a.CalleeMethod)
	} else {
		// Method call: obj.method(...)
		querySource = fmt.Sprintf(`(call_expression
			function: (member_expression
				object: (identifier) @obj
				property: (property_identifier) @method
			)
			(#eq? @method "%s")
		) @call`, a.CalleeMethod)
	}

	query, err := sitter.NewQuery(lang, querySource)
	if err != nil {
		return nil, fmt.Errorf("tree-sitter query error: %w", err)
	}
	defer query.Close()

	cursor := sitter.NewQueryCursor()
	defer cursor.Close()

	type callMatch struct {
		line   int
		object string
	}

	var calls []callMatch

	matches := cursor.Matches(query, root, fileContent)
	for match := matches.Next(); match != nil; match = matches.Next() {
		var cm callMatch
		for _, capture := range match.Captures {
			name := query.CaptureNames()[capture.Index]
			switch name {
			case "call":
				cm.line = int(capture.Node.StartPosition().Row) + 1
			case "obj":
				cm.object = capture.Node.Utf8Text(fileContent)
			}
		}
		if cm.line > 0 {
			calls = append(calls, cm)
		}
	}

	if len(calls) == 0 {
		return &Result{
			Status:          "missing",
			Confidence:      0.85,
			Reason:          fmt.Sprintf("no call to %s found", formatCallTarget(a)),
			SuggestedAction: "mark_stale",
		}, nil
	}

	// Filter by callee object if specified
	if a.CalleeObject != "" && !a.FreeFunction {
		var filtered []callMatch
		for _, cm := range calls {
			if cm.object == a.CalleeObject {
				filtered = append(filtered, cm)
			}
		}
		if len(filtered) == 0 {
			// Method exists but on different object
			return &Result{
				Status:          "valid",
				ChangeType:      "target_changed",
				NewLocator:      &Locator{LineStart: calls[0].line, LineEnd: calls[0].line},
				Confidence:      0.70,
				Reason:          fmt.Sprintf("method %s called but on different object (expected %s, found %s)", a.CalleeMethod, a.CalleeObject, calls[0].object),
				SuggestedAction: "needs_claude",
			}, nil
		}
		calls = filtered
	}

	// TODO: filter by caller_context (check enclosing function/class)
	// For now, any matching call is valid

	return &Result{
		Status:          "valid",
		NewLocator:      &Locator{LineStart: calls[0].line, LineEnd: calls[0].line},
		Confidence:      0.85,
		SuggestedAction: "revalidate",
	}, nil
}

func formatCallTarget(a TSCallAssertion) string {
	if a.CalleeObject != "" {
		return fmt.Sprintf("%s.%s()", a.CalleeObject, a.CalleeMethod)
	}
	return fmt.Sprintf("%s()", a.CalleeMethod)
}
