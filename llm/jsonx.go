package llm

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
)

var fenceRe = regexp.MustCompile("(?s)```(?:json)?[ \t]*\r?\n?(.*?)```")

// ErrNoJSON is returned by ExtractJSON when text holds no complete JSON value.
var ErrNoJSON = errors.New("llm: no JSON object or array in response")

// ErrAmbiguousJSON is returned when more than one JSON value may be present.
var ErrAmbiguousJSON = errors.New("llm: ambiguous: more than one JSON value")

// ExtractJSON returns the JSON object or array that starts at the first '{' or
// '[' in text (inside the first ``` fence, if there is one). First-value
// semantics: later candidates are never tried, so a truncated payload is an
// error rather than a nested fragment. If the first value decodes but the text
// after it still contains a '{' or '[', the answer is ambiguous and an error.
func ExtractJSON(text string) (json.RawMessage, error) {
	if m := fenceRe.FindStringSubmatch(text); m != nil {
		text = m[1]
	}
	start := strings.IndexAny(text, "{[")
	if start < 0 {
		return nil, ErrNoJSON
	}
	dec := json.NewDecoder(strings.NewReader(text[start:]))
	var raw json.RawMessage
	if err := dec.Decode(&raw); err != nil {
		return nil, fmt.Errorf("llm: invalid JSON in response: %w", err)
	}
	rest := text[start+int(dec.InputOffset()):]
	if strings.ContainsAny(rest, "{[") {
		return nil, ErrAmbiguousJSON
	}
	return raw, nil
}
