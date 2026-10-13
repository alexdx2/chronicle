package verify

import (
	"encoding/json"
	"errors"
	"strings"
)

// ManifestLineVerifier checks a dependency manifest line written by the
// deterministic deps parser: the text must sit on the recorded line
// (valid), elsewhere in the file (valid, change_type moved) or nowhere (missing).
type ManifestLineVerifier struct{}

type manifestLineAssertion struct {
	Line int    `json:"line"`
	Text string `json:"text"`
}

func (ManifestLineVerifier) Kind() string { return "manifest_line" }

func (ManifestLineVerifier) Verify(fileContent []byte, assertion json.RawMessage, _ *Locator) (*Result, error) {
	var a manifestLineAssertion
	if err := json.Unmarshal(assertion, &a); err != nil {
		return nil, err
	}
	if strings.TrimSpace(a.Text) == "" {
		return nil, errors.New("manifest_line: empty text")
	}
	lines := strings.Split(strings.ReplaceAll(string(fileContent), "\r\n", "\n"), "\n")
	if a.Line >= 1 && a.Line <= len(lines) && strings.Contains(lines[a.Line-1], a.Text) {
		return &Result{Status: "valid", Confidence: 1.0, SuggestedAction: "revalidate", NewLocator: &Locator{LineStart: a.Line, LineEnd: a.Line}}, nil
	}
	for i, l := range lines {
		if strings.Contains(l, a.Text) {
			return &Result{Status: "valid", ChangeType: "moved", Confidence: 1.0, SuggestedAction: "revalidate", NewLocator: &Locator{LineStart: i + 1, LineEnd: i + 1}}, nil
		}
	}
	return &Result{Status: "missing", Reason: "manifest line not found", Confidence: 0, SuggestedAction: "mark_stale"}, nil
}
