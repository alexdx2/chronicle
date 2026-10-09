package llm

import (
	"bytes"
	"encoding/json"
	"errors"
	"regexp"
)

var fenceRe = regexp.MustCompile("(?s)```(?:json)?[ \t]*\r?\n?(.*?)```")

// ErrNoJSON is returned by ExtractJSON when text holds no complete JSON value.
var ErrNoJSON = errors.New("llm: no JSON object or array in response")

// ExtractJSON returns the first JSON object or array in text, tolerating
// ```json fences and prose around it.
func ExtractJSON(text string) (json.RawMessage, error) {
	if m := fenceRe.FindStringSubmatch(text); m != nil {
		if raw, ok := firstValue(m[1]); ok {
			return raw, nil
		}
	}
	if raw, ok := firstValue(text); ok {
		return raw, nil
	}
	return nil, ErrNoJSON
}

// firstValue decodes from each '{' or '[' in turn until one yields a complete value.
func firstValue(s string) (json.RawMessage, bool) {
	b := []byte(s)
	for i := 0; i < len(b); i++ {
		if b[i] != '{' && b[i] != '[' {
			continue
		}
		dec := json.NewDecoder(bytes.NewReader(b[i:]))
		var raw json.RawMessage
		if err := dec.Decode(&raw); err == nil {
			return raw, true
		}
	}
	return nil, false
}
