package verify

import (
	"encoding/json"
	"fmt"
	"strings"
)

// TextVerifier is a fallback verifier that does substring/context search.
// Max confidence capped at 0.60 — cannot alone verify architectural edges.
type TextVerifier struct{}

func (v *TextVerifier) Kind() string { return "text_contains" }

// TextAssertion is the expected shape.
type TextAssertion struct {
	Substring string `json:"substring"`
	Context   string `json:"context,omitempty"` // e.g. "import", "class", "function"
}

func (v *TextVerifier) Verify(fileContent []byte, assertion json.RawMessage, oldLocator *Locator) (*Result, error) {
	var a TextAssertion
	if err := json.Unmarshal(assertion, &a); err != nil {
		return nil, fmt.Errorf("invalid text_contains assertion: %w", err)
	}
	if a.Substring == "" {
		return nil, fmt.Errorf("text_contains assertion missing 'substring' field")
	}

	content := string(fileContent)
	lines := strings.Split(content, "\n")

	var matches []int
	for i, line := range lines {
		if strings.Contains(line, a.Substring) {
			// If context specified, check that the line also matches context
			if a.Context != "" {
				if !lineMatchesContext(line, a.Context) {
					continue
				}
			}
			matches = append(matches, i)
		}
	}

	switch len(matches) {
	case 0:
		return &Result{
			Status:          "missing",
			Confidence:      0.60,
			Reason:          fmt.Sprintf("substring %q not found", a.Substring),
			SuggestedAction: "mark_stale",
		}, nil
	case 1:
		loc := &Locator{LineStart: matches[0] + 1, LineEnd: matches[0] + 1}
		return &Result{
			Status:          "valid",
			NewLocator:      loc,
			Confidence:      0.60, // capped
			SuggestedAction: "revalidate",
		}, nil
	default:
		// Multiple matches — ambiguous
		loc := &Locator{LineStart: matches[0] + 1, LineEnd: matches[0] + 1}
		return &Result{
			Status:          "ambiguous",
			NewLocator:      loc,
			Confidence:      0.40,
			Reason:          fmt.Sprintf("found %d occurrences of %q", len(matches), a.Substring),
			SuggestedAction: "needs_claude",
		}, nil
	}
}

func lineMatchesContext(line, context string) bool {
	trimmed := strings.TrimSpace(line)
	switch context {
	case "import":
		return strings.HasPrefix(trimmed, "import ") || strings.Contains(trimmed, "require(") || strings.Contains(trimmed, "from ")
	case "class":
		return strings.Contains(trimmed, "class ")
	case "function":
		return strings.Contains(trimmed, "func ") || strings.Contains(trimmed, "function ")
	default:
		return strings.Contains(trimmed, context)
	}
}
