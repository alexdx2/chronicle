package verify

import (
	"encoding/json"
	"fmt"
	"strings"

	sitter "github.com/tree-sitter/go-tree-sitter"
	tree_sitter_typescript "github.com/tree-sitter/tree-sitter-typescript/bindings/go"
)

// TSImportVerifier uses tree-sitter to verify TypeScript/JavaScript import statements.
type TSImportVerifier struct{}

func (v *TSImportVerifier) Kind() string { return "import_specifier" }

// TSImportAssertion describes an expected import.
type TSImportAssertion struct {
	Module  string   `json:"module"`            // e.g. "@otopoint/pricing-engine" or "./pricing"
	Symbols []string `json:"symbols,omitempty"` // e.g. ["calculatePrice", "PriceResult"]
}

func (v *TSImportVerifier) Verify(fileContent []byte, assertion json.RawMessage, oldLocator *Locator) (*Result, error) {
	var a TSImportAssertion
	if err := json.Unmarshal(assertion, &a); err != nil {
		return nil, fmt.Errorf("invalid import_specifier assertion: %w", err)
	}
	if a.Module == "" {
		return nil, fmt.Errorf("import_specifier assertion missing 'module' field")
	}

	// Parse with tree-sitter
	parser := sitter.NewParser()
	defer parser.Close()

	lang := sitter.NewLanguage(tree_sitter_typescript.LanguageTypescript())
	parser.SetLanguage(lang)

	tree := parser.Parse(fileContent, nil)
	defer tree.Close()

	root := tree.RootNode()

	// Query for import statements
	querySource := `(import_statement
		source: (string (string_fragment) @source)
	) @import`

	query, err := sitter.NewQuery(lang, querySource)
	if err != nil {
		return nil, fmt.Errorf("tree-sitter query error: %w", err)
	}
	defer query.Close()

	cursor := sitter.NewQueryCursor()
	defer cursor.Close()

	type importMatch struct {
		line     int
		endLine  int
		module   string
		importNode *sitter.Node
	}

	var imports []importMatch

	matches := cursor.Matches(query, root, fileContent)
	for match := matches.Next(); match != nil; match = matches.Next() {
		var im importMatch
		for _, capture := range match.Captures {
			name := query.CaptureNames()[capture.Index]
			node := capture.Node
			switch name {
			case "source":
				im.module = node.Utf8Text(fileContent)
			case "import":
				im.line = int(node.StartPosition().Row) + 1
				im.endLine = int(node.EndPosition().Row) + 1
				// Save a reference to the import node for symbol extraction
				nodeCopy := node
				im.importNode = &nodeCopy
			}
		}
		if im.module != "" {
			imports = append(imports, im)
		}
	}

	// Find imports matching our module
	var matchingImports []importMatch
	for _, im := range imports {
		if im.module == a.Module {
			matchingImports = append(matchingImports, im)
		}
	}

	if len(matchingImports) == 0 {
		return &Result{
			Status:          "missing",
			Confidence:      0.90,
			Reason:          fmt.Sprintf("no import from %q found", a.Module),
			SuggestedAction: "mark_stale",
		}, nil
	}

	// Module found — now check symbols if specified
	if len(a.Symbols) == 0 {
		im := matchingImports[0]
		return &Result{
			Status:          "valid",
			NewLocator:      &Locator{LineStart: im.line, LineEnd: im.endLine},
			Confidence:      0.95,
			SuggestedAction: "revalidate",
		}, nil
	}

	// Check for specific imported symbols
	for _, im := range matchingImports {
		if im.importNode == nil {
			continue
		}
		importedSymbols := extractImportedSymbols(im.importNode, fileContent)
		missing := findMissingSymbols(a.Symbols, importedSymbols)

		if len(missing) == 0 {
			return &Result{
				Status:          "valid",
				NewLocator:      &Locator{LineStart: im.line, LineEnd: im.endLine},
				Confidence:      0.95,
				SuggestedAction: "revalidate",
			}, nil
		}

		if len(missing) < len(a.Symbols) {
			return &Result{
				Status:          "valid",
				ChangeType:      "value_changed",
				NewLocator:      &Locator{LineStart: im.line, LineEnd: im.endLine},
				Confidence:      0.80,
				Reason:          fmt.Sprintf("import exists but symbols missing: %s", strings.Join(missing, ", ")),
				SuggestedAction: "revalidate",
			}, nil
		}
	}

	// Module found but none of the expected symbols are imported
	im := matchingImports[0]
	return &Result{
		Status:          "valid",
		ChangeType:      "value_changed",
		NewLocator:      &Locator{LineStart: im.line, LineEnd: im.endLine},
		Confidence:      0.70,
		Reason:          fmt.Sprintf("import from %q exists but expected symbols not found: %s", a.Module, strings.Join(a.Symbols, ", ")),
		SuggestedAction: "needs_claude",
	}, nil
}

// extractImportedSymbols walks the import statement node to find named imports.
func extractImportedSymbols(importNode *sitter.Node, source []byte) []string {
	var symbols []string
	walkNode(importNode, func(n *sitter.Node) {
		switch n.Kind() {
		case "import_specifier":
			// import { foo, bar } from "..." — each is an import_specifier
			nameNode := n.ChildByFieldName("name")
			if nameNode != nil {
				symbols = append(symbols, nameNode.Utf8Text(source))
			}
		case "identifier":
			// Default import: import Foo from "..."
			parent := n.Parent()
			if parent != nil && parent.Kind() == "import_clause" {
				symbols = append(symbols, n.Utf8Text(source))
			}
		case "namespace_import":
			// import * as ns from "..."
			symbols = append(symbols, "*")
		}
	})
	return symbols
}

func walkNode(node *sitter.Node, fn func(*sitter.Node)) {
	fn(node)
	for i := uint(0); i < node.ChildCount(); i++ {
		child := node.Child(i)
		if child != nil {
			walkNode(child, fn)
		}
	}
}

func findMissingSymbols(expected, found []string) []string {
	set := make(map[string]bool, len(found))
	for _, s := range found {
		set[s] = true
	}
	var missing []string
	for _, s := range expected {
		if !set[s] {
			missing = append(missing, s)
		}
	}
	return missing
}
