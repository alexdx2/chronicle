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
		// Exact match failed — try weaker fallbacks before declaring missing.
		// Identifiers often differ only in case between the canonical fact and
		// the source (Prisma client accessors: BattleEvent → prisma.battleEvent),
		// and route paths are commonly declared piecewise (NestJS
		// @Controller('arena') + @Post('attack') for "/arena/attack",
		// C# [Route("api/[controller]")] + [HttpGet("history")], SignalR hub
		// method names without the leading slash).
		if r := verifyCaseInsensitive(lines, a); r != nil {
			return r, nil
		}
		if r := verifyRouteSegments(content, lines, a.Substring); r != nil {
			return r, nil
		}
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

// verifyCaseInsensitive retries the substring search ignoring case.
// Returns nil if there is no unambiguous case-insensitive match.
func verifyCaseInsensitive(lines []string, a TextAssertion) *Result {
	lowered := strings.ToLower(a.Substring)
	var matches []int
	for i, line := range lines {
		if strings.Contains(strings.ToLower(line), lowered) {
			if a.Context != "" && !lineMatchesContext(line, a.Context) {
				continue
			}
			matches = append(matches, i)
		}
	}
	switch len(matches) {
	case 0:
		return nil
	case 1:
		loc := &Locator{LineStart: matches[0] + 1, LineEnd: matches[0] + 1}
		return &Result{
			Status:          "valid",
			NewLocator:      loc,
			Confidence:      0.55, // weaker than an exact match
			Reason:          fmt.Sprintf("case-insensitive match for %q", a.Substring),
			SuggestedAction: "revalidate",
		}
	default:
		loc := &Locator{LineStart: matches[0] + 1, LineEnd: matches[0] + 1}
		return &Result{
			Status:          "ambiguous",
			NewLocator:      loc,
			Confidence:      0.40,
			Reason:          fmt.Sprintf("found %d case-insensitive occurrences of %q", len(matches), a.Substring),
			SuggestedAction: "needs_claude",
		}
	}
}

// verifyRouteSegments handles path-like substrings ("/arena/attack",
// "/api/score/{fighterId}/history") whose canonical form never appears
// literally because the route is assembled from parts (controller prefix +
// method decorator/attribute). Every concrete segment must appear in the file
// (case-insensitive); route parameters ({id}, :id, [controller]) are skipped.
// Returns nil if the substring is not path-like or any segment is absent.
func verifyRouteSegments(content string, lines []string, substring string) *Result {
	if !strings.Contains(substring, "/") {
		return nil
	}
	segments := routeSegments(substring)
	if len(segments) == 0 {
		return nil
	}
	loweredContent := strings.ToLower(content)
	for _, seg := range segments {
		if !strings.Contains(loweredContent, strings.ToLower(seg)) {
			return nil
		}
	}
	// Locate the last (most specific) segment for the locator.
	last := strings.ToLower(segments[len(segments)-1])
	line := 1
	for i, l := range lines {
		if strings.Contains(strings.ToLower(l), last) {
			line = i + 1
			break
		}
	}
	return &Result{
		Status:          "valid",
		NewLocator:      &Locator{LineStart: line, LineEnd: line},
		Confidence:      0.50, // weakest verified tier: segments matched individually
		Reason:          fmt.Sprintf("route %q matched by segments (route declared piecewise)", substring),
		SuggestedAction: "revalidate",
	}
}

// routeSegments splits a path into concrete segments, dropping URL scheme,
// route parameters and template tokens.
func routeSegments(path string) []string {
	for _, prefix := range []string{"https://", "http://", "grpc://", "grpcs://", "ws://", "wss://"} {
		path = strings.TrimPrefix(path, prefix)
	}
	var segs []string
	for _, p := range strings.Split(path, "/") {
		p = strings.TrimSpace(p)
		// Route parameters and template tokens can't be matched literally.
		if p == "" ||
			strings.HasPrefix(p, "{") || strings.HasPrefix(p, ":") ||
			strings.HasPrefix(p, "[") || strings.Contains(p, "$") {
			continue
		}
		segs = append(segs, p)
	}
	return segs
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
