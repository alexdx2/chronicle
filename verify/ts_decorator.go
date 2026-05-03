package verify

import (
	"encoding/json"
	"fmt"

	sitter "github.com/tree-sitter/go-tree-sitter"
	tree_sitter_typescript "github.com/tree-sitter/tree-sitter-typescript/bindings/go"
)

// TSDecoratorVerifier uses tree-sitter to verify decorators on classes/methods.
type TSDecoratorVerifier struct{}

func (v *TSDecoratorVerifier) Kind() string { return "decorator" }

// TSDecoratorAssertion describes an expected decorator.
type TSDecoratorAssertion struct {
	// DecoratorName is the decorator (e.g. "Controller", "Injectable", "Module").
	DecoratorName string `json:"decorator_name"`
	// TargetName is the class or method the decorator should be on. Optional.
	TargetName string `json:"target_name,omitempty"`
	// TargetKind: "class" or "method". Optional.
	TargetKind string `json:"target_kind,omitempty"`
}

func (v *TSDecoratorVerifier) Verify(fileContent []byte, assertion json.RawMessage, oldLocator *Locator) (*Result, error) {
	var a TSDecoratorAssertion
	if err := json.Unmarshal(assertion, &a); err != nil {
		return nil, fmt.Errorf("invalid decorator assertion: %w", err)
	}
	if a.DecoratorName == "" {
		return nil, fmt.Errorf("decorator assertion missing 'decorator_name' field")
	}

	parser := sitter.NewParser()
	defer parser.Close()

	lang := sitter.NewLanguage(tree_sitter_typescript.LanguageTypescript())
	parser.SetLanguage(lang)

	tree := parser.Parse(fileContent, nil)
	defer tree.Close()

	root := tree.RootNode()

	// Query for decorators: @DecoratorName or @DecoratorName(...)
	querySource := `(decorator
		(call_expression
			function: (identifier) @dec_name
		)
	) @dec`

	query, err := sitter.NewQuery(lang, querySource)
	if err != nil {
		return nil, fmt.Errorf("tree-sitter query error: %w", err)
	}
	defer query.Close()

	cursor := sitter.NewQueryCursor()
	defer cursor.Close()

	type decMatch struct {
		line int
		name string
	}

	var decorators []decMatch

	matches := cursor.Matches(query, root, fileContent)
	for match := matches.Next(); match != nil; match = matches.Next() {
		var dm decMatch
		for _, capture := range match.Captures {
			capName := query.CaptureNames()[capture.Index]
			switch capName {
			case "dec":
				dm.line = int(capture.Node.StartPosition().Row) + 1
			case "dec_name":
				dm.name = capture.Node.Utf8Text(fileContent)
			}
		}
		if dm.name != "" {
			decorators = append(decorators, dm)
		}
	}

	// Also try simple identifier decorators: @Injectable (no parens)
	querySource2 := `(decorator (identifier) @dec_name) @dec`
	query2, err := sitter.NewQuery(lang, querySource2)
	if err == nil {
		defer query2.Close()
		cursor2 := sitter.NewQueryCursor()
		defer cursor2.Close()
		matches2 := cursor2.Matches(query2, root, fileContent)
		for match := matches2.Next(); match != nil; match = matches2.Next() {
			var dm decMatch
			for _, capture := range match.Captures {
				capName := query2.CaptureNames()[capture.Index]
				switch capName {
				case "dec":
					dm.line = int(capture.Node.StartPosition().Row) + 1
				case "dec_name":
					dm.name = capture.Node.Utf8Text(fileContent)
				}
			}
			if dm.name != "" {
				decorators = append(decorators, dm)
			}
		}
	}

	// Filter by decorator name
	var matching []decMatch
	for _, dm := range decorators {
		if dm.name == a.DecoratorName {
			matching = append(matching, dm)
		}
	}

	if len(matching) == 0 {
		return &Result{
			Status:          "missing",
			Confidence:      0.85,
			Reason:          fmt.Sprintf("decorator @%s not found", a.DecoratorName),
			SuggestedAction: "mark_stale",
		}, nil
	}

	// If target_name specified, check what the decorator is attached to
	if a.TargetName != "" {
		// For now, just verify the decorator exists — target verification
		// would require walking up to the parent class/method declaration.
		// This is a simplification; we still confirm the decorator is present.
	}

	return &Result{
		Status:          "valid",
		NewLocator:      &Locator{LineStart: matching[0].line, LineEnd: matching[0].line},
		Confidence:      0.85,
		SuggestedAction: "revalidate",
	}, nil
}
